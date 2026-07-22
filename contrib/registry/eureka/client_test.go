package eureka

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDoRetriesAcrossServersAndReplaysBody(t *testing.T) {
	var hosts []string
	var bodies []string
	client := NewClient([]string{"http://one", "http://two"}, WithMaxRetry(4))
	client.pickStart = func(int) int { return 0 }
	client.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		hosts = append(hosts, request.URL.Host)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, string(body))
		if len(hosts) < 5 {
			return nil, errors.New("temporary transport error")
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       http.NoBody,
			Header:     make(http.Header),
		}, nil
	})}

	err := client.do(t.Context(), http.MethodPost, []string{"apps", "GREETER"}, bytes.NewBufferString("payload"), nil)
	if err != nil {
		t.Fatal(err)
	}
	wantHosts := []string{"one", "two", "one", "two", "one"}
	if len(hosts) != len(wantHosts) {
		t.Fatalf("hosts = %v, want %v", hosts, wantHosts)
	}
	for i := range hosts {
		if hosts[i] != wantHosts[i] {
			t.Fatalf("hosts = %v, want %v", hosts, wantHosts)
		}
		if bodies[i] != "payload" {
			t.Fatalf("body %d = %q, want payload", i, bodies[i])
		}
	}
}

func TestDoWithZeroRetriesMakesOneAttempt(t *testing.T) {
	var attempts int
	client := NewClient([]string{"http://one"}, WithMaxRetry(0))
	client.pickStart = func(int) int { return 0 }
	client.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return nil, errors.New("offline")
	})}

	err := client.do(context.Background(), http.MethodGet, nil, nil, nil)
	if err == nil {
		t.Fatal("do() succeeded")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestDoRejectsEmptyServerList(t *testing.T) {
	client := NewClient(nil)
	if err := client.do(t.Context(), http.MethodGet, nil, nil, nil); err == nil {
		t.Fatal("do() succeeded without server URLs")
	}
}
