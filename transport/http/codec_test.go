package http

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/httpbody"

	_ "github.com/sylphylabs/forge/encoding/protojson"
	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/internal/testdata/binding"
)

func TestDefaultRequestDecoder(t *testing.T) {
	var (
		bodyStr = `{"a":"1", "b": 2}`
		r, _    = http.NewRequest(http.MethodPost, "", io.NopCloser(bytes.NewBufferString(bodyStr)))
	)
	r.Header.Set("Content-Type", "application/json")

	v1 := &struct {
		A string `json:"a"`
		B int64  `json:"b"`
	}{}
	err := DefaultRequestDecoder(r, &v1)
	if err != nil {
		t.Fatal(err)
	}
	if v1.A != "1" {
		t.Errorf("expected %v, got %v", "1", v1.A)
	}
	if v1.B != int64(2) {
		t.Errorf("expected %v, got %v", 2, v1.B)
	}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bodyStr != string(data) {
		t.Errorf("expected %v, got %v", bodyStr, string(data))
	}
}

func BenchmarkDefaultRequestDecoder(b *testing.B) {
	body := []byte(`{"name":"forge"}`)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/protojson")
	var target binding.HelloRequest
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		target.Reset()
		if err := DefaultRequestDecoder(req, &target); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDefaultRequestVarsProto(t *testing.T) {
	srv := NewServer(WithTimeout(0))
	srv.Route("").GET("/hello/{name}", func(ctx Context) error {
		var request binding.HelloRequest
		if err := ctx.BindVars(&request); err != nil {
			return err
		}
		return ctx.String(http.StatusOK, request.GetName())
	})

	response := httptest.NewRecorder()
	srv.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/hello/forge", nil))
	if response.Code != http.StatusOK || response.Body.String() != "forge" {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestDefaultRequestDecoderEmptyBodyWithoutContentType(t *testing.T) {
	r, err := http.NewRequest(http.MethodPost, "", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	var target struct{}
	if err := DefaultRequestDecoder(r, &target); err != nil {
		t.Fatalf("empty body returned an error: %v", err)
	}
}

func TestDefaultRequestDecoderNonEmptyBodyRequiresContentType(t *testing.T) {
	r, err := http.NewRequest(http.MethodPost, "", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var target struct{}
	err = DefaultRequestDecoder(r, &target)
	if err == nil || !strings.Contains(err.Error(), "unregistered Content-Type") {
		t.Fatalf("non-empty body error = %v, want unregistered Content-Type", err)
	}
}

func TestDefaultRequestDecoderHTTPBody(t *testing.T) {
	const bodyStr = "raw file content"
	r, _ := http.NewRequest(http.MethodPost, "", io.NopCloser(bytes.NewBufferString(bodyStr)))
	r.Header.Set("Content-Type", "text/plain")

	var body *httpbody.HttpBody
	if err := DefaultRequestDecoder(r, &body); err != nil {
		t.Fatal(err)
	}
	if body.GetContentType() != "text/plain" {
		t.Errorf("expected %v, got %v", "text/plain", body.GetContentType())
	}
	if string(body.GetData()) != bodyStr {
		t.Errorf("expected %v, got %v", bodyStr, string(body.GetData()))
	}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != bodyStr {
		t.Errorf("expected request body reset to %q, got %q", bodyStr, string(data))
	}
}

func TestDefaultRequestDecoderProtoJSONMessageFieldPointer(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "", io.NopCloser(bytes.NewBufferString(`{"naming":"go"}`)))
	r.Header.Set("Content-Type", "application/protojson")

	var sub *binding.Sub
	if err := DefaultRequestDecoder(r, &sub); err != nil {
		t.Fatal(err)
	}
	if sub == nil {
		t.Fatal("expected message field to be allocated")
	}
	if sub.Name != "go" {
		t.Errorf("expected %v, got %v", "go", sub.Name)
	}
}

func TestDefaultRequestDecoderProtoJSONRejectsScalarField(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "", io.NopCloser(bytes.NewBufferString(`"forge"`)))
	r.Header.Set("Content-Type", "application/protojson")

	var name string
	err := DefaultRequestDecoder(r, &name)
	if err == nil {
		t.Fatal("expected scalar protojson body to fail")
	}
	if !strings.Contains(err.Error(), "want proto.Message") {
		t.Errorf("expected proto message type error, got %v", err)
	}
}

func TestDefaultResponseEncoderProtoJSONRejectsScalarField(t *testing.T) {
	w := &mockResponseWriter{StatusCode: http.StatusOK, header: make(http.Header)}
	r, _ := http.NewRequest(http.MethodGet, "", nil)
	r.Header.Set("Accept", "application/protojson")

	err := DefaultResponseEncoder(w, r, "forge")
	if err == nil {
		t.Fatal("expected scalar protojson response to fail")
	}
	if !strings.Contains(err.Error(), "want proto.Message") {
		t.Errorf("expected proto message type error, got %v", err)
	}
}

func TestDefaultResponseDecoderProtoJSONMessage(t *testing.T) {
	resp := &http.Response{
		Header:     http.Header{"Content-Type": []string{"application/protojson"}},
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(`{"naming":"go"}`)),
	}

	sub := new(binding.Sub)
	if err := DefaultResponseDecoder(context.TODO(), resp, sub); err != nil {
		t.Fatal(err)
	}
	if sub.Name != "go" {
		t.Errorf("expected %v, got %v", "go", sub.Name)
	}
}

func TestDefaultResponseDecoderProtoJSONRejectsScalarField(t *testing.T) {
	resp := &http.Response{
		Header:     http.Header{"Content-Type": []string{"application/protojson"}},
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(`"forge"`)),
	}

	var name string
	err := DefaultResponseDecoder(context.TODO(), resp, &name)
	if err == nil {
		t.Fatal("expected scalar protojson response to fail")
	}
	if !strings.Contains(err.Error(), "want proto.Message") {
		t.Errorf("expected proto message type error, got %v", err)
	}
}

type mockResponseWriter struct {
	StatusCode int
	Data       []byte
	header     http.Header
}

func (w *mockResponseWriter) Header() http.Header {
	return w.header
}

func (w *mockResponseWriter) Write(b []byte) (int, error) {
	w.Data = b
	return len(b), nil
}

func (w *mockResponseWriter) WriteHeader(statusCode int) {
	w.StatusCode = statusCode
}

func TestDefaultResponseEncoder(t *testing.T) {
	var (
		w    = &mockResponseWriter{StatusCode: 200, header: make(http.Header)}
		r, _ = http.NewRequest(http.MethodPost, "", nil)
		v    = &struct {
			A string `json:"a"`
			B int64  `json:"b"`
		}{
			A: "1",
			B: 2,
		}
	)
	r.Header.Set("Content-Type", "application/json")

	err := DefaultResponseEncoder(w, r, v)
	if err != nil {
		t.Fatal(err)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected %v, got %v", "application/json", w.Header().Get("Content-Type"))
	}
	if w.StatusCode != 200 {
		t.Errorf("expected %v, got %v", 200, w.StatusCode)
	}
	if w.Data == nil {
		t.Errorf("expected not nil, got %v", w.Data)
	}
}

func TestDefaultResponseEncoderHTTPBody(t *testing.T) {
	w := &mockResponseWriter{StatusCode: 200, header: make(http.Header)}
	r, _ := http.NewRequest(http.MethodGet, "", nil)
	body := &httpbody.HttpBody{
		ContentType: "application/octet-stream",
		Data:        []byte("raw response"),
	}

	if err := DefaultResponseEncoder(w, r, body); err != nil {
		t.Fatal(err)
	}
	if got := w.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("expected %v, got %v", "application/octet-stream", got)
	}
	if string(w.Data) != "raw response" {
		t.Errorf("expected %v, got %v", "raw response", string(w.Data))
	}
}

func TestDefaultErrorEncoder(t *testing.T) {
	var (
		w    = &mockResponseWriter{header: make(http.Header)}
		r, _ = http.NewRequest(http.MethodPost, "", nil)
		err  = errors.Of(errors.KindInternal)
	)
	r.Header.Set("Content-Type", "application/json")

	DefaultErrorEncoder(w, r, err)
	// An error has one representation whatever the request asked for.
	if w.Header().Get("Content-Type") != ProblemContentType {
		t.Errorf("expected %v, got %v", ProblemContentType, w.Header().Get("Content-Type"))
	}
	if w.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %v, want %v", w.StatusCode, http.StatusInternalServerError)
	}
	if w.Data == nil {
		t.Errorf("expected not nil, got %v", w.Data)
	}
}

func TestDefaultErrorEncoderRedirect(t *testing.T) {
	w := &mockResponseWriter{header: make(http.Header)}
	r, _ := http.NewRequest(http.MethodGet, "/test", nil)

	DefaultErrorEncoder(w, r, NewRedirect("/redirect", http.StatusTemporaryRedirect))

	if w.StatusCode != http.StatusTemporaryRedirect {
		t.Errorf("expected %v, got %v", http.StatusTemporaryRedirect, w.StatusCode)
	}
	if w.Header().Get("Location") != "/redirect" {
		t.Errorf("expected %v, got %v", "/redirect", w.Header().Get("Location"))
	}
}

// customRedirect is a user-defined Redirector: the interface is exported, so
// an implementation outside this package must redirect like the built-in one.
type customRedirect struct{}

func (customRedirect) Error() string { return "redirect to /custom" }

func (customRedirect) Redirect() (string, int) {
	return "/custom", http.StatusFound
}

func TestDefaultErrorEncoderCustomRedirector(t *testing.T) {
	w := &mockResponseWriter{header: make(http.Header)}
	r, _ := http.NewRequest(http.MethodGet, "/test", nil)

	DefaultErrorEncoder(w, r, customRedirect{})

	if w.StatusCode != http.StatusFound {
		t.Errorf("expected %v, got %v", http.StatusFound, w.StatusCode)
	}
	if w.Header().Get("Location") != "/custom" {
		t.Errorf("expected %v, got %v", "/custom", w.Header().Get("Location"))
	}
}

// An error response does not take part in content negotiation, so a codec that
// cannot marshal is no longer reachable from this path. What must hold instead
// is that an exotic Accept header changes nothing about the response.
func TestDefaultErrorEncoderIgnoresAccept(t *testing.T) {
	for _, accept := range []string{"", "application/json", "application/protojson", "application/mock"} {
		w := &mockResponseWriter{header: make(http.Header)}
		r, _ := http.NewRequest(http.MethodGet, "", nil)
		if accept != "" {
			r.Header.Set("Accept", accept)
		}

		DefaultErrorEncoder(w, r, errors.Of(errors.KindInternal).WithReason("MOCK").Msg("boom"))

		if got := w.Header().Get("Content-Type"); got != ProblemContentType {
			t.Errorf("Accept %q: content type = %v, want %v", accept, got, ProblemContentType)
		}
		if w.StatusCode != http.StatusInternalServerError {
			t.Errorf("Accept %q: status = %v, want %v", accept, w.StatusCode, http.StatusInternalServerError)
		}
		if !bytes.Contains(w.Data, []byte(`"reason":"MOCK"`)) {
			t.Errorf("Accept %q: body = %s, want it to carry the reason", accept, w.Data)
		}
	}
}

func TestDefaultResponseEncoderEncodeNil(t *testing.T) {
	var (
		w    = &mockResponseWriter{StatusCode: 204, header: make(http.Header)}
		r, _ = http.NewRequest(http.MethodPost, "", io.NopCloser(bytes.NewBufferString("<xml></xml>")))
	)
	r.Header.Set("Content-Type", "application/json")

	err := DefaultResponseEncoder(w, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w.Header().Get("Content-Type") != "" {
		t.Errorf("expected empty string, got %v", w.Header().Get("Content-Type"))
	}
	if w.StatusCode != 204 {
		t.Errorf("expected %v, got %v", 204, w.StatusCode)
	}
	if w.Data != nil {
		t.Errorf("expected nil, got %v", w.Data)
	}
}

func TestCodecForRequest(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "", io.NopCloser(bytes.NewBufferString("<xml></xml>")))
	r.Header.Set("Content-Type", "application/xml")
	c, ok := CodecForRequest(r, "Content-Type")
	if !ok {
		t.Fatalf("expected true, got %v", ok)
	}
	if c.Name() != "xml" {
		t.Errorf("expected %v, got %v", "xml", c.Name())
	}

	r, _ = http.NewRequest(http.MethodPost, "", io.NopCloser(bytes.NewBufferString(`{"a":"1", "b": 2}`)))
	r.Header.Set("Content-Type", "blablablabla")
	c, ok = CodecForRequest(r, "Content-Type")
	if ok {
		t.Fatalf("expected false, got %v", ok)
	}
	if c.Name() != "json" {
		t.Errorf("expected %v, got %v", "json", c.Name())
	}
}
