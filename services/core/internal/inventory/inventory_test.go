package inventory

import (
	"testing"

	"github.com/yatrax/yatrax/services/core/internal/network"
	"github.com/yatrax/yatrax/services/core/internal/seed"
)

func world(t *testing.T) *network.Network {
	t.Helper()
	w, err := seed.Generate(42, 10)
	if err != nil {
		t.Fatal(err)
	}
	return seed.Network(w)
}

func catalog(t *testing.T, n *network.Network, train, class string) (*Catalog, *network.Train) {
	t.Helper()
	tr := n.Trains[train]
	c, err := NewCatalog(n, tr, class)
	if err != nil {
		t.Fatal(err)
	}
	return c, tr
}

func TestSegMask(t *testing.T) {
	if SegMask(0, 1) != 1 || SegMask(2, 5) != 0b11100 {
		t.Errorf("SegMask wrong: %b %b", SegMask(0, 1), SegMask(2, 5))
	}
	if SegMask(0, 64) != ^uint64(0) {
		t.Error("full-width mask")
	}
}

func TestCatalogQuotasAndRAC(t *testing.T) {
	n := world(t)
	c, tr := catalog(t, n, "12623", "SL")
	coaches := 0
	for _, co := range tr.Coaches {
		if co.Class == "SL" {
			coaches++
		}
	}
	rac := 0
	quotas := map[string]int{}
	for _, u := range c.Units {
		if u.RAC {
			rac++
			if u.Type != "SL" || u.Quota != "GN" {
				t.Errorf("RAC unit %+v must be a General side-lower", u)
			}
			continue
		}
		quotas[u.Quota]++
	}
	if rac != coaches*racBaysPerCoach*2 {
		t.Errorf("RAC halves = %d, want %d", rac, coaches*racBaysPerCoach*2)
	}
	if c.Capacity("") != coaches*72-coaches*racBaysPerCoach {
		t.Errorf("confirmable capacity %d", c.Capacity(""))
	}
	for _, q := range []string{"GN", "TQ", "PT", "LD", "SS"} {
		if quotas[q] == 0 {
			t.Errorf("no %s berths: %v", q, quotas)
		}
	}
	if share := float64(quotas["TQ"]) / float64(c.Capacity("")); share < 0.15 || share > 0.3 {
		t.Errorf("Tatkal share %.2f out of range", share)
	}
	// Chair cars have no RAC.
	cc, _ := catalog(t, n, "12243", "CC")
	for _, u := range cc.Units {
		if u.RAC {
			t.Fatal("chair car must have no RAC units")
		}
	}
}

// The plan's demo: MAS→SA and SA→ERN share one berth.
func TestSegmentSharing(t *testing.T) {
	n := world(t)
	c, tr := catalog(t, n, "12623", "SL")
	mas, sa, ers := tr.StopIndex("MAS"), tr.StopIndex("SA"), tr.StopIndex("ERN")
	want := Key{"S1", 1, 0}
	occ := Occupancy{}
	a, err := c.Allocate(occ, Request{Quota: "GN", From: mas, To: sa, Pax: []Passenger{{Want: &want}}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Allocate(occ, Request{Quota: "GN", From: sa, To: ers, Pax: []Passenger{{Want: &want}}})
	if err != nil {
		t.Fatalf("SA→ERN on the same berth: %v", err)
	}
	if a[0].Unit.Key() != want || b[0].Unit.Key() != want {
		t.Fatal("both passengers should hold S1/1")
	}
	if _, err := c.Allocate(occ, Request{Quota: "GN", From: mas, To: ers, Pax: []Passenger{{Want: &want}}}); err != ErrBerthTaken {
		t.Errorf("MAS→ERN overlaps both; got %v", err)
	}
}

func TestBestFitFillsGaps(t *testing.T) {
	n := world(t)
	c, tr := catalog(t, n, "12623", "SL")
	mas, sa, ers := tr.StopIndex("MAS"), tr.StopIndex("SA"), tr.StopIndex("ERN")
	occ := Occupancy{}
	// Someone already holds S3/12 from MAS to SA.
	occ[Key{"S3", 12, 0}] = SegMask(mas, sa)
	got, _ := c.Allocate(occ, Request{Quota: "GN", From: sa, To: ers, Pax: []Passenger{{}}})
	if got[0].Unit.Key() != (Key{"S3", 12, 0}) {
		t.Errorf("SA→ERN should reuse the half-used berth, got %+v", got[0].Unit)
	}
}

func TestPreferencesAndTogether(t *testing.T) {
	n := world(t)
	c, tr := catalog(t, n, "12623", "SL")
	from, to := 0, len(tr.Stops)-1
	occ := Occupancy{}
	got, _ := c.Allocate(occ, Request{Quota: "GN", From: from, To: to, Together: true,
		Pax: []Passenger{{Pref: "LB"}, {Pref: "UB"}, {Age: 70}, {}}})
	bay := got[0].Unit.Bay
	for i, p := range got {
		if p.Status != CNF || p.Unit.Coach != got[0].Unit.Coach || p.Unit.Bay != bay {
			t.Errorf("passenger %d not in the same bay: %+v", i, p.Unit)
		}
	}
	if got[0].Unit.Type != "LB" || got[1].Unit.Type != "UB" {
		t.Errorf("preferences ignored: %s %s", got[0].Unit.Type, got[1].Unit.Type)
	}
	if got[2].Unit.Type != "LB" {
		t.Errorf("senior without preference should get a lower berth, got %s", got[2].Unit.Type)
	}
}

func TestFullTrainGoesToRACThenWaitlist(t *testing.T) {
	n := world(t)
	c, tr := catalog(t, n, "12623", "SL")
	from, to := 0, len(tr.Stops)-1
	occ := Occupancy{}
	gn := c.Capacity("GN")
	pax := make([]Passenger, gn)
	got, _ := c.Allocate(occ, Request{Quota: "GN", From: from, To: to, Pax: pax})
	for _, p := range got {
		if p.Status != CNF {
			t.Fatal("first capacity-worth of passengers must all confirm")
		}
	}
	if c.FreeCount(occ, "GN", from, to) != 0 {
		t.Fatal("General should be full")
	}
	racFree := c.FreeRAC(occ, from, to)
	more, _ := c.Allocate(occ, Request{Quota: "GN", From: from, To: to, Pax: make([]Passenger, racFree+3)})
	for i, p := range more {
		want := RAC
		if i >= racFree {
			want = WL
		}
		if p.Status != want {
			t.Fatalf("passenger %d: %s, want %s", i, p.Status, want)
		}
	}
	// Other quotas are untouched by General demand.
	if c.FreeCount(occ, "TQ", from, to) != c.Capacity("TQ") {
		t.Error("General bookings leaked into Tatkal berths")
	}
}

func TestQuotaIsolationForChosenBerths(t *testing.T) {
	n := world(t)
	c, _ := catalog(t, n, "12623", "SL")
	var tq Key
	for _, u := range c.Units {
		if u.Quota == "TQ" {
			tq = u.Key()
			break
		}
	}
	if _, err := c.Allocate(Occupancy{}, Request{Quota: "GN", From: 0, To: 3, Pax: []Passenger{{Want: &tq}}}); err != ErrBadBerth {
		t.Errorf("General booking picked a Tatkal berth: %v", err)
	}
}

func TestEveryTrainFitsTheBitmask(t *testing.T) {
	n := world(t)
	for num, tr := range n.Trains {
		for _, cls := range tr.Classes(n) {
			if _, err := NewCatalog(n, tr, cls); err != nil {
				t.Errorf("%s %s: %v", num, cls, err)
			}
		}
	}
}

// The small quotas keep exactly two berths each, in the last coach.
func TestSmallQuotas(t *testing.T) {
	n := world(t)
	c, _ := catalog(t, n, "12623", "SL")
	count := map[string]int{}
	coach := map[string]map[string]bool{}
	for _, u := range c.Units {
		count[u.Quota]++
		if coach[u.Quota] == nil {
			coach[u.Quota] = map[string]bool{}
		}
		coach[u.Quota][u.Coach] = true
	}
	for _, q := range []string{"HP", "DF"} {
		if count[q] != 2 || len(coach[q]) != 1 {
			t.Errorf("%s: %d berths in %d coaches, want 2 in 1", q, count[q], len(coach[q]))
		}
	}
	if count["FT"] != 0 {
		t.Error("sleeper class has no foreign tourist quota")
	}
	a, _ := catalog(t, n, "12623", "3A")
	ft := 0
	for _, u := range a.Units {
		if u.Quota == "FT" {
			ft++
		}
	}
	if ft != 2 {
		t.Errorf("3A foreign tourist berths: %d, want 2", ft)
	}
}
