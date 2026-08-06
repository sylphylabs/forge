package httprule

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		pattern  string
		serveMux string
		vars     []Variable
	}{
		{pattern: "/", serveMux: "/{$}"},
		{pattern: "/v1/books", serveMux: "/v1/books"},
		{
			pattern:  "/v1/{name}",
			serveMux: "/v1/{__forge0}",
			vars:     []Variable{{FieldPath: "name", Template: "*"}},
		},
		{
			pattern:  "/v1/{name=publishers/*/books/*}",
			serveMux: "/v1/publishers/{__forge0}/books/{__forge1}",
			vars:     []Variable{{FieldPath: "name", Template: "publishers/*/books/*", Multi: true}},
		},
		{
			pattern:  "/v1/{name=**}:archive",
			serveMux: "/v1/{__forge0...}",
			vars:     []Variable{{FieldPath: "name", Template: "**", Multi: true}},
		},
		{pattern: "/v1/*", serveMux: "/v1/{__forge0}"},
		{pattern: "/v1/**", serveMux: "/v1/{__forge0...}"},
		{pattern: "/v1/books:archive", serveMux: "/v1/books:archive"},
		{pattern: "/v1/a%2Fb", serveMux: "/v1/a%2Fb"},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got, err := Parse(tt.pattern)
			if err != nil {
				t.Fatal(err)
			}
			if got.Pattern() != tt.pattern {
				t.Fatalf("Pattern() = %q, want %q", got.Pattern(), tt.pattern)
			}
			if got.ServeMuxPattern() != tt.serveMux {
				t.Fatalf("ServeMuxPattern() = %q, want %q", got.ServeMuxPattern(), tt.serveMux)
			}
			if !reflect.DeepEqual(got.Variables(), tt.vars) {
				t.Fatalf("Variables() = %#v, want %#v", got.Variables(), tt.vars)
			}
		})
	}
}

func TestParseRejectsInvalidTemplates(t *testing.T) {
	tests := []string{
		"", "v1/books", "/v1/", "/v1//books", "/v1/{name", "/v1/name}",
		"/v1/x{name}", "/v1/{name=}", "/v1/{name=foo/**/bar}",
		"/v1/{name=**}/books", "/v1/{name}/{name}", "/v1/{9name}",
		"/v1/{__forge0}",
		"/v1/{name=foo:bar}", "/v1/books:", "/v1/books:archive/more",
		"/v1/a b", "/v1/a%2", "/v1/{name={nested}}",
	}
	for _, pattern := range tests {
		t.Run(pattern, func(t *testing.T) {
			_, err := Parse(pattern)
			if err == nil {
				t.Fatal("expected an error")
			}
			var syntax *SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatalf("error = %T, want *SyntaxError", err)
			}
		})
	}
}

func TestExpand(t *testing.T) {
	tests := []struct {
		pattern string
		values  map[string]string
		want    string
		wantErr error
	}{
		{pattern: "/", want: "/"},
		{pattern: "/v1/{name}", values: map[string]string{"name": "a/b ?#%"}, want: "/v1/a%2Fb%20%3F%23%25"},
		{
			pattern: "/v1/{name=publishers/*/books/*}",
			values:  map[string]string{"name": "publishers/a b/books/1"},
			want:    "/v1/publishers/a%20b/books/1",
		},
		{pattern: "/v1/{name=**}:archive", values: map[string]string{"name": "a/b?c"}, want: "/v1/a/b%3Fc:archive"},
		{pattern: "/v1/*", wantErr: ErrUnboundWildcard},
		{
			pattern: "/v1/{name=publishers/*/books/*}",
			values:  map[string]string{"name": "organizations/acme/secrets/root"},
			wantErr: ErrPathMismatch,
		},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			template, err := Parse(tt.pattern)
			if err != nil {
				t.Fatal(err)
			}
			got, err := template.Expand(func(field string) (string, error) {
				value, ok := tt.values[field]
				if !ok {
					return "", errors.New("missing value")
				}
				return value, nil
			})
			if tt.wantErr != nil {
				if err == nil {
					t.Fatal("expected an error")
				}
				if tt.wantErr == ErrUnboundWildcard && !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("Expand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtract(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    map[string]string
	}{
		{pattern: "/", path: "/", want: map[string]string{}},
		{pattern: "/v1/{name}", path: "/v1/a%2Fb", want: map[string]string{"name": "a/b"}},
		{
			pattern: "/v1/{name=publishers/*/books/*}",
			path:    "/v1/publishers/a%2Fb/books/1",
			want:    map[string]string{"name": "publishers/a%2Fb/books/1"},
		},
		{
			pattern: "/v1/{name=publishers/*/books/*}",
			path:    "/v1/%70ublishers/a%2fb/books/%31",
			want:    map[string]string{"name": "publishers/a%2fb/books/1"},
		},
		{pattern: "/v1/{name=**}:archive", path: "/v1/a%2Fb/c%3Fd:archive", want: map[string]string{"name": "a%2Fb/c?d"}},
		{pattern: "/v1/{name}:archive", path: "/v1/a%3Ab:archive", want: map[string]string{"name": "a:b"}},
		{pattern: "/v1/{name}:archive", path: "/v1/a%3Ab%3Aarchive", want: map[string]string{"name": "a:b"}},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+tt.path, func(t *testing.T) {
			template, err := Parse(tt.pattern)
			if err != nil {
				t.Fatal(err)
			}
			got, err := template.Extract(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Extract() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestExtractValuesUsesVariableOrder(t *testing.T) {
	template, err := Parse("/v1/{parent=publishers/*}/books/{book}")
	if err != nil {
		t.Fatal(err)
	}
	got, err := template.ExtractValues("/v1/publishers/acme/books/42")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"publishers/acme", "42"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractValues() = %#v, want %#v", got, want)
	}
}

func BenchmarkExtractValues(b *testing.B) {
	template, err := Parse("/v1/{name=publishers/*/books/*}")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := template.ExtractValues("/v1/publishers/acme/books/42"); err != nil {
			b.Fatal(err)
		}
	}
}

func TestExtractRejectsMismatches(t *testing.T) {
	template, err := Parse("/v1/{name=publishers/*}:archive")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/v1/publishers/a:delete",
		"/v1/organizations/a:archive",
		"/v1/publishers:archive",
		"/v1/publishers/a/more:archive",
		"/v1/publishers/%ZZ:archive",
	} {
		if _, err := template.Extract(path); err == nil {
			t.Errorf("Extract(%q) succeeded", path)
		}
	}
}

func TestVariablesReturnsCopy(t *testing.T) {
	template, err := Parse("/v1/{name}")
	if err != nil {
		t.Fatal(err)
	}
	variables := template.Variables()
	variables[0].FieldPath = "changed"
	if got := template.Variables()[0].FieldPath; got != "name" {
		t.Fatalf("FieldPath = %q, want name", got)
	}
}

func TestMatchKeyIgnoresFieldNamesAndEscapeSpelling(t *testing.T) {
	first, err := Parse("/v1/{name=publishers/*}:archive")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse("/v1/{resource=%70ublishers/*}:archive")
	if err != nil {
		t.Fatal(err)
	}
	if first.MatchKey() != second.MatchKey() {
		t.Fatalf("match keys differ: %q != %q", first.MatchKey(), second.MatchKey())
	}
	if first.HasUnboundWildcard() {
		t.Fatal("variable wildcard reported as unbound")
	}
	unbound, err := Parse("/v1/*")
	if err != nil {
		t.Fatal(err)
	}
	if !unbound.HasUnboundWildcard() {
		t.Fatal("bare wildcard was not reported")
	}
}

func TestServeMuxPatternDispatch(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
	}{
		{pattern: "/", path: "/"},
		{pattern: "/v1/{name}", path: "/v1/a%2Fb"},
		{pattern: "/v1/{name=publishers/*/books/*}", path: "/v1/publishers/a%2Fb/books/1"},
		{pattern: "/v1/{name=**}:archive", path: "/v1/a/b:archive"},
		{pattern: "/v1/books:archive", path: "/v1/books:archive"},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			template, err := Parse(tt.pattern)
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			mux.HandleFunc("GET "+template.ServeMuxPattern(), func(w http.ResponseWriter, r *http.Request) {
				if _, err := template.Extract(r.URL.EscapedPath()); err != nil {
					t.Errorf("Extract() error = %v", err)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
			}
		})
	}
}
