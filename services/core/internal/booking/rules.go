package booking

import (
	"time"
)

// Cancellation and refund rules, following Indian Railways' Refund Rules 2015.

const (
	// RACWLClerkage is deducted per RAC or waitlisted passenger on cancellation.
	RACWLClerkage = 60
	// LateGrace: a payment arriving this long after the hold expired still gets a
	// berth if one is free; later than that, it is refunded instead.
	LateGrace = 15 * time.Minute
	// BoardingChangeCutoff: the boarding point can be changed up to this long before departure.
	BoardingChangeCutoff = 24 * time.Hour
)

// flatCharge is the minimum cancellation charge per confirmed passenger.
var flatCharge = map[string]int{"1A": 240, "EC": 240, "2A": 200, "3A": 180, "CC": 180, "3E": 180, "SL": 120, "2S": 60}

// Refund is what one passenger gets back.
type Refund struct {
	Seq       int    `json:"seq"`
	Status    string `json:"status"`
	Fare      int    `json:"fare"`
	Deduction int    `json:"deduction"`
	Refund    int    `json:"refund"`
	Rule      string `json:"rule"`
}

// RefundFor applies the rules to one passenger. departure is the scheduled time at
// the boarding station; chartPrepared means the first chart is out.
func RefundFor(class, quota, status string, fare int, departure, now time.Time, chartPrepared bool) Refund {
	r := Refund{Status: status, Fare: fare}
	left := departure.Sub(now)
	refundAll := func(deduct int, rule string) Refund {
		r.Deduction = min(deduct, fare)
		r.Refund = fare - r.Deduction
		r.Rule = rule
		return r
	}
	switch status {
	case "RAC", "WL":
		if left < 30*time.Minute {
			return refundAll(fare, "No refund within 30 minutes of departure; file a TDR if you didn't travel")
		}
		return refundAll(RACWLClerkage, "RAC/waitlist: ₹60 clerkage per passenger")
	}
	// Confirmed.
	flat := flatCharge[class]
	switch {
	case quota == "TQ" || quota == "PT":
		return refundAll(fare, "Confirmed Tatkal tickets are not refundable")
	case left <= 0 || chartPrepared || left < 4*time.Hour:
		return refundAll(fare, "No refund after chart preparation; file a TDR if eligible")
	case left > 48*time.Hour:
		return refundAll(flat, "More than 48 hours before departure: flat charge")
	case left > 12*time.Hour:
		return refundAll(max(flat, fare*25/100), "48 to 12 hours before departure: 25% of fare (minimum the flat charge)")
	default:
		return refundAll(max(flat, fare*50/100), "12 to 4 hours before departure: 50% of fare (minimum the flat charge)")
	}
}
