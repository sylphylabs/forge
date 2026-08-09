package healthz

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type staticHealthzer bool

func (h staticHealthzer) Healthz() bool { return bool(h) }

func TestNewHandler(t *testing.T) {
	tests := []struct {
		name     string
		healthy  bool
		wantCode int
		wantBody string
	}{
		{name: "ready", healthy: true, wantCode: http.StatusOK, wantBody: "ok"},
		{name: "not ready", healthy: false, wantCode: http.StatusServiceUnavailable, wantBody: "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			NewHandler(staticHealthzer(tt.healthy)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
				t.Errorf("content type = %q", ct)
			}
		})
	}
}
