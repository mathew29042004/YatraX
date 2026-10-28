package fare

import (
	"testing"

	"github.com/yatrax/yatrax/services/core/internal/network"
)

func f(v float64) *float64 { return &v }
func i(v int) *int         { return &v }

func testNetwork() *network.Network {
	return &network.Network{
		TrainTypes: map[string]*network.TrainType{
			"MAIL_EXPRESS": {Code: "MAIL_EXPRESS", FareMultiplier: 1},
			"SUPERFAST":    {Code: "SUPERFAST", FareMultiplier: 1, Superfast: true},
			"RAJDHANI":     {Code: "RAJDHANI", FareMultiplier: 1.3, Superfast: true, FlexiFare: true},
		},
		Classes: map[string]*network.CoachClass{
			"SL": {Code: "SL", Bookable: true, FarePerKm: 0.55, ReservationCharge: 20, SuperfastCharge: 30, MinDistanceKm: 200,
				TatkalPct: f(10), TatkalMin: i(100), TatkalMax: i(200)},
			"3A": {Code: "3A", Bookable: true, AC: true, FarePerKm: 1.45, ReservationCharge: 40, SuperfastCharge: 45, GSTPct: 5,
				MinDistanceKm: 300, TatkalPct: f(30), TatkalMin: i(300), TatkalMax: i(400)},
			"1A": {Code: "1A", Bookable: true, AC: true, FarePerKm: 3.5, ReservationCharge: 60, SuperfastCharge: 75, GSTPct: 5, MinDistanceKm: 300},
			"GN": {Code: "GN"},
		},
		Taper: []network.TaperBand{
			{FromKm: 1, ToKm: 300, Factor: 1}, {FromKm: 301, ToKm: 700, Factor: 0.92}, {FromKm: 701, ToKm: 1200, Factor: 0.85},
			{FromKm: 1201, ToKm: 2000, Factor: 0.78}, {FromKm: 2001, ToKm: 99999, Factor: 0.7},
		},
	}
}

func TestTaperIsTelescopic(t *testing.T) {
	n := testNetwork()
	if got := taperedKm(n.Taper, 300); got != 300 {
		t.Errorf("300 km → %v, want 300", got)
	}
	// 300 at full rate + 100 at 0.92
	if got := taperedKm(n.Taper, 400); got < 391.99 || got > 392.01 {
		t.Errorf("400 km → %v, want 392", got)
	}
}

func TestSleeperMailChennaiErnakulam(t *testing.T) {
	q, err := Compute(testNetwork(), "MAIL_EXPRESS", "SL", 794, "GN")
	if err != nil {
		t.Fatal(err)
	}
	// 300 + 400×0.92 + 94×0.85 = 747.9 full-rate km × ₹0.55 = ₹411.3 → ₹415, + ₹20 reservation.
	if q.Base != 415 || q.Reservation != 20 || q.Superfast != 0 || q.GST != 0 || q.Total != 435 {
		t.Errorf("unexpected quote %+v", q)
	}
}

func TestACClassAddsGSTAndSuperfast(t *testing.T) {
	q, _ := Compute(testNetwork(), "SUPERFAST", "3A", 794, "GN")
	sub := q.Base + 40 + 45
	if q.Superfast != 45 || q.GST != int(float64(sub)*0.05+0.5) || q.Total != sub+q.GST {
		t.Errorf("unexpected quote %+v", q)
	}
}

func TestMinimumChargeableDistance(t *testing.T) {
	q, _ := Compute(testNetwork(), "MAIL_EXPRESS", "SL", 40, "GN")
	if q.ChargedKm != 200 || q.DistanceKm != 40 {
		t.Errorf("short trip charged %d km, want 200", q.ChargedKm)
	}
}

func TestTatkalIsClamped(t *testing.T) {
	short, _ := Compute(testNetwork(), "MAIL_EXPRESS", "SL", 250, "TQ")
	long, _ := Compute(testNetwork(), "MAIL_EXPRESS", "3A", 2500, "TQ")
	if short.Tatkal != 100 {
		t.Errorf("short Tatkal = %d, want minimum 100", short.Tatkal)
	}
	if long.Tatkal != 400 {
		t.Errorf("long 3A Tatkal = %d, want maximum 400", long.Tatkal)
	}
	if _, err := Compute(testNetwork(), "RAJDHANI", "1A", 1000, "TQ"); err != ErrNoTatkal {
		t.Errorf("1A Tatkal err = %v, want ErrNoTatkal", err)
	}
}

func TestPremiumTrainsCostMore(t *testing.T) {
	mail, _ := Compute(testNetwork(), "MAIL_EXPRESS", "3A", 1000, "GN")
	raj, _ := Compute(testNetwork(), "RAJDHANI", "3A", 1000, "GN")
	if raj.Base <= mail.Base || !raj.CateringIncluded || !raj.Flexi {
		t.Errorf("Rajdhani %+v should cost more than Mail %+v and include catering", raj, mail)
	}
}

func TestRejectsBadInput(t *testing.T) {
	if _, err := Compute(testNetwork(), "MAIL_EXPRESS", "GN", 100, "GN"); err == nil {
		t.Error("unreserved class should have no fare")
	}
	if _, err := Compute(testNetwork(), "MAIL_EXPRESS", "SL", 100, "XX"); err == nil {
		t.Error("unknown quota should fail")
	}
}

// Divyangjan travel at a quarter of the base fare in sleeper, half in AC sleepers; charges stay.
func TestDivyangjanConcession(t *testing.T) {
	n := testNetwork()
	full, _ := Compute(n, "SUPERFAST", "SL", 700, "GN")
	hp, err := Compute(n, "SUPERFAST", "SL", 700, "HP")
	if err != nil {
		t.Fatal(err)
	}
	if hp.Concession == 0 || hp.Base+hp.Concession != full.Base || hp.Base*4 > full.Base+3 {
		t.Fatalf("SL base %d, concession %d of %d", hp.Base, hp.Concession, full.Base)
	}
	if hp.Reservation != full.Reservation || hp.Superfast != full.Superfast {
		t.Fatal("the concession is on the base fare only")
	}
	ac, _ := Compute(n, "SUPERFAST", "3A", 700, "HP")
	acFull, _ := Compute(n, "SUPERFAST", "3A", 700, "GN")
	if ac.Concession*2 < acFull.Base-1 || ac.Concession*2 > acFull.Base+1 || ac.GST >= acFull.GST {
		t.Fatalf("3A: concession %d of base %d; GST %d vs %d", ac.Concession, acFull.Base, ac.GST, acFull.GST)
	}
}
