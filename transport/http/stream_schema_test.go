package http

import (
	"net/url"
	"testing"

	"github.com/sylphylabs/forge/encoding"
)

// fakeSchema records which operations the transport delegated.
type fakeSchema struct {
	owns        bool
	codecAsked  bool
	fieldDecode bool
}

func (f *fakeSchema) Owns(any) bool { return f.owns }
func (f *fakeSchema) Codec(negotiated encoding.Codec, _ any) encoding.Codec {
	f.codecAsked = true
	return negotiated
}
func (f *fakeSchema) BindPath(any, []PathVar) error    { return nil }
func (f *fakeSchema) BindValues(any, url.Values) error { return nil }
func (f *fakeSchema) RawBody(any) (RawBody, bool)      { return nil, false }
func (f *fakeSchema) Target(v any) any                 { return v }
func (f *fakeSchema) DecodeField(any, string, func(any) error) error {
	f.fieldDecode = true
	return nil
}

func withSchema(t *testing.T, s Schema) {
	t.Helper()
	saved := schema
	schema = s
	t.Cleanup(func() { schema = saved })
}

// Streaming frames take the same encode path as unary bodies, so a schema
// message must be spelled by its schema there too.
//
// The stream default is protojson, so the defect this guards against only
// appears when a client names a content type explicitly — which is exactly what
// a well-behaved client does.
func TestStreamHelpersConsultTheSchema(t *testing.T) {
	fake := &fakeSchema{owns: true}
	withSchema(t, fake)

	if _, err := marshalStreamMessage(new(struct{}), encoding.GetCodec("json")); err != nil {
		t.Fatalf("marshalStreamMessage: %v", err)
	}
	if !fake.codecAsked {
		t.Error("marshalStreamMessage did not consult the schema runtime")
	}

	fake.codecAsked = false
	if err := unmarshalStreamMessage([]byte(`{}`), new(struct{}), encoding.GetCodec("json")); err != nil {
		t.Fatalf("unmarshalStreamMessage: %v", err)
	}
	if !fake.codecAsked {
		t.Error("unmarshalStreamMessage did not consult the schema runtime")
	}
}

// A value the runtime does not own keeps the negotiated codec, which is what
// lets a plain JSON service work without a schema runtime at all.
func TestUnownedValueKeepsNegotiatedCodec(t *testing.T) {
	fake := &fakeSchema{owns: false}
	withSchema(t, fake)

	if got := schemaCodec(encoding.GetCodec("json"), new(struct{})); got.Name() != "json" {
		t.Errorf("codec = %q, want the negotiated one", got.Name())
	}
	if fake.codecAsked {
		t.Error("the runtime was asked about a value it does not own")
	}
}

// With no runtime linked at all, the transport still binds a plain Go value.
func TestNoSchemaRuntimeBindsPlainValues(t *testing.T) {
	withSchema(t, nil)

	var target struct {
		Name string `json:"name"`
	}
	if err := bindValues(url.Values{"name": {"forge"}}, &target); err != nil {
		t.Fatalf("bindValues() error = %v", err)
	}
	if target.Name != "forge" {
		t.Errorf("name = %q, want forge", target.Name)
	}
}
