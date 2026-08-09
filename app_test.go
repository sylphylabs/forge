package forge

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/sylphylabs/forge/registry"
	"github.com/sylphylabs/forge/transport/grpc"
	"github.com/sylphylabs/forge/transport/http"
)

type mockRegistry struct {
	lk      sync.Mutex
	service map[string]*registry.ServiceInstance
}

type errorRegistry struct {
	registerErr   error
	deregisterErr error
	registerWait  <-chan struct{}
	registered    chan struct{}
	deregistered  chan struct{}
}

type delayedRegistry struct {
	registerStarted chan struct{}
	registerRelease chan struct{}
	registered      chan struct{}
	deregistered    chan struct{}
}

type lifecycleServer struct {
	started  chan struct{}
	stopped  chan struct{}
	exited   chan struct{}
	stopOnce sync.Once
	startErr error
	exitErr  error
	stopErr  error
}

func newLifecycleServer() *lifecycleServer {
	return &lifecycleServer{
		started: make(chan struct{}),
		stopped: make(chan struct{}),
		exited:  make(chan struct{}),
	}
}

func (s *lifecycleServer) Start(context.Context) error {
	close(s.started)
	defer close(s.exited)
	if s.startErr != nil {
		return s.startErr
	}
	<-s.stopped
	return s.exitErr
}

func (s *lifecycleServer) Stop(context.Context) error {
	s.stopOnce.Do(func() { close(s.stopped) })
	return s.stopErr
}

func (r *errorRegistry) Register(ctx context.Context, _ *registry.ServiceInstance) error {
	if r.registerWait != nil {
		select {
		case <-r.registerWait:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if r.registered != nil {
		close(r.registered)
	}
	return r.registerErr
}

func (r *errorRegistry) Deregister(context.Context, *registry.ServiceInstance) error {
	if r.deregistered != nil {
		close(r.deregistered)
	}
	return r.deregisterErr
}

func (r *delayedRegistry) Register(context.Context, *registry.ServiceInstance) error {
	close(r.registerStarted)
	<-r.registerRelease // Deliberately ignore cancellation to exercise the ownership handshake.
	close(r.registered)
	return nil
}

func (r *delayedRegistry) Deregister(context.Context, *registry.ServiceInstance) error {
	close(r.deregistered)
	return nil
}

type endpointLifecycleServer struct {
	*lifecycleServer
	endpointBuilt chan struct{}
}

func newEndpointLifecycleServer() *endpointLifecycleServer {
	return &endpointLifecycleServer{
		lifecycleServer: newLifecycleServer(),
		endpointBuilt:   make(chan struct{}),
	}
}

func (s *endpointLifecycleServer) Endpoint() (*url.URL, error) {
	close(s.endpointBuilt)
	return url.Parse("test://127.0.0.1")
}

func requireClosed(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	default:
		t.Fatalf("%s was not closed", name)
	}
}

func requireOpen(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s was closed", name)
	default:
	}
}

func (r *mockRegistry) Register(_ context.Context, service *registry.ServiceInstance) error {
	if service == nil || service.ID == "" {
		return errors.New("no service id")
	}
	r.lk.Lock()
	defer r.lk.Unlock()
	r.service[service.ID] = service
	return nil
}

// Deregister the registration.
func (r *mockRegistry) Deregister(_ context.Context, service *registry.ServiceInstance) error {
	r.lk.Lock()
	defer r.lk.Unlock()
	if r.service[service.ID] == nil {
		return errors.New("deregister service not found")
	}
	delete(r.service, service.ID)
	return nil
}

func TestApp(t *testing.T) {
	hs := http.NewServer()
	gs := grpc.NewServer()
	app := New(
		Name("forge"),
		Version("v1.0.0"),
		Server(hs, gs),
		BeforeStart(func(_ context.Context) error {
			t.Log("BeforeStart...")
			return nil
		}),
		BeforeStop(func(_ context.Context) error {
			t.Log("BeforeStop...")
			return nil
		}),
		AfterStart(func(_ context.Context) error {
			t.Log("AfterStart...")
			return nil
		}),
		AfterStop(func(_ context.Context) error {
			t.Log("AfterStop...")
			return nil
		}),
		Registrar(&mockRegistry{service: make(map[string]*registry.ServiceInstance)}),
	)
	time.AfterFunc(time.Second, func() {
		_ = app.Stop()
	})
	if err := app.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestAppDefaultIDIsUUIDv4(t *testing.T) {
	id, err := uuid.Parse(New().ID())
	if err != nil {
		t.Fatal(err)
	}
	if version := id[6] >> 4; version != 4 {
		t.Fatalf("default application ID version = %d, want 4", version)
	}
}

func TestAppBeforeStartFailureInvokesTransportStopAfterEndpointBuild(t *testing.T) {
	startErr := errors.New("before start")
	server := newEndpointLifecycleServer()
	registerCalled := make(chan struct{})
	deregisterCalled := make(chan struct{})
	beforeStopCalled := make(chan struct{})
	afterStopCalled := make(chan struct{})
	app := New(
		Server(server),
		Registrar(&errorRegistry{
			registered:   registerCalled,
			deregistered: deregisterCalled,
		}),
		BeforeStart(func(context.Context) error {
			requireClosed(t, server.endpointBuilt, "endpoint build signal")
			return startErr
		}),
		BeforeStop(func(context.Context) error {
			close(beforeStopCalled)
			return nil
		}),
		AfterStop(func(context.Context) error {
			close(afterStopCalled)
			return nil
		}),
	)

	err := app.Run()
	if !errors.Is(err, startErr) {
		t.Fatalf("Run() error = %v, want %v", err, startErr)
	}
	requireClosed(t, server.stopped, "server stop signal")
	requireOpen(t, server.started, "server start signal")
	requireOpen(t, registerCalled, "register signal")
	requireOpen(t, deregisterCalled, "deregister signal")
	requireClosed(t, beforeStopCalled, "BeforeStop signal")
	requireClosed(t, afterStopCalled, "AfterStop signal")
	requireClosed(t, app.ctx.Done(), "application context")
}

func TestAppRegisterFailureRollsBackStartedServers(t *testing.T) {
	registerErr := errors.New("register")
	beforeStopErr := errors.New("before stop")
	serverStopErr := errors.New("server stop")
	afterStopErr := errors.New("after stop")
	server := newLifecycleServer()
	server.stopErr = serverStopErr
	registerCalled := make(chan struct{})
	deregisterCalled := make(chan struct{})
	app := New(
		Server(server),
		Registrar(&errorRegistry{
			registerErr:  registerErr,
			registerWait: server.started,
			registered:   registerCalled,
			deregistered: deregisterCalled,
		}),
		BeforeStop(func(context.Context) error { return beforeStopErr }),
		AfterStop(func(context.Context) error { return afterStopErr }),
	)

	err := app.Run()
	for _, want := range []error{registerErr, beforeStopErr, serverStopErr, afterStopErr} {
		if !errors.Is(err, want) {
			t.Errorf("Run() error %v does not contain %v", err, want)
		}
	}
	requireClosed(t, registerCalled, "register signal")
	requireOpen(t, deregisterCalled, "deregister signal")
	requireClosed(t, server.started, "server start signal")
	requireClosed(t, server.stopped, "server stop signal")
	requireClosed(t, server.exited, "server exit signal")
	requireClosed(t, app.ctx.Done(), "application context")
}

func TestAppAfterStartFailureRollsBackRegisteredServers(t *testing.T) {
	afterStartErr := errors.New("after start")
	beforeStopErr := errors.New("before stop")
	deregisterErr := errors.New("deregister")
	serverStopErr := errors.New("server stop")
	afterStopErr := errors.New("after stop")
	server := newLifecycleServer()
	server.stopErr = serverStopErr
	registerCalled := make(chan struct{})
	deregisterCalled := make(chan struct{})
	app := New(
		Server(server),
		Registrar(&errorRegistry{
			deregisterErr: deregisterErr,
			registerWait:  server.started,
			registered:    registerCalled,
			deregistered:  deregisterCalled,
		}),
		AfterStart(func(context.Context) error { return afterStartErr }),
		BeforeStop(func(context.Context) error { return beforeStopErr }),
		AfterStop(func(context.Context) error { return afterStopErr }),
	)

	err := app.Run()
	for _, want := range []error{afterStartErr, beforeStopErr, deregisterErr, serverStopErr, afterStopErr} {
		if !errors.Is(err, want) {
			t.Errorf("Run() error %v does not contain %v", err, want)
		}
	}
	requireClosed(t, registerCalled, "register signal")
	requireClosed(t, deregisterCalled, "deregister signal")
	requireClosed(t, server.started, "server start signal")
	requireClosed(t, server.stopped, "server stop signal")
	requireClosed(t, server.exited, "server exit signal")
	requireClosed(t, app.ctx.Done(), "application context")
}

func TestAppStartFailureRollsBackAndJoinsCleanupErrors(t *testing.T) {
	startErr := errors.New("server start")
	beforeStopErr := errors.New("before stop")
	serverStopErr := errors.New("server stop")
	afterStopErr := errors.New("after stop")
	server := newLifecycleServer()
	server.startErr = startErr
	server.stopErr = serverStopErr
	registered := make(chan struct{})
	deregistered := make(chan struct{})
	app := New(
		Server(server),
		Registrar(&errorRegistry{registered: registered, deregistered: deregistered}),
		BeforeStop(func(context.Context) error { return beforeStopErr }),
		AfterStop(func(context.Context) error { return afterStopErr }),
	)

	err := app.Run()
	for _, want := range []error{startErr, beforeStopErr, serverStopErr, afterStopErr} {
		if !errors.Is(err, want) {
			t.Errorf("Run() error %v does not contain %v", err, want)
		}
	}
	requireClosed(t, server.started, "server start signal")
	requireClosed(t, server.stopped, "server stop signal")
	requireClosed(t, server.exited, "server exit signal")
	requireClosed(t, registered, "register signal")
	requireClosed(t, deregistered, "deregister signal")
	requireClosed(t, app.ctx.Done(), "application context")
}

func TestAppPreservesCleanupErrorJoinedWithCancellation(t *testing.T) {
	cleanupErr := errors.New("cleanup")
	server := newLifecycleServer()
	server.stopErr = errors.Join(context.Canceled, cleanupErr)
	app := New(Server(server))
	go func() {
		<-server.started
		_ = app.Stop()
	}()

	err := app.Run()
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("Run() error = %v, want cleanup error", err)
	}
}

func TestAppReturnsUnexpectedServerCancellation(t *testing.T) {
	server := newLifecycleServer()
	server.startErr = context.Canceled
	app := New(Server(server))

	if err := app.Run(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

func TestAppSuppressesServerCancellationDuringRequestedStop(t *testing.T) {
	server := newLifecycleServer()
	server.exitErr = context.Canceled
	app := New(Server(server))
	go func() {
		<-server.started
		_ = app.Stop()
	}()

	if err := app.Run(); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}

func TestAppStopDuringRegisterRollsBackLateSuccess(t *testing.T) {
	server := newLifecycleServer()
	registrar := &delayedRegistry{
		registerStarted: make(chan struct{}),
		registerRelease: make(chan struct{}),
		registered:      make(chan struct{}),
		deregistered:    make(chan struct{}),
	}
	app := New(Server(server), Registrar(registrar))
	runErr := make(chan error, 1)
	go func() { runErr <- app.Run() }()

	<-registrar.registerStarted
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	requireOpen(t, registrar.deregistered, "deregister signal")
	close(registrar.registerRelease)
	if err := <-runErr; err != nil {
		t.Fatal(err)
	}
	requireClosed(t, registrar.registered, "register signal")
	requireClosed(t, registrar.deregistered, "deregister signal")
	requireClosed(t, server.stopped, "server stop signal")
	requireClosed(t, server.exited, "server exit signal")
}

func TestAppAfterStopUsesFreshBoundedContext(t *testing.T) {
	called := false
	server := newLifecycleServer()
	var app *App
	app = New(
		Server(server),
		AfterStopTimeout(time.Second),
		AfterStop(func(ctx context.Context) error {
			called = true
			if err := ctx.Err(); err != nil {
				t.Fatalf("AfterStop context is already canceled: %v", err)
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("AfterStop context has no deadline")
			}
			if info, ok := FromContext(ctx); !ok || info != app {
				t.Fatal("AfterStop context lost application information")
			}
			return nil
		}),
	)
	go func() {
		<-server.started
		_ = app.Stop()
	}()
	if err := app.Run(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("AfterStop hook was not called")
	}
}

func TestAppJoinsLifecycleErrorsAndStillCancels(t *testing.T) {
	beforeErr := errors.New("before stop")
	deregisterErr := errors.New("deregister")
	afterErr := errors.New("after stop")
	server := newLifecycleServer()
	registered := make(chan struct{})
	app := New(
		Server(server),
		Registrar(&errorRegistry{deregisterErr: deregisterErr, registered: registered}),
		BeforeStop(func(context.Context) error { return beforeErr }),
		AfterStop(func(context.Context) error { return afterErr }),
	)
	go func() {
		<-registered
		_ = app.Stop()
	}()

	err := app.Run()
	for _, want := range []error{beforeErr, deregisterErr, afterErr} {
		if !errors.Is(err, want) {
			t.Errorf("Run() error %v does not contain %v", err, want)
		}
	}
	select {
	case <-app.ctx.Done():
	default:
		t.Fatal("application context was not canceled")
	}
}

func TestAppStopIsIdempotent(t *testing.T) {
	var calls int
	app := New(BeforeStop(func(context.Context) error {
		calls++
		return nil
	}))
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("BeforeStop called %d times, want 1", calls)
	}
}

func TestAppConcurrentStopIsIdempotent(t *testing.T) {
	const callers = 32
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	app := New(BeforeStop(func(context.Context) error {
		calls.Add(1)
		close(entered)
		<-release
		return nil
	}))

	errs := make(chan error, callers)
	for range callers {
		go func() { errs <- app.Stop() }()
	}
	<-entered
	close(release)
	for range callers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("BeforeStop called %d times, want 1", got)
	}
}

func TestApp_ID(t *testing.T) {
	v := "123"
	o := New(ID(v))
	if !reflect.DeepEqual(v, o.ID()) {
		t.Fatalf("o.ID():%s is not equal to v:%s", o.ID(), v)
	}
}

func TestApp_Name(t *testing.T) {
	v := "123"
	o := New(Name(v))
	if !reflect.DeepEqual(v, o.Name()) {
		t.Fatalf("o.Name():%s is not equal to v:%s", o.Name(), v)
	}
}

func TestApp_Version(t *testing.T) {
	v := "123"
	o := New(Version(v))
	if !reflect.DeepEqual(v, o.Version()) {
		t.Fatalf("o.Version():%s is not equal to v:%s", o.Version(), v)
	}
}

func TestApp_Metadata(t *testing.T) {
	v := map[string]string{
		"a": "1",
		"b": "2",
	}
	o := New(Metadata(v))
	if !reflect.DeepEqual(v, o.Metadata()) {
		t.Fatalf("o.Metadata():%s is not equal to v:%s", o.Metadata(), v)
	}
}

func TestAppMetadataReturnsSnapshot(t *testing.T) {
	metadata := map[string]string{"region": "ap-northeast-1"}
	app := New(Metadata(metadata))
	metadata["region"] = "external-change"

	got := app.Metadata()
	if got["region"] != "ap-northeast-1" {
		t.Fatalf("Metadata()[region] = %q, want %q", got["region"], "ap-northeast-1")
	}
	got["region"] = "returned-map-change"
	if current := app.Metadata()["region"]; current != "ap-northeast-1" {
		t.Fatalf("Metadata()[region] after returned map mutation = %q", current)
	}
}

func TestApp_Endpoint(t *testing.T) {
	v := []string{"https://go-kratos.dev", "localhost"}
	var endpoints []*url.URL
	for _, urlStr := range v {
		if endpoint, err := url.Parse(urlStr); err != nil {
			t.Errorf("invalid endpoint:%v", urlStr)
		} else {
			endpoints = append(endpoints, endpoint)
		}
	}
	o := New(Endpoint(endpoints...))
	if instance, err := o.buildInstance(); err != nil {
		t.Error("build instance failed")
	} else {
		o.instance = instance
	}
	if !reflect.DeepEqual(o.Endpoint(), v) {
		t.Errorf("Endpoint() = %v, want %v", o.Endpoint(), v)
	}
}

func TestAppEndpointReturnsSnapshot(t *testing.T) {
	app := New()
	app.mu.Lock()
	app.endpoints = []string{"http://127.0.0.1:8000"}
	app.mu.Unlock()

	got := app.Endpoint()
	got[0] = "http://127.0.0.1:9000"
	if current := app.Endpoint()[0]; current != "http://127.0.0.1:8000" {
		t.Fatalf("Endpoint()[0] after returned slice mutation = %q", current)
	}
}

func TestApp_buildInstance(t *testing.T) {
	want := struct {
		id        string
		name      string
		version   string
		metadata  map[string]string
		endpoints []string
	}{
		id:      "1",
		name:    "forge",
		version: "v1.0.0",
		metadata: map[string]string{
			"a": "1",
			"b": "2",
		},
		endpoints: []string{"https://go-kratos.dev", "localhost"},
	}
	var endpoints []*url.URL
	for _, urlStr := range want.endpoints {
		if endpoint, err := url.Parse(urlStr); err != nil {
			t.Errorf("invalid endpoint:%v", urlStr)
		} else {
			endpoints = append(endpoints, endpoint)
		}
	}
	app := New(
		ID(want.id),
		Name(want.name),
		Version(want.version),
		Metadata(want.metadata),
		Endpoint(endpoints...),
	)
	if got, err := app.buildInstance(); err != nil {
		t.Error("build got failed")
	} else {
		if got.ID != want.id {
			t.Errorf("ID() = %v, want %v", got.ID, want.id)
		}
		if got.Name != want.name {
			t.Errorf("Name() = %v, want %v", got.Name, want.name)
		}
		if got.Version != want.version {
			t.Errorf("Version() = %v, want %v", got.Version, want.version)
		}
		if !reflect.DeepEqual(got.Endpoints, want.endpoints) {
			t.Errorf("Endpoint() = %v, want %v", got.Endpoints, want.endpoints)
		}
		if !reflect.DeepEqual(got.Metadata, want.metadata) {
			t.Errorf("Metadata() = %v, want %v", got.Metadata, want.metadata)
		}
	}
}

func TestApp_Context(t *testing.T) {
	type fields struct {
		id       string
		version  string
		name     string
		instance *registry.ServiceInstance
		metadata map[string]string
		want     struct {
			id       string
			version  string
			name     string
			endpoint []string
			metadata map[string]string
		}
	}
	tests := []fields{
		{
			id:       "1",
			name:     "forge-v1",
			instance: &registry.ServiceInstance{Endpoints: []string{"https://go-kratos.dev", "localhost"}},
			metadata: map[string]string{},
			version:  "v1",
			want: struct {
				id       string
				version  string
				name     string
				endpoint []string
				metadata map[string]string
			}{
				id: "1", version: "v1", name: "forge-v1", endpoint: []string{"https://go-kratos.dev", "localhost"},
				metadata: map[string]string{},
			},
		},
		{
			id:       "2",
			name:     "forge-v2",
			instance: &registry.ServiceInstance{Endpoints: []string{"test"}},
			metadata: map[string]string{"forge": "https://github.com/go-kratos/kratos"},
			version:  "v2",
			want: struct {
				id       string
				version  string
				name     string
				endpoint []string
				metadata map[string]string
			}{
				id: "2", version: "v2", name: "forge-v2", endpoint: []string{"test"},
				metadata: map[string]string{"forge": "https://github.com/go-kratos/kratos"},
			},
		},
		{
			id:       "3",
			name:     "forge-v3",
			instance: nil,
			metadata: make(map[string]string),
			version:  "v3",
			want: struct {
				id       string
				version  string
				name     string
				endpoint []string
				metadata map[string]string
			}{
				id: "3", version: "v3", name: "forge-v3", endpoint: nil,
				metadata: map[string]string{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &App{
				opts:     options{id: tt.id, name: tt.name, metadata: tt.metadata, version: tt.version},
				ctx:      context.Background(),
				cancel:   nil,
				instance: tt.instance,
			}

			ctx := NewContext(context.Background(), a)

			if got, ok := FromContext(ctx); ok {
				if got.ID() != tt.want.id {
					t.Errorf("ID() = %v, want %v", got.ID(), tt.want.id)
				}
				if got.Name() != tt.want.name {
					t.Errorf("Name() = %v, want %v", got.Name(), tt.want.name)
				}
				if got.Version() != tt.want.version {
					t.Errorf("Version() = %v, want %v", got.Version(), tt.want.version)
				}
				if !reflect.DeepEqual(got.Endpoint(), tt.want.endpoint) {
					t.Errorf("Endpoint() = %v, want %v", got.Endpoint(), tt.want.endpoint)
				}
				if !reflect.DeepEqual(got.Metadata(), tt.want.metadata) {
					t.Errorf("Metadata() = %v, want %v", got.Metadata(), tt.want.metadata)
				}
			} else {
				t.Errorf("ok() = %v, want %v", ok, true)
			}
		})
	}
}

func TestApp_ContextCanceled(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	stopFn := func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			t.Fatal("context should not be done yet")
		default:
		}
		return nil
	}
	app := New(Context(ctx), Server(&mockServer{stopFn: stopFn}), StopTimeout(time.Hour))
	time.AfterFunc(time.Millisecond*10, stop)
	_ = app.Run()
}

// gracefulServer records the shutdown call sequence App drives on a server
// that implements transport.GracefulStopper.
type gracefulServer struct {
	*lifecycleServer
	healthy      atomic.Bool
	gracefulErr  error
	gracefulHook func(context.Context)
	calls        []string
	callsMu      sync.Mutex
}

func newGracefulServer() *gracefulServer {
	return &gracefulServer{lifecycleServer: newLifecycleServer()}
}

func (s *gracefulServer) Start(ctx context.Context) error {
	s.healthy.Store(true)
	return s.lifecycleServer.Start(ctx)
}

func (s *gracefulServer) Healthz() bool { return s.healthy.Load() }

func (s *gracefulServer) record(call string) {
	s.callsMu.Lock()
	s.calls = append(s.calls, call)
	s.callsMu.Unlock()
}

func (s *gracefulServer) callSequence() []string {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *gracefulServer) GracefulStop(ctx context.Context) error {
	s.healthy.Store(false)
	s.record("GracefulStop")
	if s.gracefulHook != nil {
		s.gracefulHook(ctx)
	}
	if s.gracefulErr != nil {
		return s.gracefulErr
	}
	s.stopOnce.Do(func() { close(s.stopped) })
	return nil
}

func (s *gracefulServer) Stop(ctx context.Context) error {
	s.healthy.Store(false)
	s.record("Stop")
	return s.lifecycleServer.Stop(ctx)
}

func TestAppShutdownPrefersGracefulStop(t *testing.T) {
	server := newGracefulServer()
	app := New(Server(server), StopTimeout(time.Second))
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run() }()
	<-server.started
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if got, want := server.callSequence(), []string{"GracefulStop"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("call sequence = %v, want %v", got, want)
	}
}

func TestAppShutdownFallsBackToStopOnAbandonedDrain(t *testing.T) {
	server := newGracefulServer()
	server.gracefulErr = context.DeadlineExceeded
	server.gracefulHook = func(ctx context.Context) { <-ctx.Done() }
	app := New(Server(server), StopTimeout(50*time.Millisecond))
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run() }()
	<-server.started
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("abandoned drain is the designed fallback, want nil error, got %v", err)
	}
	if got, want := server.callSequence(), []string{"GracefulStop", "Stop"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("call sequence = %v, want %v", got, want)
	}
	requireClosed(t, server.stopped, "server stop signal")
}

func TestAppShutdownJoinsIndependentDrainError(t *testing.T) {
	drainErr := errors.New("drain failed")
	server := newGracefulServer()
	server.gracefulErr = drainErr
	app := New(Server(server), StopTimeout(time.Second))
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run() }()
	<-server.started
	_ = app.Stop()
	err := <-errCh
	if !errors.Is(err, drainErr) {
		t.Fatalf("Run() error = %v, want %v joined", err, drainErr)
	}
	if got, want := server.callSequence(), []string{"GracefulStop", "Stop"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("call sequence = %v, want %v", got, want)
	}
}

// TestAppShutdownWithoutGracefulStopperIsUnchanged proves zero breakage: a
// third-party server implementing only Start and Stop sees exactly the calls
// it saw before the capability interfaces existed.
func TestAppShutdownWithoutGracefulStopperIsUnchanged(t *testing.T) {
	var stopCalls atomic.Int32
	server := newLifecycleServer()
	wrapped := &countingServer{lifecycleServer: server, stopCalls: &stopCalls}
	app := New(Server(wrapped), StopTimeout(time.Second))
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run() }()
	<-server.started
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if got := stopCalls.Load(); got != 1 {
		t.Fatalf("Stop calls = %d, want exactly 1", got)
	}
	requireClosed(t, server.stopped, "server stop signal")
	requireClosed(t, server.exited, "server exit signal")
}

// countingServer implements only transport.Server, mimicking a third-party
// transport that predates the capability interfaces.
type countingServer struct {
	*lifecycleServer
	stopCalls *atomic.Int32
}

func (s *countingServer) Stop(ctx context.Context) error {
	s.stopCalls.Add(1)
	return s.lifecycleServer.Stop(ctx)
}

func TestAppHealthzAggregatesHealthzers(t *testing.T) {
	healthy := newGracefulServer()
	healthy.healthy.Store(true)
	unhealthy := newGracefulServer()
	opaque := &mockServer{} // no Healthz: makes no claim

	if app := New(Server(healthy, opaque)); !app.Healthz() {
		t.Fatal("all reporting servers healthy, want true")
	}
	if app := New(Server(healthy, unhealthy)); app.Healthz() {
		t.Fatal("one reporting server unhealthy, want false")
	}
	if app := New(); !app.Healthz() {
		t.Fatal("no servers, want vacuous true")
	}
}

func TestAppHealthzTurnsFalseDuringShutdown(t *testing.T) {
	server := newGracefulServer()
	app := New(Server(server), StopTimeout(time.Second))
	errCh := make(chan error, 1)
	go func() { errCh <- app.Run() }()
	<-server.started
	if !app.Healthz() {
		t.Fatal("running server, want healthy")
	}
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if app.Healthz() {
		t.Fatal("stopped server, want unhealthy")
	}
}
