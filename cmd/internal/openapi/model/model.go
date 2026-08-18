// Package model is the OpenAPI document model of protoc-gen-openapi.
//
// It is a write-only model: the generator builds a Document and serializes
// it; nothing here parses, validates, or round-trips existing documents.
// Validation happens on the test side, where generated fixtures are parsed
// independently and checked against the official OpenAPI schema.
//
// Serialization is deterministic by construction. Every keyed collection is
// an ordered slice of named entries and every node tree is built in declared
// field order, so equal documents marshal to equal bytes with no reliance on
// encoder map ordering.
//
// The model speaks OpenAPI 3.2: the QUERY path item operation,
// additionalOperations, sequential media type fields (itemSchema,
// prefixEncoding, itemEncoding), hierarchical tags (parent, kind, summary),
// and $self are all expressible. What the generator chooses to emit is the
// generator's decision; the model is not the bottleneck.
package model

// Document is an OpenAPI document.
type Document struct {
	OpenAPI    string
	Self       string // $self, OpenAPI 3.2
	Info       Info
	Servers    []*Server
	Paths      []*NamedPathItem
	Components *Components
	Security   []SecurityRequirement
	Tags       []*Tag
}

// Info is the document information object.
type Info struct {
	Title       string
	Summary     string
	Description string
	Version     string
}

// Server is one server the API is available at.
type Server struct {
	URL         string
	Description string
}

// NamedPathItem is one path entry of the document.
type NamedPathItem struct {
	Path string
	Item *PathItem
}

// PathItem holds the operations of one path.
type PathItem struct {
	Get     *Operation
	Put     *Operation
	Post    *Operation
	Delete  *Operation
	Options *Operation
	Head    *Operation
	Patch   *Operation
	Trace   *Operation
	Query   *Operation // OpenAPI 3.2

	// AdditionalOperations maps custom HTTP methods to operations,
	// OpenAPI 3.2.
	AdditionalOperations []*NamedOperation

	Servers []*Server
}

// NamedOperation is one custom-method operation of a path item.
type NamedOperation struct {
	Method    string
	Operation *Operation
}

// Operation is one API operation.
type Operation struct {
	Tags        []string
	Summary     string
	Description string
	OperationID string
	Deprecated  bool
	Parameters  []*Parameter
	RequestBody *RequestBody
	Responses   []*NamedResponse
	Security    []SecurityRequirement
	Servers     []*Server
}

// Parameter describes one operation parameter.
type Parameter struct {
	Name        string
	In          string
	Description string
	Required    bool
	Schema      *Schema
}

// RequestBody describes an operation's request body.
type RequestBody struct {
	Description string
	Required    bool
	Content     MediaTypes
}

// NamedResponse is one response entry, keyed by a status code or "default".
type NamedResponse struct {
	Name     string
	Response *Response
}

// Response describes one operation response.
type Response struct {
	Description string

	// Content maps media type names to their definitions. A nil map omits
	// the content member entirely; a non-nil empty map emits `content: {}`.
	Content MediaTypes
}

// MediaTypes is an ordered media type map.
type MediaTypes []*NamedMediaType

// NamedMediaType is one media type entry.
type NamedMediaType struct {
	Name  string
	Value *MediaType
}

// MediaType describes one media type of a request or response body.
type MediaType struct {
	Schema *Schema

	// ItemSchema describes each item of a sequential media type,
	// OpenAPI 3.2.
	ItemSchema *Schema

	Encoding []*NamedEncoding

	// PrefixEncoding is the positional encoding list of a sequential media
	// type, OpenAPI 3.2.
	PrefixEncoding []*Encoding

	// ItemEncoding is the encoding of every item of a sequential media
	// type, OpenAPI 3.2.
	ItemEncoding *Encoding
}

// NamedEncoding is one encoding entry, keyed by property name.
type NamedEncoding struct {
	Name  string
	Value *Encoding
}

// Encoding describes the serialization of one property or item.
type Encoding struct {
	ContentType string
}

// Schema is a JSON Schema. A non-empty Ref makes it a reference: only the
// $ref member is emitted and every other field is ignored.
type Schema struct {
	Ref string

	Type                 string
	Format               string
	Description          string
	Pattern              string
	Enum                 []string
	Items                *Schema
	Properties           []*NamedSchema
	Required             []string
	AdditionalProperties *AdditionalProperties
	AllOf                []*Schema
	ReadOnly             bool
	WriteOnly            bool
	Example              string
	HasExample           bool
}

// NamedSchema is one property or component schema entry.
type NamedSchema struct {
	Name   string
	Schema *Schema
}

// AdditionalProperties is either a boolean or a schema. Exactly one of the
// two fields is set.
type AdditionalProperties struct {
	Allowed *bool
	Schema  *Schema
}

// Components holds the reusable objects of the document.
type Components struct {
	Schemas         []*NamedSchema
	SecuritySchemes []*NamedSecurityScheme
}

// NamedSecurityScheme is one security scheme entry.
type NamedSecurityScheme struct {
	Name   string
	Scheme *SecurityScheme
}

// SecurityScheme describes one way of authenticating a request.
type SecurityScheme struct {
	Type         string
	Description  string
	Scheme       string // type: http
	BearerFormat string // type: http, scheme: bearer
	In           string // type: apiKey
	Name         string // type: apiKey
}

// SecurityRequirement names the schemes a request must satisfy together. An
// empty requirement makes authentication optional.
type SecurityRequirement []*SchemeScopes

// SchemeScopes names one required scheme and its scopes.
type SchemeScopes struct {
	Name   string
	Scopes []string
}

// Tag is one document tag.
type Tag struct {
	Name        string
	Summary     string // OpenAPI 3.2
	Description string
	Parent      string // OpenAPI 3.2
	Kind        string // OpenAPI 3.2
}
