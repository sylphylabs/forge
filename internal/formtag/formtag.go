// Package formtag holds the struct tag used to bind URL values.
//
// It is a leaf package so that a decoder needing no schema runtime can share
// the tag with encoding/form without importing it, and with it the Protobuf
// reflection that package carries.
package formtag

// Name is the struct tag URL-value binding reads.
//
// It can be replaced at link time:
//
//	go build "-ldflags=-X github.com/sylphylabs/forge/internal/formtag.Name=form"
var Name = "json"
