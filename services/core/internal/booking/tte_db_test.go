package booking

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatrax/yatrax/services/core/internal/network"
)

var opSeq int

func op(kind string, payload any) TTEOp {
	opSeq++
	b, _ := json.Marshal(payload)
	return TTEOp{ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", time.Now().UnixNano()%1e9*1000+int64(opSeq)), Kind: kind,
		At: time.Now().Add(time.Duration(opSeq) * time.Millisecond), Payload: b}
}

func applyOne(t *testing.T, s *Service, user string, o TTEOp) TTEResult {
	t.Helper()
	res, err := s.ApplyTTEOps(context.Background(), user, "12623", ttDate, []TTEOp{o})
	if err != nil {
		t.Fatalf("%s: %v", o.Kind, err)
	}
	return res[0]
}

var ttDate = time.Date(2026, 11, 26, 0, 0, 0, 0, network.IST)

func TestTTEOnBoard(t *testing.T) {
	s, users := setup(t)
	s = quiet(s)
	ctx := context.Background()

	// Fill 2A on the Trivandrum Mail end to end until passengers are on RAC.
	var racs []string
	for i := 0; i < 60 && len(racs) < 3; i++ {
		b, err := s.Hold(ctx, users[100+i], "", HoldRequest{Train: "12623", From: "MAS", To: "TVC", Date: ttDate.Format(time.DateOnly),
			Class: "2A", Quota: "GN", Passengers: []PassengerInput{{Name: fmt.Sprintf("Full %d", i), Age: 30 + i%40, Gender: "M"}, {Name: fmt.Sprintf("Half %d", i), Age: 28, Gender: "F"}}})
		if err != nil {
			continue
		}
		confirmNow(t, s, b)
	}
	bundle, err := s.TTEBundle(ctx, "12623", ttDate)
	if err != nil {
		t.Fatal(err)
	}
	var cnf []TTEPassenger
	for _, p := range bundle.Passengers {
		switch {
		case p.Class == "2A" && p.Status == "CNF" && p.FromIdx == 0:
			cnf = append(cnf, p)
		case p.Class == "2A" && p.Status == "RAC":
			racs = append(racs, p.ID)
		}
	}
	if len(cnf) < 4 || len(racs) < 2 {
		t.Fatalf("setup: %d confirmed and %d RAC passengers in 2A", len(cnf), len(racs))
	}
	if len(bundle.Coaches) == 0 || len(bundle.Fares["2A"]) != len(bundle.Stops)*(len(bundle.Stops)-1)/2 {
		t.Fatal("bundle is missing coaches or the fare table")
	}
	tte := users[0]
	tr := s.Net.Trains["12623"]
	st := func(i int) string { return tr.Stops[i].Station }

	// Too early: the passenger may still turn up within two stations of boarding.
	if r := applyOne(t, s, tte, op("no_show", map[string]any{"passenger_id": cnf[0].ID, "station": st(1)})); r.Status != "rejected" {
		t.Errorf("no-show one station out: %s %s", r.Status, r.Message)
	}
	// A boarded passenger is not a no-show.
	if r := applyOne(t, s, tte, op("board", map[string]any{"passenger_id": cnf[0].ID, "station": st(0)})); r.Status != "applied" {
		t.Fatalf("board: %s", r.Message)
	}
	if r := applyOne(t, s, tte, op("no_show", map[string]any{"passenger_id": cnf[0].ID, "station": st(2)})); r.Status != "rejected" {
		t.Errorf("boarded passenger marked no-show: %s", r.Message)
	}

	// The real thing: release cnf[1]'s berth at stop 2 and move the chosen RAC passenger onto it.
	noShow := op("no_show", map[string]any{"passenger_id": cnf[1].ID, "station": st(2), "assign_to": racs[1]})
	r := applyOne(t, s, tte, noShow)
	moved, _ := r.Data["moved"].(map[string]any)
	if r.Status != "applied" || moved == nil || moved["passenger_id"] != racs[1] {
		t.Fatalf("no-show with RAC move: %s %s %v", r.Status, r.Message, r.Data)
	}
	noOverlaps(t, s)
	// Replaying the same operation (a retried sync) changes nothing.
	if again := applyOne(t, s, tte, noShow); again.Status != "duplicate" {
		t.Errorf("replay: %s", again.Status)
	}

	// A second device, offline, also chose racs[1] for another berth: the server's allocation wins.
	r = applyOne(t, s, tte, op("no_show", map[string]any{"passenger_id": cnf[2].ID, "station": st(3), "assign_to": racs[1]}))
	if r.Status != "resolved" {
		t.Errorf("conflicting RAC choice: %s %s", r.Status, r.Message)
	}
	noOverlaps(t, s)

	var status, coach string
	var berth int
	if err := s.DB.QueryRow(ctx, `SELECT status::text, coach_no, berth_no FROM booking_passengers WHERE id = $1`, racs[1]).Scan(&status, &coach, &berth); err != nil {
		t.Fatal(err)
	}
	if status != "CNF" || fmt.Sprintf("%s/%d", coach, berth) != fmt.Sprintf("%s/%d", cnf[1].Coach, cnf[1].Berth) {
		t.Errorf("RAC passenger is %s %s/%d, want CNF on %s/%d", status, coach, berth, cnf[1].Coach, cnf[1].Berth)
	}
	// Their berth runs from the release point to their destination.
	var lo, hi int
	s.DB.QueryRow(ctx, `SELECT lower(seg), upper(seg) FROM seat_allocations WHERE passenger_id = $1`, racs[1]).Scan(&lo, &hi)
	if lo != 2 || hi != len(tr.Stops)-1 {
		t.Errorf("moved RAC passenger holds stops [%d, %d), want [2, %d)", lo, hi, len(tr.Stops)-1)
	}

	// Asking for a berth someone holds gets another free one, or a clear refusal.
	if len(racs) > 2 {
		r = applyOne(t, s, tte, op("assign_berth", map[string]any{"passenger_id": racs[2], "coach": cnf[3].Coach, "berth": cnf[3].Berth, "station": st(3)}))
		if r.Status == "applied" {
			t.Errorf("assigned a berth that is taken: %s", r.Message)
		}
		noOverlaps(t, s)
	}

	// Excess-fare ticket: fare + ₹250, cash with the TTE, ledger still balanced.
	r = applyOne(t, s, tte, op("fine", map[string]any{"name": "Without Ticket", "age": 35, "gender": "M", "from": st(2), "to": "TVC", "class": "SL", "reason": "ticketless"}))
	if r.Status != "applied" || r.Data["receipt_no"] == nil {
		t.Fatalf("fine: %s %s", r.Status, r.Message)
	}
	if total, fareAmt := int(r.Data["total"].(int)), int(r.Data["fare"].(int)); total != fareAmt+Penalty || fareAmt <= 0 {
		t.Errorf("fine total %d, fare %d", total, fareAmt)
	}
	trialBalanceZero(t, s)

	r = applyOne(t, s, tte, op("complaint", map[string]any{"category": "cleanliness", "description": "Toilet in A1 not cleaned", "coach": "A1"}))
	if r.Status != "applied" {
		t.Errorf("complaint: %s", r.Message)
	}
	var other string
	if err := s.DB.QueryRow(ctx, `SELECT employee_id FROM users WHERE role = 'tte' ORDER BY employee_id OFFSET 1 LIMIT 1`).Scan(&other); err != nil && err != pgx.ErrNoRows {
		t.Fatal(err)
	}
	r = applyOne(t, s, tte, op("handoff", map[string]any{"to_employee_id": other, "station": "ED", "note": "2 no-shows in A1"}))
	if r.Status != "applied" {
		t.Errorf("handoff: %s", r.Message)
	}

	// The RAC passenger moved onto a berth at stop 2 is then marked a no-show at stop 2: their
	// berth only started there, so the allocation goes (an empty range would break occupancy).
	if r := applyOne(t, s, tte, op("no_show", map[string]any{"passenger_id": racs[1], "station": st(2)})); r.Status != "applied" {
		t.Errorf("no-show of a passenger moved here: %s %s", r.Status, r.Message)
	}
	noOverlaps(t, s)
	if _, _, err := loadOccupancy(ctx, s.DB, bundle.RunID, "2A"); err != nil {
		t.Fatalf("occupancy after the no-show: %v", err)
	}

	after, err := s.TTEBundle(ctx, "12623", ttDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Fines) != 1 || len(after.Handoffs) != 1 || after.OpsApplied < 9 {
		t.Errorf("bundle after sync: %d fines, %d hand-offs, %d ops", len(after.Fines), len(after.Handoffs), after.OpsApplied)
	}
	for _, p := range after.Passengers {
		if p.ID == cnf[1].ID && (p.Status != "NOSHOW" || p.NoShowAt != st(2)) {
			t.Errorf("no-show passenger shows as %s at %q", p.Status, p.NoShowAt)
		}
	}
}
