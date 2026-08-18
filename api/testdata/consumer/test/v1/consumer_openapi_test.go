package testv1

import (
	"testing"

	"google.golang.org/protobuf/proto"

	openapi "github.com/sylphylabs/forge/api/openapi/v1"
)

// TestOpenAPIAnnotations proves the sylphy.openapi.v1 vocabulary end to end
// through real protoc output: the annotations compile against an application
// proto and read back through proto.GetExtension exactly as a generator reads
// them.
func TestOpenAPIAnnotations(t *testing.T) {
	document, ok := proto.GetExtension(
		File_test_v1_consumer_proto.Options(), openapi.E_Document).(*openapi.Document)
	if !ok || document == nil {
		t.Fatal("document annotation is missing")
	}
	if document.GetTitle() != "Document API" || document.GetVersion() != "1.0.0" {
		t.Fatalf("document = title %q version %q", document.GetTitle(), document.GetVersion())
	}
	if len(document.GetServers()) != 1 || document.GetServers()[0].GetUrl() != "https://api.example.com" {
		t.Fatalf("document servers = %v", document.GetServers())
	}
	schemes := document.GetSecuritySchemes()
	if len(schemes) != 2 {
		t.Fatalf("security schemes = %d, want 2", len(schemes))
	}
	if schemes[0].GetName() != "bearer" || schemes[0].GetHttpBearer().GetBearerFormat() != "JWT" {
		t.Fatalf("bearer scheme = %v", schemes[0])
	}
	if schemes[1].GetName() != "api_key" || schemes[1].GetApiKeyHeader().GetHeader() != "X-Api-Key" {
		t.Fatalf("api key scheme = %v", schemes[1])
	}

	method := File_test_v1_consumer_proto.Services().ByName("DocumentService").Methods().ByName("GetDocument")
	operation, ok := proto.GetExtension(method.Options(), openapi.E_Operation).(*openapi.Operation)
	if !ok || operation == nil {
		t.Fatal("operation annotation is missing")
	}
	if operation.GetSummary() != "Get one document" {
		t.Fatalf("operation summary = %q", operation.GetSummary())
	}
	if tags := operation.GetTags(); len(tags) != 1 || tags[0] != "documents" {
		t.Fatalf("operation tags = %v", tags)
	}
	security := operation.GetSecurity()
	if len(security) != 1 || len(security[0].GetSchemes()) != 1 || security[0].GetSchemes()[0] != "bearer" {
		t.Fatalf("operation security = %v", security)
	}

	message := File_test_v1_consumer_proto.Messages().ByName("GetDocumentRequest")
	schema, ok := proto.GetExtension(message.Options(), openapi.E_Schema).(*openapi.Schema)
	if !ok || schema.GetDescription() != "Request to fetch one document." {
		t.Fatalf("schema annotation = %v", schema)
	}

	field, ok := proto.GetExtension(message.Fields().ByName("name").Options(), openapi.E_Field).(*openapi.Field)
	if !ok || field.GetDescription() != "Resource name of the document." || field.GetExample() != "documents/1" {
		t.Fatalf("field annotation = %v", field)
	}
}
