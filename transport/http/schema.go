package http

import (
	"net/url"

	"github.com/sylphylabs/forge/encoding"
)

// This file defines the seam through which schema-aware behaviour reaches the
// HTTP transport.
//
// The transport itself speaks bytes and Go values. Everything that needs a
// schema — binding a path variable onto a declared field, encoding an error as
// the generated message, decoding one frame into a named sub-message — lives in
// transport/http/transcoding, which installs itself here during
// initialization.
//
// The seam exists so that a service serving plain JSON does not link a schema
// runtime it never calls. The linker drops a package nothing references; a
// build tag or a runtime flag would not, because the import would remain. An
// application gets the schema behaviour by importing the subpackage, which
// generated code does on its behalf.

// PathVar is one path variable extracted from a route.
type PathVar struct {
	// Name is the variable's name in the path template.
	Name string
	// Value is the value captured from the request path.
	Value string
}

// RawBody is the transport's view of a message carrying a raw payload and its
// content type, without naming the generated type that implements it.
type RawBody interface {
	GetContentType() string
	GetData() []byte
	SetContentType(string)
	SetData([]byte)
}

// Schema is the schema runtime the HTTP transport delegates to.
//
// Every operation begins with the same question — does this runtime own this
// value? — so the interface asks it once, in [Schema.Owns], and the remaining
// methods assume the answer was yes. The transport falls back to treating a
// value it does not own as a plain Go value.
//
// Collapsing the question into one method is what keeps the interface from
// growing a field per capability: a new schema-aware behaviour adds a method
// that may assume ownership, not another way to ask about it.
type Schema interface {
	// Owns reports whether v is a value this runtime understands.
	Owns(v any) bool

	// BindPath binds path variables onto v.
	BindPath(v any, vars []PathVar) error
	// BindValues binds URL values onto v.
	BindValues(v any, values url.Values) error

	// Codec returns the codec to use for v, given the one the request
	// negotiated. Returning that codec unchanged accepts the negotiated choice.
	Codec(negotiated encoding.Codec, v any) encoding.Codec

	// RawBody returns the raw payload carrier v represents, if it is one.
	RawBody(v any) (RawBody, bool)

	// Target returns the value a codec should decode into.
	//
	// A pointer to a nil message pointer is allocated here, so that decoding
	// into one does not silently discard the body. Returning v unchanged is
	// correct for everything else.
	Target(v any) any

	// DecodeField decodes one frame into the named field of v, using read to
	// obtain the frame into a freshly allocated sub-message.
	DecodeField(v any, field string, read func(target any) error) error
}

// schema is the installed runtime. A nil value means none was linked.
var schema Schema

// RegisterSchema installs the schema runtime used by generated code.
//
// It is called by transport/http/transcoding during initialization. An
// application does not call it directly; importing the subpackage is what
// enables the behaviour.
func RegisterSchema(s Schema) { schema = s }

// schemaOwns reports whether a schema runtime is linked and owns v.
//
// Every delegation goes through it, so "no runtime linked" and "not my value"
// stay one case at the call site rather than two checks repeated everywhere.
func schemaOwns(v any) bool { return schema != nil && schema.Owns(v) }

// schemaCodec returns the codec to use for v, honouring the negotiated one for
// any value the schema runtime does not own.
func schemaCodec(negotiated encoding.Codec, v any) encoding.Codec {
	if !schemaOwns(v) {
		return negotiated
	}
	return schema.Codec(negotiated, v)
}

// schemaTarget returns the value a codec should decode into.
func schemaTarget(v any) any {
	if !schemaOwns(v) {
		return v
	}
	return schema.Target(v)
}
