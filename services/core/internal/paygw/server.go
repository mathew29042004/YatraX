package paygw

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Modes the gateway can run in.
const (
	ModeNormal    = "normal"    // pay succeeds (unless the customer picks failure), webhook sent at once
	ModeFail      = "fail"      // every payment is declined
	ModeTimeout   = "timeout"   // money is taken, but no redirect and no webhook ever arrive
	ModeLate      = "late"      // money is taken; no redirect; webhook arrives LateDelay later
	ModeDuplicate = "duplicate" // every webhook is delivered three times
)

var Modes = []string{ModeNormal, ModeFail, ModeTimeout, ModeLate, ModeDuplicate}

const LateDelay = 90 * time.Second

type Server struct {
	DB            *pgxpool.Pool
	KeyID         string
	KeySecret     string
	WebhookSecret string
	WebhookURL    string // core's webhook endpoint

	mu   sync.RWMutex
	mode string
}

// Migrate creates the gateway's own schema. It shares the Postgres server with
// core but none of its tables: core only ever talks to it over HTTP.
func (s *Server) Migrate(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS gateway;
		CREATE TABLE IF NOT EXISTS gateway.orders (
			id text PRIMARY KEY, amount int NOT NULL CHECK (amount > 0), currency text NOT NULL, receipt text NOT NULL,
			status text NOT NULL, callback_url text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now());
		CREATE TABLE IF NOT EXISTS gateway.payments (
			id text PRIMARY KEY, order_id text NOT NULL REFERENCES gateway.orders(id), method text NOT NULL,
			status text NOT NULL, error text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now());
		CREATE TABLE IF NOT EXISTS gateway.webhooks (
			id bigserial PRIMARY KEY, event_id text NOT NULL, order_id text NOT NULL, body bytea NOT NULL, attempts int NOT NULL DEFAULT 0,
			next_at timestamptz NOT NULL, delivered_at timestamptz, last_error text NOT NULL DEFAULT '');
		CREATE INDEX IF NOT EXISTS webhooks_due ON gateway.webhooks (next_at) WHERE delivered_at IS NULL;
		CREATE TABLE IF NOT EXISTS gateway.refunds (
			id text PRIMARY KEY, payment_id text NOT NULL REFERENCES gateway.payments(id), amount int NOT NULL CHECK (amount > 0),
			receipt text NOT NULL, status text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
			UNIQUE (payment_id, receipt));`)
	return err
}

func (s *Server) Mode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.mode == "" {
		return ModeNormal
	}
	return s.mode
}

func (s *Server) SetMode(m string) error {
	for _, ok := range Modes {
		if m == ok {
			s.mu.Lock()
			s.mode = m
			s.mu.Unlock()
			slog.Info("gateway mode changed", "mode", m)
			return nil
		}
	}
	return fmt.Errorf("unknown mode %q", m)
}

func newID(prefix string) string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 14)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return prefix + "_" + string(b)
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	r.Group(func(r chi.Router) {
		r.Use(s.basicAuth)
		r.Post("/v1/orders", s.createOrder)
		r.Get("/v1/orders/{id}", s.getOrder)
		r.Post("/v1/payments/{id}/refund", s.refund)
	})
	r.Get("/checkout/{id}", s.checkout)
	r.Post("/checkout/{id}/pay", s.pay)
	r.Get("/admin", s.adminPage)
	r.Get("/admin/mode", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"mode": s.Mode(), "modes": Modes})
	})
	r.Post("/admin/mode", s.setMode)
	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(id), []byte(s.KeyID)) != 1 ||
			subtle.ConstantTimeCompare([]byte(secret), []byte(s.KeySecret)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad API key"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Amount      int    `json:"amount"`
		Currency    string `json:"currency"`
		Receipt     string `json:"receipt"`
		CallbackURL string `json:"callback_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Amount <= 0 || in.Receipt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount and receipt are required"})
		return
	}
	if u, err := url.Parse(in.CallbackURL); in.CallbackURL != "" && (err != nil || (u.Scheme != "http" && u.Scheme != "https")) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "callback_url must be http(s)"})
		return
	}
	o := Order{ID: newID("order"), Amount: in.Amount, Currency: "INR", Receipt: in.Receipt, Status: OrderCreated,
		CallbackURL: in.CallbackURL, CreatedAt: time.Now()}
	if _, err := s.DB.Exec(r.Context(), `INSERT INTO gateway.orders (id, amount, currency, receipt, status, callback_url)
		VALUES ($1,$2,$3,$4,$5,$6)`, o.ID, o.Amount, o.Currency, o.Receipt, o.Status, o.CallbackURL); err != nil {
		slog.Error("create order", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) loadOrder(ctx context.Context, id string) (*Order, error) {
	var o Order
	err := s.DB.QueryRow(ctx, `SELECT id, amount, currency, receipt, status, callback_url, created_at FROM gateway.orders WHERE id = $1`, id).
		Scan(&o.ID, &o.Amount, &o.Currency, &o.Receipt, &o.Status, &o.CallbackURL, &o.CreatedAt)
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT id, order_id, method, status, error, created_at FROM gateway.payments WHERE order_id = $1 ORDER BY created_at`, id)
	if err != nil {
		return nil, err
	}
	o.Payments, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Payment, error) {
		var p Payment
		return p, r.Scan(&p.ID, &p.OrderID, &p.Method, &p.Status, &p.Error, &p.CreatedAt)
	})
	return &o, err
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	o, err := s.loadOrder(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such order"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func rupees(paise int) string { return strconv.Itoa(paise/100) + "." + fmt.Sprintf("%02d", paise%100) }

// refund returns money to the customer's payment method. The merchant's receipt
// (its own refund id) makes retries safe: the same receipt returns the same refund.
func (s *Server) refund(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Amount  int    `json:"amount"`
		Receipt string `json:"receipt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Amount <= 0 || in.Receipt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "amount and receipt are required"})
		return
	}
	paymentID := chi.URLParam(r, "id")
	var out Refund
	err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		var status string
		var captured int
		if err := tx.QueryRow(ctx, `SELECT p.status, o.amount FROM gateway.payments p JOIN gateway.orders o ON o.id = p.order_id
			WHERE p.id = $1 FOR UPDATE OF p`, paymentID).Scan(&status, &captured); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT id, payment_id, amount, receipt, status, created_at FROM gateway.refunds WHERE payment_id = $1 AND receipt = $2`,
			paymentID, in.Receipt).Scan(&out.ID, &out.PaymentID, &out.Amount, &out.Receipt, &out.Status, &out.CreatedAt)
		if err == nil {
			return nil // already refunded under this receipt
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if status != "captured" {
			return errRefundRejected
		}
		var refunded int
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(amount), 0) FROM gateway.refunds WHERE payment_id = $1`, paymentID).Scan(&refunded); err != nil {
			return err
		}
		if refunded+in.Amount > captured {
			return errRefundRejected
		}
		out = Refund{ID: newID("rfnd"), PaymentID: paymentID, Amount: in.Amount, Receipt: in.Receipt, Status: "processed", CreatedAt: time.Now()}
		_, err = tx.Exec(ctx, `INSERT INTO gateway.refunds (id, payment_id, amount, receipt, status) VALUES ($1,$2,$3,$4,$5)`,
			out.ID, out.PaymentID, out.Amount, out.Receipt, out.Status)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such payment"})
	case errors.Is(err, errRefundRejected):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "payment is not captured or the amount exceeds what is left"})
	case err != nil:
		slog.Error("refund", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
	default:
		slog.Info("refund", "payment", paymentID, "refund", out.ID, "amount", out.Amount)
		writeJSON(w, http.StatusOK, out)
	}
}

var errRefundRejected = errors.New("refund rejected")

func (s *Server) checkout(w http.ResponseWriter, r *http.Request) {
	o, err := s.loadOrder(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "No such order", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	checkoutTmpl.Execute(w, map[string]any{"Order": o, "Amount": rupees(o.Amount), "Mode": s.Mode()})
}

func (s *Server) pay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	o, err := s.loadOrder(ctx, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "No such order", http.StatusNotFound)
		return
	}
	if o.Status == OrderPaid {
		http.Error(w, "This order is already paid.", http.StatusConflict)
		return
	}
	method := r.FormValue("method")
	if method == "" {
		method = "upi"
	}
	mode := s.Mode()
	p := Payment{ID: newID("pay"), OrderID: o.ID, Method: method, Status: "captured", CreatedAt: time.Now()}
	if r.FormValue("outcome") == "failure" || mode == ModeFail {
		p.Status, p.Error = "failed", "Payment declined by the bank (simulated)"
	}
	orderStatus := OrderPaid
	if p.Status == "failed" {
		orderStatus = OrderFailed
	}
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO gateway.payments (id, order_id, method, status, error) VALUES ($1,$2,$3,$4,$5)`,
			p.ID, p.OrderID, p.Method, p.Status, p.Error); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE gateway.orders SET status = $2 WHERE id = $1`, o.ID, orderStatus); err != nil {
			return err
		}
		if mode == ModeTimeout {
			return nil // the money is taken but nobody is told
		}
		ev := Event{ID: newID("evt"), Type: "payment." + p.Status, Payment: p, Amount: o.Amount, Created: time.Now().Unix()}
		body, _ := json.Marshal(ev)
		at := time.Now()
		copies := 1
		switch mode {
		case ModeLate:
			at = at.Add(LateDelay)
		case ModeDuplicate:
			copies = 3
		}
		for range copies {
			if _, err := tx.Exec(ctx, `INSERT INTO gateway.webhooks (event_id, order_id, body, next_at) VALUES ($1, $2, $3, $4)`, ev.ID, o.ID, body, at); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("pay", "err", err)
		http.Error(w, "Payment could not be processed", http.StatusInternalServerError)
		return
	}
	slog.Info("payment", "order", o.ID, "payment", p.ID, "status", p.Status, "mode", mode)

	// In timeout and late modes the customer's browser never makes it back.
	if mode == ModeTimeout || mode == ModeLate || o.CallbackURL == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		pendingTmpl.Execute(w, map[string]any{"Order": o, "Amount": rupees(o.Amount), "Mode": mode})
		return
	}
	q := url.Values{"order_id": {o.ID}, "payment_id": {p.ID}, "status": {p.Status}}
	if p.Status == "captured" {
		q.Set("signature", SignCallback(s.KeySecret, o.ID, p.ID))
	}
	sep := "?"
	if u, _ := url.Parse(o.CallbackURL); u != nil && u.RawQuery != "" {
		sep = "&"
	}
	http.Redirect(w, r, o.CallbackURL+sep+q.Encode(), http.StatusSeeOther)
}

func (s *Server) setMode(w http.ResponseWriter, r *http.Request) {
	m := r.FormValue("mode")
	if m == "" {
		var in struct{ Mode string }
		json.NewDecoder(r.Body).Decode(&in)
		m = in.Mode
	}
	if err := s.SetMode(m); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mode": m})
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `
		SELECT o.id, o.amount, o.status, o.receipt, o.created_at,
		       (SELECT count(*) FROM gateway.webhooks h WHERE h.order_id = o.id AND h.delivered_at IS NULL)
		  FROM gateway.orders o ORDER BY o.created_at DESC LIMIT 20`)
	type row struct {
		ID, Status, Receipt, Amount string
		At                          time.Time
		Pending                     int
	}
	var list []row
	if err == nil {
		for rows.Next() {
			var x row
			var amt int
			if rows.Scan(&x.ID, &amt, &x.Status, &x.Receipt, &x.At, &x.Pending) == nil {
				x.Amount = rupees(amt)
				list = append(list, x)
			}
		}
		rows.Close()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	adminTmpl.Execute(w, map[string]any{"Mode": s.Mode(), "Modes": Modes, "Orders": list})
}

// DeliverWebhooks posts due webhooks to core, retrying failures with exponential
// backoff (2 s, 4 s, 8 s … up to 8 attempts).
func (s *Server) DeliverWebhooks(ctx context.Context) {
	client := &http.Client{Timeout: 5 * time.Second}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		rows, err := s.DB.Query(ctx, `SELECT id, body, attempts FROM gateway.webhooks
			WHERE delivered_at IS NULL AND attempts < 8 AND next_at <= now() ORDER BY next_at LIMIT 20`)
		if err != nil {
			slog.Warn("webhook poll", "err", err)
			continue
		}
		type due struct {
			id       int64
			body     []byte
			attempts int
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
			var d due
			return d, r.Scan(&d.id, &d.body, &d.attempts)
		})
		if err != nil {
			continue
		}
		for _, d := range list {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.WebhookURL, bytes.NewReader(d.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(SignatureHeader, SignWebhook(s.WebhookSecret, d.body))
			res, err := client.Do(req)
			if err == nil && res.StatusCode < 300 {
				res.Body.Close()
				s.DB.Exec(ctx, `UPDATE gateway.webhooks SET delivered_at = now(), attempts = attempts + 1 WHERE id = $1`, d.id)
				continue
			}
			msg := "unreachable"
			if err != nil {
				msg = err.Error()
			} else {
				msg = res.Status
				res.Body.Close()
			}
			backoff := time.Duration(1<<uint(d.attempts+1)) * time.Second
			s.DB.Exec(ctx, `UPDATE gateway.webhooks SET attempts = attempts + 1, next_at = now() + $2::interval, last_error = $3 WHERE id = $1`,
				d.id, fmt.Sprintf("%d seconds", int(backoff.Seconds())), msg)
			slog.Warn("webhook delivery failed; will retry", "id", d.id, "attempt", d.attempts+1, "err", msg)
		}
	}
}

const pageCSS = `
body{margin:0;font-family:Inter,system-ui,-apple-system,"Segoe UI",sans-serif;background:#f1f4f9;color:#0f172a}
.wrap{max-width:440px;margin:40px auto;padding:0 16px}
.card{background:#fff;border:1px solid #e2e8f0;border-radius:16px;box-shadow:0 8px 30px rgba(15,23,42,.08);overflow:hidden}
.head{background:#0b2545;color:#fff;padding:18px 20px;display:flex;justify-content:space-between;align-items:center}
.head small{opacity:.7}.amt{font-size:26px;font-weight:700}
.body{padding:20px}.method{display:flex;gap:10px;align-items:center;border:1px solid #e2e8f0;border-radius:10px;padding:12px;margin-bottom:8px;cursor:pointer}
.method input{accent-color:#0b2545}.hint{color:#64748b;font-size:12px}
button{width:100%;border:0;border-radius:10px;padding:13px;font-size:15px;font-weight:600;cursor:pointer;margin-top:8px}
.pay{background:#0b2545;color:#fff}.fail{background:#fff;color:#b91c1c;border:1px solid #fecaca}
.banner{background:#fff7ed;color:#9a3412;border:1px solid #fed7aa;border-radius:10px;padding:10px 12px;font-size:13px;margin-bottom:14px}
.foot{text-align:center;color:#94a3b8;font-size:12px;margin-top:14px}
table{width:100%;border-collapse:collapse;font-size:13px}td,th{padding:6px 4px;border-bottom:1px solid #e2e8f0;text-align:left}`

var checkoutTmpl = template.Must(template.New("checkout").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>YatraPay checkout</title><style>` + pageCSS + `</style></head><body>
<div class="wrap"><div class="card">
<div class="head"><div><div>YatraPay</div><small>Mock gateway · test mode</small></div><div class="amt">₹{{.Amount}}</div></div>
<div class="body">
{{if ne .Mode "normal"}}<div class="banner">Gateway mode: <b>{{.Mode}}</b>. This payment will behave accordingly.</div>{{end}}
{{if eq .Order.Status "paid"}}<p>This order is already paid.</p>{{else}}
<form method="post" action="/checkout/{{.Order.ID}}/pay">
<label class="method"><input type="radio" name="method" value="upi" checked><div><b>UPI</b><div class="hint">Test VPA success@yatrapay</div></div></label>
<label class="method"><input type="radio" name="method" value="card"><div><b>Card</b><div class="hint">Saved test card · Visa •••• 0000</div></div></label>
<label class="method"><input type="radio" name="method" value="netbanking"><div><b>Net banking</b><div class="hint">Test bank</div></div></label>
<label class="method"><input type="radio" name="method" value="wallet"><div><b>Wallet</b><div class="hint">Test wallet</div></div></label>
<button class="pay" name="outcome" value="success">Pay ₹{{.Amount}}</button>
<button class="fail" name="outcome" value="failure">Simulate a declined payment</button>
</form>{{end}}
<div class="foot">Order {{.Order.ID}} · No real money moves. No card or bank details are collected.</div>
</div></div></div></body></html>`))

var pendingTmpl = template.Must(template.New("pending").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>Payment processing</title><style>` + pageCSS + `</style></head><body>
<div class="wrap"><div class="card"><div class="head"><div>YatraPay</div><div class="amt">₹{{.Amount}}</div></div>
<div class="body"><h3>Waiting for your bank…</h3>
<p>Your bank has not confirmed this payment yet. You can close this page; the merchant will be told when the bank responds.</p>
<div class="banner">Simulated <b>{{.Mode}}</b> mode: the redirect back to YatraX was lost.</div>
<div class="foot">Order {{.Order.ID}}</div></div></div></div></body></html>`))

var adminTmpl = template.Must(template.New("admin").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>YatraPay admin</title><style>` + pageCSS + `
.wrap{max-width:760px}</style></head><body><div class="wrap"><div class="card"><div class="head"><div>YatraPay · gateway controls</div><small>mode: {{.Mode}}</small></div>
<div class="body"><form method="post" action="/admin/mode">
{{range .Modes}}<label class="method"><input type="radio" name="mode" value="{{.}}" {{if eq . $.Mode}}checked{{end}}><b>{{.}}</b></label>{{end}}
<button class="pay">Set mode</button></form>
<h3>Recent orders</h3><table><tr><th>Order</th><th>Booking</th><th>Amount</th><th>Status</th><th>Webhooks pending</th></tr>
{{range .Orders}}<tr><td>{{.ID}}</td><td>{{.Receipt}}</td><td>₹{{.Amount}}</td><td>{{.Status}}</td><td>{{.Pending}}</td></tr>{{end}}</table>
</div></div></div></body></html>`))
