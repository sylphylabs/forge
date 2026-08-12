package ratelimit

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/sylphylabs/forge/config"
)

var _ config.Value = (*testValue)(nil)

// testValue is a minimal config.Value carrying one decoded config node, as a
// governance section entry would.
type testValue struct {
	val any
}

func newTestValue(val any) *testValue { return &testValue{val: val} }

func (v *testValue) Bool() (bool, error)              { return false, errors.ErrUnsupported }
func (v *testValue) Int() (int64, error)              { return 0, errors.ErrUnsupported }
func (v *testValue) Float() (float64, error)          { return 0, errors.ErrUnsupported }
func (v *testValue) String() (string, error)          { return "", errors.ErrUnsupported }
func (v *testValue) Duration() (time.Duration, error) { return 0, errors.ErrUnsupported }
func (v *testValue) Slice() ([]config.Value, error)   { return nil, errors.ErrUnsupported }
func (v *testValue) Map() (map[string]config.Value, error) {
	return nil, errors.ErrUnsupported
}

func (v *testValue) Scan(obj any) error {
	data, err := json.Marshal(v.val)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, obj)
}
