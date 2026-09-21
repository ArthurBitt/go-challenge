package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiveNoAuth(t *testing.T) {
	api := &API{}
	rr := httptest.NewRecorder()
	api.live(rr, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if rr.Code != 200 {
		t.Fatalf("code %d", rr.Code)
	}
}
