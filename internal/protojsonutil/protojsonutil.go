// Package protojsonutil centralizes the protojson marshal/unmarshal policy for
// packages that already carry a protobuf dependency (config, encoding/form).
// Keeping the option values in one place means a marshaling-policy change
// touches exactly one file instead of every call site.
//
// This package must never be imported by transport/http or by any package that
// a plain-JSON HTTP service links, because that would drag the protobuf runtime
// into services that have no proto types.  Only packages that are already
// unconditionally protobuf-dependent may import it.
package protojsonutil

import (
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Marshal serializes v as JSON.  When v implements proto.Message the output
// uses protojson with EmitUnpopulated so that zero-valued fields survive a
// round-trip through the config layer.  All other values fall back to
// encoding/json.
func Marshal(v any) ([]byte, error) {
	if m, ok := v.(proto.Message); ok {
		return protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(m)
	}
	return json.Marshal(v)
}

// Unmarshal deserializes data into v.  When v implements proto.Message the
// input is parsed with protojson with DiscardUnknown so that schema evolution
// (adding fields to a .proto) does not break existing config files.  All other
// values fall back to encoding/json.
func Unmarshal(data []byte, v any) error {
	if m, ok := v.(proto.Message); ok {
		return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(data, m)
	}
	return json.Unmarshal(data, v)
}
