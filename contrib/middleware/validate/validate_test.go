package validate

import (
	"context"
	"errors"
	"testing"

	"github.com/sylphylabs/forge/contrib/middleware/validate/internal/testdata"
	kerrors "github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
)

type testcase struct {
	name string
	req  any
	err  bool
}

type legacyRequest struct {
	err error
}

func (r *legacyRequest) Validate() error { return r.err }

type mixedRequest struct {
	*testdata.Modern
	err error
}

func (r *mixedRequest) Validate() error { return r.err }

func TestTable(t *testing.T) {
	var mock middleware.UnaryHandler = func(context.Context, any) (any, error) { return nil, nil }

	tests := []testcase{
		{
			name: "valid_legacy",
			req:  &legacyRequest{},
			err:  false,
		},
		{
			name: "invalid_legacy",
			req:  &legacyRequest{err: errors.New("legacy validation failed")},
			err:  true,
		},
		{
			name: "valid_mixed",
			req:  &mixedRequest{Modern: &testdata.Modern{Name: "testcase", Age: 19}},
			err:  false,
		},
		{
			name: "invalid_mixed_modern",
			req:  &mixedRequest{Modern: &testdata.Modern{Name: "test", Age: 19}},
			err:  true,
		},
		{
			name: "invalid_mixed_legacy",
			req: &mixedRequest{
				Modern: &testdata.Modern{Name: "testcase", Age: 19},
				err:    errors.New("legacy validation failed"),
			},
			err: true,
		},
		{
			name: "valid_modern",
			req:  &testdata.Modern{Name: "testcase", Age: 19},
			err:  false,
		},
		{
			name: "invalid_modern1",
			req:  &testdata.Modern{Name: "testcase", Age: 10},
			err:  true,
		},
		{
			name: "invalid_modern2",
			req:  &testdata.Modern{Name: "test", Age: 100},
			err:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handle := ProtoValidate()(mock)
			_, err := handle(context.Background(), test.req)
			expect := test.err
			actual := kerrors.IsBadRequest(err)
			if expect != actual {
				t.Errorf("case %s expect %v, actual %v, err %v", test.name, expect, actual, err)
			}
		})
	}
}
