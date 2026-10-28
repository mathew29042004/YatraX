package ticket

import (
	"crypto/ed25519"
	"strings"
	"testing"
)

func signer() *Signer {
	priv := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	return &Signer{priv: priv, Pub: priv.Public().(ed25519.PublicKey)}
}

func TestSignAndVerify(t *testing.T) {
	s := signer()
	tok, err := s.Sign(Payload{PNR: "4123456789", Train: "12623", Pax: [][]string{{"Ananya K", "32F", "CNF", "S1/7"}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Verify(s.Pub, tok)
	if err != nil || p.PNR != "4123456789" || p.Pax[0][3] != "S1/7" {
		t.Fatalf("verify: %v %+v", err, p)
	}
}

func TestTamperingIsDetected(t *testing.T) {
	s := signer()
	tok, _ := s.Sign(Payload{PNR: "4123456789", Class: "SL"})
	parts := strings.Split(tok, ".")
	forged, _ := s.Sign(Payload{PNR: "4123456789", Class: "1A"})
	// Upgrade the class by swapping in another payload but keeping the old signature.
	swapped := parts[0] + "." + strings.Split(forged, ".")[1] + "." + parts[2]
	if _, err := Verify(s.Pub, swapped); err != ErrBadTicket {
		t.Error("a payload with someone else's signature must be rejected")
	}
	priv2 := ed25519.NewKeyFromSeed([]byte("0123456789abcdef0123456789abcdef"))
	if _, err := Verify(priv2.Public().(ed25519.PublicKey), tok); err != ErrBadTicket {
		t.Error("a ticket must not verify under a different key")
	}
	if _, err := Verify(s.Pub, "garbage"); err != ErrBadTicket {
		t.Error("garbage must be rejected")
	}
}
