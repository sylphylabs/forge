// Package throws resolves method error declarations (ADR-0013) from the
// descriptor set of a CodeGeneratorRequest.
//
// It is the single implementation of the marker-claiming rules shared by
// protoc-gen-openapi, which turns declarations into exact error responses,
// and protoc-gen-go-middleware, which compiles them into runtime assertions
// (ADR-0014). One resolver means one set of failure semantics: a declaration
// that is invalid for one generator is invalid for both.
//
// Plugins compile against the published Forge API module, while the marker
// and the application's marked extension fields arrive as descriptors, so
// everything is resolved dynamically: options are re-unmarshaled against a
// type registry built from the request's own descriptors. Extensions the
// registry cannot resolve stay unknown and are ignored; everything it can
// resolve is visited, so a marked declaration is never silently dropped.
package throws

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/pluginpb"

	errorapi "github.com/sylphylabs/forge/api/errors/v1"
	forgeerrors "github.com/sylphylabs/forge/errors"
	forgehttp "github.com/sylphylabs/forge/transport/http"
)

// The extension full names the analyzer resolves dynamically. The plugins
// compile against the published Forge API module, so these are matched by
// name against the descriptors the build supplies rather than by generated
// extension types: the descriptors in the CodeGeneratorRequest are the
// authority on what the application declared.
const (
	MarkerName         = "sylphy.errors.v1.throws"
	kindExtensionName  = "sylphy.errors.v1.kind"
	defaultKindExtName = "sylphy.errors.v1.default_kind"

	methodOptionsName  = "google.protobuf.MethodOptions"
	serviceOptionsName = "google.protobuf.ServiceOptions"
	markerHostHint     = "the marker belongs on extensions of " + methodOptionsName + " or " + serviceOptionsName
)

// StatusOf projects an error Kind onto an HTTP status code. It is the same
// projection the Forge runtime applies when it encodes an error, taken
// directly from the published runtime so no generator can drift from the wire
// behavior. It is a variable only so tests can prove the 4xx/5xx guard fires
// on a projection that leaves the error range.
var StatusOf = forgehttp.StatusOf

// Identity is one declared error identity a method can produce, joined with
// the HTTP status its Kind projects to.
type Identity struct {
	Kind   string // wire name of the Kind, e.g. "NOT_FOUND"
	Domain string // proto package of the declaring enum
	Reason string // enum value name
	Status int

	// DedupeKey identifies the declaration for duplicate detection. It is the
	// enum value full name for declared reasons and empty for identities a
	// generator merges in itself, such as the framework validation identity.
	DedupeKey string
}

// Analyzer resolves method error declarations from the descriptor set of a
// CodeGeneratorRequest.
type Analyzer struct {
	pool  *protoregistry.Files
	types *dynamicpb.Types
}

// NewAnalyzer builds an analyzer over the request's own descriptors.
func NewAnalyzer(request *pluginpb.CodeGeneratorRequest) (*Analyzer, error) {
	pool, err := protodesc.NewFiles(&descriptorpb.FileDescriptorSet{File: request.GetProtoFile()})
	if err != nil {
		return nil, fmt.Errorf("build descriptor pool for error declarations: %w", err)
	}
	return &Analyzer{pool: pool, types: dynamicpb.NewTypes(pool)}, nil
}

// Resolved re-unmarshals an options message against the request's descriptor
// pool so extension fields that arrived as unknown bytes become inspectable.
func (a *Analyzer) Resolved(options proto.Message) (protoreflect.Message, error) {
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
func (a *Analyzer) hasBoolExtension(options proto.Message, fullName protoreflect.FullName) (bool, error) {
	resolved, err := a.Resolved(options)
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
func (a *Analyzer) isMarked(fd protoreflect.FieldDescriptor) (bool, error) {
	options, ok := fd.Options().(*descriptorpb.FieldOptions)
	if !ok || options == nil {
		return false, nil
	}
	return a.hasBoolExtension(options, MarkerName)
}

// enumExtension returns the enum-valued extension with the given full name
// from resolved options, if present.
func (a *Analyzer) enumExtension(options proto.Message, fullName protoreflect.FullName) (protoreflect.EnumNumber, bool, error) {
	resolved, err := a.Resolved(options)
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
		return fmt.Errorf("field %s carries (%s) but is not an extension field; %s", fd.FullName(), MarkerName, markerHostHint)
	}
	extendee := fd.ContainingMessage().FullName()
	if extendee != methodOptionsName && extendee != serviceOptionsName {
		return fmt.Errorf("extension %s carries (%s) but extends %s; %s", fd.FullName(), MarkerName, extendee, markerHostHint)
	}
	if fd.Kind() != protoreflect.EnumKind || !fd.IsList() {
		return fmt.Errorf(
			"extension %s carries (%s) but is not a repeated enum; a throws declaration must be a repeated field of an error reason enum",
			fd.FullName(), MarkerName,
		)
	}
	return nil
}

// ScanMarkers walks every field and extension descriptor in the pool and
// fails on any throws marker attached to an illegal host. Running the scan
// over the whole pool, not just the fields a method happens to use, turns a
// misplaced marker into a build failure instead of a silently dead
// annotation.
func (a *Analyzer) ScanMarkers() error {
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

func (a *Analyzer) scanMessages(messages protoreflect.MessageDescriptors) error {
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

func (a *Analyzer) scanExtensionList(extensions protoreflect.ExtensionDescriptors) error {
	for i := 0; i < extensions.Len(); i++ {
		if err := a.checkMarkedHost(extensions.Get(i)); err != nil {
			return err
		}
	}
	return nil
}

func (a *Analyzer) checkMarkedHost(fd protoreflect.FieldDescriptor) error {
	marked, err := a.isMarked(fd)
	if err != nil {
		return err
	}
	if !marked {
		return nil
	}
	return validateMarkedField(fd)
}

// Declarations collects the error identities declared through marked
// extensions on one options message.
func (a *Analyzer) Declarations(options proto.Message) ([]Identity, error) {
	resolved, err := a.Resolved(options)
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, nil
	}
	var identities []Identity
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

// MethodDeclarations resolves the effective declared identity set of one
// method: the union of its service's and its own throws declarations, with
// the duplicate-declaration guard applied across the union.
func (a *Analyzer) MethodDeclarations(serviceOptions, methodOptions proto.Message) ([]Identity, error) {
	serviceIdentities, err := a.Declarations(serviceOptions)
	if err != nil {
		return nil, err
	}
	methodIdentities, err := a.Declarations(methodOptions)
	if err != nil {
		return nil, err
	}
	identities := append(serviceIdentities, methodIdentities...)

	declared := make(map[string]bool, len(identities))
	for _, identity := range identities {
		if declared[identity.DedupeKey] {
			return nil, fmt.Errorf(
				"error %s is declared more than once across the method and its service; each identity must be declared exactly once",
				identity.DedupeKey,
			)
		}
		declared[identity.DedupeKey] = true
	}
	return identities, nil
}

// identityForValue resolves one declared enum value into an error identity.
func (a *Analyzer) identityForValue(fd protoreflect.FieldDescriptor, number protoreflect.EnumNumber) (Identity, error) {
	enum := fd.Enum()
	if number == 0 {
		return Identity{}, fmt.Errorf("enum %s: a throws declaration must not reference the zero value; it names the absence of an error", enum.FullName())
	}
	value := enum.Values().ByNumber(number)
	if value == nil {
		return Identity{}, fmt.Errorf("enum %s: declared value %d does not exist", enum.FullName(), number)
	}
	kindNumber, err := a.kindForValue(enum, value)
	if err != nil {
		return Identity{}, err
	}
	kindName, ok := errorapi.Kind_name[int32(kindNumber)]
	if !ok {
		return Identity{}, fmt.Errorf("enum value %s: kind %d is not a known Kind", value.FullName(), kindNumber)
	}
	status, err := ProjectDeclaredStatus(strings.TrimPrefix(kindName, "KIND_"))
	if err != nil {
		return Identity{}, fmt.Errorf("enum value %s: %w", value.FullName(), err)
	}
	return Identity{
		Kind:      strings.TrimPrefix(kindName, "KIND_"),
		Domain:    string(enum.ParentFile().Package()),
		Reason:    string(value.Name()),
		Status:    status,
		DedupeKey: string(value.FullName()),
	}, nil
}

// kindForValue resolves the Kind of an enum value: the value-level kind
// annotation, falling back to the enum-level default_kind.
func (a *Analyzer) kindForValue(enum protoreflect.EnumDescriptor, value protoreflect.EnumValueDescriptor) (protoreflect.EnumNumber, error) {
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

// ProjectDeclaredStatus projects a Kind wire name onto the HTTP status the
// runtime would answer with, and rejects a projection outside the error
// range: a declared error that documents a success status is a contradiction.
func ProjectDeclaredStatus(kindName string) (int, error) {
	kind, ok := forgeerrors.ParseKind(kindName)
	if !ok {
		return 0, fmt.Errorf("kind %q is not a known Kind wire name", kindName)
	}
	status := StatusOf(kind)
	if status < 400 || status > 599 {
		return 0, fmt.Errorf("kind %s projects to HTTP %d, which is not a 4xx or 5xx status", kindName, status)
	}
	return status, nil
}
