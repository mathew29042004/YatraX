// Package paygw is YatraX's mock payment gateway, shaped like Razorpay: merchants
// create orders over an authenticated API, customers pay on a hosted checkout page,
// and the gateway reports results through a signed redirect and signed webhooks
// (retried with backoff). Switchable modes make it fail, time out, send webhooks late
// or send them twice, so the booking saga can be tested against every failure.
//
// The client half lives here too, so core and the gateway share one definition of
// the wire format and signatures.
package paygw

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Order statuses.
const (
	OrderCreated = "created"
	OrderPaid    = "paid"
	OrderFailed  = "failed"
)

type Order struct {
	ID          string    `json:"id"`
	Amount      int       `json:"amount"` // paise
	Currency    string    `json:"currency"`
	Receipt     string    `json:"receipt"`
	Status      string    `json:"status"`
	CallbackURL string    `json:"callback_url"`
	CreatedAt   time.Time `json:"created_at"`
	Payments    []Payment `json:"payments,omitempty"`
}

type Payment struct {
	ID        string    `json:"id"`
	OrderID   string    `json:"order_id"`
	Method    string    `json:"method"`
	Status    string    `json:"status"` // captured, failed
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Refund struct {
	ID        string    `json:"id"`
	PaymentID string    `json:"payment_id"`
	Amount    int       `json:"amount"` // paise
	Receipt   string    `json:"receipt"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Event is the webhook body.
type Event struct {
	ID      string  `json:"id"`
	Type    string  `json:"event"` // payment.captured, payment.failed
	Payment Payment `json:"payment"`
	Amount  int     `json:"amount"`
	Created int64   `json:"created_at"`
}

// SignatureHeader carries the webhook body's HMAC.
const SignatureHeader = "X-Gateway-Signature"

func hmacHex(secret string, msg []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(msg)
	return hex.EncodeToString(m.Sum(nil))
}

// SignWebhook signs a webhook body with the shared webhook secret.
func SignWebhook(secret string, body []byte) string { return hmacHex(secret, body) }

// VerifyWebhook checks a webhook signature in constant time.
func VerifyWebhook(secret string, body []byte, sig string) bool {
	return hmac.Equal([]byte(SignWebhook(secret, body)), []byte(sig))
}

// SignCallback is the signature on the redirect back to the merchant, over
// "order_id|payment_id" with the merchant's key secret (Razorpay's scheme).
func SignCallback(keySecret, orderID, paymentID string) string {
	return hmacHex(keySecret, []byte(orderID+"|"+paymentID))
}

func VerifyCallback(keySecret, orderID, paymentID, sig string) bool {
	return hmac.Equal([]byte(SignCallback(keySecret, orderID, paymentID)), []byte(sig))
}

// Client is the merchant (core) side of the gateway API.
type Client struct {
	BaseURL   string // server-to-server URL, e.g. http://payments:8081
	PublicURL string // what browsers use, e.g. http://localhost:8081
	KeyID     string
	KeySecret string
	HTTP      *http.Client
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.KeyID, c.KeySecret)
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("payment gateway unreachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("payment gateway %s %s: HTTP %d", method, path, res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// CreateOrder registers an amount (in paise) to collect for a receipt (our booking id).
func (c *Client) CreateOrder(ctx context.Context, amountPaise int, receipt, callbackURL string) (*Order, error) {
	var o Order
	err := c.do(ctx, http.MethodPost, "/v1/orders", map[string]any{
		"amount": amountPaise, "currency": "INR", "receipt": receipt, "callback_url": callbackURL,
	}, &o)
	return &o, err
}

// GetOrder fetches an order with its payment attempts.
func (c *Client) GetOrder(ctx context.Context, id string) (*Order, error) {
	var o Order
	err := c.do(ctx, http.MethodGet, "/v1/orders/"+id, nil, &o)
	return &o, err
}

// Refund pays amountPaise back to the customer. receipt is the merchant's refund id;
// retrying with the same receipt returns the original refund instead of paying twice.
func (c *Client) Refund(ctx context.Context, paymentID string, amountPaise int, receipt string) (*Refund, error) {
	var r Refund
	err := c.do(ctx, http.MethodPost, "/v1/payments/"+paymentID+"/refund", map[string]any{"amount": amountPaise, "receipt": receipt}, &r)
	return &r, err
}

// CheckoutURL is the hosted payment page for an order.
func (c *Client) CheckoutURL(orderID string) string {
	return c.PublicURL + "/checkout/" + orderID
}
