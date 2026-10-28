package booking

import (
	"context"
	"fmt"
	"time"
)

// The tax invoice for a ticket, laid out as a GST invoice for passenger transport by rail
// (SAC 996421). YatraX and its GSTIN are synthetic: this is a demonstration, not a tax document.

type InvoiceLine struct {
	Description string `json:"description"`
	Qty         int    `json:"qty"`
	Rate        int    `json:"rate"`   // rupees each
	Amount      int    `json:"amount"` // rupees
}

type Invoice struct {
	Number         string        `json:"number"`
	Date           time.Time     `json:"date"`
	Supplier       string        `json:"supplier"`
	SupplierGSTIN  string        `json:"supplier_gstin"`
	SupplierState  string        `json:"supplier_state"`
	Customer       string        `json:"customer"`
	PlaceOfSupply  string        `json:"place_of_supply"`
	SAC            string        `json:"sac"`
	PNR            string        `json:"pnr"`
	Journey        string        `json:"journey"`
	Lines          []InvoiceLine `json:"lines"`
	Taxable        int           `json:"taxable"`
	CGST           int           `json:"cgst"`
	SGST           int           `json:"sgst"`
	IGST           int           `json:"igst"`
	GSTRate        float64       `json:"gst_rate"`
	Insurance      int           `json:"insurance"` // premium collected for the insurer, outside GST here
	Total          int           `json:"total"`
	Note           string        `json:"note"`
	Cancelled      bool          `json:"cancelled"`
	PassengerCount int           `json:"passengers"`
}

const supplierState = "Delhi" // YatraX is registered at New Delhi

func (s *Service) Invoice(ctx context.Context, userID, bookingID string) (*Invoice, error) {
	b, err := s.GetFor(ctx, userID, bookingID)
	if err != nil {
		return nil, err
	}
	if b.ConfirmedAt == nil {
		return nil, invalid("An invoice is issued once the ticket is paid for")
	}
	n := len(b.Passengers)
	q := b.Fare
	var name string
	s.DB.QueryRow(ctx, `SELECT full_name FROM users WHERE id = $1`, userID).Scan(&name)
	place := s.Net.Stations[b.From].State
	inv := &Invoice{
		Number:         fmt.Sprintf("YX/%s/%s", fiscalYear(*b.ConfirmedAt), b.PNR),
		Date:           *b.ConfirmedAt,
		Supplier:       "YatraX Rail Services (synthetic)",
		SupplierGSTIN:  "SYNTHETIC-GSTIN-07",
		SupplierState:  supplierState,
		Customer:       name,
		PlaceOfSupply:  place,
		SAC:            "996421",
		PNR:            b.PNR,
		Journey:        fmt.Sprintf("%s %s · %s → %s · %s · %s", b.Train, b.TrainName, b.From, b.To, b.JourneyDate, b.ClassName),
		Insurance:      b.Insurance,
		Cancelled:      b.Status == "CANCELLED",
		PassengerCount: n,
		Note: "Synthetic data for a demonstration. GST applies to AC classes only; travel insurance is " +
			"collected on behalf of the insurer.",
	}
	add := func(desc string, rate int) {
		if rate != 0 {
			inv.Lines = append(inv.Lines, InvoiceLine{desc, n, rate, rate * n})
			inv.Taxable += rate * n
		}
	}
	add("Base fare", q.Base+q.Concession)
	add("Divyangjan concession", -q.Concession)
	add("Reservation charge", q.Reservation)
	add("Superfast surcharge", q.Superfast)
	add("Tatkal charge", q.Tatkal)
	gst := q.GST * n
	if gst > 0 {
		inv.GSTRate = s.Net.Classes[b.Class].GSTPct
		if place == supplierState {
			inv.CGST = gst / 2
			inv.SGST = gst - inv.CGST
		} else {
			inv.IGST = gst
		}
	}
	inv.Total = inv.Taxable + gst + b.Insurance
	return inv, nil
}

// fiscalYear is India's April–March financial year, e.g. "2026-27".
func fiscalYear(t time.Time) string {
	y := t.Year()
	if t.Month() < time.April {
		y--
	}
	return fmt.Sprintf("%d-%02d", y, (y+1)%100)
}
