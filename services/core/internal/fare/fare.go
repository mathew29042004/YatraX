// Package fare computes ticket fares the way Indian Railways structures them:
// a distance-tapered base fare per class, plus reservation charge, superfast
// surcharge, Tatkal charge and GST on AC classes.
package fare

import (
	"errors"
	"fmt"
	"math"

	"github.com/yatrax/yatrax/services/core/internal/network"
)

// Quotas supported by the fare engine and search.
var Quotas = map[string]string{
	"GN": "General",
	"TQ": "Tatkal",
	"PT": "Premium Tatkal",
	"LD": "Ladies",
	"SS": "Senior Citizen / Lower Berth",
	"HP": "Divyangjan",
	"DF": "Defence",
	"FT": "Foreign Tourist",
}

var ErrNoTatkal = errors.New("this class has no Tatkal quota")

// Quote is the per-passenger fare, in whole rupees.
type Quote struct {
	Class            string `json:"class"`
	Quota            string `json:"quota"`
	DistanceKm       int    `json:"distance_km"`
	ChargedKm        int    `json:"charged_km"`
	Base             int    `json:"base"`
	Reservation      int    `json:"reservation"`
	Superfast        int    `json:"superfast"`
	Tatkal           int    `json:"tatkal"`
	Concession       int    `json:"concession,omitempty"` // off the base fare (divyangjan quota)
	GST              int    `json:"gst"`
	Total            int    `json:"total"`
	CateringIncluded bool   `json:"catering_included"`
	Flexi            bool   `json:"flexi"`
}

// cateringTrains include meals in the fare.
var cateringTrains = map[string]bool{"RAJDHANI": true, "DURONTO": true, "VANDE_BHARAT": true, "SHATABDI": true}

// roundUp5 rounds up to the next multiple of ₹5, as Indian Railways does for base fares.
func roundUp5(v float64) int { return int(math.Ceil(v/5-1e-9)) * 5 }

// taperedKm converts a distance into "full-rate kilometres": each distance band
// is charged at its own factor, so long journeys cost less per km.
func taperedKm(bands []network.TaperBand, km int) float64 {
	total := 0.0
	for _, b := range bands {
		if km < b.FromKm {
			break
		}
		upper := min(km, b.ToKm)
		total += float64(upper-b.FromKm+1) * b.Factor
	}
	return total
}

// Compute quotes the fare for one passenger travelling km kilometres.
func Compute(n *network.Network, trainType, class string, km int, quota string) (Quote, error) {
	tt := n.TrainTypes[trainType]
	cc := n.Classes[class]
	if tt == nil || cc == nil || !cc.Bookable {
		return Quote{}, fmt.Errorf("no fare for %s in %s", class, trainType)
	}
	if _, ok := Quotas[quota]; !ok {
		return Quote{}, fmt.Errorf("unknown quota %q", quota)
	}
	q := Quote{Class: class, Quota: quota, DistanceKm: km, ChargedKm: max(km, cc.MinDistanceKm),
		CateringIncluded: cateringTrains[trainType], Flexi: tt.FlexiFare}

	q.Base = roundUp5(taperedKm(n.Taper, q.ChargedKm) * cc.FarePerKm * tt.FareMultiplier)
	q.Reservation = cc.ReservationCharge
	if tt.Superfast {
		q.Superfast = cc.SuperfastCharge
	}

	if quota == "TQ" || quota == "PT" {
		if cc.TatkalPct == nil {
			return Quote{}, ErrNoTatkal
		}
		charge := float64(q.Base) * *cc.TatkalPct / 100
		charge = math.Min(math.Max(charge, float64(*cc.TatkalMin)), float64(*cc.TatkalMax))
		if quota == "PT" {
			// Premium Tatkal is dynamically priced; quote its opening price.
			charge += float64(q.Base) * 0.3
		}
		q.Tatkal = roundUp5(charge)
	}

	if quota == "HP" {
		// Divyangjan travel at a quarter of the base fare in sleeper and chair classes, half in AC sleepers.
		pct := 0.75
		if class == "1A" || class == "2A" || class == "3A" {
			pct = 0.5
		}
		q.Concession = int(math.Round(float64(q.Base) * pct))
		q.Base -= q.Concession
	}

	sub := q.Base + q.Reservation + q.Superfast + q.Tatkal
	if cc.AC {
		q.GST = int(math.Round(float64(sub) * cc.GSTPct / 100))
	}
	q.Total = sub + q.GST
	return q, nil
}
