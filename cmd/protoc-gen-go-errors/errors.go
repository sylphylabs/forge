package main

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"

	errorapi "github.com/sylphylabs/forge/api/errors/v1"
	"github.com/sylphylabs/forge/cmd/internal/generator"
)

const errorsPackage = protogen.GoImportPath("github.com/sylphylabs/forge/errors")

// kindIdents maps a wire Kind onto the Go constant that names it in the errors
// package.
var kindIdents = map[errorapi.Kind]string{
	errorapi.Kind_KIND_UNSPECIFIED:         "KindUnknown",
	errorapi.Kind_KIND_INVALID_ARGUMENT:    "KindInvalidArgument",
	errorapi.Kind_KIND_FAILED_PRECONDITION: "KindFailedPrecondition",
	errorapi.Kind_KIND_OUT_OF_RANGE:        "KindOutOfRange",
	errorapi.Kind_KIND_UNAUTHENTICATED:     "KindUnauthenticated",
	errorapi.Kind_KIND_PERMISSION_DENIED:   "KindPermissionDenied",
	errorapi.Kind_KIND_NOT_FOUND:           "KindNotFound",
	errorapi.Kind_KIND_ALREADY_EXISTS:      "KindAlreadyExists",
	errorapi.Kind_KIND_CONFLICT:            "KindConflict",
	errorapi.Kind_KIND_RESOURCE_EXHAUSTED:  "KindResourceExhausted",
	errorapi.Kind_KIND_CANCELED:            "KindCanceled",
	errorapi.Kind_KIND_DEADLINE_EXCEEDED:   "KindDeadlineExceeded",
	errorapi.Kind_KIND_UNAVAILABLE:         "KindUnavailable",
	errorapi.Kind_KIND_UNIMPLEMENTED:       "KindUnimplemented",
	errorapi.Kind_KIND_INTERNAL:            "KindInternal",
	errorapi.Kind_KIND_DATA_LOSS:           "KindDataLoss",
}

type errorFile struct {
	wrappers []*errorWrapper
}

func analyzeErrorFile(file *protogen.File) (*errorFile, error) {
	facts := new(errorFile)
	for _, enum := range file.Enums {
		wrapper, err := analyzeErrorEnum(file, enum)
		if err != nil {
			return nil, err
		}
		if len(wrapper.Errors) != 0 {
			facts.wrappers = append(facts.wrappers, wrapper)
		}
	}
	return facts, nil
}

// analyzeErrorEnum collects the sentinels to generate for one enum.
//
// An enum takes part only when it declares default_kind or at least one value
// declares kind. Absent both, it is an ordinary enum and is left alone.
func analyzeErrorEnum(file *protogen.File, enum *protogen.Enum) (*errorWrapper, error) {
	opts := enum.Desc.Options()
	defaultKind := errorapi.Kind_KIND_INTERNAL
	declared := proto.HasExtension(opts, errorapi.E_DefaultKind)
	if declared {
		defaultKind = proto.GetExtension(opts, errorapi.E_DefaultKind).(errorapi.Kind)
		if _, ok := kindIdents[defaultKind]; !ok {
			return nil, fmt.Errorf("proto %q enum %s: default_kind %v is not a known Kind",
				file.Desc.Path(), enum.Desc.FullName(), defaultKind)
		}
	}

	wrapper := new(errorWrapper)
	enumName := string(enum.Desc.Name())
	domain := string(file.Desc.Package())

	for _, value := range enum.Values {
		valueName := string(value.Desc.Name())
		valueOpts := value.Desc.Options()
		hasKind := proto.HasExtension(valueOpts, errorapi.E_Kind)

		// The zero value names the absence of a failure, so it never becomes a
		// sentinel. Declaring a kind on it is a mistake worth reporting rather
		// than ignoring.
		if value.Desc.Number() == 0 {
			if hasKind {
				return nil, fmt.Errorf(
					"proto %q enum value %s: the zero value must not declare a kind; it names the absence of an error",
					file.Desc.Path(), value.Desc.FullName())
			}
			continue
		}

		if !declared && !hasKind {
			continue
		}

		kind := defaultKind
		if hasKind {
			kind = proto.GetExtension(valueOpts, errorapi.E_Kind).(errorapi.Kind)
			if _, ok := kindIdents[kind]; !ok {
				return nil, fmt.Errorf("proto %q enum value %s: kind %v is not a known Kind",
					file.Desc.Path(), value.Desc.FullName(), kind)
			}
		}
		if kind == errorapi.Kind_KIND_UNSPECIFIED {
			return nil, fmt.Errorf(
				"proto %q enum value %s: kind must not be KIND_UNSPECIFIED; an error that cannot be classified should use KIND_INTERNAL",
				file.Desc.Path(), value.Desc.FullName())
		}

		if err := validateReason(file, value, enumName, valueName); err != nil {
			return nil, err
		}

		comment := value.Comments.Leading.String()
		if comment == "" {
			comment = value.Comments.Trailing.String()
		}
		wrapper.Errors = append(wrapper.Errors, &errorInfo{
			Enum:         enum.GoIdent.GoName,
			Value:        valueName,
			SentinelName: sentinelName(enumName, valueName),
			KindIdent:    kindIdents[kind],
			Domain:       domain,
			Comment:      comment,
			HasComment:   comment != "",
		})
	}
	return wrapper, nil
}

// validateReason enforces the naming rules that keep a reason usable as a
// cross-process contract.
//
// A reason travels to other services and is matched there as a literal, so an
// inconsistent one is a wire-format defect. Rejecting it at build time is
// cheaper than discovering it in a caller.
func validateReason(file *protogen.File, value *protogen.EnumValue, enumName, valueName string) error {
	if valueName != strings.ToUpper(valueName) {
		return fmt.Errorf("proto %q enum value %s: reason must be SCREAMING_SNAKE_CASE",
			file.Desc.Path(), value.Desc.FullName())
	}
	prefix := screamingSnake(enumName) + "_"
	if !strings.HasPrefix(valueName, prefix) {
		return fmt.Errorf("proto %q enum value %s: reason must be prefixed with %q",
			file.Desc.Path(), value.Desc.FullName(), prefix)
	}
	if valueName == prefix {
		return fmt.Errorf("proto %q enum value %s: reason must name a failure after the %q prefix",
			file.Desc.Path(), value.Desc.FullName(), prefix)
	}
	return nil
}

// sentinelName derives the Go identifier for a sentinel, dropping the enum name
// prefix that Protobuf scoping rules require on the value.
func sentinelName(enumName, valueName string) string {
	trimmed := strings.TrimPrefix(valueName, screamingSnake(enumName)+"_")
	return "Err" + case2Camel(trimmed)
}

// screamingSnake converts a CamelCase Protobuf identifier to the
// SCREAMING_SNAKE_CASE form used for its enum values.
func screamingSnake(name string) string {
	var b strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToUpper(b.String())
}

// generateErrorFile analyzes and emits one file for focused tests.
func generateErrorFile(gen *protogen.Plugin, file *protogen.File) (*protogen.GeneratedFile, error) {
	facts, err := analyzeErrorFile(file)
	if err != nil {
		return nil, err
	}
	return emitErrorFile(gen, file, facts)
}

func emitErrorFile(gen *protogen.Plugin, file *protogen.File, facts *errorFile) (*protogen.GeneratedFile, error) {
	if len(facts.wrappers) == 0 {
		return nil, nil
	}
	filename := file.GeneratedFilenamePrefix + "_errors.pb.go"
	g := gen.NewGeneratedFile(filename, file.GoImportPath)
	g.P("// Code generated by protoc-gen-go-errors. DO NOT EDIT.")
	g.P("// versions:")
	g.P(fmt.Sprintf("// - protoc-gen-go-errors %s", generator.Release))
	g.P("// - protoc               ", generator.ProtocVersion(gen))
	g.P()
	g.P("package ", file.GoPackageName)
	g.P()
	// Referencing an identifier makes protogen emit the import for it.
	g.P("// This is a compile-time assertion to ensure that this generated file")
	g.P("// is compatible with the Forge package it is being compiled against.")
	g.P("const _ = ", errorsPackage.Ident("SupportPackageIsVersion1"))
	g.P()
	// QualifiedGoIdent returns the name protogen will use for the package in
	// this file, which the template needs in order to qualify its references.
	errorsIdent := g.QualifiedGoIdent(errorsPackage.Ident("Define"))
	errorsIdent = strings.TrimSuffix(errorsIdent, ".Define")
	for _, wrapper := range facts.wrappers {
		wrapper.ErrorsIdent = errorsIdent
		source, err := wrapper.execute()
		if err != nil {
			return nil, fmt.Errorf("proto %q: render errors: %w", file.Desc.Path(), err)
		}
		g.P(source)
	}
	return g, nil
}

func case2Camel(name string) string {
	if !strings.Contains(name, "_") {
		return titleProtoIdentifier(name)
	}
	strs := strings.Split(name, "_")
	words := make([]string, 0, len(strs))
	for _, w := range strs {
		words = append(words, titleProtoIdentifier(w))
	}

	return strings.Join(words, "")
}

// Protobuf identifiers are ASCII, so language-aware title casing is unnecessary.
func titleProtoIdentifier(word string) string {
	if word == "" {
		return ""
	}
	if word == strings.ToUpper(word) {
		word = strings.ToLower(word)
	}
	return strings.ToUpper(word[:1]) + word[1:]
}
