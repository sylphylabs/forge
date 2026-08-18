package generator

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	forgeerrors "github.com/sylphylabs/forge/errors"

	"github.com/sylphylabs/forge/cmd/internal/openapi/model"
	"github.com/sylphylabs/forge/cmd/internal/throws"
)

// validateExtNamePrfx prefixes every buf.validate extension the analyzer
// treats as a validation constraint.
const validateExtNamePrfx = "buf.validate."

// throwsResponseSpec is one exact error response derived from declarations:
// a status code and a description listing every identity behind it.
type throwsResponseSpec struct {
	code        string
	description string
}

// hasValidationConstraints reports whether a request message carries any
// buf.validate constraint, on the message itself, any field or oneof, or any
// transitively reachable message field.
func hasValidationConstraints(a *throws.Analyzer, message protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool) (bool, error) {
	if seen[message.FullName()] {
		return false, nil
	}
	seen[message.FullName()] = true

	if constrained, err := hasValidateExtension(a, message.Options()); err != nil || constrained {
		return constrained, err
	}
	for i := 0; i < message.Oneofs().Len(); i++ {
		if constrained, err := hasValidateExtension(a, message.Oneofs().Get(i).Options()); err != nil || constrained {
			return constrained, err
		}
	}
	for i := 0; i < message.Fields().Len(); i++ {
		field := message.Fields().Get(i)
		if constrained, err := hasValidateExtension(a, field.Options()); err != nil || constrained {
			return constrained, err
		}
		nested := nestedMessage(field)
		if nested == nil {
			continue
		}
		if constrained, err := hasValidationConstraints(a, nested, seen); err != nil || constrained {
			return constrained, err
		}
	}
	return false, nil
}

// nestedMessage returns the message a field leads into for constraint
// discovery: the field's message type, or a map's value message type.
func nestedMessage(field protoreflect.FieldDescriptor) protoreflect.MessageDescriptor {
	if field.IsMap() {
		if value := field.MapValue(); value.Kind() == protoreflect.MessageKind {
			return value.Message()
		}
		return nil
	}
	if field.Kind() == protoreflect.MessageKind {
		return field.Message()
	}
	return nil
}

func hasValidateExtension(a *throws.Analyzer, options proto.Message) (bool, error) {
	resolved, err := a.Resolved(options)
	if err != nil {
		return false, err
	}
	if resolved == nil {
		return false, nil
	}
	found := false
	resolved.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if fd.IsExtension() && strings.HasPrefix(string(fd.FullName()), validateExtNamePrfx) {
			found = true
			return false
		}
		return true
	})
	return found, nil
}

// methodErrorResponses derives the exact error responses of one method: the
// union of service-level and method-level throws declarations, plus the
// framework validation identity when the request message carries buf.validate
// constraints and the validation_reason option is enabled.
func (g *OpenAPIv3Generator) methodErrorResponses(service *protogen.Service, method *protogen.Method) ([]throwsResponseSpec, error) {
	identities, err := g.throws.MethodDeclarations(service.Desc.Options(), method.Desc.Options())
	if err != nil {
		return nil, err
	}

	if g.validationReasonEnabled() {
		constrained, err := hasValidationConstraints(g.throws, method.Input.Desc, map[protoreflect.FullName]bool{})
		if err != nil {
			return nil, err
		}
		if constrained {
			status, err := throws.ProjectDeclaredStatus(forgeerrors.KindInvalidArgument.String())
			if err != nil {
				return nil, err
			}
			identities = append(identities, throws.Identity{
				Kind:   forgeerrors.KindInvalidArgument.String(),
				Domain: forgeerrors.Domain,
				Reason: "VALIDATION_FAILED",
				Status: status,
			})
		}
	}

	return groupIdentitiesByStatus(identities), nil
}

func (g *OpenAPIv3Generator) validationReasonEnabled() bool {
	return g.conf.ValidationReason == nil || *g.conf.ValidationReason
}

// groupIdentitiesByStatus folds identities into one response spec per status
// code. Each (kind, domain) pair becomes one description line listing its
// reasons; everything is sorted so the output is deterministic.
func groupIdentitiesByStatus(identities []throws.Identity) []throwsResponseSpec {
	if len(identities) == 0 {
		return nil
	}
	byStatus := map[int][]throws.Identity{}
	for _, identity := range identities {
		byStatus[identity.Status] = append(byStatus[identity.Status], identity)
	}
	statuses := make([]int, 0, len(byStatus))
	for status := range byStatus {
		statuses = append(statuses, status)
	}
	sort.Ints(statuses)

	specs := make([]throwsResponseSpec, 0, len(statuses))
	for _, status := range statuses {
		group := byStatus[status]
		sort.Slice(group, func(i, j int) bool {
			if group[i].Kind != group[j].Kind {
				return group[i].Kind < group[j].Kind
			}
			if group[i].Domain != group[j].Domain {
				return group[i].Domain < group[j].Domain
			}
			return group[i].Reason < group[j].Reason
		})
		var lines []string
		for i := 0; i < len(group); {
			j := i
			var reasons []string
			for ; j < len(group) && group[j].Kind == group[i].Kind && group[j].Domain == group[i].Domain; j++ {
				reasons = append(reasons, group[j].Reason)
			}
			lines = append(lines, fmt.Sprintf("%s (%s) — reasons: %s", group[i].Kind, group[i].Domain, strings.Join(reasons, ", ")))
			i = j
		}
		specs = append(specs, throwsResponseSpec{
			code:        strconv.Itoa(status),
			description: strings.Join(lines, "\n"),
		})
	}
	return specs
}

// applyThrowsResponses adds the derived error responses to an operation. A
// response already present on a status code the declarations also produce is
// a second source of truth for the same fact, so it fails generation instead
// of being merged or overwritten.
func (g *OpenAPIv3Generator) applyThrowsResponses(d *model.Document, op *model.Operation, specs []throwsResponseSpec) error {
	if len(specs) == 0 {
		return nil
	}
	existing := make(map[string]bool, len(op.Responses))
	for _, namedResponse := range op.Responses {
		existing[namedResponse.Name] = true
	}
	for _, spec := range specs {
		if existing[spec.code] {
			return fmt.Errorf("declared errors produce response %s, which the method also declares as a handwritten OpenAPI response; delete the handwritten response and keep the throws declaration as the single source", spec.code)
		}
		op.Responses = append(op.Responses, &model.NamedResponse{
			Name: spec.code,
			Response: &model.Response{
				Description: spec.description,
				Content:     g.forgeErrorContent(d),
			},
		})
	}
	return nil
}

// sortOperationResponses orders responses by status code with the default
// response last, so generated documents are stable however responses were
// accumulated.
func sortOperationResponses(op *model.Operation) {
	sort.SliceStable(op.Responses, func(i, j int) bool {
		return responseRank(op.Responses[i].Name) < responseRank(op.Responses[j].Name)
	})
}

// responseRank orders a response name: numeric codes ascending, then any
// non-numeric name, with default last.
func responseRank(name string) int {
	if name == defaultResponseName {
		return 1 << 20
	}
	if code, err := strconv.Atoi(name); err == nil {
		return code
	}
	return 1<<20 - 1
}
