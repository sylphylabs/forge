package config

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const (
	_testJSON = `
{
    "server":{
        "http":{
            "addr":"0.0.0.0",
			"port":80,
            "timeout":0.5,
			"enable_ssl":true
        },
        "grpc":{
            "addr":"0.0.0.0",
			"port":10080,
            "timeout":0.2
        }
    },
    "data":{
        "database":{
            "driver":"mysql",
            "source":"root:root@tcp(127.0.0.1:3306)/karta_id?parseTime=true"
        }
    },
	"endpoints":[
		"www.aaa.com",
		"www.bbb.org"
	]
}`
)

type testConfigStruct struct {
	Server struct {
		HTTP struct {
			Addr      string  `json:"addr"`
			Port      int     `json:"port"`
			Timeout   float64 `json:"timeout"`
			EnableSSL bool    `json:"enable_ssl"`
		} `json:"http"`
		GRPC struct {
			Addr    string  `json:"addr"`
			Port    int     `json:"port"`
			Timeout float64 `json:"timeout"`
		} `json:"grpc"`
	} `json:"server"`
	Data struct {
		Database struct {
			Driver string `json:"driver"`
			Source string `json:"source"`
		} `json:"database"`
	} `json:"data"`
	Endpoints []string `json:"endpoints"`
}

type testJSONSource struct {
	mu   sync.RWMutex
	data string
	sig  chan struct{}
	err  chan struct{}
}

func newTestJSONSource(data string) *testJSONSource {
	return &testJSONSource{data: data, sig: make(chan struct{}), err: make(chan struct{})}
}

func (p *testJSONSource) Load(context.Context) ([]*KeyValue, error) {
	p.mu.RLock()
	data := p.data
	p.mu.RUnlock()
	kv := &KeyValue{
		Key:    "json",
		Value:  []byte(data),
		Format: "json",
	}
	return []*KeyValue{kv}, nil
}

func (p *testJSONSource) setData(data string) {
	p.mu.Lock()
	p.data = data
	p.mu.Unlock()
}

func (p *testJSONSource) Watch(context.Context) (Watcher, error) {
	return newTestWatcher(p.sig, p.err), nil
}

type testWatcher struct {
	sig  chan struct{}
	err  chan struct{}
	exit chan struct{}
}

func newTestWatcher(sig, err chan struct{}) Watcher {
	return &testWatcher{sig: sig, err: err, exit: make(chan struct{})}
}

func (w *testWatcher) Next(ctx context.Context) ([]*KeyValue, error) {
	select {
	case <-w.sig:
		return nil, nil
	case <-w.err:
		return nil, errors.New("error")
	case <-w.exit:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (w *testWatcher) Stop() error {
	close(w.exit)
	return nil
}

func TestConfig(t *testing.T) {
	var (
		err            error
		httpAddr       = "0.0.0.0"
		httpTimeout    = 0.5
		grpcPort       = 10080
		endpoint1      = "www.aaa.com"
		databaseDriver = "mysql"
	)

	c, err := New(
		t.Context(),
		WithSource(newTestJSONSource(_testJSON)),
		WithDecoder(defaultDecoder),
		WithResolver(defaultResolver),
	)
	if err != nil {
		t.Fatal(err)
	}
	err = c.Close()
	if err != nil {
		t.Fatal(err)
	}

	jSource := newTestJSONSource(_testJSON)
	cf, err := New(t.Context(), WithSource(jSource))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cf.Close() })

	driver, err := cf.Value("data.database.driver").String()
	if err != nil {
		t.Fatal(err)
	}
	if databaseDriver != driver {
		t.Fatal("databaseDriver is not equal to val")
	}

	driverGet, err := Get[string](cf, "data.database.driver")
	if err != nil {
		t.Fatal(err)
	}
	if databaseDriver != driverGet {
		t.Errorf("Get[string] want: %s, got: %s", databaseDriver, driverGet)
	}

	type HTTPConfig struct {
		Addr string `json:"addr"`
		Port int    `json:"port"`
	}
	v, err := Get[HTTPConfig](cf, "server.http")
	if err != nil {
		t.Fatal(err)
	} else if v.Addr != httpAddr {
		t.Errorf("Get[HttpConfig] Addr want: %s, got: %s", httpAddr, v.Addr)
	} else if v.Port != 80 {
		t.Errorf("Get[HttpConfig] Port want: 80, got: %d", v.Port)
	}

	err = cf.Watch("endpoints", func(string, Value) {})
	if err != nil {
		t.Fatal(err)
	}

	jSource.sig <- struct{}{}
	jSource.err <- struct{}{}

	var testConf testConfigStruct
	err = cf.Scan(&testConf)
	if err != nil {
		t.Fatal(err)
	}
	if httpAddr != testConf.Server.HTTP.Addr {
		t.Errorf("testConf.Server.HTTP.Addr want: %s, got: %s", httpAddr, testConf.Server.HTTP.Addr)
	}
	if httpTimeout != testConf.Server.HTTP.Timeout {
		t.Errorf("testConf.Server.HTTP.Timeout want: %.1f, got: %.1f", httpTimeout, testConf.Server.HTTP.Timeout)
	}
	if !testConf.Server.HTTP.EnableSSL {
		t.Error("testConf.Server.HTTP.EnableSSL is not equal to true")
	}
	if grpcPort != testConf.Server.GRPC.Port {
		t.Errorf("testConf.Server.GRPC.Port want: %d, got: %d", grpcPort, testConf.Server.GRPC.Port)
	}
	if endpoint1 != testConf.Endpoints[0] {
		t.Errorf("testConf.Endpoints[0] want: %s, got: %s", endpoint1, testConf.Endpoints[0])
	}
	if len(testConf.Endpoints) != 2 {
		t.Error("len(testConf.Endpoints) is not equal to 2")
	}
}

func TestConfigWatchMissingKey(t *testing.T) {
	c, err := New(t.Context(), WithSource(newTestJSONSource(_testJSON)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.Watch("no.such.key", func(string, Value) {}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Watch(missing key) = %v, want ErrNotFound", err)
	}
}

func TestConfigWatchNilObserverPanics(t *testing.T) {
	c, err := New(t.Context(), WithSource(newTestJSONSource(_testJSON)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	defer func() {
		if recover() == nil {
			t.Fatal("Watch(nil observer) did not panic")
		}
	}()
	_ = c.Watch("endpoints", nil)
}

// Every observer registered for a key must see every change; a second
// registration must not displace the first.
func TestConfigWatchMultipleObservers(t *testing.T) {
	src := newTestJSONSource(`{"key":"one"}`)
	c, err := New(t.Context(), WithSource(src))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	first := make(chan string, 1)
	second := make(chan string, 1)
	if err := c.Watch("key", func(_ string, v Value) {
		got, _ := v.String()
		first <- got
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Watch("key", func(_ string, v Value) {
		got, _ := v.String()
		second <- got
	}); err != nil {
		t.Fatal(err)
	}

	src.setData(`{"key":"two"}`)
	src.sig <- struct{}{}
	for name, ch := range map[string]chan string{"first": first, "second": second} {
		select {
		case got := <-ch:
			if got != "two" {
				t.Fatalf("%s observer saw %q, want two", name, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for the %s observer", name)
		}
	}
}

func TestConfigWatchReloadsCrossSourceReferences(t *testing.T) {
	reference := newTestJSONSource(`{"endpoint":"${remote.endpoint}"}`)
	remote := newTestJSONSource(`{"remote":{"endpoint":"one"}}`)
	c, err := New(t.Context(), WithSource(reference, remote))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	value, err := c.Value("endpoint").String()
	if err != nil || value != "one" {
		t.Fatalf("initial endpoint = %q, %v", value, err)
	}
	updated := make(chan string, 1)
	if err = c.Watch("endpoint", func(_ string, value Value) {
		got, _ := value.String()
		updated <- got
	}); err != nil {
		t.Fatal(err)
	}

	remote.setData(`{"remote":{"endpoint":"two"}}`)
	remote.sig <- struct{}{}
	select {
	case got := <-updated:
		if got != "two" {
			t.Fatalf("updated endpoint = %q, want two", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for config update")
	}
	value, err = c.Value("endpoint").String()
	if err != nil || value != "two" {
		t.Fatalf("current endpoint = %q, %v", value, err)
	}
}

// failingWatchSource loads but cannot watch, which must fail construction
// and stop the watchers of sources constructed before it.
type failingWatchSource struct {
	inner *testJSONSource
}

func (s *failingWatchSource) Load(ctx context.Context) ([]*KeyValue, error) {
	return s.inner.Load(ctx)
}

func (s *failingWatchSource) Watch(context.Context) (Watcher, error) {
	return nil, errors.New("watch refused")
}

func TestNewRollsBackWatchersOnFailure(t *testing.T) {
	good := newTestJSONSource(`{"a":1}`)
	bad := &failingWatchSource{inner: newTestJSONSource(`{"b":2}`)}
	if _, err := New(t.Context(), WithSource(good, bad)); err == nil {
		t.Fatal("New with a failing source watch must return an error")
	}
	// The good source's watcher was stopped: nothing is left consuming its
	// signal channel.
	select {
	case good.sig <- struct{}{}:
		t.Fatal("good source watcher still consuming signals after failed New")
	default:
	}
}
