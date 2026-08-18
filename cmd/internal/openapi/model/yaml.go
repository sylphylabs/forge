package model

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// YAML serializes the document deterministically. A non-empty comment is
// emitted as a header comment above the document.
func (d *Document) YAML(comment string) ([]byte, error) {
	root := d.node()
	if comment != "" {
		root.HeadComment = comment
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(root); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// JSON serializes the document deterministically as JSON. It renders the
// same node tree YAML rendering walks, so the two formats cannot disagree on
// content or order.
func (d *Document) JSON() ([]byte, error) {
	var buf bytes.Buffer
	if err := writeJSON(&buf, d.node()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeJSON renders one node of the document tree as JSON.
func writeJSON(buf *bytes.Buffer, node *yaml.Node) error {
	switch node.Kind {
	case yaml.MappingNode:
		buf.WriteByte('{')
		for i := 0; i+1 < len(node.Content); i += 2 {
			if i > 0 {
				buf.WriteByte(',')
			}
			key, err := json.Marshal(node.Content[i].Value)
			if err != nil {
				return err
			}
			buf.Write(key)
			buf.WriteByte(':')
			if err := writeJSON(buf, node.Content[i+1]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case yaml.SequenceNode:
		buf.WriteByte('[')
		for i, item := range node.Content {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeJSON(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!bool", "!!int", "!!float":
			buf.WriteString(node.Value)
		default:
			value, err := json.Marshal(node.Value)
			if err != nil {
				return err
			}
			buf.Write(value)
		}
	default:
		return fmt.Errorf("openapi model: cannot render node kind %d as JSON", node.Kind)
	}
	return nil
}

// mapping accumulates a YAML mapping node whose entries appear exactly in
// the order they are added.
type mapping struct {
	node *yaml.Node
}

func newMapping() *mapping {
	return &mapping{node: &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}}
}

func (m *mapping) set(key string, value *yaml.Node) {
	m.node.Content = append(m.node.Content, strScalar(key), value)
}

// setStr adds a string entry, omitting it when the value is empty.
func (m *mapping) setStr(key, value string) {
	if value != "" {
		m.set(key, strScalar(value))
	}
}

// setBool adds a boolean entry, omitting it when the value is false.
func (m *mapping) setBool(key string, value bool) {
	if value {
		m.set(key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	}
}

// setStrings adds a string sequence entry, omitting it when the list is
// empty.
func (m *mapping) setStrings(key string, values []string) {
	if len(values) == 0 {
		return
	}
	sequence := newSequence()
	for _, value := range values {
		sequence.Content = append(sequence.Content, strScalar(value))
	}
	m.set(key, sequence)
}

func strScalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func newSequence() *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
}

func (d *Document) node() *yaml.Node {
	m := newMapping()
	m.setStr("openapi", d.OpenAPI)
	m.setStr("$self", d.Self)
	m.set("info", d.Info.node())
	if len(d.Servers) > 0 {
		m.set("servers", serversNode(d.Servers))
	}
	paths := newMapping()
	for _, path := range d.Paths {
		paths.set(path.Path, path.Item.node())
	}
	m.set("paths", paths.node)
	if d.Components != nil {
		if node := d.Components.node(); len(node.Content) > 0 {
			m.set("components", node)
		}
	}
	if len(d.Security) > 0 {
		m.set("security", securityNode(d.Security))
	}
	if len(d.Tags) > 0 {
		tags := newSequence()
		for _, tag := range d.Tags {
			tags.Content = append(tags.Content, tag.node())
		}
		m.set("tags", tags)
	}
	return m.node
}

func (i Info) node() *yaml.Node {
	m := newMapping()
	m.setStr("title", i.Title)
	m.setStr("summary", i.Summary)
	m.setStr("description", i.Description)
	m.setStr("version", i.Version)
	return m.node
}

func serversNode(servers []*Server) *yaml.Node {
	sequence := newSequence()
	for _, server := range servers {
		m := newMapping()
		m.setStr("url", server.URL)
		m.setStr("description", server.Description)
		sequence.Content = append(sequence.Content, m.node)
	}
	return sequence
}

func (p *PathItem) node() *yaml.Node {
	m := newMapping()
	for _, entry := range []struct {
		method    string
		operation *Operation
	}{
		{"get", p.Get}, {"put", p.Put}, {"post", p.Post}, {"delete", p.Delete},
		{"options", p.Options}, {"head", p.Head}, {"patch", p.Patch},
		{"trace", p.Trace}, {"query", p.Query},
	} {
		if entry.operation != nil {
			m.set(entry.method, entry.operation.node())
		}
	}
	if len(p.AdditionalOperations) > 0 {
		operations := newMapping()
		for _, named := range p.AdditionalOperations {
			operations.set(named.Method, named.Operation.node())
		}
		m.set("additionalOperations", operations.node)
	}
	if len(p.Servers) > 0 {
		m.set("servers", serversNode(p.Servers))
	}
	return m.node
}

func (o *Operation) node() *yaml.Node {
	m := newMapping()
	m.setStrings("tags", o.Tags)
	m.setStr("summary", o.Summary)
	m.setStr("description", o.Description)
	m.setStr("operationId", o.OperationID)
	m.setBool("deprecated", o.Deprecated)
	if len(o.Parameters) > 0 {
		parameters := newSequence()
		for _, parameter := range o.Parameters {
			parameters.Content = append(parameters.Content, parameter.node())
		}
		m.set("parameters", parameters)
	}
	if o.RequestBody != nil {
		m.set("requestBody", o.RequestBody.node())
	}
	if len(o.Responses) > 0 {
		responses := newMapping()
		for _, named := range o.Responses {
			responses.set(named.Name, named.Response.node())
		}
		m.set("responses", responses.node)
	}
	if len(o.Security) > 0 {
		m.set("security", securityNode(o.Security))
	}
	if len(o.Servers) > 0 {
		m.set("servers", serversNode(o.Servers))
	}
	return m.node
}

func (p *Parameter) node() *yaml.Node {
	m := newMapping()
	m.setStr("name", p.Name)
	m.setStr("in", p.In)
	m.setStr("description", p.Description)
	m.setBool("required", p.Required)
	if p.Schema != nil {
		m.set("schema", p.Schema.node())
	}
	return m.node
}

func (r *RequestBody) node() *yaml.Node {
	m := newMapping()
	m.setStr("description", r.Description)
	if r.Content != nil {
		m.set("content", r.Content.node())
	}
	m.setBool("required", r.Required)
	return m.node
}

func (r *Response) node() *yaml.Node {
	m := newMapping()
	m.setStr("description", r.Description)
	if r.Content != nil {
		m.set("content", r.Content.node())
	}
	return m.node
}

func (mt MediaTypes) node() *yaml.Node {
	m := newMapping()
	for _, named := range mt {
		m.set(named.Name, named.Value.node())
	}
	return m.node
}

func (mt *MediaType) node() *yaml.Node {
	m := newMapping()
	if mt == nil {
		return m.node
	}
	if mt.Schema != nil {
		m.set("schema", mt.Schema.node())
	}
	if mt.ItemSchema != nil {
		m.set("itemSchema", mt.ItemSchema.node())
	}
	if len(mt.Encoding) > 0 {
		encoding := newMapping()
		for _, named := range mt.Encoding {
			encoding.set(named.Name, named.Value.node())
		}
		m.set("encoding", encoding.node)
	}
	if len(mt.PrefixEncoding) > 0 {
		sequence := newSequence()
		for _, encoding := range mt.PrefixEncoding {
			sequence.Content = append(sequence.Content, encoding.node())
		}
		m.set("prefixEncoding", sequence)
	}
	if mt.ItemEncoding != nil {
		m.set("itemEncoding", mt.ItemEncoding.node())
	}
	return m.node
}

func (e *Encoding) node() *yaml.Node {
	m := newMapping()
	m.setStr("contentType", e.ContentType)
	return m.node
}

func (s *Schema) node() *yaml.Node {
	m := newMapping()
	if s == nil {
		return m.node
	}
	if s.Ref != "" {
		m.setStr("$ref", s.Ref)
		return m.node
	}
	m.setStr("type", s.Type)
	m.setStr("format", s.Format)
	m.setStrings("enum", s.Enum)
	m.setStr("pattern", s.Pattern)
	m.setStr("description", s.Description)
	if s.HasExample || s.Example != "" {
		m.set("example", strScalar(s.Example))
	}
	m.setBool("readOnly", s.ReadOnly)
	m.setBool("writeOnly", s.WriteOnly)
	m.setStrings("required", s.Required)
	if len(s.Properties) > 0 {
		properties := newMapping()
		for _, named := range s.Properties {
			properties.set(named.Name, named.Schema.node())
		}
		m.set("properties", properties.node)
	}
	if s.AdditionalProperties != nil {
		if s.AdditionalProperties.Schema != nil {
			m.set("additionalProperties", s.AdditionalProperties.Schema.node())
		} else if s.AdditionalProperties.Allowed != nil {
			value := "false"
			if *s.AdditionalProperties.Allowed {
				value = "true"
			}
			m.set("additionalProperties", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: value})
		}
	}
	if s.Items != nil {
		m.set("items", s.Items.node())
	}
	if len(s.AllOf) > 0 {
		allOf := newSequence()
		for _, member := range s.AllOf {
			allOf.Content = append(allOf.Content, member.node())
		}
		m.set("allOf", allOf)
	}
	return m.node
}

func (c *Components) node() *yaml.Node {
	m := newMapping()
	if len(c.Schemas) > 0 {
		schemas := newMapping()
		for _, named := range c.Schemas {
			schemas.set(named.Name, named.Schema.node())
		}
		m.set("schemas", schemas.node)
	}
	if len(c.SecuritySchemes) > 0 {
		schemes := newMapping()
		for _, named := range c.SecuritySchemes {
			schemes.set(named.Name, named.Scheme.node())
		}
		m.set("securitySchemes", schemes.node)
	}
	return m.node
}

func (s *SecurityScheme) node() *yaml.Node {
	m := newMapping()
	m.setStr("type", s.Type)
	m.setStr("description", s.Description)
	m.setStr("name", s.Name)
	m.setStr("in", s.In)
	m.setStr("scheme", s.Scheme)
	m.setStr("bearerFormat", s.BearerFormat)
	return m.node
}

func securityNode(requirements []SecurityRequirement) *yaml.Node {
	sequence := newSequence()
	for _, requirement := range requirements {
		m := newMapping()
		for _, scheme := range requirement {
			scopes := newSequence()
			for _, scope := range scheme.Scopes {
				scopes.Content = append(scopes.Content, strScalar(scope))
			}
			scopes.Style = yaml.FlowStyle
			m.set(scheme.Name, scopes)
		}
		if len(m.node.Content) == 0 {
			m.node.Style = yaml.FlowStyle
		}
		sequence.Content = append(sequence.Content, m.node)
	}
	return sequence
}

func (t *Tag) node() *yaml.Node {
	m := newMapping()
	m.setStr("name", t.Name)
	m.setStr("summary", t.Summary)
	m.setStr("description", t.Description)
	m.setStr("parent", t.Parent)
	m.setStr("kind", t.Kind)
	return m.node
}
