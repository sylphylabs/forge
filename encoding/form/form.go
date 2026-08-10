package form

import (
	"net/url"
	"reflect"

	"github.com/go-playground/form/v4"
	"google.golang.org/protobuf/proto"

	"github.com/sylphylabs/forge/encoding"
	"github.com/sylphylabs/forge/internal/formtag"
)

const (
	// Name is form codec name
	Name = "x-www-form-urlencoded"
	// Null value string
	nullStr = "null"
)

var (
	encoder = form.NewEncoder()
	decoder = form.NewDecoder()
)

// tagName is the struct tag this package binds by. It lives in a leaf package
// so that transport/http can share it without importing this one.
var tagName = formtag.Name

func init() {
	decoder.SetTagName(tagName)
	encoder.SetTagName(tagName)
	encoding.RegisterCodec(codec{encoder: encoder, decoder: decoder})
}

type codec struct {
	encoder *form.Encoder
	decoder *form.Decoder
}

func (c codec) Marshal(v any) ([]byte, error) {
	var vs url.Values
	var err error
	if m, ok := v.(proto.Message); ok {
		vs, err = EncodeValues(m)
		if err != nil {
			return nil, err
		}
	} else {
		vs, err = c.encoder.Encode(v)
		if err != nil {
			return nil, err
		}
	}
	for k, v := range vs {
		if len(v) == 0 {
			delete(vs, k)
		}
	}
	return []byte(vs.Encode()), nil
}

func (c codec) Unmarshal(data []byte, v any) error {
	vs, err := url.ParseQuery(string(data))
	if err != nil {
		return err
	}
	return Unmarshal(vs, v)
}

// Unmarshal decodes URL values directly into v.
func Unmarshal(vs url.Values, v any) error {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			rv.Set(reflect.New(rv.Type().Elem()))
		}
		rv = rv.Elem()
	}
	// Check both the original value and the fully-dereferenced value so that
	// pointer-to-pointer types (e.g. **MyProto) are handled correctly: the
	// outer pointer layers are stripped by the loop above, so rv.Interface()
	// may satisfy proto.Message even when v itself does not.
	if m, ok := v.(proto.Message); ok {
		return DecodeValues(m, vs)
	}
	if m, ok := rv.Interface().(proto.Message); ok {
		return DecodeValues(m, vs)
	}

	return decoder.Decode(v, vs)
}

func (codec) Name() string {
	return Name
}
