package station

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yatrax/yatrax/services/core/internal/live"
	"github.com/yatrax/yatrax/services/core/internal/network"
	"github.com/yatrax/yatrax/services/core/internal/seed"
)

var (
	once sync.Once
	net  *network.Network
)

func svc(t *testing.T, now time.Time) *Service {
	once.Do(func() {
		w, err := seed.Generate(42, 10)
		if err != nil {
			t.Fatal(err)
		}
		net = seed.Network(w)
	})
	clock := func() time.Time { return now }
	return &Service{Net: net, Live: live.NewEngine(net, nil, clock), Now: clock}
}

func TestLanguages(t *testing.T) {
	if got, missing := Languages("ta"); strings.Join(got, ",") != "ta,hi,en" || missing != "" {
		t.Errorf("Tamil Nadu: %v (missing %q)", got, missing)
	}
	if got, _ := Languages("hi"); strings.Join(got, ",") != "hi,en" {
		t.Errorf("a Hindi-speaking station shouldn't say it twice: %v", got)
	}
	if got, missing := Languages("gu"); strings.Join(got, ",") != "hi,en" || missing != "gu" {
		t.Errorf("Gujarati has no template yet: %v (missing %q)", got, missing)
	}
}

func TestCompose(t *testing.T) {
	lines := Compose("ta", facts{Kind: KindArriving, Number: "12623", Name: "Trivandrum Mail", From: "Chennai Central", To: "Thiruvananthapuram Central", Platform: 5})
	if len(lines) != 3 || lines[0].BCP47 != "ta-IN" || lines[1].BCP47 != "hi-IN" || lines[2].BCP47 != "en-IN" {
		t.Fatalf("lines: %+v", lines)
	}
	for _, l := range lines {
		if !strings.Contains(l.Text, "12623") || !strings.Contains(l.Speech, "1 2 6 2 3") || strings.Contains(l.Text, "{") {
			t.Errorf("%s: %q / %q", l.Lang, l.Text, l.Speech)
		}
	}
	if !strings.Contains(lines[1].Text, "प्लेटफ़ॉर्म संख्या 5") || !strings.Contains(lines[0].Text, "நடைமேடை எண் 5") {
		t.Error("platform number missing from the Hindi or Tamil text")
	}
	for kind := range templates["en"] {
		for lang := range templates {
			if templates[lang][kind] == "" {
				t.Errorf("%s has no %s template", lang, kind)
			}
		}
	}
}

func TestCoachPosition(t *testing.T) {
	s := svc(t, time.Date(2026, 11, 9, 20, 0, 0, 0, network.IST))
	cp, err := s.CoachPosition("SA", "12623", time.Date(2026, 11, 9, 0, 0, 0, 0, network.IST))
	if err != nil {
		t.Fatal(err)
	}
	if cp.Spots[0].Coach != "ENG" || len(cp.Spots) != len(net.Trains["12623"].Coaches)+1 {
		t.Fatalf("spots: %+v", cp.Spots[:3])
	}
	first, last := cp.Spots[0].Marker, cp.Spots[len(cp.Spots)-1].Marker
	if first < 1 || last > cp.Markers || (first-1)-(cp.Markers-last) > 1 || (cp.Markers-last)-(first-1) > 1 {
		t.Errorf("rake not centred: markers %d..%d of %d", first, last, cp.Markers)
	}
	if cp.EngineToward == "" || cp.Platform < 1 {
		t.Errorf("toward %q, platform %d", cp.EngineToward, cp.Platform)
	}
	if _, err := s.CoachPosition("NDLS", "12623", time.Now()); err == nil {
		t.Error("12623 doesn't call at New Delhi")
	}
}

func TestAnnouncements(t *testing.T) {
	s := svc(t, time.Date(2026, 11, 10, 9, 30, 0, 0, network.IST))
	a, err := s.Announcements("MAS")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Items) == 0 {
		t.Fatal("Chennai Central has nothing to announce on a weekday morning")
	}
	seen := map[string]bool{}
	for _, it := range a.Items {
		if seen[it.ID] {
			t.Errorf("duplicate announcement id %s", it.ID)
		}
		seen[it.ID] = true
		if len(it.Lines) != 3 || it.Lines[0].Lang != "ta" {
			t.Errorf("%s: want Tamil, Hindi, English; got %d lines", it.ID, len(it.Lines))
		}
	}
	again, _ := s.Announcements("MAS")
	if len(again.Items) != len(a.Items) || again.Items[0].ID != a.Items[0].ID {
		t.Error("the same moment should give the same announcements")
	}
	if _, err := s.Announcements("NOPE"); err != ErrNoStation {
		t.Errorf("unknown station: %v", err)
	}
}
