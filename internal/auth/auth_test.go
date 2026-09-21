package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireInternalWithoutPrincipal(t *testing.T) {
	h := RequireInternal(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d", rr.Code)
	}
}

func TestRequireProviderWithoutPrincipal(t *testing.T) {
	h := RequireProvider(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("got %d", rr.Code)
	}
}
