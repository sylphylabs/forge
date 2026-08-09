package diagnosis

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestHandler() http.Handler {
	reg := NewRegistry()
	reg.Register("app", staticProbe(map[string]string{"name": "svc", "version": "v1.2.3"}))
	reg.Register("governance/ratelimit", staticProbe(map[string]any{"*": 800}))
	reg.Register("failing", func(context.Context) (any, error) {
		return nil, errors.New("state unavailable")
	})
	reg.Register("panicking", func(context.Context) (any, error) { panic("boom") })
	reg.Register("unserializable", staticProbe(func() {}))
	return NewHandler(reg)
}

func TestHandlerServesAllProbes(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var dump map[string]struct {
		Value json.RawMessage `json:"value"`
		Error string          `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dump); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, rec.Body)
	}
	if len(dump) != 5 {
		t.Fatalf("dump has %d entries, want 5: %v", len(dump), rec.Body)
	}
	if e := dump["app"]; e.Error != "" || !strings.Contains(string(e.Value), "v1.2.3") {
		t.Fatalf("app entry = %+v", e)
	}
	if e := dump["failing"]; e.Error != "state unavailable" {
		t.Fatalf("failing entry error = %q", e.Error)
	}
	if e := dump["panicking"]; !strings.Contains(e.Error, "boom") {
		t.Fatalf("panicking entry error = %q", e.Error)
	}
	if e := dump["unserializable"]; !strings.Contains(e.Error, "not JSON-serializable") {
		t.Fatalf("unserializable entry error = %q", e.Error)
	}
}

func TestHandlerDumpIsDeterministic(t *testing.T) {
	h := newTestHandler()
	get := func() string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return rec.Body.String()
	}
	first := get()
	if second := get(); second != first {
		t.Fatalf("two dumps of identical state differ:\n%s\n%s", first, second)
	}
	if !strings.Contains(first, `"app":`) {
		t.Fatalf("dump missing app key: %s", first)
	}
}

func TestHandlerServesOneProbe(t *testing.T) {
	h := newTestHandler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var snapshot map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("single-probe response is not valid JSON: %v", err)
	}
	if snapshot["name"] != "svc" {
		t.Fatalf("snapshot = %v, want name svc", snapshot)
	}

	// Slash-containing names resolve: the whole remaining path is the name.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/governance/ratelimit", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("slash-named probe status = %d, want 200", rec.Code)
	}
}

func TestHandlerReportsFailuresAndUnknowns(t *testing.T) {
	h := newTestHandler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/failing", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("failing probe status = %d, want 500", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error response is not valid JSON: %v", err)
	}
	if body["error"] != "state unavailable" {
		t.Fatalf("error body = %v", body)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown probe status = %d, want 404", rec.Code)
	}
}

func TestHandlerRejectsNonGET(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", allow)
	}
}

// TestHandlerEndToEnd mounts the handler on a plain net/http mux the way an
// application would and exercises it over a real listener.
func TestHandlerEndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/debug/probes/", http.StripPrefix("/debug/probes", newTestHandler()))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/debug/probes/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var dump map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&dump); err != nil {
		t.Fatalf("dump is not valid JSON: %v", err)
	}
	if _, ok := dump["app"]; !ok {
		t.Fatalf("dump missing app probe: %v", dump)
	}

	resp, err = http.Get(srv.URL + "/debug/probes/governance/ratelimit")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("nested probe status = %d, want 200", resp.StatusCode)
	}
}
