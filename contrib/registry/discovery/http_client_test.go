package discovery

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDoJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want %q", r.Method, http.MethodPost)
		}
		if got := r.URL.Query()["appid"]; len(got) != 2 || got[0] != "one" || got[1] != "two" {
			t.Errorf("appid query = %v, want [one two]", got)
		}
		if got := r.URL.Query().Get("existing"); got != "value" {
			t.Errorf("existing query = %q, want value", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok"}`))
	}))
	t.Cleanup(server.Close)

	d := &Discovery{httpClient: server.Client()}
	var result discoveryCommonResp
	err := d.postJSON(t.Context(), server.URL+"?existing=value", url.Values{
		"appid": {"one", "two"},
	}, &result)
	if err != nil {
		t.Fatalf("postJSON() error = %v", err)
	}
	if result.Code != 0 || result.Message != "ok" {
		t.Fatalf("postJSON() result = %+v, want code 0 and message ok", result)
	}
}

func TestDoJSONHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	d := &Discovery{httpClient: server.Client()}
	err := d.getJSON(t.Context(), server.URL, nil, &discoveryCommonResp{})
	if err == nil || !strings.Contains(err.Error(), "503 Service Unavailable") {
		t.Fatalf("getJSON() error = %v, want HTTP status error", err)
	}
}

func TestDoJSONContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	d := &Discovery{httpClient: http.DefaultClient}
	err := d.getJSON(ctx, "http://127.0.0.1/", nil, &discoveryCommonResp{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("getJSON() error = %v, want context.Canceled", err)
	}
}

func TestDoJSONNoContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	d := &Discovery{httpClient: server.Client()}
	if err := d.getJSON(t.Context(), server.URL, nil, &discoveryCommonResp{}); err != nil {
		t.Fatalf("getJSON() error = %v", err)
	}
}
