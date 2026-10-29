package seed

import (
	"slices"
	"testing"
)

func TestGenerateIsDeterministicAndValid(t *testing.T) {
	a, err := Generate(42, 500)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(42, 500)
	if err != nil {
		t.Fatal(err)
	}
	sa, sb := a.Trains[5].Stops[1], b.Trains[5].Stops[1]
	if a.People[10].FullName != b.People[10].FullName || a.People[499].Phone != b.People[499].Phone ||
		sa.Station != sb.Station || *sa.Arr != *sb.Arr || sa.Platform != sb.Platform {
		t.Fatal("same seed produced different worlds")
	}

	stations := map[string]bool{}
	for _, s := range a.Stations {
		stations[s.Code] = true
	}
	if len(stations) < 300 {
		t.Errorf("want ≥300 stations, got %d", len(stations))
	}
	for _, tr := range a.Trains {
		if len(tr.Stops) < 2 {
			t.Fatalf("train %s has %d stops", tr.Number, len(tr.Stops))
		}
		if tr.Stops[0].Arr != nil || tr.Stops[0].Dep == nil {
			t.Errorf("train %s origin must have departure only", tr.Number)
		}
		last := tr.Stops[len(tr.Stops)-1]
		if last.Dep != nil || last.Arr == nil {
			t.Errorf("train %s destination must have arrival only", tr.Number)
		}
		prev, prevKm := -1, -1
		for _, s := range tr.Stops {
			for _, m := range []*int{s.Arr, s.Dep} {
				if m != nil {
					if *m < prev {
						t.Fatalf("train %s: time goes backwards at %s", tr.Number, s.Station)
					}
					prev = *m
				}
			}
			if s.DistKm < prevKm {
				t.Fatalf("train %s: distance goes backwards at %s", tr.Number, s.Station)
			}
			prevKm = s.DistKm
		}
		if !slices.ContainsFunc(tr.Coaches, func(c Coach) bool { return classByCode(c.Class).Bookable }) {
			t.Errorf("train %s has no bookable coaches", tr.Number)
		}
	}

	demo := map[string]bool{}
	ids := map[[16]byte]bool{}
	for _, p := range a.People {
		if p.Demo {
			demo[p.Role] = true
		}
		if ids[p.ID] {
			t.Fatalf("duplicate person id")
		}
		ids[p.ID] = true
	}
	for _, role := range []string{"passenger", "tte", "station", "vendor", "controller", "admin"} {
		if !demo[role] {
			t.Errorf("no demo account for role %s", role)
		}
	}
}

func TestSegmentSharingRouteExists(t *testing.T) {
	// The booking demo shares one berth MAS→SA and SA→ERN; some train must call at all three.
	w, err := Generate(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range w.Trains {
		idx := map[string]int{}
		for i, s := range tr.Stops {
			idx[s.Station] = i
		}
		m, okM := idx["MAS"]
		s, okS := idx["SA"]
		e, okE := idx["ERN"]
		if okM && okS && okE && m < s && s < e {
			return
		}
	}
	t.Error("no train runs MAS → SA → ERN")
}
