package form

import (
	"math"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/sylphylabs/forge/internal/testdata/complex"
)

func TestEncodeValues(t *testing.T) {
	in := &complex.Complex{
		Id:          2233,
		NoOne:       "2233",
		Simple:      &complex.Simple{Component: "5566"},
		Strings:     []string{"3344", "5566"},
		B:           true,
		Sex:         complex.Sex_woman,
		Age:         18,
		A:           19,
		Count:       3,
		Price:       11.23,
		D:           22.22,
		Byte:        []byte("123"),
		Map:         map[string]string{"kratos": "https://go-kratos.dev/", "kratos_start": "https://go-kratos.dev/docs/getting-started/start/"},
		MapInt64Key: map[int64]string{1: "kratos", 2: "go-zero"},

		Timestamp: timestamppb.New(time.Date(1970, 1, 1, 0, 0, 20, 2, time.Local)),
		Duration:  &durationpb.Duration{Seconds: 120, Nanos: 22},
		Field:     &fieldmaskpb.FieldMask{Paths: []string{"1", "2"}},
		Double:    &wrapperspb.DoubleValue{Value: 12.33},
		Float:     &wrapperspb.FloatValue{Value: 12.34},
		Int64:     &wrapperspb.Int64Value{Value: 64},
		Int32:     &wrapperspb.Int32Value{Value: 32},
		Uint64:    &wrapperspb.UInt64Value{Value: 64},
		Uint32:    &wrapperspb.UInt32Value{Value: 32},
		Bool:      &wrapperspb.BoolValue{Value: false},
		String_:   &wrapperspb.StringValue{Value: "go-kratos"},
		Bytes:     &wrapperspb.BytesValue{Value: []byte("123")},
	}
	query, err := EncodeValues(in)
	if err != nil {
		t.Fatal(err)
	}
	want := "a=19&age=18&b=true&bool=false&byte=MTIz&bytes=MTIz&count=3&d=22.22&double=12.33&duration=2m0.000000022s&field=1%2C2&float=12.34&id=2233&int32=32&int64=64&map%5Bkratos%5D=https%3A%2F%2Fgo-kratos.dev%2F&map%5Bkratos_start%5D=https%3A%2F%2Fgo-kratos.dev%2Fdocs%2Fgetting-started%2Fstart%2F&map_int64_key%5B1%5D=kratos&map_int64_key%5B2%5D=go-zero&numberOne=2233&price=11.23&sex=woman&string=go-kratos&strings=3344&strings=5566&timestamp=1970-01-01T00%3A00%3A20.000000002Z&uint32=32&uint64=64&very_simple.component=5566" // nolint:lll
	if got := query.Encode(); want != got {
		t.Errorf("\nwant: %s, \ngot: %s", want, got)
	}
}

func TestEncodeValuesUsesProtoJSONScalarText(t *testing.T) {
	mask := &fieldmaskpb.FieldMask{Paths: []string{"foo_bar"}}
	query, err := EncodeValues(&complex.Complex{
		D:     math.Inf(1),
		Byte:  []byte{0xfb, 0xff},
		Field: mask,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := query.Get("d"); got != "Infinity" {
		t.Fatalf("double query value = %q, want Infinity", got)
	}
	if got := query.Get("byte"); got != "+/8=" {
		t.Fatalf("bytes query value = %q, want standard base64", got)
	}
	if got := query.Get("field"); got != "fooBar" {
		t.Fatalf("field mask query value = %q, want fooBar", got)
	}
	if got := mask.Paths[0]; got != "foo_bar" {
		t.Fatalf("EncodeValues mutated field mask to %q", got)
	}
}

func TestEncodeValuesOmitsFieldsBeforeEncoding(t *testing.T) {
	query, err := EncodeValuesExcept(&complex.Complex{
		Id:     2233,
		Age:    18,
		Simple: &complex.Simple{Component: "hidden"},
	}, "id", "very_simple.component")
	if err != nil {
		t.Fatal(err)
	}
	if got := query.Encode(); got != "age=18" {
		t.Fatalf("encoded query = %q, want age=18", got)
	}
}

func TestJsonCamelCase(t *testing.T) {
	tests := []struct {
		camelCase string
		snakeCase string
	}{
		{
			"userId", "user_id",
		},
		{
			"user", "user",
		},
		{
			"userIdAndUsername", "user_id_and_username",
		},
		{
			"", "",
		},
	}
	for _, test := range tests {
		t.Run(test.snakeCase, func(t *testing.T) {
			camel := jsonCamelCase(test.snakeCase)
			if camel != test.camelCase {
				t.Errorf("want: %s, got: %s", test.camelCase, camel)
			}
		})
	}
}

func TestIsASCIILower(t *testing.T) {
	tests := []struct {
		b     byte
		lower bool
	}{
		{
			'A', false,
		},
		{
			'a', true,
		},
		{
			',', false,
		},
		{
			'1', false,
		},
		{
			' ', false,
		},
	}
	for _, test := range tests {
		t.Run(string(test.b), func(t *testing.T) {
			lower := isASCIILower(test.b)
			if test.lower != lower {
				t.Errorf("'%s' is not ascii lower", string(test.b))
			}
		})
	}
}
