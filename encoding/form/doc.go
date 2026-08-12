// Package form provides the URL-encoded form [encoding.Codec], handling
// both plain Go structs (via go-playground/form) and Protobuf messages
// (field-name aware, honoring json_name). Importing this package registers
// the codec under the name "x-www-form-urlencoded".
package form
