package http_test

import (
	"testing"

	"google.golang.org/genproto/googleapis/api/httpbody"

	transporthttp "github.com/sylphylabs/forge/transport/http"
	// Importing the subpackage is what installs the schema runtime; an
	// application gets it through generated bindings, which import it too.
	_ "github.com/sylphylabs/forge/transport/http/transcoding"
)

// The schema runtime must reach the transport by import alone, with no
// registration call in application code.
func TestSchemaRuntimeIsInstalledByImport(t *testing.T) {
	// A raw HTTP body is only recognized when the runtime is linked, so it
	// stands in for the seam as a whole.
	if got := transporthttp.BodyContentType(rawBodyValue()); got != "application/octet-stream" {
		t.Fatalf("content type = %q; the schema runtime is not installed", got)
	}
}

func rawBodyValue() any {
	return &httpbody.HttpBody{}
}
