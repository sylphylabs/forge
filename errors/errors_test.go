package errors

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	errorapi "github.com/openkratos/api/errors/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type TestError struct{ message string }

func (e *TestError) Error() string { return e.message }

func TestErrors(t *testing.T) {
	var base *Error
	err := Newf(http.StatusBadRequest, "reason", "message")
	err2 := Newf(http.StatusBadRequest, "reason", "message")
	err3 := err.WithMetadata(map[string]string{
		"foo": "bar",
	})
	werr := fmt.Errorf("wrap %w", err)

	if errors.Is(err, new(Error)) {
		t.Errorf("should not be equal: %v", err)
	}
	if !errors.Is(werr, err) {
		t.Errorf("should be equal: %v", err)
	}
	if !errors.Is(werr, err2) {
		t.Errorf("should be equal: %v", err)
	}

	if !errors.As(err, &base) {
		t.Errorf("should be matches: %v", err)
	}
	if !IsBadRequest(err) {
		t.Errorf("should be matches: %v", err)
	}

	if reason := Reason(err); reason != err3.Reason {
		t.Errorf("got %s want: %s", reason, err)
	}

	if err3.Metadata["foo"] != "bar" {
		t.Error("not expected metadata")
	}

	gs := err.GRPCStatus()
	se := FromError(gs.Err())
	if se.Reason != "reason" {
		t.Errorf("got %+v want %+v", se, err)
	}

	gs2 := status.New(codes.InvalidArgument, "bad request")
	se2 := FromError(gs2.Err())
	// codes.InvalidArgument should convert to http.StatusBadRequest
	if se2.Code != http.StatusBadRequest {
		t.Errorf("convert code err, got %d want %d", UnknownCode, http.StatusBadRequest)
	}
	if FromError(nil) != nil {
		t.Errorf("FromError(nil) should be nil")
	}
	e := FromError(errors.New("test"))
	if !reflect.DeepEqual(e.Code, int32(UnknownCode)) {
		t.Errorf("no expect value: %v, but got: %v", e.Code, int32(UnknownCode))
	}
}

func TestIs(t *testing.T) {
	tests := []struct {
		name string
		e    *Error
		err  error
		want bool
	}{
		{
			name: "true",
			e:    New(404, "test", ""),
			err:  New(http.StatusNotFound, "test", ""),
			want: true,
		},
		{
			name: "false",
			e:    New(0, "test", ""),
			err:  errors.New("test"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if ok := tt.e.Is(tt.err); ok != tt.want {
				t.Errorf("Error.Error() = %v, want %v", ok, tt.want)
			}
		})
	}
}

func TestCause(t *testing.T) {
	testError := &TestError{message: "test"}
	err := BadRequest("foo", "bar").WithCause(testError)
	if !errors.Is(err, testError) {
		t.Fatalf("want %v but got %v", testError, err)
	}
	if te := new(TestError); errors.As(err, &te) {
		if te.message != testError.message {
			t.Fatalf("want %s but got %s", testError.message, te.message)
		}
	}
}

func TestStatusContract(t *testing.T) {
	want := New(409, "DOCUMENT_CONFLICT", "document changed").WithMetadata(map[string]string{
		"document": "documents/42",
	})

	wire, err := proto.Marshal(want)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}
	var status errorapi.Status
	if err := proto.Unmarshal(wire, &status); err != nil {
		t.Fatalf("proto.Unmarshal() error = %v", err)
	}
	if !proto.Equal(&status, &want.Status) {
		t.Fatalf("wire status = %v, want %v", &status, &want.Status)
	}
	grpcRoundTrip := FromError(want.GRPCStatus().Err())
	if !proto.Equal(&grpcRoundTrip.Status, &want.Status) {
		t.Fatalf("gRPC status = %v, want %v", &grpcRoundTrip.Status, &want.Status)
	}

	for name, marshal := range map[string]func(any) ([]byte, error){
		"encoding/json": json.Marshal,
		"protojson": func(v any) ([]byte, error) {
			return protojson.Marshal(v.(proto.Message))
		},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := marshal(want)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}
			if len(got) != 4 {
				t.Errorf("JSON body %s has %d fields, want 4", data, len(got))
			}
			if got["code"] != float64(409) {
				t.Errorf("JSON code = %v, want 409", got["code"])
			}
			if got["reason"] != "DOCUMENT_CONFLICT" {
				t.Errorf("JSON reason = %v, want DOCUMENT_CONFLICT", got["reason"])
			}
			if got["message"] != "document changed" {
				t.Errorf("JSON message = %v, want document changed", got["message"])
			}
			metadata, ok := got["metadata"].(map[string]any)
			if !ok || metadata["document"] != "documents/42" {
				t.Errorf("JSON metadata = %v, want document=documents/42", got["metadata"])
			}
		})
	}
}

func TestOther(t *testing.T) {
	err := Errorf(10001, "test code 10001", "message")
	// Code
	if !reflect.DeepEqual(Code(nil), 200) {
		t.Errorf("Code(nil) = %v, want %v", Code(nil), 200)
	}
	if !reflect.DeepEqual(Code(errors.New("test")), UnknownCode) {
		t.Errorf(`Code(errors.New("test")) = %v, want %v`, Code(nil), 200)
	}
	if !reflect.DeepEqual(Code(err), 10001) {
		t.Errorf(`Code(err) = %v, want %v`, Code(err), 10001)
	}
	// Reason
	if !reflect.DeepEqual(Reason(nil), UnknownReason) {
		t.Errorf(`Reason(nil) = %v, want %v`, Reason(nil), UnknownReason)
	}
	if !reflect.DeepEqual(Reason(errors.New("test")), UnknownReason) {
		t.Errorf(`Reason(errors.New("test")) = %v, want %v`, Reason(nil), UnknownReason)
	}
	if !reflect.DeepEqual(Reason(err), "test code 10001") {
		t.Errorf(`Reason(err) = %v, want %v`, Reason(err), "test code 10001")
	}
	// Clone
	err400 := Newf(http.StatusBadRequest, "BAD_REQUEST", "param invalid")
	err400.Metadata = map[string]string{
		"key1": "val1",
		"key2": "val2",
	}
	if cerr := Clone(err400); cerr == nil || cerr.Error() != err400.Error() {
		t.Errorf("Clone(err) = %v, want %v", Clone(err400), err400)
	}
	if cerr := Clone(nil); cerr != nil {
		t.Errorf("Clone(nil) = %v, want %v", Clone(err400), err400)
	}
}
