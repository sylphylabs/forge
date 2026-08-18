// Copyright 2020 Google LLC. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package wellknown

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/sylphylabs/forge/cmd/internal/openapi/model"
)

// OpenAPI schema type names.
const (
	typeString  = "string"
	typeInteger = "integer"
	typeObject  = "object"
)

// fieldCode is the name of the status code property of the google.rpc.Status
// schema.
const fieldCode = "code"

func NewStringSchema() *model.Schema {
	return &model.Schema{Type: typeString}
}

func NewBooleanSchema() *model.Schema {
	return &model.Schema{Type: "boolean"}
}

func NewBytesSchema() *model.Schema {
	return &model.Schema{Type: typeString, Format: "bytes"}
}

func NewIntegerSchema(format string) *model.Schema {
	return &model.Schema{Type: typeInteger, Format: format}
}

func NewNumberSchema(format string) *model.Schema {
	return &model.Schema{Type: "number", Format: format}
}

func NewEnumSchema(enumType *string, field protoreflect.FieldDescriptor) *model.Schema {
	schema := &model.Schema{Format: "enum"}
	if enumType != nil && *enumType == typeString {
		schema.Type = typeString
		schema.Enum = make([]string, 0, field.Enum().Values().Len())
		for i := 0; i < field.Enum().Values().Len(); i++ {
			schema.Enum = append(schema.Enum, string(field.Enum().Values().Get(i).Name()))
		}
	} else {
		schema.Type = typeInteger
	}
	return schema
}

func NewListSchema(itemSchema *model.Schema) *model.Schema {
	return &model.Schema{Type: "array", Items: itemSchema}
}

// google.api.HttpBody will contain POST body data
// This is based on how Envoy handles google.api.HttpBody
func NewGoogleAPIHTTPBodySchema() *model.Schema {
	return &model.Schema{Type: typeString}
}

// google.protobuf.Timestamp is serialized as a string
func NewGoogleProtobufTimestampSchema() *model.Schema {
	return &model.Schema{Type: typeString, Format: "date-time"}
}

// google.protobuf.Duration is serialized as a string
//
// From: https://github.com/protocolbuffers/protobuf/blob/ece5ef6b9b6fa66ef4638335612284379ee4548f/src/google/protobuf/duration.proto
// In JSON format, the Duration type is encoded as a string rather than an
// object, where the string ends in the suffix "s" (indicating seconds) and
// is preceded by the number of seconds, with nanoseconds expressed as
// fractional seconds. For example, 3 seconds with 0 nanoseconds should be
// encoded in JSON format as "3s", while 3 seconds and 1 nanosecond should
// be expressed in JSON format as "3.000000001s", and 3 seconds and 1
// microsecond should be expressed in JSON format as "3.000001s".
//
// The fields of message google.protobuf.Duration are further described as:
// "int64 seconds"
// Signed seconds of the span of time. Must be from -315,576,000,000
// to +315,576,000,000 inclusive. Note: these bounds are computed from:
// 60 sec/min * 60 min/hr * 24 hr/day * 365.25 days/year * 10000 years
// `int32 nanos`
// Signed fractions of a second at nanosecond resolution of the span
// of time. Durations less than one second are represented with a 0
// `seconds` field and a positive or negative `nanos` field. For durations
// of one second or more, a non-zero value for the `nanos` field must be
// of the same sign as the `seconds` field. Must be from -999,999,999
// to +999,999,999 inclusive.
//
// This leads to the regex below limiting range from -315.576,000,000s to 315,576,000,000s
// allowing -0.999,999,999s to 0.999,999,999s in the floating precision range.
// That full range cannot be expressed precisely in float64 as demonstrated in
// the example at https://go.dev/play/p/XNtuhwdyu8Y for your reference.
// So the well known type google.protobuf.Duration needs a string.
//
// Please note that JSON schemas duration format is NOT the same, as that uses
// a different syntax starting with "P", supports daylight saving times and other
// different features, so it is NOT compatible.
func NewGoogleProtobufDurationSchema() *model.Schema {
	return &model.Schema{
		Type:    typeString,
		Pattern: `^-?(?:0|[1-9][0-9]{0,11})(?:\.[0-9]{1,9})?s$`,
		Description: "Represents a a duration between -315,576,000,000s and 315,576,000,000s " +
			"(around 10000 years). Precision is in nanoseconds. " +
			"1 nanosecond is represented as 0.000000001s",
	}
}

// google.type.Date is serialized as a string
func NewGoogleTypeDateSchema() *model.Schema {
	return &model.Schema{Type: typeString, Format: "date"}
}

// google.type.DateTime is serialized as a string
func NewGoogleTypeDateTimeSchema() *model.Schema {
	return &model.Schema{Type: typeString, Format: "date-time"}
}

// google.protobuf.FieldMask masks is serialized as a string
func NewGoogleProtobufFieldMaskSchema() *model.Schema {
	return &model.Schema{Type: typeString, Format: "field-mask"}
}

// google.protobuf.Struct is equivalent to a JSON object
func NewGoogleProtobufStructSchema() *model.Schema {
	return &model.Schema{Type: typeObject}
}

// google.protobuf.Value is handled specially
// See here for the details on the JSON mapping:
//
//	https://developers.google.com/protocol-buffers/docs/proto3#json
//
// and here:
//
//	https://developers.google.com/protocol-buffers/docs/reference/google.protobuf#google.protobuf.Value
func NewGoogleProtobufValueSchema(name string) *model.NamedSchema {
	return &model.NamedSchema{
		Name: name,
		Schema: &model.Schema{
			Description: "Represents a dynamically typed value which can be either null, " +
				"a number, a string, a boolean, a recursive struct value, or a list of values.",
		},
	}
}

// google.protobuf.Any is handled specially
// See here for the details on the JSON mapping:
//
//	https://developers.google.com/protocol-buffers/docs/proto3#json
func NewGoogleProtobufAnySchema(name string) *model.NamedSchema {
	allowed := true
	return &model.NamedSchema{
		Name: name,
		Schema: &model.Schema{
			Type:        typeObject,
			Description: "Contains an arbitrary serialized message along with a @type that describes the type of the serialized message.",
			Properties: []*model.NamedSchema{
				{
					Name: "@type",
					Schema: &model.Schema{
						Type:        typeString,
						Description: "The type of the serialized message.",
					},
				},
			},
			AdditionalProperties: &model.AdditionalProperties{Allowed: &allowed},
		},
	}
}

// google.rpc.Status is handled specially
func NewGoogleRPCStatusSchema(name string, anyName string) *model.NamedSchema {
	return &model.NamedSchema{
		Name: name,
		Schema: &model.Schema{
			Type: typeObject,
			Description: "The `Status` type defines a logical error model that is suitable for " +
				"different programming environments, including REST APIs and RPC APIs. It is used by " +
				"[gRPC](https://github.com/grpc). Each `Status` message contains three pieces of data: " +
				"error code, error message, and error details. You can find out more about this error " +
				"model and how to work with it in the " +
				"[API Design Guide](https://cloud.google.com/apis/design/errors).",
			Properties: []*model.NamedSchema{
				{
					Name: fieldCode,
					Schema: &model.Schema{
						Type:        typeInteger,
						Format:      "int32",
						Description: "The status code, which should be an enum value of [google.rpc.Code][google.rpc.Code].",
					},
				},
				{
					Name: "message",
					Schema: &model.Schema{
						Type: typeString,
						Description: "A developer-facing error message, which should be in " +
							"English. Any user-facing error message should be localized and sent in " +
							"the [google.rpc.Status.details][google.rpc.Status.details] field, or " +
							"localized by the client.",
					},
				},
				{
					Name: "details",
					Schema: &model.Schema{
						Type:        "array",
						Items:       &model.Schema{Ref: "#/components/schemas/" + anyName},
						Description: "A list of messages that carry the error details.  There is a common set of message types for APIs to use.",
					},
				},
			},
		},
	}
}

// NewForgeProblemSchema describes the Forge HTTP error response body.
//
// The document is served as RFC 9457 application/problem+json, but its members
// are the Forge errors contract's own vocabulary — kind, domain, reason,
// message, metadata, trace_id, violations — not RFC 9457's type/title/status/
// detail prose members. The shape mirrors the runtime problem encoder in
// transport/http; gRPC transports project the same error into google.rpc.Status
// with google.rpc.ErrorInfo details.
func NewForgeProblemSchema(name string) *model.NamedSchema {
	stringProperty := func(name, description string) *model.NamedSchema {
		return &model.NamedSchema{
			Name:   name,
			Schema: &model.Schema{Type: typeString, Description: description},
		}
	}
	violationSchema := &model.Schema{
		Type:        typeObject,
		Description: "A single field-level failure within an aggregate error.",
		Required:    []string{"field"},
		Properties: []*model.NamedSchema{
			stringProperty("field", "Path into the request message identifying what failed, for example \"user.email\"."),
			stringProperty("description", "Explanation of the failure in terms a caller can act on."),
		},
	}
	return &model.NamedSchema{
		Name: name,
		Schema: &model.Schema{
			Type: typeObject,
			Description: "Forge error response, served as RFC 9457 application/problem+json. " +
				"The members are the Forge errors contract's vocabulary: a caller branches on kind and reason. " +
				"Empty members are omitted. gRPC transports project the same error into google.rpc.Status with google.rpc.ErrorInfo details.",
			Required: []string{"kind"},
			Properties: []*model.NamedSchema{
				stringProperty("kind", "Stable classification of the failure, for example NOT_FOUND. The HTTP status line is authoritative when the two disagree."),
				stringProperty("domain", "Namespace owning the reason, typically the service's protobuf package."),
				stringProperty("reason", "Stable machine-readable error reason, normally generated from an annotated protobuf enum value."),
				stringProperty("message", "Human-readable error message."),
				{
					Name: "metadata",
					Schema: &model.Schema{
						Type:                 typeObject,
						Description:          "Bounded string metadata attached to the error.",
						AdditionalProperties: &model.AdditionalProperties{Schema: NewStringSchema()},
					},
				},
				stringProperty("trace_id", "Trace identifier of the request the error was produced in."),
				{
					Name: "violations",
					Schema: &model.Schema{
						Type:        "array",
						Description: "Field-level failures collected by a validation pass.",
						Items:       violationSchema,
					},
				},
			},
		},
	}
}

func NewGoogleProtobufMapFieldEntrySchema(valueFieldSchema *model.Schema) *model.Schema {
	return &model.Schema{
		Type:                 typeObject,
		AdditionalProperties: &model.AdditionalProperties{Schema: valueFieldSchema},
	}
}
