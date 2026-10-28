package layout

import "testing"

func TestSleeperNumbering(t *testing.T) {
	seats, err := Seats("sleeper_8", 72)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[int]string{1: "LB", 2: "MB", 3: "UB", 7: "SL", 8: "SU", 9: "LB", 71: "SL", 72: "SU"}
	for n, want := range cases {
		if got := seats[n-1].Type; got != want {
			t.Errorf("berth %d = %s, want %s", n, got, want)
		}
	}
	if seats[71].Bay != 9 {
		t.Errorf("berth 72 bay = %d, want 9", seats[71].Bay)
	}
}

func TestTwoTierAC(t *testing.T) {
	seats, _ := Seats("sleeper_6", 48)
	if seats[4].Type != "SL" || seats[5].Type != "SU" || seats[1].Type != "UB" {
		t.Errorf("unexpected 2A pattern: %+v", seats[:6])
	}
}

func TestUnknownLayout(t *testing.T) {
	if _, err := Seats("bogus", 10); err == nil {
		t.Error("expected error for unknown layout")
	}
}
