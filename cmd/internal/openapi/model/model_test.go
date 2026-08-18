package model

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// testDocument builds one document exercising every object kind the model
// serializes, including the OpenAPI 3.2 members the generator does not emit
// yet.
func testDocument() *Document {
	allowed := true
	return &Document{
		OpenAPI: "3.2.0",
		Self:    "https://example.com/openapi.yaml",
		Info: Info{
			Title:       "Library API",
			Summary:     "Books and shelves",
			Description: "Manages books.",
			Version:     "1.2.3",
		},
		Servers: []*Server{{URL: "https://api.example.com", Description: "Production"}},
		Paths: []*NamedPathItem{
			{
				Path: "/v1/books/{name}",
				Item: &PathItem{
					Get: &Operation{
						Tags:        []string{"books"},
						Summary:     "Get one book",
						Description: "Returns one book.",
						OperationID: "Library_GetBook",
						Deprecated:  true,
						Parameters: []*Parameter{
							{
								Name:     "name",
								In:       "path",
								Required: true,
								Schema:   &Schema{Type: "string"},
							},
							{
								Name:        "view",
								In:          "query",
								Description: "How much detail to return.",
								Schema:      &Schema{Type: "string", Enum: []string{"BASIC", "FULL"}},
							},
						},
						Responses: []*NamedResponse{
							{
								Name: "200",
								Response: &Response{
									Description: "OK",
									Content: MediaTypes{
										{
											Name:  "application/json",
											Value: &MediaType{Schema: &Schema{Ref: "#/components/schemas/Book"}},
										},
									},
								},
							},
							{
								Name: "default",
								Response: &Response{
									Description: "Error",
									Content: MediaTypes{
										{
											Name:  "application/problem+json",
											Value: &MediaType{Schema: &Schema{Ref: "#/components/schemas/Problem"}},
										},
									},
								},
							},
						},
						Security: []SecurityRequirement{
							{&SchemeScopes{Name: "bearer"}},
							{}, // authentication optional
						},
					},
					Query: &Operation{
						OperationID: "Library_QueryBooks",
						Responses: []*NamedResponse{
							{Name: "200", Response: &Response{Description: "OK"}},
						},
					},
					AdditionalOperations: []*NamedOperation{
						{
							Method: "COPY",
							Operation: &Operation{
								OperationID: "Library_CopyBook",
								Responses: []*NamedResponse{
									{Name: "200", Response: &Response{Description: "OK"}},
								},
							},
						},
					},
				},
			},
			{
				Path: "/v1/books",
				Item: &PathItem{
					Post: &Operation{
						OperationID: "Library_CreateBook",
						RequestBody: &RequestBody{
							Required: true,
							Content: MediaTypes{
								{
									Name:  "application/json",
									Value: &MediaType{Schema: &Schema{Ref: "#/components/schemas/Book"}},
								},
							},
						},
						Responses: []*NamedResponse{
							{Name: "200", Response: &Response{Description: "OK", Content: MediaTypes{}}},
						},
					},
					Get: &Operation{
						OperationID: "Library_WatchBooks",
						Responses: []*NamedResponse{
							{
								Name: "200",
								Response: &Response{
									Description: "OK",
									Content: MediaTypes{
										{
											Name: "application/jsonl",
											Value: &MediaType{
												ItemSchema:   &Schema{Ref: "#/components/schemas/Book"},
												ItemEncoding: &Encoding{ContentType: "application/json"},
											},
										},
										{
											Name: "multipart/mixed",
											Value: &MediaType{
												PrefixEncoding: []*Encoding{{ContentType: "application/json"}},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		Components: &Components{
			Schemas: []*NamedSchema{
				{
					Name: "Book",
					Schema: &Schema{
						Type:        "object",
						Description: "One book.",
						Required:    []string{"name"},
						Properties: []*NamedSchema{
							{Name: "name", Schema: &Schema{Type: "string", Example: "books/1", HasExample: true}},
							{Name: "pages", Schema: &Schema{Type: "integer", Format: "int32"}},
							{Name: "etag", Schema: &Schema{Type: "string", ReadOnly: true}},
							{Name: "secret", Schema: &Schema{Type: "string", WriteOnly: true}},
							{Name: "labels", Schema: &Schema{
								Type:                 "object",
								AdditionalProperties: &AdditionalProperties{Schema: &Schema{Type: "string"}},
							}},
							{Name: "meta", Schema: &Schema{
								Type:                 "object",
								AdditionalProperties: &AdditionalProperties{Allowed: &allowed},
							}},
							{Name: "shelf", Schema: &Schema{
								AllOf: []*Schema{{Ref: "#/components/schemas/Shelf"}},
							}},
							{Name: "tags", Schema: &Schema{Type: "array", Items: &Schema{Type: "string"}}},
							{Name: "duration", Schema: &Schema{Type: "string", Pattern: `^\d+s$`}},
						},
					},
				},
				{
					Name:   "Problem",
					Schema: &Schema{Type: "object"},
				},
				{
					Name:   "Shelf",
					Schema: &Schema{Type: "object"},
				},
			},
			SecuritySchemes: []*NamedSecurityScheme{
				{
					Name: "bearer",
					Scheme: &SecurityScheme{
						Type:         "http",
						Description:  "Service-issued JWT.",
						Scheme:       "bearer",
						BearerFormat: "JWT",
					},
				},
				{
					Name: "api_key",
					Scheme: &SecurityScheme{
						Type: "apiKey",
						In:   "header",
						Name: "X-Api-Key",
					},
				},
			},
		},
		Security: []SecurityRequirement{
			{&SchemeScopes{Name: "api_key", Scopes: []string{"read"}}},
		},
		Tags: []*Tag{
			{Name: "books", Summary: "Books", Description: "Book operations", Parent: "library", Kind: "nav"},
			{Name: "library", Description: "Everything"},
		},
	}
}

// TestYAMLDeterministic proves byte determinism: repeatedly building and
// serializing an equal document yields identical bytes.
func TestYAMLDeterministic(t *testing.T) {
	first, err := testDocument().YAML("header")
	if err != nil {
		t.Fatalf("YAML() error = %v", err)
	}
	for i := 0; i < 8; i++ {
		next, err := testDocument().YAML("header")
		if err != nil {
			t.Fatalf("YAML() error = %v", err)
		}
		if !bytes.Equal(first, next) {
			t.Fatalf("serialization is not deterministic:\nfirst:\n%s\nnext:\n%s", first, next)
		}
	}
}

// TestJSONDeterministicAndValid proves the JSON rendering is deterministic
// and well-formed.
func TestJSONDeterministicAndValid(t *testing.T) {
	first, err := testDocument().JSON()
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("JSON output is not valid JSON: %v\n%s", err, first)
	}
	for i := 0; i < 8; i++ {
		next, err := testDocument().JSON()
		if err != nil {
			t.Fatalf("JSON() error = %v", err)
		}
		if !bytes.Equal(first, next) {
			t.Fatalf("JSON serialization is not deterministic")
		}
	}
}

// TestYAMLGolden locks the full serialized form of the test document.
func TestYAMLGolden(t *testing.T) {
	got, err := testDocument().YAML("Generated with protoc-gen-openapi")
	if err != nil {
		t.Fatalf("YAML() error = %v", err)
	}
	want := `# Generated with protoc-gen-openapi
openapi: 3.2.0
$self: https://example.com/openapi.yaml
info:
  title: Library API
  summary: Books and shelves
  description: Manages books.
  version: 1.2.3
servers:
  - url: https://api.example.com
    description: Production
paths:
  /v1/books/{name}:
    get:
      tags:
        - books
      summary: Get one book
      description: Returns one book.
      operationId: Library_GetBook
      deprecated: true
      parameters:
        - name: name
          in: path
          required: true
          schema:
            type: string
        - name: view
          in: query
          description: How much detail to return.
          schema:
            type: string
            enum:
              - BASIC
              - FULL
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Book'
        default:
          description: Error
          content:
            application/problem+json:
              schema:
                $ref: '#/components/schemas/Problem'
      security:
        - bearer: []
        - {}
    query:
      operationId: Library_QueryBooks
      responses:
        "200":
          description: OK
    additionalOperations:
      COPY:
        operationId: Library_CopyBook
        responses:
          "200":
            description: OK
  /v1/books:
    get:
      operationId: Library_WatchBooks
      responses:
        "200":
          description: OK
          content:
            application/jsonl:
              itemSchema:
                $ref: '#/components/schemas/Book'
              itemEncoding:
                contentType: application/json
            multipart/mixed:
              prefixEncoding:
                - contentType: application/json
    post:
      operationId: Library_CreateBook
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/Book'
        required: true
      responses:
        "200":
          description: OK
          content: {}
components:
  schemas:
    Book:
      type: object
      description: One book.
      required:
        - name
      properties:
        name:
          type: string
          example: books/1
        pages:
          type: integer
          format: int32
        etag:
          type: string
          readOnly: true
        secret:
          type: string
          writeOnly: true
        labels:
          type: object
          additionalProperties:
            type: string
        meta:
          type: object
          additionalProperties: true
        shelf:
          allOf:
            - $ref: '#/components/schemas/Shelf'
        tags:
          type: array
          items:
            type: string
        duration:
          type: string
          pattern: ^\d+s$
    Problem:
      type: object
    Shelf:
      type: object
  securitySchemes:
    bearer:
      type: http
      description: Service-issued JWT.
      scheme: bearer
      bearerFormat: JWT
    api_key:
      type: apiKey
      name: X-Api-Key
      in: header
security:
  - api_key: [read]
tags:
  - name: books
    summary: Books
    description: Book operations
    parent: library
    kind: nav
  - name: library
    description: Everything
`
	if string(got) != want {
		t.Fatalf("golden mismatch:\n--- got ---\n%s\n--- want ---\n%s\n--- first diff line ---\n%s",
			got, want, firstDiffLine(string(got), want))
	}
}

func firstDiffLine(got, want string) string {
	gotLines := strings.Split(got, "\n")
	wantLines := strings.Split(want, "\n")
	for i := 0; i < len(gotLines) && i < len(wantLines); i++ {
		if gotLines[i] != wantLines[i] {
			return "line " + string(rune('0'+i%10)) + ": got " + gotLines[i] + " | want " + wantLines[i]
		}
	}
	return "length mismatch"
}
