// Package ticket signs e-ticket QR codes with Ed25519 so they can be checked
// offline: the TTE app only needs the public key, never a network connection.
//
// Token format: "YX1." + base64url(JSON payload) + "." + base64url(signature).
package ticket

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const prefix = "YX1"

// Payload is what the QR code carries. Field names are short to keep the QR small.
type Payload struct {
	PNR    string     `json:"p"`
	Train  string     `json:"t"`
	Date   string     `json:"d"` // boarding date
	From   string     `json:"f"`
	To     string     `json:"to"`
	Class  string     `json:"c"`
	Quota  string     `json:"q"`
	Pax    [][]string `json:"ps"` // [name, age+gender, status, coach/berth]
	Issued int64      `json:"i"`
}

type Signer struct {
	priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
}

const secretName = "ticket_ed25519_seed"

// Load reads the signing key from TICKET_SIGNING_SEED (hex), or from app_secrets,
// generating and storing one on first start. Production would use a KMS instead.
func Load(ctx context.Context, db *pgxpool.Pool) (*Signer, error) {
	var seed []byte
	if hexSeed := os.Getenv("TICKET_SIGNING_SEED"); hexSeed != "" {
		b, err := hex.DecodeString(hexSeed)
		if err != nil || len(b) != ed25519.SeedSize {
			return nil, errors.New("TICKET_SIGNING_SEED must be 64 hex characters")
		}
		seed = b
	} else {
		err := db.QueryRow(ctx, `SELECT value FROM app_secrets WHERE name = $1`, secretName).Scan(&seed)
		if errors.Is(err, pgx.ErrNoRows) {
			seed = make([]byte, ed25519.SeedSize)
			if _, err := rand.Read(seed); err != nil {
				return nil, err
			}
			// Two instances starting together: keep whichever key was stored first.
			if err := db.QueryRow(ctx, `INSERT INTO app_secrets (name, value) VALUES ($1, $2)
				ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING value`, secretName, seed).Scan(&seed); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		}
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return &Signer{priv: priv, Pub: priv.Public().(ed25519.PublicKey)}, nil
}

var b64 = base64.RawURLEncoding

func (s *Signer) Sign(p Payload) (string, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	signed := prefix + "." + b64.EncodeToString(body)
	return signed + "." + b64.EncodeToString(ed25519.Sign(s.priv, []byte(signed))), nil
}

// PublicKeyBase64 is what clients cache to verify tickets offline.
func (s *Signer) PublicKeyBase64() string { return base64.StdEncoding.EncodeToString(s.Pub) }

var ErrBadTicket = errors.New("ticket signature is not valid")

// Verify checks a token against a public key and returns its payload.
func Verify(pub ed25519.PublicKey, token string) (*Payload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != prefix {
		return nil, ErrBadTicket
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		return nil, ErrBadTicket
	}
	body, err := b64.DecodeString(parts[1])
	if err != nil {
		return nil, ErrBadTicket
	}
	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadTicket, err)
	}
	return &p, nil
}
