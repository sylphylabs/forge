package generator

import (
	"strings"
	"testing"

	"github.com/sylphylabs/forge/cmd/internal/openapi/model"
)

// TestApplyThrowsResponsesRejectsDuplicateStatus locks the single-source
// guard: a declaration-produced status code colliding with a response already
// present on the operation fails generation instead of merging (ADR-0013
// fail g). The sylphy.openapi.v1 operation annotation deliberately has no
// responses field, so the guard is an internal invariant against any future
// path that seeds responses before declarations apply.
func TestApplyThrowsResponsesRejectsDuplicateStatus(t *testing.T) {
	g := &OpenAPIv3Generator{conf: testConfiguration()}
	d := &model.Document{Components: &model.Components{}}
	op := &model.Operation{
		Responses: []*model.NamedResponse{
			{Name: "404", Response: &model.Response{Description: "handwritten"}},
		},
	}
	err := g.applyThrowsResponses(d, op, []throwsResponseSpec{{code: "404", description: "declared"}})
	if err == nil || !strings.Contains(err.Error(), "single source") {
		t.Fatalf("applyThrowsResponses() error = %v, want the single-source guard", err)
	}
}

// TestSortOperationResponsesOrder locks the response ordering: numeric codes
// ascending, default last.
func TestSortOperationResponsesOrder(t *testing.T) {
	op := &model.Operation{
		Responses: []*model.NamedResponse{
			{Name: "default", Response: &model.Response{}},
			{Name: "500", Response: &model.Response{}},
			{Name: "200", Response: &model.Response{}},
			{Name: "400", Response: &model.Response{}},
		},
	}
	sortOperationResponses(op)
	want := []string{"200", "400", "500", "default"}
	for i, name := range want {
		if op.Responses[i].Name != name {
			t.Fatalf("response %d = %q, want %q", i, op.Responses[i].Name, name)
		}
	}
}

func testConfiguration() Configuration {
	errorSchemaName := DefaultErrorSchemaName
	return Configuration{ErrorSchemaName: &errorSchemaName}
}
