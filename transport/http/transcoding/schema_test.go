package transcoding_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/protobuf/types/known/durationpb"

	_ "github.com/sylphylabs/forge/encoding/json"
	transporthttp "github.com/sylphylabs/forge/transport/http"
	_ "github.com/sylphylabs/forge/transport/http/transcoding"
)

// A Protobuf message must be decoded by its schema even when the request says
// "application/json".
//
// encoding/json cannot read a message: a Duration, a Timestamp, any well-known
// type, and an int64 encoded as a string all have a JSON form that differs from
// their Go form. Worse, it fails *silently* — it sees well-formed JSON and a
// struct that happens not to match, reports no error, and leaves the field at
// its zero value. A caller would see a request accepted with its data missing.
func TestJSONContentTypeDecodesMessageBySchema(t *testing.T) {
	req := httptest.NewRequest("POST", "/x", bytes.NewBufferString(`{"retryDelay":"1.500s"}`))
	req.Header.Set("Content-Type", "application/json")

	msg := new(errdetails.RetryInfo)
	if err := transporthttp.DefaultRequestDecoder(req, msg); err != nil {
		t.Fatalf("decode error = %v", err)
	}
	if got := msg.GetRetryDelay().AsDuration().Seconds(); got != 1.5 {
		t.Errorf("retryDelay = %vs, want 1.5s; the field was dropped silently", got)
	}
}

// The response side must spell fields the same way, so a client can read back
// what it sent.
func TestJSONAcceptEncodesMessageBySchema(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()

	reply := &errdetails.RetryInfo{RetryDelay: durationpb.New(1500000000)}
	if err := transporthttp.DefaultResponseEncoder(w, req, reply); err != nil {
		t.Fatalf("encode error = %v", err)
	}
	if got, want := w.Body.String(), `{"retryDelay":"1.500s"}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

// Round-tripping is the property that matters: what a client sends must be what
// it reads back.
func TestMessageRoundTripsOverJSON(t *testing.T) {
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	sent := &errdetails.RetryInfo{RetryDelay: durationpb.New(1500000000)}
	if err := transporthttp.DefaultResponseEncoder(w, req, sent); err != nil {
		t.Fatalf("encode error = %v", err)
	}

	back := httptest.NewRequest("POST", "/x", bytes.NewReader(w.Body.Bytes()))
	back.Header.Set("Content-Type", "application/json")
	received := new(errdetails.RetryInfo)
	if err := transporthttp.DefaultRequestDecoder(back, received); err != nil {
		t.Fatalf("decode error = %v", err)
	}
	if sent.GetRetryDelay().AsDuration() != received.GetRetryDelay().AsDuration() {
		t.Errorf("round trip lost data: sent %v, received %v",
			sent.GetRetryDelay().AsDuration(), received.GetRetryDelay().AsDuration())
	}
}

// A plain Go value keeps using the requested codec: the schema runtime only
// claims targets it actually understands.
func TestPlainValueStillUsesRequestedCodec(t *testing.T) {
	req := httptest.NewRequest("POST", "/x", bytes.NewBufferString(`{"name":"forge"}`))
	req.Header.Set("Content-Type", "application/json")

	var target struct {
		Name string `json:"name"`
	}
	if err := transporthttp.DefaultRequestDecoder(req, &target); err != nil {
		t.Fatalf("decode error = %v", err)
	}
	if target.Name != "forge" {
		t.Errorf("name = %q, want forge", target.Name)
	}
}

// The client half must speak the same JSON as the server half.
//
// The two halves are separate code paths — the server encodes a reply and
// decodes a request body, the client encodes a request and decodes a reply — so
// fixing one and not the other leaves two Forge services unable to talk to each
// other over the same content type.
func TestClientAndServerAgreeOverJSON(t *testing.T) {
	sent := &errdetails.RetryInfo{RetryDelay: durationpb.New(1500000000)}

	// Client encodes a request; the server decodes it.
	requestBody, err := transporthttp.DefaultRequestEncoder(t.Context(), "application/json", sent)
	if err != nil {
		t.Fatalf("client encode: %v", err)
	}
	serverReq := httptest.NewRequest("POST", "/x", bytes.NewReader(requestBody))
	serverReq.Header.Set("Content-Type", "application/json")
	serverSaw := new(errdetails.RetryInfo)
	if err := transporthttp.DefaultRequestDecoder(serverReq, serverSaw); err != nil {
		t.Fatalf("server decode: %v", err)
	}
	if serverSaw.GetRetryDelay().AsDuration() != sent.GetRetryDelay().AsDuration() {
		t.Errorf("server saw %v, client sent %v",
			serverSaw.GetRetryDelay().AsDuration(), sent.GetRetryDelay().AsDuration())
	}

	// Server encodes a reply; the client decodes it.
	replyReq := httptest.NewRequest("GET", "/x", nil)
	replyReq.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	if err := transporthttp.DefaultResponseEncoder(w, replyReq, sent); err != nil {
		t.Fatalf("server encode: %v", err)
	}
	res := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(bytes.NewReader(w.Body.Bytes())),
	}
	clientSaw := new(errdetails.RetryInfo)
	if err := transporthttp.DefaultResponseDecoder(t.Context(), res, clientSaw); err != nil {
		t.Fatalf("client decode: %v", err)
	}
	if clientSaw.GetRetryDelay().AsDuration() != sent.GetRetryDelay().AsDuration() {
		t.Errorf("client saw %v, server sent %v",
			clientSaw.GetRetryDelay().AsDuration(), sent.GetRetryDelay().AsDuration())
	}
}
