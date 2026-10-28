// Package layout turns a coach class layout name into numbered seats and berths,
// following Indian Railways numbering (e.g. SL bay of 8: LB MB UB LB MB UB SL SU).
package layout

import "fmt"

type Seat struct {
	Number int    `json:"number"`
	Type   string `json:"type"` // LB MB UB SL SU for berths; W M A for chairs
	Bay    int    `json:"bay"`  // compartment / row number, 1-based
}

var (
	sleeper8 = []string{"LB", "MB", "UB", "LB", "MB", "UB", "SL", "SU"} // SL, 3A, 3E
	sleeper6 = []string{"LB", "UB", "LB", "UB", "SL", "SU"}             // 2A
	sleeper4 = []string{"LB", "UB", "LB", "UB"}                         // 1A cabins
	sitting6 = []string{"W", "M", "A", "A", "M", "W"}                   // 2S (3+3)
	sitting5 = []string{"W", "M", "A", "A", "W"}                        // CC (3+2)
	sitting4 = []string{"W", "A", "A", "W"}                             // EC (2+2)
)

func pattern(name string) ([]string, error) {
	switch name {
	case "sleeper_8":
		return sleeper8, nil
	case "sleeper_6":
		return sleeper6, nil
	case "sleeper_4":
		return sleeper4, nil
	case "sitting_6":
		return sitting6, nil
	case "sitting_5":
		return sitting5, nil
	case "sitting_4":
		return sitting4, nil
	case "none":
		return nil, nil
	}
	return nil, fmt.Errorf("unknown layout %q", name)
}

// Seats lists every seat in a coach of the given layout and capacity.
func Seats(name string, count int) ([]Seat, error) {
	p, err := pattern(name)
	if err != nil || p == nil {
		return []Seat{}, err
	}
	seats := make([]Seat, count)
	for i := range seats {
		seats[i] = Seat{Number: i + 1, Type: p[i%len(p)], Bay: i/len(p) + 1}
	}
	return seats, nil
}
