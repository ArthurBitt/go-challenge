package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

type Role string

const (
	RoleInternal Role = "internal"
	RoleProvider Role = "provider"
)

type Principal struct {
	Role       Role
	ProviderID string
	Subject    string
}

type ctxKey struct{}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

type Verifier struct {
	verifier *oidc.IDTokenVerifier
}

func New(ctx context.Context, issuer, audience string) (*Verifier, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc provider: %w", err)
	}
	v := provider.Verifier(&oidc.Config{ClientID: audience})
	return &Verifier{verifier: v}, nil
}

type claims struct {
	ProviderID string `json:"provider_id"`
	Role       string `json:"role"`
	Azp        string `json:"azp"`
}

func (v *Verifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
			http.Error(w, `{"code":"UNAUTHORIZED","message":"missing bearer token"}`, http.StatusUnauthorized)
			return
		}
		raw := strings.TrimSpace(h[7:])
		tok, err := v.verifier.Verify(r.Context(), raw)
		if err != nil {
			http.Error(w, `{"code":"UNAUTHORIZED","message":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}
		var c claims
		if err := tok.Claims(&c); err != nil {
			http.Error(w, `{"code":"UNAUTHORIZED","message":"invalid claims"}`, http.StatusUnauthorized)
			return
		}
		p := Principal{Subject: tok.Subject}
		switch {
		case c.Role == "internal" || c.Azp == "internal-service":
			p.Role = RoleInternal
		case c.ProviderID != "":
			p.Role = RoleProvider
			p.ProviderID = c.ProviderID
		default:
			http.Error(w, `{"code":"FORBIDDEN","message":"unknown identity"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

func RequireInternal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := FromContext(r.Context())
		if !ok || p.Role != RoleInternal {
			http.Error(w, `{"code":"FORBIDDEN","message":"internal role required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func RequireProvider(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := FromContext(r.Context())
		if !ok || p.Role != RoleProvider {
			http.Error(w, `{"code":"FORBIDDEN","message":"provider role required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
