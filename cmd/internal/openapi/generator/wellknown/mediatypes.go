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
	"github.com/sylphylabs/forge/cmd/internal/openapi/model"
)

func NewGoogleAPIHTTPBodyMediaType() model.MediaTypes {
	return model.MediaTypes{
		{Name: "*/*", Value: &model.MediaType{}},
	}
}

func NewApplicationJSONMediaType(schema *model.Schema) model.MediaTypes {
	return model.MediaTypes{
		{Name: "application/json", Value: &model.MediaType{Schema: schema}},
	}
}

// NewProblemJSONMediaType is the media type of a Forge error response.
//
// The runtime error encoder always serves application/problem+json (RFC 9457),
// and the client accepts a problem document under no other media type, so an
// error response documented as anything else would describe a body the client
// refuses to parse.
func NewProblemJSONMediaType(schema *model.Schema) model.MediaTypes {
	return model.MediaTypes{
		{Name: "application/problem+json", Value: &model.MediaType{Schema: schema}},
	}
}
