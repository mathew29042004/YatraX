package booking

import (
	"testing"
	"time"
)

func TestRefundRules(t *testing.T) {
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	in := func(d time.Duration) time.Time { return now.Add(d) }
	cases := []struct {
		name                   string
		class, quota, status   string
		fare                   int
		departure              time.Time
		chart                  bool
		wantRefund, wantDeduct int
	}{
		{"SL, 3 days out: flat ₹120", "SL", "GN", "CNF", 435, in(72 * time.Hour), false, 315, 120},
		{"3A, 3 days out: flat ₹180", "3A", "GN", "CNF", 1181, in(72 * time.Hour), false, 1001, 180},
		{"3A, 24 h out: 25%", "3A", "GN", "CNF", 1181, in(24 * time.Hour), false, 886, 295},
		{"SL, 24 h out: 25% below flat, so flat", "SL", "GN", "CNF", 435, in(24 * time.Hour), false, 315, 120},
		{"3A, 6 h out: 50%", "3A", "GN", "CNF", 1181, in(6 * time.Hour), false, 591, 590},
		{"3A, 3 h out: nothing", "3A", "GN", "CNF", 1181, in(3 * time.Hour), false, 0, 1181},
		{"after chart: nothing", "3A", "GN", "CNF", 1181, in(5 * time.Hour), true, 0, 1181},
		{"Tatkal confirmed: nothing", "SL", "TQ", "CNF", 535, in(20 * time.Hour), false, 0, 535},
		{"WL: ₹60 clerkage", "SL", "GN", "WL", 435, in(2 * time.Hour), false, 375, 60},
		{"RAC: ₹60 clerkage", "SL", "GN", "RAC", 435, in(1 * time.Hour), true, 375, 60},
		{"WL within 30 min: nothing", "SL", "GN", "WL", 435, in(20 * time.Minute), false, 0, 435},
		{"departed: nothing", "SL", "GN", "CNF", 435, in(-time.Hour), false, 0, 435},
	}
	for _, c := range cases {
		r := RefundFor(c.class, c.quota, c.status, c.fare, c.departure, now, c.chart)
		if r.Refund != c.wantRefund || r.Deduction != c.wantDeduct || r.Refund+r.Deduction != c.fare {
			t.Errorf("%s: refund %d deduction %d, want %d / %d (%s)", c.name, r.Refund, r.Deduction, c.wantRefund, c.wantDeduct, r.Rule)
		}
	}
}
