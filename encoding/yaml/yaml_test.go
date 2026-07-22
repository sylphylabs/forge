package yaml

import (
	"math"
	"reflect"
	"testing"
)

func TestCodec_Unmarshal(t *testing.T) {
	tests := []struct {
		data  string
		value any
	}{
		{
			"",
			(*struct{})(nil),
		},
		{
			"{}", &struct{}{},
		},
		{
			"v: hi",
			map[string]string{"v": "hi"},
		},
		{
			"v: hi", map[string]any{"v": "hi"},
		},
		{
			"v: true",
			map[string]string{"v": "true"},
		},
		{
			"v: true",
			map[string]any{"v": true},
		},
		{
			"v: 10",
			map[string]any{"v": 10},
		},
		{
			"v: 0b10",
			map[string]any{"v": 2},
		},
		{
			"v: 0xA",
			map[string]any{"v": 10},
		},
		{
			"v: 4294967296",
			map[string]int64{"v": 4294967296},
		},
		{
			"v: 0.1",
			map[string]any{"v": 0.1},
		},
		{
			"v: .1",
			map[string]any{"v": 0.1},
		},
		{
			"v: .Inf",
			map[string]any{"v": math.Inf(+1)},
		},
		{
			"v: -.Inf",
			map[string]any{"v": math.Inf(-1)},
		},
		{
			"v: -10",
			map[string]any{"v": -10},
		},
		{
			"v: -.1",
			map[string]any{"v": -0.1},
		},
	}
	for _, tt := range tests {
		v := reflect.ValueOf(tt.value).Type()
		value := reflect.New(v)
		err := (codec{}).Unmarshal([]byte(tt.data), value.Interface())
		if err != nil {
			t.Fatalf("(codec{}).Unmarshal should not return err")
		}
	}
	spec := struct {
		A string
		B map[string]any
	}{A: "a"}
	err := (codec{}).Unmarshal([]byte("v: hi"), &spec.B)
	if err != nil {
		t.Fatalf("(codec{}).Unmarshal should not return err")
	}
}

func TestCodec_Marshal(t *testing.T) {
	value := map[string]string{"v": "hi"}
	got, err := (codec{}).Marshal(value)
	if err != nil {
		t.Fatalf("should not return err")
	}
	if string(got) != "v: hi\n" {
		t.Fatalf("want \"v: hi\n\" return \"%s\"", string(got))
	}
}

func TestCodecCompatibility(t *testing.T) {
	type document struct {
		Enabled bool              `yaml:"enabled"`
		Labels  map[string]string `yaml:"labels"`
		Ports   []int             `yaml:"ports"`
	}
	want := document{
		Enabled: true,
		Labels:  map[string]string{"app": "openkratos"},
		Ports:   []int{8000, 9000},
	}
	data, err := (codec{}).Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got document
	if err := (codec{}).Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}

	var legacy struct {
		Enabled bool `yaml:"enabled"`
	}
	if err := (codec{}).Unmarshal([]byte("enabled: yes\n"), &legacy); err != nil {
		t.Fatal(err)
	}
	if !legacy.Enabled {
		t.Fatal("typed YAML 1.1 boolean compatibility was lost")
	}
}
