package generator

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/sylphylabs/forge/cmd/internal/openapi/model"
)

// The sylphy.openapi.v1 extension full names the generator resolves
// dynamically. The plugin compiles against the published Forge API module,
// while the annotations arrive as descriptors in the CodeGeneratorRequest, so
// they are matched by full name against the request's own descriptor pool and
// read through protoreflect — the same mechanism the throws analyzer uses for
// error declarations. Requests whose descriptors do not include the
// annotations file simply resolve nothing.
const (
	documentExtensionName  = "sylphy.openapi.v1.document"
	operationExtensionName = "sylphy.openapi.v1.operation"
	schemaExtensionName    = "sylphy.openapi.v1.schema"
	fieldExtensionName     = "sylphy.openapi.v1.field"
)

// annDocument is a resolved (sylphy.openapi.v1.document) file annotation.
type annDocument struct {
	title       string
	version     string
	description string
	servers     []*model.Server
	schemes     []annSecurityScheme
}

// annSecurityScheme is one resolved security scheme definition.
type annSecurityScheme struct {
	name         string
	description  string
	form         protoreflect.Name // which oneof member is set
	bearerFormat string            // form == "http_bearer"
	header       string            // form == "api_key_header"
}

// annOperation is a resolved (sylphy.openapi.v1.operation) method annotation.
type annOperation struct {
	summary     string
	description string
	tags        []string
	deprecated  bool
	security    []model.SecurityRequirement
}

// annSchema is a resolved (sylphy.openapi.v1.schema) message annotation.
type annSchema struct {
	description string
}

// annField is a resolved (sylphy.openapi.v1.field) field annotation.
type annField struct {
	description string
	example     string
	format      string
}

// messageExtension resolves an options message against the request's
// descriptor pool and returns the message-valued extension with the given
// full name, or nil when the options do not carry it.
func (g *OpenAPIv3Generator) messageExtension(options proto.Message, fullName protoreflect.FullName) (protoreflect.Message, error) {
	resolved, err := g.throws.Resolved(options)
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, nil
	}
	var found protoreflect.Message
	resolved.Range(func(fd protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if fd.IsExtension() && fd.FullName() == fullName && fd.Kind() == protoreflect.MessageKind && !fd.IsList() {
			found = value.Message()
			return false
		}
		return true
	})
	return found, nil
}

// documentAnnotation resolves the document annotation of one file.
func (g *OpenAPIv3Generator) documentAnnotation(options proto.Message) (*annDocument, error) {
	message, err := g.messageExtension(options, documentExtensionName)
	if err != nil || message == nil {
		return nil, err
	}
	annotation := &annDocument{
		title:       getString(message, "title"),
		version:     getString(message, "version"),
		description: getString(message, "description"),
	}
	for _, server := range getMessageList(message, "servers") {
		annotation.servers = append(annotation.servers, &model.Server{
			URL:         getString(server, "url"),
			Description: getString(server, "description"),
		})
	}
	for _, scheme := range getMessageList(message, "security_schemes") {
		resolved, err := resolveSecurityScheme(scheme)
		if err != nil {
			return nil, err
		}
		annotation.schemes = append(annotation.schemes, resolved)
	}
	return annotation, nil
}

// resolveSecurityScheme reads one SecurityScheme message, requiring a name
// and exactly one scheme form.
func resolveSecurityScheme(message protoreflect.Message) (annSecurityScheme, error) {
	scheme := annSecurityScheme{
		name:        getString(message, "name"),
		description: getString(message, "description"),
	}
	if scheme.name == "" {
		return scheme, fmt.Errorf("a security scheme requires a name")
	}
	if bearer := getMessage(message, "http_bearer"); bearer != nil {
		scheme.form = "http_bearer"
		scheme.bearerFormat = getString(bearer, "bearer_format")
		return scheme, nil
	}
	if apiKey := getMessage(message, "api_key_header"); apiKey != nil {
		scheme.form = "api_key_header"
		scheme.header = getString(apiKey, "header")
		if scheme.header == "" {
			return scheme, fmt.Errorf("security scheme %q: an API key scheme requires the header name", scheme.name)
		}
		return scheme, nil
	}
	return scheme, fmt.Errorf("security scheme %q declares no scheme form; set http_bearer or api_key_header", scheme.name)
}

// securityScheme projects one resolved scheme definition onto the OpenAPI
// security scheme object.
func (s annSecurityScheme) securityScheme() *model.SecurityScheme {
	switch s.form {
	case "http_bearer":
		return &model.SecurityScheme{
			Type:         "http",
			Description:  s.description,
			Scheme:       "bearer",
			BearerFormat: s.bearerFormat,
		}
	case "api_key_header":
		return &model.SecurityScheme{
			Type:        "apiKey",
			Description: s.description,
			In:          "header",
			Name:        s.header,
		}
	}
	return nil
}

// operationAnnotation resolves the operation annotation of one method.
func (g *OpenAPIv3Generator) operationAnnotation(options proto.Message) (*annOperation, error) {
	message, err := g.messageExtension(options, operationExtensionName)
	if err != nil || message == nil {
		return nil, err
	}
	annotation := &annOperation{
		summary:     getString(message, "summary"),
		description: getString(message, "description"),
		tags:        getStringList(message, "tags"),
		deprecated:  getBool(message, "deprecated"),
	}
	for _, requirement := range getMessageList(message, "security") {
		var schemes model.SecurityRequirement
		for _, name := range getStringList(requirement, "schemes") {
			schemes = append(schemes, &model.SchemeScopes{Name: name})
		}
		annotation.security = append(annotation.security, schemes)
	}
	return annotation, nil
}

// schemaAnnotation resolves the schema annotation of one message.
func (g *OpenAPIv3Generator) schemaAnnotation(options proto.Message) (*annSchema, error) {
	message, err := g.messageExtension(options, schemaExtensionName)
	if err != nil || message == nil {
		return nil, err
	}
	return &annSchema{description: getString(message, "description")}, nil
}

// fieldAnnotation resolves the field annotation of one field.
func (g *OpenAPIv3Generator) fieldAnnotation(options proto.Message) (*annField, error) {
	message, err := g.messageExtension(options, fieldExtensionName)
	if err != nil || message == nil {
		return nil, err
	}
	return &annField{
		description: getString(message, "description"),
		example:     getString(message, "example"),
		format:      getString(message, "format"),
	}, nil
}

// getString reads a string field by name from a dynamic message.
func getString(message protoreflect.Message, name protoreflect.Name) string {
	fd := message.Descriptor().Fields().ByName(name)
	if fd == nil || fd.Kind() != protoreflect.StringKind || fd.IsList() {
		return ""
	}
	return message.Get(fd).String()
}

// getBool reads a bool field by name from a dynamic message.
func getBool(message protoreflect.Message, name protoreflect.Name) bool {
	fd := message.Descriptor().Fields().ByName(name)
	if fd == nil || fd.Kind() != protoreflect.BoolKind || fd.IsList() {
		return false
	}
	return message.Get(fd).Bool()
}

// getStringList reads a repeated string field by name from a dynamic message.
func getStringList(message protoreflect.Message, name protoreflect.Name) []string {
	fd := message.Descriptor().Fields().ByName(name)
	if fd == nil || fd.Kind() != protoreflect.StringKind || !fd.IsList() {
		return nil
	}
	list := message.Get(fd).List()
	values := make([]string, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		values = append(values, list.Get(i).String())
	}
	return values
}

// getMessage reads a singular message field by name from a dynamic message,
// returning nil when it is absent.
func getMessage(message protoreflect.Message, name protoreflect.Name) protoreflect.Message {
	fd := message.Descriptor().Fields().ByName(name)
	if fd == nil || fd.Kind() != protoreflect.MessageKind || fd.IsList() || !message.Has(fd) {
		return nil
	}
	return message.Get(fd).Message()
}

// getMessageList reads a repeated message field by name from a dynamic
// message.
func getMessageList(message protoreflect.Message, name protoreflect.Name) []protoreflect.Message {
	fd := message.Descriptor().Fields().ByName(name)
	if fd == nil || fd.Kind() != protoreflect.MessageKind || !fd.IsList() {
		return nil
	}
	list := message.Get(fd).List()
	messages := make([]protoreflect.Message, 0, list.Len())
	for i := 0; i < list.Len(); i++ {
		messages = append(messages, list.Get(i).Message())
	}
	return messages
}
