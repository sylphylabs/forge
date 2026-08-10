package http

import (
	"net/http"
	"net/url"

	"github.com/go-playground/form/v4"

	"github.com/sylphylabs/forge/internal/formtag"

	"github.com/sylphylabs/forge/errors"
)

// formDecoder binds URL values onto a plain Go value.
//
// It is the same decoder encoding/form uses, configured identically, but
// reached directly so that the transport does not import the schema-aware
// wrapper and with it the Protobuf runtime.
var formDecoder = newFormDecoder()

func newFormDecoder() *form.Decoder {
	d := form.NewDecoder()
	d.SetTagName(formtag.Name)
	return d
}

func bindQuery(vars url.Values, target any) error {
	if err := bindValues(vars, target); err != nil {
		return ErrCodec.Msg(err.Error()).Wrap(err)
	}
	return nil
}

func bindForm(req *http.Request, target any) error {
	if err := req.ParseForm(); err != nil {
		return err
	}
	if err := bindValues(req.Form, target); err != nil {
		return ErrCodec.Msg(err.Error()).Wrap(err)
	}
	return nil
}

// bindValues decodes URL values onto target.
//
// A schema message is bound by the schema runtime, which knows its declared
// fields. Anything else is decoded as a plain Go value, which needs no schema
// and therefore no Protobuf reflection.
func bindValues(values url.Values, target any) error {
	if schemaOwns(target) {
		return schema.BindValues(target, values)
	}
	return formDecoder.Decode(target, values)
}

// Sentinels for the failures this transport raises itself. Each use wraps the
// underlying error, so a handler can reach it with [errors.As].
var (
	// ErrCodec identifies a request or response that could not be encoded or
	// decoded.
	ErrCodec = errors.MustDefine(errors.KindInvalidArgument, errors.Domain, "CODEC")

	// ErrNodeNotFound identifies a call that found no usable node, so the
	// request never left the client.
	ErrNodeNotFound = errors.MustDefine(errors.KindUnavailable, errors.Domain, "NODE_NOT_FOUND")

	// ErrTranscoding identifies a request that could not be mapped between its
	// HTTP form and the RPC it targets.
	ErrTranscoding = errors.MustDefine(errors.KindInvalidArgument, errors.Domain, "HTTP_TRANSCODING")
)
