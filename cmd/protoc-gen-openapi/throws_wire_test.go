package main

// This file is the full-chain example for method error declarations: one
// fixture travels the whole distance an application's contract travels in
// production.
//
//	proto declarations (service_throws + throws + buf.validate)
//	    → protoc-gen-openapi document
//	    → the same identities raised through the published Forge runtime
//	    → real HTTP responses via transport/http.DefaultErrorEncoder
//
// and the test closes the loop: everything the runtime puts on the wire —
// status code, media type, identity, body shape — must be exactly what the
// generated document promised for that operation. A drift on either side
// (a projection change in the runtime, a description or schema change in the
// generator) breaks the closure here.

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	highv3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"
	"google.golang.org/protobuf/types/descriptorpb"

	openapigen "github.com/sylphylabs/forge/cmd/internal/openapi/generator"
	forgeerrors "github.com/sylphylabs/forge/errors"
	forgehttp "github.com/sylphylabs/forge/transport/http"
)

// The runtime-side declarations. Identity is (domain, reason): these pairs are
// exactly what the fixture proto declares, spelled through errors.MustDefine
// the way generated *_errors.pb.go files spell them. MustDefine also registers
// the identity as contract, which is what lets PublicOf disclose it on the
// wire (ADR-0012).
var (
	errWireNotFound = forgeerrors.MustDefine(forgeerrors.KindNotFound, "test.v1", "FAILURE_REASON_NOT_FOUND")
	errWireDenied   = forgeerrors.MustDefine(forgeerrors.KindPermissionDenied, "test.v1", "FAILURE_REASON_DENIED")
	errWireStale    = forgeerrors.MustDefine(forgeerrors.KindInternal, "test.v1", "FAILURE_REASON_STALE")

	// The framework validation identity, the same (kind, domain, reason) that
	// contrib/middleware/validate declares as ErrValidation. The middleware is
	// not run here — the closure under test is identity → document, and the
	// identity is what the middleware stamps on every rejection.
	errWireValidation = forgeerrors.MustDefine(forgeerrors.KindInvalidArgument, forgeerrors.Domain, "VALIDATION_FAILED")
)

func TestThrowsDeclarationToWireClosure(t *testing.T) {
	// The application-side fixture: the shared throws service — service-level
	// FAILURE_REASON_DENIED, method-level NOT_FOUND / EXPIRED / STALE on
	// GetBook — with one buf.validate constraint added to the request so the
	// framework validation identity is documented on the 400 as well.
	file := throwsServiceFile(nil, func(file *descriptorpb.FileDescriptorProto) {
		file.Dependency = append(file.Dependency, "buf/validate/validate.proto")
		name := file.MessageType[0].Field[0] // GetBookRequest.name
		name.Options = new(descriptorpb.FieldOptions)
		appendUnknownField(name.Options, rawBytesField(bufValidateFieldRules, rawVarintField(25, 1)))
	})

	content, document := generateThrowsDocument(t, testConfig(), file)
	validateOpenAPI32(t, content)

	operation := findOperation(t, document, "/v1/books/{name}", "GET")
	problemProperties := propertyNames(t, findComponentSchema(t, document, openapigen.DefaultErrorSchemaName))

	// Each case is one declared identity raised the way a handler raises it,
	// carrying the optional public fields a real failure carries.
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name: "method-level declaration",
			err: errWireNotFound.
				Msg("book not found").
				Meta("resource", "shelves/1/books/2").
				WithTraceID("4bf92f3577b34da6a3ce929d0e0e4736"),
			wantStatus: 404,
		},
		{
			name:       "service-level declaration",
			err:        errWireDenied.Msg("reader is not allowed"),
			wantStatus: 403,
		},
		{
			name:       "declaration resolved through default_kind",
			err:        errWireStale.Msg("index is stale"),
			wantStatus: 500,
		},
		{
			name: "framework validation identity",
			// Mirrors what contrib/middleware/validate builds on rejection:
			// aggregated violations under the declared framework identity.
			err: func() error {
				var v forgeerrors.Violations
				v.Add("name", "value is required")
				return forgeerrors.FromError(v.Err(forgeerrors.KindInvalidArgument)).
					WithDomain(errWireValidation.Domain()).
					WithReason(errWireValidation.Reason()).
					Msg("validation failed")
			}(),
			wantStatus: 400,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Runtime side: the published runtime writes the error to a real
			// HTTP response, exactly as a Forge server does.
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest("GET", "/v1/books/1", nil)
			forgehttp.DefaultErrorEncoder(recorder, request, tt.err)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("wire status = %d, want %d", recorder.Code, tt.wantStatus)
			}

			// Closure 1: the wire status is documented on the operation. The
			// independent parser's response map cannot hold a duplicate code,
			// and generation itself fails on one (the single-source guard), so
			// presence here is presence exactly once.
			response := findOperationResponse(t, operation, strconv.Itoa(recorder.Code))

			// Closure 2: the wire Content-Type is the documented media type.
			if orderedmap.Len(response.Content) != 1 {
				t.Fatalf("documented media types = %d, want 1", orderedmap.Len(response.Content))
			}
			documentedPair := response.Content.First()
			if contentType := recorder.Header().Get("Content-Type"); contentType != documentedPair.Key() {
				t.Fatalf("wire Content-Type = %q, documented media type = %q", contentType, documentedPair.Key())
			}

			// Closure 3: the identity on the wire is listed in the documented
			// response's description.
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(recorder.Body.Bytes(), &wire); err != nil {
				t.Fatalf("decode wire body %q: %v", recorder.Body.String(), err)
			}
			for _, member := range []string{"reason", "domain"} {
				var value string
				if err := json.Unmarshal(wire[member], &value); err != nil {
					t.Fatalf("wire body has no string %q member: %v", member, err)
				}
				if !strings.Contains(response.Description, value) {
					t.Fatalf("documented %d description %q does not name the wire %s %q",
						recorder.Code, response.Description, member, value)
				}
			}

			// Closure 4: every member on the wire is a documented ForgeProblem
			// property, and the response schema is that shared component.
			assertSchemaReference(t, documentedPair.Value(), "#/components/schemas/"+openapigen.DefaultErrorSchemaName)
			for key := range wire {
				if !slices.Contains(problemProperties, key) {
					t.Fatalf("wire member %q is not a documented %s property %v",
						key, openapigen.DefaultErrorSchemaName, problemProperties)
				}
			}
		})
	}
}

// assertSchemaReference asserts a media type's schema is a reference to the
// given component.
func assertSchemaReference(t *testing.T, mediaType *highv3.MediaType, want string) {
	t.Helper()

	schema := mediaType.Schema
	if schema == nil || !schema.IsReference() {
		t.Fatal("documented schema is not a reference")
	}
	if schema.GetReference() != want {
		t.Fatalf("documented schema reference = %q, want %q", schema.GetReference(), want)
	}
}
