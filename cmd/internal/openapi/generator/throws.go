package generator

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/pluginpb"

	v3 "github.com/google/gnostic/openapiv3"
	errorapi "github.com/sylphylabs/forge/api/errors/v1"
	forgeerrors "github.com/sylphylabs/forge/errors"
	forgehttp "github.com/sylphylabs/forge/transport/http"
)

// The extension full names the analyzer resolves dynamically. The plugin
// compiles against the published Forge API module, so these are matched by
// name against the descriptors the build supplies rather than by generated
// extension types: the descriptors in the CodeGeneratorRequest are the
// authority on what the application declared.
const (
	throwsMarkerName    = "sylphy.errors.v1.throws"
	kindExtensionName   = "sylphy.errors.v1.kind"
	defaultKindExtName  = "sylphy.errors.v1.default_kind"
	validateExtNamePrfx = "buf.validate."
)

// statusOf projects an error Kind onto an HTTP status code. It is the same
// projection the Forge runtime applies when it encodes an error, taken
// directly from the published runtime so the documentation cannot drift from
// the wire behavior. It is a variable only so tests can prove the 4xx/5xx
// guard fires on a projection that leaves the error range.
var statusOf = forgehttp.StatusOf

// throwsIdentity is one declared error identity a method can produce, joined
// with the HTTP status its Kind projects to.
type throwsIdentity struct {
	kind   string // wire name of the Kind, e.g. "NOT_FOUND"
	domain string // proto package of the declaring enum
	reason string // enum value name
	status int

	// dedupeKey identifies the declaration for duplicate detection. It is the
	// enum value full name for declared reasons and empty for the framework
	// validation identity, which is merged in exactly once.
	dedupeKey string
}

// throwsResponseSpec is one exact error response derived from declarations:
// a status code and a description listing every identity behind it.
type throwsResponseSpec struct {
	code        string
	description string
}

// throwsAnalyzer resolves method error declarations from the descriptor set
// of a CodeGeneratorRequest.
//
// The plugin depends on the published Forge API module, which predates the
// throws marker, so the marker and the application's marked extension fields
// are resolved dynamically: options are re-unmarshaled against a type
// registry built from the request's own descriptors. Extensions the registry
// cannot resolve stay unknown and are ignored; everything it can resolve is
// visited, so a marked declaration is never silently dropped.
type throwsAnalyzer struct {
	pool  *protoregistry.Files
	types *dynamicpb.Types
}

func newThrowsAnalyzer(request *pluginpb.CodeGeneratorRequest) (*throwsAnalyzer, error) {
	pool, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{File: request.GetProtoFile()})
	if err != nil {
		return nil, fmt.Errorf("build descriptor pool for error declarations: %w", err)
	}
	return &throwsAnalyzer{pool: pool, types: dynamicpb.NewTypes(pool)}, nil
}

// resolved re-unmarshals an options message against the request's descriptor
// pool so extension fields that arrived as unknown bytes become inspectable.
func (a *throwsAnalyzer) resolved(options proto.Message) (protoreflect.Message, error) {
	if options == nil {
		return nil, nil
	}
	raw, err := proto.Marshal(options)
	if err != nil {
		return nil, fmt.Errorf("re-encode options: %w", err)
	}
	out := options.ProtoReflect().New()
	if err := (proto.UnmarshalOptions{Resolver: a.types}).Unmarshal(raw, out.Interface()); err != nil {
		return nil, fmt.Errorf("resolve options against descriptor pool: %w", err)
	}
	return out, nil
}

// hasBoolExtension reports whether the resolved options message carries the
// named boolean extension set to true.
func (a *throwsAnalyzer) hasBoolExtension(options proto.Message, fullName protoreflect.FullName) (bool, error) {
	resolved, err := a.resolved(options)
	if err != nil {
		return false, err
	}
	if resolved == nil {
		return false, nil
	}
	found := false
	resolved.Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if fd.IsExtension() && fd.FullName() == fullName && fd.Kind() == protoreflect.BoolKind && value.Bool() {
			found = true
			return false
		}
		return true
	})
	return found, nil
}

// isMarked reports whether a field descriptor carries the throws marker.
func (a *throwsAnalyzer) isMarked(fd protoreflect.FieldDescriptor) (bool, error) {
	options, ok := fd.Options().(*descriptorpb.FieldOptions)
	if !ok || options == nil {
		return false, nil
	}
	return a.hasBoolExtension(options, throwsMarkerName)
}

// enumExtension returns the enum-valued extension with the given full name
// from resolved options, if present.
func (a *throwsAnalyzer) enumExtension(options proto.Message, fullName protoreflect.FullName) (protoreflect.EnumNumber, bool, error) {
	resolved, err := a.resolved(options)
	if err != nil {
		return 0, false, err
	}
	if resolved == nil {
		return 0, false, nil
	}
	var number protoreflect.EnumNumber
	found := false
	resolved.Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if fd.IsExtension() && fd.FullName() == fullName && fd.Kind() == protoreflect.EnumKind && !fd.IsList() {
			number = value.Enum()
			found = true
			return false
		}
		return true
	})
	return number, found, nil
}

// validateMarkedField enforces what a throws-marked field must be: an
// extension field of google.protobuf.MethodOptions or
// google.protobuf.ServiceOptions whose type is a repeated enum.
func validateMarkedField(fd protoreflect.FieldDescriptor) error {
	if !fd.IsExtension() {
		return fmt.Errorf("field %s carries (%s) but is not an extension field; the marker belongs on extensions of google.protobuf.MethodOptions or google.protobuf.ServiceOptions", fd.FullName(), throwsMarkerName)
	}
	extendee := fd.ContainingMessage().FullName()
	if extendee != "google.protobuf.MethodOptions" && extendee != "google.protobuf.ServiceOptions" {
		return fmt.Errorf("extension %s carries (%s) but extends %s; the marker belongs on extensions of google.protobuf.MethodOptions or google.protobuf.ServiceOptions", fd.FullName(), throwsMarkerName, extendee)
	}
	if fd.Kind() != protoreflect.EnumKind || !fd.IsList() {
		return fmt.Errorf("extension %s carries (%s) but is not a repeated enum; a throws declaration must be a repeated field of an error reason enum", fd.FullName(), throwsMarkerName)
	}
	return nil
}

// scanMarkers walks every field and extension descriptor in the pool and
// fails on any throws marker attached to an illegal host. Running the scan
// over the whole pool, not just the fields a method happens to use, turns a
// misplaced marker into a build failure instead of a silently dead
// annotation.
func (a *throwsAnalyzer) scanMarkers() error {
	var scanErr error
	a.pool.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if err := a.scanExtensionList(file.Extensions()); err != nil {
			scanErr = err
			return false
		}
		if err := a.scanMessages(file.Messages()); err != nil {
			scanErr = err
			return false
		}
		return true
	})
	return scanErr
}

func (a *throwsAnalyzer) scanMessages(messages protoreflect.MessageDescriptors) error {
	for i := 0; i < messages.Len(); i++ {
		message := messages.Get(i)
		for j := 0; j < message.Fields().Len(); j++ {
			if err := a.checkMarkedHost(message.Fields().Get(j)); err != nil {
				return err
			}
		}
		if err := a.scanExtensionList(message.Extensions()); err != nil {
			return err
		}
		if err := a.scanMessages(message.Messages()); err != nil {
			return err
		}
	}
	return nil
}

func (a *throwsAnalyzer) scanExtensionList(extensions protoreflect.ExtensionDescriptors) error {
	for i := 0; i < extensions.Len(); i++ {
		if err := a.checkMarkedHost(extensions.Get(i)); err != nil {
			return err
		}
	}
	return nil
}

func (a *throwsAnalyzer) checkMarkedHost(fd protoreflect.FieldDescriptor) error {
	marked, err := a.isMarked(fd)
	if err != nil {
		return err
	}
	if !marked {
		return nil
	}
	return validateMarkedField(fd)
}

// declarations collects the error identities declared through marked
// extensions on one options message.
func (a *throwsAnalyzer) declarations(options proto.Message) ([]throwsIdentity, error) {
	resolved, err := a.resolved(options)
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, nil
	}
	var identities []throwsIdentity
	var rangeErr error
	resolved.Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if !fd.IsExtension() {
			return true
		}
		marked, err := a.isMarked(fd)
		if err != nil {
			rangeErr = err
			return false
		}
		if !marked {
			return true
		}
		if err := validateMarkedField(fd); err != nil {
			rangeErr = err
			return false
		}
		list := value.List()
		for i := 0; i < list.Len(); i++ {
			identity, err := a.identityForValue(fd, list.Get(i).Enum())
			if err != nil {
				rangeErr = fmt.Errorf("extension %s: %w", fd.FullName(), err)
				return false
			}
			identities = append(identities, identity)
		}
		return true
	})
	if rangeErr != nil {
		return nil, rangeErr
	}
	return identities, nil
}

// identityForValue resolves one declared enum value into an error identity.
func (a *throwsAnalyzer) identityForValue(fd protoreflect.FieldDescriptor, number protoreflect.EnumNumber) (throwsIdentity, error) {
	enum := fd.Enum()
	if number == 0 {
		return throwsIdentity{}, fmt.Errorf("enum %s: a throws declaration must not reference the zero value; it names the absence of an error", enum.FullName())
	}
	value := enum.Values().ByNumber(number)
	if value == nil {
		return throwsIdentity{}, fmt.Errorf("enum %s: declared value %d does not exist", enum.FullName(), number)
	}
	kindNumber, err := a.kindForValue(enum, value)
	if err != nil {
		return throwsIdentity{}, err
	}
	kindName, ok := errorapi.Kind_name[int32(kindNumber)]
	if !ok {
		return throwsIdentity{}, fmt.Errorf("enum value %s: kind %d is not a known Kind", value.FullName(), kindNumber)
	}
	status, err := projectDeclaredStatus(strings.TrimPrefix(kindName, "KIND_"))
	if err != nil {
		return throwsIdentity{}, fmt.Errorf("enum value %s: %w", value.FullName(), err)
	}
	return throwsIdentity{
		kind:      strings.TrimPrefix(kindName, "KIND_"),
		domain:    string(enum.ParentFile().Package()),
		reason:    string(value.Name()),
		status:    status,
		dedupeKey: string(value.FullName()),
	}, nil
}

// kindForValue resolves the Kind of an enum value: the value-level kind
// annotation, falling back to the enum-level default_kind.
func (a *throwsAnalyzer) kindForValue(enum protoreflect.EnumDescriptor, value protoreflect.EnumValueDescriptor) (protoreflect.EnumNumber, error) {
	if options, ok := value.Options().(*descriptorpb.EnumValueOptions); ok && options != nil {
		number, found, err := a.enumExtension(options, kindExtensionName)
		if err != nil {
			return 0, err
		}
		if found {
			if number == 0 {
				return 0, fmt.Errorf("enum value %s: kind must not be KIND_UNSPECIFIED", value.FullName())
			}
			return number, nil
		}
	}
	if options, ok := enum.Options().(*descriptorpb.EnumOptions); ok && options != nil {
		number, found, err := a.enumExtension(options, defaultKindExtName)
		if err != nil {
			return 0, err
		}
		if found {
			if number == 0 {
				return 0, fmt.Errorf("enum %s: default_kind must not be KIND_UNSPECIFIED", enum.FullName())
			}
			return number, nil
		}
	}
	return 0, fmt.Errorf("enum value %s resolves to no kind: it has no (%s) annotation and enum %s declares no (%s)",
		value.FullName(), kindExtensionName, enum.FullName(), defaultKindExtName)
}

// projectDeclaredStatus projects a Kind wire name onto the HTTP status the
// runtime would answer with, and rejects a projection outside the error
// range: a declared error that documents a success status is a contradiction.
func projectDeclaredStatus(kindName string) (int, error) {
	kind, ok := forgeerrors.ParseKind(kindName)
	if !ok {
		return 0, fmt.Errorf("kind %q is not a known Kind wire name", kindName)
	}
	status := statusOf(kind)
	if status < 400 || status > 599 {
		return 0, fmt.Errorf("kind %s projects to HTTP %d, which is not a 4xx or 5xx status", kindName, status)
	}
	return status, nil
}

// hasValidationConstraints reports whether a request message carries any
// buf.validate constraint, on the message itself, any field or oneof, or any
// transitively reachable message field.
func (a *throwsAnalyzer) hasValidationConstraints(message protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool) (bool, error) {
	if seen[message.FullName()] {
		return false, nil
	}
	seen[message.FullName()] = true

	if constrained, err := a.hasValidateExtension(message.Options()); err != nil || constrained {
		return constrained, err
	}
	for i := 0; i < message.Oneofs().Len(); i++ {
		if constrained, err := a.hasValidateExtension(message.Oneofs().Get(i).Options()); err != nil || constrained {
			return constrained, err
		}
	}
	for i := 0; i < message.Fields().Len(); i++ {
		field := message.Fields().Get(i)
		if constrained, err := a.hasValidateExtension(field.Options()); err != nil || constrained {
			return constrained, err
		}
		nested := nestedMessage(field)
		if nested == nil {
			continue
		}
		if constrained, err := a.hasValidationConstraints(nested, seen); err != nil || constrained {
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

func (a *throwsAnalyzer) hasValidateExtension(options proto.Message) (bool, error) {
	resolved, err := a.resolved(options)
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
	serviceIdentities, err := g.throws.declarations(service.Desc.Options())
	if err != nil {
		return nil, err
	}
	methodIdentities, err := g.throws.declarations(method.Desc.Options())
	if err != nil {
		return nil, err
	}
	identities := append(serviceIdentities, methodIdentities...)

	declared := make(map[string]bool, len(identities))
	for _, identity := range identities {
		if declared[identity.dedupeKey] {
			return nil, fmt.Errorf("error %s is declared more than once across the method and its service; each identity must be declared exactly once", identity.dedupeKey)
		}
		declared[identity.dedupeKey] = true
	}

	if g.validationReasonEnabled() {
		constrained, err := g.throws.hasValidationConstraints(method.Input.Desc, map[protoreflect.FullName]bool{})
		if err != nil {
			return nil, err
		}
		if constrained {
			status, err := projectDeclaredStatus(forgeerrors.KindInvalidArgument.String())
			if err != nil {
				return nil, err
			}
			identities = append(identities, throwsIdentity{
				kind:   forgeerrors.KindInvalidArgument.String(),
				domain: forgeerrors.Domain,
				reason: "VALIDATION_FAILED",
				status: status,
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
func groupIdentitiesByStatus(identities []throwsIdentity) []throwsResponseSpec {
	if len(identities) == 0 {
		return nil
	}
	byStatus := map[int][]throwsIdentity{}
	for _, identity := range identities {
		byStatus[identity.status] = append(byStatus[identity.status], identity)
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
			if group[i].kind != group[j].kind {
				return group[i].kind < group[j].kind
			}
			if group[i].domain != group[j].domain {
				return group[i].domain < group[j].domain
			}
			return group[i].reason < group[j].reason
		})
		var lines []string
		for i := 0; i < len(group); {
			j := i
			var reasons []string
			for ; j < len(group) && group[j].kind == group[i].kind && group[j].domain == group[i].domain; j++ {
				reasons = append(reasons, group[j].reason)
			}
			lines = append(lines, fmt.Sprintf("%s (%s) — reasons: %s", group[i].kind, group[i].domain, strings.Join(reasons, ", ")))
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
// handwritten response on a status code the declarations also produce is a
// second source of truth for the same fact, so it fails generation instead of
// being merged or overwritten.
func (g *OpenAPIv3Generator) applyThrowsResponses(d *v3.Document, op *v3.Operation, specs []throwsResponseSpec) error {
	if len(specs) == 0 {
		return nil
	}
	if op.Responses == nil {
		op.Responses = &v3.Responses{}
	}
	existing := make(map[string]bool, len(op.Responses.ResponseOrReference))
	for _, namedResponse := range op.Responses.ResponseOrReference {
		existing[namedResponse.GetName()] = true
	}
	for _, spec := range specs {
		if existing[spec.code] {
			return fmt.Errorf("declared errors produce response %s, which the method also declares as a handwritten OpenAPI response; delete the handwritten response and keep the throws declaration as the single source", spec.code)
		}
		op.Responses.ResponseOrReference = append(op.Responses.ResponseOrReference, &v3.NamedResponseOrReference{
			Name: spec.code,
			Value: &v3.ResponseOrReference{
				Oneof: &v3.ResponseOrReference_Response{
					Response: &v3.Response{
						Description: spec.description,
						Content:     g.forgeErrorContent(d),
					},
				},
			},
		})
	}
	return nil
}

// sortOperationResponses orders responses by status code with the default
// response last, so generated documents are stable however responses were
// accumulated.
func sortOperationResponses(op *v3.Operation) {
	if op.Responses == nil {
		return
	}
	sort.SliceStable(op.Responses.ResponseOrReference, func(i, j int) bool {
		return responseRank(op.Responses.ResponseOrReference[i].GetName()) < responseRank(op.Responses.ResponseOrReference[j].GetName())
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
