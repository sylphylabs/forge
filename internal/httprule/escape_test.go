package httprule

import "testing"

var benchmarkEscapedString string

func TestEscapeSegment(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "unreserved", value: "publishers_1-books.go~", want: "publishers_1-books.go~"},
		{name: "reserved", value: "go lang/books?", want: "go%20lang%2Fbooks%3F"},
		{name: "utf8", value: "Forge\xe8\xb7\xaf\xe5\xbe\x84", want: "Forge%E8%B7%AF%E5%BE%84"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := escapeSegment(test.value); got != test.want {
				t.Fatalf("escapeSegment(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestDecodeMulti(t *testing.T) {
	for _, test := range []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "unescaped", value: "publishers/acme/books/1", want: "publishers/acme/books/1"},
		{name: "decode", value: "publishers/go%20lang/books/1", want: "publishers/go lang/books/1"},
		{name: "preserve encoded slash", value: "publishers/go%2Flang/books/1", want: "publishers/go%2Flang/books/1"},
		{name: "invalid", value: "publishers/%ZZ", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := decodeMulti(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatal("decodeMulti() succeeded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("decodeMulti(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func BenchmarkEscapeSegment(b *testing.B) {
	for _, value := range []string{"publishers", "go lang/books?"} {
		b.Run(value, func(b *testing.B) {
			for b.Loop() {
				benchmarkEscapedString = escapeSegment(value)
			}
		})
	}
}

func BenchmarkDecodeMulti(b *testing.B) {
	for _, value := range []string{"publishers/acme/books/1", "publishers/go%20lang/books/1"} {
		b.Run(value, func(b *testing.B) {
			for b.Loop() {
				benchmarkEscapedString, _ = decodeMulti(value)
			}
		})
	}
}
