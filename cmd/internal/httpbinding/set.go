package httpbinding

import (
	"fmt"
	"net/http"
)

// Set validates route equivalence and net/http ServeMux structural conflicts.
type Set struct {
	mux        *http.ServeMux
	matches    map[string]struct{}
	structures map[string]struct{}
}

func NewSet() *Set {
	return &Set{
		mux:        http.NewServeMux(),
		matches:    make(map[string]struct{}),
		structures: make(map[string]struct{}),
	}
}

func (s *Set) Add(binding *Binding) (err error) {
	matchKey := binding.Method + "\x00" + binding.Template.MatchKey()
	if _, exists := s.matches[matchKey]; exists {
		return fmt.Errorf("duplicate HTTP match set for %s %s", binding.Method, binding.Path)
	}
	s.matches[matchKey] = struct{}{}

	serveMuxPattern := binding.Template.ServeMuxPattern()
	structureKey := binding.Method + "\x00" + serveMuxPattern
	if _, exists := s.structures[structureKey]; exists {
		return nil
	}
	pattern := serveMuxPattern
	if binding.Method != "*" {
		pattern = binding.Method + " " + pattern
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("conflicting HTTP rule %s %s: %v", binding.Method, binding.Path, recovered)
		}
	}()
	s.mux.Handle(pattern, http.NotFoundHandler())
	s.structures[structureKey] = struct{}{}
	return nil
}
