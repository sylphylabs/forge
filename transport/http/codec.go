package http

import (
	"bytes"
	"io"
	"net/http"

	"github.com/sylphylabs/forge/encoding"
	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/internal/httputil"
)

// These constants should not be referenced from any other code.
const (
	SupportPackageIsVersion5 = true
)

const defaultHTTPBodyContentType = "application/octet-stream"

// Redirector replies to the request with a redirect to url
// which may be a path relative to the request path.
type Redirector interface {
	error
	Redirect() (string, int)
}

// Request type net/http.
type Request = http.Request

// ResponseWriter type net/http.
type ResponseWriter = http.ResponseWriter

// Flusher type net/http
type Flusher = http.Flusher

// DecodeRequestFunc is decode request func.
type DecodeRequestFunc func(*http.Request, any) error

// EncodeResponseFunc is encode response func.
type EncodeResponseFunc func(http.ResponseWriter, *http.Request, any) error

// EncodeErrorFunc is encode error func.
type EncodeErrorFunc func(http.ResponseWriter, *http.Request, error)

// DefaultRequestVars decodes the request vars to object.
func DefaultRequestVars(r *http.Request, v any) error {
	if route, ok := routeFromRequest(r); ok && schemaOwns(v) {
		vars := make([]PathVar, 0, len(route.vars))
		for _, variable := range route.vars {
			vars = append(vars, PathVar{Name: variable.name, Value: r.PathValue(variable.name)})
		}
		if err := schema.BindPath(v, vars); err != nil {
			return ErrCodec.Msg(err.Error()).Wrap(err)
		}
		return nil
	}
	return bindQuery(requestVars(r), v)
}

// DefaultRequestQuery decodes the request vars to object.
func DefaultRequestQuery(r *http.Request, v any) error {
	if r.URL.RawQuery == "" {
		return nil
	}
	return bindQuery(r.URL.Query(), v)
}

// DefaultRequestDecoder decodes the request body to object.
func DefaultRequestDecoder(r *http.Request, v any) error {
	if body, ok := httpBody(v); ok {
		data, err := io.ReadAll(r.Body)
		r.Body = newReplayBody(data)
		if err != nil {
			return ErrCodec.Msg(err.Error()).Wrap(err)
		}
		body.SetContentType(r.Header.Get("Content-Type"))
		body.SetData(data)
		return nil
	}
	data, err := io.ReadAll(r.Body)
	r.Body = newReplayBody(data)
	if err != nil {
		return ErrCodec.Msg(err.Error()).Wrap(err)
	}
	if len(data) == 0 {
		return nil
	}
	codec, ok := CodecForRequest(r, "Content-Type")
	if !ok {
		return ErrCodec.Msgf("unregistered Content-Type: %s", r.Header.Get("Content-Type"))
	}
	if err = decodeWithCodec(codec, data, v); err != nil {
		return ErrCodec.Msgf("body unmarshal: %s", err.Error()).Wrap(err)
	}
	return nil
}

type replayBody struct {
	bytes.Reader
}

func newReplayBody(data []byte) *replayBody {
	body := new(replayBody)
	body.Reset(data)
	return body
}

func (*replayBody) Close() error { return nil }

// DefaultResponseEncoder encodes the object to the HTTP response.
func DefaultResponseEncoder(w http.ResponseWriter, r *http.Request, v any) error {
	if v == nil {
		return nil
	}
	if body, ok := httpBody(v); ok {
		contentType := body.GetContentType()
		if contentType == "" {
			contentType = defaultHTTPBodyContentType
		}
		w.Header().Set("Content-Type", contentType)
		_, err := w.Write(body.GetData())
		return err
	}
	if rd, ok := v.(Redirector); ok {
		url, code := rd.Redirect()
		http.Redirect(w, r, url, code)
		return nil
	}
	codec, _ := CodecForRequest(r, "Accept")
	data, err := encodeWithCodec(codec, v)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", httputil.ContentType(codec.Name()))
	_, err = w.Write(data)
	if err != nil {
		return err
	}
	return nil
}

// DefaultErrorEncoder encodes the error to the HTTP response.
//
// What it discloses is the error's public data; a cause never leaves the
// process. A server that needs a different representation supplies its own
// encoder.
func DefaultErrorEncoder(w http.ResponseWriter, r *http.Request, err error) {
	encodeError(w, r, err)
}

func encodeError(w http.ResponseWriter, r *http.Request, err error) {
	var rd Redirector
	if errors.As(err, &rd) {
		url, code := rd.Redirect()
		http.Redirect(w, r, url, code)
		return
	}
	public := errors.PublicOf(err)
	// An error has one representation regardless of what the request asked
	// for. Negotiating it produced two incompatible spellings of the same
	// value, and a client reading the one it did not expect lost the kind or
	// the reason without any error being raised.
	body, marshalErr := marshalProblem(public)
	if marshalErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", ProblemContentType)
	w.WriteHeader(StatusOf(public.Kind))
	_, _ = w.Write(body)
}

// CodecForRequest get encoding.Codec via http.Request
func CodecForRequest(r *http.Request, name string) (encoding.Codec, bool) {
	for _, accept := range r.Header[name] {
		codec := encoding.GetCodec(httputil.ContentSubtype(accept))
		if codec != nil {
			return codec, true
		}
	}
	return encoding.GetCodec("json"), false
}

func httpBody(v any) (RawBody, bool) {
	if !schemaOwns(v) {
		return nil, false
	}
	return schema.RawBody(v)
}

// encodeWithCodec encodes v with the codec the schema runtime prefers for it,
// falling back to the negotiated one for a plain Go value.
func encodeWithCodec(codec encoding.Codec, v any) ([]byte, error) {
	return schemaCodec(codec, v).Marshal(v)
}

func decodeWithCodec(codec encoding.Codec, data []byte, v any) error {
	return schemaCodec(codec, v).Unmarshal(data, schemaTarget(v))
}

// BodyContentType returns the content type carried by v or a binary default.
func BodyContentType(v any) string {
	if body, ok := httpBody(v); ok && body.GetContentType() != "" {
		return body.GetContentType()
	}
	return defaultHTTPBodyContentType
}
