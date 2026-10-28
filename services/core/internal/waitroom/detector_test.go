package waitroom

import "testing"

func TestHumanLooksHuman(t *testing.T) {
	score, reasons := Score(Signals{Device: "dev-a1", UserAgent: "Mozilla/5.0 (Windows NT 10.0) Chrome/130",
		FormMillis: 21000, KeyIntervals: []int{180, 95, 240, 130, 310, 160, 90}, PointerMoves: 140}, 1, 2)
	if score >= ChallengeAt {
		t.Errorf("an ordinary person scored %d: %v", score, reasons)
	}
}

func TestObviousBotIsBlocked(t *testing.T) {
	score, reasons := Score(Signals{Device: "farm-1", UserAgent: "Go-http-client/1.1",
		FormMillis: 120, KeyIntervals: []int{20, 20, 21, 20, 20, 20}}, 12, 40)
	if action(score) != "block" {
		t.Errorf("bot farm scored %d (%s): %v", score, action(score), reasons)
	}
}

func TestBorderlineIsChallenged(t *testing.T) {
	// A family booking on one phone: a few accounts, normal typing.
	score, _ := Score(Signals{Device: "family-phone", UserAgent: "Mozilla/5.0 (Android 14)", FormMillis: 9000,
		KeyIntervals: []int{150, 210, 120, 300, 170}, PointerMoves: 30}, 3, 3)
	if action(score) == "block" {
		t.Errorf("a shared family phone must not be blocked outright (score %d)", score)
	}
	// Very fast form, no device: suspicious enough to challenge, not to block.
	score, _ = Score(Signals{UserAgent: "Mozilla/5.0", FormMillis: 1200, PointerMoves: 3, KeyIntervals: []int{60, 75, 50, 90, 66}}, 1, 1)
	if action(score) != "challenge" {
		t.Errorf("fast form without a device should be challenged, got %s (score %d)", action(score), score)
	}
}

func TestProofOfWork(t *testing.T) {
	nonce := SolvePoW("abc123", 12)
	if !ValidPoW("abc123", nonce, 12) {
		t.Fatal("solved nonce must validate")
	}
	if ValidPoW("abc123", nonce+"x", 12) && ValidPoW("other", nonce, 12) {
		t.Error("a nonce should not validate for a different prefix (with overwhelming probability)")
	}
	if ValidPoW("", "1", 0) {
		t.Error("an empty prefix is never valid")
	}
}

func TestShuffleIsAPermutation(t *testing.T) {
	xs := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	seen := map[string]bool{}
	shuffle(xs)
	for _, x := range xs {
		seen[x] = true
	}
	if len(seen) != 8 {
		t.Fatalf("shuffle lost or duplicated entries: %v", xs)
	}
}

func TestSharedPhoneIsChallenged(t *testing.T) {
	sig := Signals{Device: "agency-1", FormMillis: 12000, KeyIntervals: []int{120, 300, 90, 210, 160, 240}, PointerMoves: 40}
	if score, _ := Score(sig, 4, 4); action(score) != "challenge" {
		t.Errorf("four accounts on one phone should be challenged, got %s (score %d)", action(score), score)
	}
	if score, _ := Score(sig, 1, 1); action(score) != "allow" {
		t.Errorf("an ordinary person should be allowed, got %s (score %d)", action(score), score)
	}
}
