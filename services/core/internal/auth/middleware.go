package auth

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/yatrax/yatrax/services/core/internal/httpx"
)

type ctxKey struct{}

// ClaimsFrom returns the verified claims placed on the context by Authenticate.
func ClaimsFrom(ctx context.Context) *Claims {
	c, _ := ctx.Value(ctxKey{}).(*Claims)
	return c
}

// Authenticate requires a valid Bearer access token.
func (s *Signer) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			httpx.Error(w, http.StatusUnauthorized, "unauthenticated", "Sign in required")
			return
		}
		c, err := s.Parse(raw)
		if err != nil {
			httpx.Error(w, http.StatusUnauthorized, "bad_token", "Session expired. Please sign in again.")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, c)))
	})
}

// RequireRole allows only the listed roles. Admins are always allowed.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c := ClaimsFrom(r.Context())
			if c == nil || (c.Role != RoleAdmin && !slices.Contains(roles, c.Role)) {
				httpx.Error(w, http.StatusForbidden, "forbidden", "Your role cannot do this")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
