package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/sylphylabs/forge/internal/protojsonutil"
)

var (
	_ Value = (*atomicValue)(nil)
	_ Value = errValue{}
)

// Value is a read-only view of one configuration value.
//
// Each accessor converts the underlying value to the requested type and
// reports an error when the conversion is not possible; Scan unmarshals a
// structured value into a caller-supplied type. A Value obtained from
// [Config.Value] reads the current snapshot even after a reload — how the
// coordinator installs new data is not part of this surface, so a Value
// cannot be used to write configuration.
type Value interface {
	Bool() (bool, error)
	Int() (int64, error)
	Float() (float64, error)
	String() (string, error)
	Duration() (time.Duration, error)
	Slice() ([]Value, error)
	Map() (map[string]Value, error)
	Scan(any) error
}

// atomicValue holds one decoded config value: readers convert it while the
// coordinator swaps it on reload. The embedded atomic.Value is the
// coordinator's write hook; keeping the type unexported keeps that hook off
// the public [Value] surface.
type atomicValue struct {
	atomic.Value
}

func (v *atomicValue) typeAssertError() error {
	return fmt.Errorf("type assert to %v failed", reflect.TypeOf(v.Load()))
}

func (v *atomicValue) Bool() (bool, error) {
	switch val := v.Load().(type) {
	case bool:
		return val, nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return strconv.ParseBool(fmt.Sprint(val))
	case string:
		return strconv.ParseBool(val)
	}
	return false, v.typeAssertError()
}

func (v *atomicValue) Int() (int64, error) {
	switch val := v.Load().(type) {
	case int:
		return int64(val), nil
	case int8:
		return int64(val), nil
	case int16:
		return int64(val), nil
	case int32:
		return int64(val), nil
	case int64:
		return val, nil
	case uint:
		return int64(val), nil
	case uint8:
		return int64(val), nil
	case uint16:
		return int64(val), nil
	case uint32:
		return int64(val), nil
	case uint64:
		return int64(val), nil
	case float32:
		return int64(val), nil
	case float64:
		return int64(val), nil
	case string:
		return strconv.ParseInt(val, 10, 64)
	}
	return 0, v.typeAssertError()
}

func (v *atomicValue) Slice() ([]Value, error) {
	vals, ok := v.Load().([]any)
	if !ok {
		return nil, v.typeAssertError()
	}
	slices := make([]Value, 0, len(vals))
	for _, val := range vals {
		a := new(atomicValue)
		a.Store(val)
		slices = append(slices, a)
	}
	return slices, nil
}

func (v *atomicValue) Map() (map[string]Value, error) {
	vals, ok := v.Load().(map[string]any)
	if !ok {
		return nil, v.typeAssertError()
	}
	m := make(map[string]Value, len(vals))
	for key, val := range vals {
		a := new(atomicValue)
		a.Store(val)
		m[key] = a
	}
	return m, nil
}

func (v *atomicValue) Float() (float64, error) {
	switch val := v.Load().(type) {
	case int:
		return float64(val), nil
	case int8:
		return float64(val), nil
	case int16:
		return float64(val), nil
	case int32:
		return float64(val), nil
	case int64:
		return float64(val), nil
	case uint:
		return float64(val), nil
	case uint8:
		return float64(val), nil
	case uint16:
		return float64(val), nil
	case uint32:
		return float64(val), nil
	case uint64:
		return float64(val), nil
	case float32:
		return float64(val), nil
	case float64:
		return val, nil
	case string:
		return strconv.ParseFloat(val, 64)
	}
	return 0.0, v.typeAssertError()
}

func (v *atomicValue) String() (string, error) {
	switch val := v.Load().(type) {
	case string:
		return val, nil
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return fmt.Sprint(val), nil
	case []byte:
		return string(val), nil
	case fmt.Stringer:
		return val.String(), nil
	}
	return "", v.typeAssertError()
}

func (v *atomicValue) Duration() (time.Duration, error) {
	val, err := v.Int()
	if err != nil {
		return 0, err
	}
	return time.Duration(val), nil
}

func (v *atomicValue) Scan(obj any) error {
	data, err := json.Marshal(v.Load())
	if err != nil {
		return err
	}
	return protojsonutil.Unmarshal(data, obj)
}

// errValue is the Value of a key that does not exist: every accessor reports
// the lookup error.
type errValue struct {
	err error
}

func (v errValue) Bool() (bool, error)              { return false, v.err }
func (v errValue) Int() (int64, error)              { return 0, v.err }
func (v errValue) Float() (float64, error)          { return 0.0, v.err }
func (v errValue) Duration() (time.Duration, error) { return 0, v.err }
func (v errValue) String() (string, error)          { return "", v.err }
func (v errValue) Scan(any) error                   { return v.err }
func (v errValue) Slice() ([]Value, error)          { return nil, v.err }
func (v errValue) Map() (map[string]Value, error)   { return nil, v.err }
