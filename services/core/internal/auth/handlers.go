package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/yatrax/yatrax/services/core/internal/httpx"
	"github.com/yatrax/yatrax/services/core/internal/metrics"
)

// Publisher sends domain events to the event bus.
type Publisher interface {
	Publish(ctx context.Context, topic, key string, v any)
}

type Service struct {
	DB         *pgxpool.Pool
	OTP        *OTPStore
	Signer     *Signer
	Events     Publisher
	RefreshTTL time.Duration
	Dev        bool
	Now        func() time.Time
}

var phoneRe = regexp.MustCompile(`^[6-9][0-9]{9}$`)

// normalizePhone accepts "9876543210", "+91 98765 43210" or "091-9876543210".
func normalizePhone(s string) (string, bool) {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	digits = strings.TrimPrefix(digits, "0")
	if len(digits) == 12 && strings.HasPrefix(digits, "91") {
		digits = digits[2:]
	}
	return digits, phoneRe.MatchString(digits)
}

func (s *Service) Routes(r chi.Router) {
	r.Post("/otp/request", s.requestOTP)
	r.Post("/otp/verify", s.verifyOTP)
	r.Post("/login", s.passwordLogin)
	r.Post("/refresh", s.refresh)
	r.Post("/logout", s.logout)
	if s.Dev {
		r.Get("/demo-accounts", s.demoAccounts)
	}
}

type User struct {
	ID          string `json:"id"`
	Role        string `json:"role"`
	FullName    string `json:"full_name"`
	Phone       string `json:"phone,omitempty"`
	EmployeeID  string `json:"employee_id,omitempty"`
	Language    string `json:"language"`
	HomeStation string `json:"home_station,omitempty"`
	Designation string `json:"designation,omitempty"`
	Zone        string `json:"zone,omitempty"`
	Division    string `json:"division,omitempty"`
	Station     string `json:"station,omitempty"`
}

type tokenResponse struct {
	AccessToken      string    `json:"access_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	User             User      `json:"user"`
}

func (s *Service) requestOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Phone string `json:"phone"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	phone, ok := normalizePhone(in.Phone)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "bad_phone", "Enter a 10-digit Indian mobile number")
		return
	}
	code, err := s.OTP.Issue(r.Context(), phone)
	if errors.Is(err, ErrOTPRateLimited) {
		httpx.Error(w, http.StatusTooManyRequests, "rate_limited", "Too many OTP requests. Try again in 10 minutes.")
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	// Mock SMS gateway: the message goes to the log instead of a phone.
	slog.Info("mock sms", "to", "+91"+phone, "text", "Your YatraX OTP is "+code+". Valid for 5 minutes.")
	resp := map[string]any{"sent": true, "expires_in": int(otpTTL.Seconds())}
	if s.Dev {
		resp["dev_otp"] = code
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Service) verifyOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Phone    string `json:"phone"`
		OTP      string `json:"otp"`
		FullName string `json:"full_name"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	phone, ok := normalizePhone(in.Phone)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "bad_phone", "Enter a 10-digit Indian mobile number")
		return
	}
	if err := s.OTP.Verify(r.Context(), phone, strings.TrimSpace(in.OTP)); errors.Is(err, ErrOTPInvalid) {
		metrics.Logins.WithLabelValues("otp", "failed").Inc()
		httpx.Error(w, http.StatusUnauthorized, "bad_otp", "That OTP is wrong or has expired")
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}

	ctx := r.Context()
	var id string
	err := s.DB.QueryRow(ctx, `SELECT id FROM users WHERE phone=$1 AND NOT disabled`, phone).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// First sign-in with this number: register a passenger account.
		name := strings.TrimSpace(in.FullName)
		if name == "" {
			name = "Passenger " + phone[6:]
		}
		err = s.DB.QueryRow(ctx,
			`INSERT INTO users (role, full_name, phone) VALUES ('passenger', $1, $2) RETURNING id`,
			name, phone).Scan(&id)
		if err == nil {
			s.audit(ctx, id, "user.registered", "user", id, map[string]any{"method": "otp"})
		}
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	s.issueAndRespond(w, r, id, "otp")
}

func (s *Service) passwordLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Identifier string `json:"identifier"` // employee ID or email
		Password   string `json:"password"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	ident := strings.TrimSpace(in.Identifier)
	var id string
	var hash *string
	err := s.DB.QueryRow(r.Context(),
		`SELECT id, password_hash FROM users
		  WHERE (upper(employee_id) = upper($1) OR lower(email) = lower($1)) AND NOT disabled`,
		ident).Scan(&id, &hash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		httpx.Internal(w, r, err)
		return
	}
	if err != nil || hash == nil || bcrypt.CompareHashAndPassword([]byte(*hash), []byte(in.Password)) != nil {
		metrics.Logins.WithLabelValues("password", "failed").Inc()
		httpx.Error(w, http.StatusUnauthorized, "bad_credentials", "Wrong employee ID or password")
		return
	}
	s.issueAndRespond(w, r, id, "password")
}

func (s *Service) refresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	now := s.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	defer tx.Rollback(ctx)

	var tokID, userID, familyID string
	var revoked *time.Time
	var expires time.Time
	err = tx.QueryRow(ctx,
		`SELECT id, user_id, family_id, revoked_at, expires_at FROM refresh_tokens WHERE token_hash=$1 FOR UPDATE`,
		hashToken(in.RefreshToken)).Scan(&tokID, &userID, &familyID, &revoked, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusUnauthorized, "bad_refresh", "Session expired. Please sign in again.")
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if revoked != nil {
		// A rotated token came back: assume it was stolen and end every session in the family.
		if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at=$2 WHERE family_id=$1 AND revoked_at IS NULL`, familyID, now); err != nil {
			httpx.Internal(w, r, err)
			return
		}
		s.audit(ctx, userID, "auth.refresh_reuse", "user", userID, map[string]any{"family": familyID})
		tx.Commit(ctx)
		httpx.Error(w, http.StatusUnauthorized, "bad_refresh", "Session expired. Please sign in again.")
		return
	}
	if now.After(expires) {
		httpx.Error(w, http.StatusUnauthorized, "bad_refresh", "Session expired. Please sign in again.")
		return
	}
	resp, err := s.issue(ctx, tx, userID, familyID, now)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at=$2,
		        replaced_by=(SELECT id FROM refresh_tokens WHERE token_hash=$3) WHERE id=$1`,
		tokID, now, hashToken(resp.RefreshToken)); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !httpx.Decode(w, r, &in) {
		return
	}
	if _, err := s.DB.Exec(r.Context(),
		`UPDATE refresh_tokens SET revoked_at=now()
		  WHERE family_id=(SELECT family_id FROM refresh_tokens WHERE token_hash=$1) AND revoked_at IS NULL`,
		hashToken(in.RefreshToken)); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Me returns the signed-in user's profile.
func (s *Service) Me(w http.ResponseWriter, r *http.Request) {
	u, err := s.loadUser(r.Context(), s.DB, ClaimsFrom(r.Context()).Subject)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusUnauthorized, "unknown_user", "Account not found")
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, u)
}

// demoAccounts lists one ready-made account per role so the login screens can offer them.
// Development only: it reveals the shared demo password.
func (s *Service) demoAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `
		SELECT u.role::text, u.full_name, coalesce(u.phone,''), coalesce(u.employee_id,''),
		       coalesce(p.designation,''), coalesce(p.station_code,'')
		  FROM users u LEFT JOIN staff_postings p ON p.user_id = u.id
		 WHERE u.is_demo ORDER BY array_position($1::text[], u.role::text)`, AllRoles)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	type demo struct {
		Role        string `json:"role"`
		FullName    string `json:"full_name"`
		Phone       string `json:"phone,omitempty"`
		EmployeeID  string `json:"employee_id,omitempty"`
		Password    string `json:"password,omitempty"`
		Designation string `json:"designation,omitempty"`
		Station     string `json:"station,omitempty"`
	}
	out := []demo{}
	for rows.Next() {
		var d demo
		if err := rows.Scan(&d.Role, &d.FullName, &d.Phone, &d.EmployeeID, &d.Designation, &d.Station); err != nil {
			httpx.Internal(w, r, err)
			return
		}
		if d.EmployeeID != "" {
			d.Password = DemoPassword
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

// DemoPassword is the password the seed gives every synthetic staff account.
const DemoPassword = "yatrax123"

func (s *Service) issueAndRespond(w http.ResponseWriter, r *http.Request, userID, method string) {
	ctx := r.Context()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	defer tx.Rollback(ctx)
	resp, err := s.issue(ctx, tx, userID, "", s.Now())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	metrics.Logins.WithLabelValues(method, "ok").Inc()
	s.audit(ctx, userID, "auth.login", "user", userID, map[string]any{"method": method})
	s.Events.Publish(ctx, "auth.events", userID, map[string]any{
		"type": "user.logged_in", "user_id": userID, "role": resp.User.Role, "method": method, "at": s.Now(),
	})
	httpx.JSON(w, http.StatusOK, resp)
}

// issue creates an access token and a new refresh token. An empty familyID starts a new family.
func (s *Service) issue(ctx context.Context, tx pgx.Tx, userID, familyID string, now time.Time) (*tokenResponse, error) {
	u, err := s.loadUser(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	access, accessExp, err := s.Signer.Issue(u.ID, u.Role, u.FullName, now)
	if err != nil {
		return nil, err
	}
	raw, hash, err := newRefreshToken()
	if err != nil {
		return nil, err
	}
	refreshExp := now.Add(s.RefreshTTL)
	if _, err := tx.Exec(ctx,
		`INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at)
		 VALUES ($1, coalesce(nullif($2,'')::uuid, gen_random_uuid()), $3, $4)`,
		userID, familyID, hash, refreshExp); err != nil {
		return nil, err
	}
	return &tokenResponse{
		AccessToken: access, AccessExpiresAt: accessExp,
		RefreshToken: raw, RefreshExpiresAt: refreshExp,
		User: *u,
	}, nil
}

type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Service) loadUser(ctx context.Context, q rowQuerier, id string) (*User, error) {
	var u User
	err := q.QueryRow(ctx, `
		SELECT u.id, u.role::text, u.full_name, coalesce(u.phone,''), coalesce(u.employee_id,''),
		       u.language, coalesce(u.home_station,''),
		       coalesce(p.designation,''), coalesce(p.zone_code,''), coalesce(d.name,''), coalesce(p.station_code,'')
		  FROM users u
		  LEFT JOIN staff_postings p ON p.user_id = u.id
		  LEFT JOIN divisions d ON d.id = p.division_id
		 WHERE u.id = $1 AND NOT u.disabled`, id).
		Scan(&u.ID, &u.Role, &u.FullName, &u.Phone, &u.EmployeeID, &u.Language, &u.HomeStation,
			&u.Designation, &u.Zone, &u.Division, &u.Station)
	return &u, err
}

func (s *Service) audit(ctx context.Context, actor, action, entity, entityID string, data map[string]any) {
	if _, err := s.DB.Exec(ctx,
		`INSERT INTO audit_log (actor_id, action, entity, entity_id, data) VALUES ($1,$2,$3,$4,$5)`,
		actor, action, entity, entityID, data); err != nil {
		slog.Warn("audit write failed", "action", action, "err", err)
	}
}
