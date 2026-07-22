package kratos

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	"uuid"

	"golang.org/x/sync/errgroup"

	"github.com/openkratos/kratos/log"
	"github.com/openkratos/kratos/registry"
	"github.com/openkratos/kratos/transport"
)

// AppInfo is application context value.
type AppInfo interface {
	ID() string
	Name() string
	Version() string
	Metadata() map[string]string
	Endpoint() []string
}

// App is an application components lifecycle manager.
type App struct {
	opts     options
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	instance *registry.ServiceInstance
	stopOnce sync.Once
	stopErr  error
}

// New create an application lifecycle manager.
func New(opts ...Option) *App {
	o := options{
		ctx:              context.Background(),
		sigs:             []os.Signal{syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGINT},
		registrarTimeout: 10 * time.Second,
		stopTimeout:      10 * time.Second,
		afterStopTimeout: 10 * time.Second,
	}
	o.id = uuid.New().String()
	for _, opt := range opts {
		opt(&o)
	}
	if o.logger != nil {
		log.SetDefault(o.logger)
	}
	ctx, cancel := context.WithCancel(o.ctx)
	return &App{
		ctx:    ctx,
		cancel: cancel,
		opts:   o,
	}
}

// ID returns app instance id.
func (a *App) ID() string { return a.opts.id }

// Name returns service name.
func (a *App) Name() string { return a.opts.name }

// Version returns app version.
func (a *App) Version() string { return a.opts.version }

// Metadata returns service metadata.
func (a *App) Metadata() map[string]string { return a.opts.metadata }

// Endpoint returns endpoints.
func (a *App) Endpoint() []string {
	if a.instance != nil {
		return a.instance.Endpoints
	}
	return nil
}

// Run executes all OnStart hooks registered with the application's Lifecycle.
func (a *App) Run() error {
	instance, err := a.buildInstance()
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.instance = instance
	a.mu.Unlock()
	sctx := NewContext(a.ctx, a)
	eg, ctx := errgroup.WithContext(sctx)
	wg := sync.WaitGroup{}

	for _, fn := range a.opts.beforeStart {
		if err = fn(sctx); err != nil {
			return err
		}
	}
	octx := NewContext(a.opts.ctx, a)
	for _, srv := range a.opts.servers {
		server := srv
		eg.Go(func() error {
			<-ctx.Done() // wait for stop signal
			stopCtx := context.WithoutCancel(octx)
			if a.opts.stopTimeout > 0 {
				var cancel context.CancelFunc
				stopCtx, cancel = context.WithTimeout(stopCtx, a.opts.stopTimeout)
				defer cancel()
			}
			return server.Stop(stopCtx)
		})
		wg.Add(1)
		eg.Go(func() error {
			wg.Done() // here is to ensure server start has begun running before register, so defer is not needed
			return server.Start(octx)
		})
	}
	wg.Wait()
	if a.opts.registrar != nil {
		rctx, rcancel := context.WithTimeout(ctx, a.opts.registrarTimeout)
		defer rcancel()
		if err = a.opts.registrar.Register(rctx, instance); err != nil {
			return err
		}
	}
	for _, fn := range a.opts.afterStart {
		if err = fn(sctx); err != nil {
			return err
		}
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, a.opts.sigs...)
	defer signal.Stop(c)
	eg.Go(func() error {
		select {
		case <-ctx.Done():
			return a.Stop()
		case <-c:
			return a.Stop()
		}
	})
	runErr := eg.Wait()
	if errors.Is(runErr, context.Canceled) {
		runErr = nil
	}
	afterCtx := context.WithoutCancel(sctx)
	if a.opts.afterStopTimeout > 0 {
		var cancel context.CancelFunc
		afterCtx, cancel = context.WithTimeout(afterCtx, a.opts.afterStopTimeout)
		defer cancel()
	}
	return errors.Join(runErr, runHooks(afterCtx, a.opts.afterStop))
}

// Stop gracefully stops the application.
func (a *App) Stop() error {
	a.stopOnce.Do(func() {
		a.stopErr = a.stop()
	})
	return a.stopErr
}

func (a *App) stop() error {
	sctx := context.WithoutCancel(NewContext(a.ctx, a))
	if a.opts.stopTimeout > 0 {
		var cancel context.CancelFunc
		sctx, cancel = context.WithTimeout(sctx, a.opts.stopTimeout)
		defer cancel()
	}
	hookErr := runHooks(sctx, a.opts.beforeStop)

	a.mu.Lock()
	instance := a.instance
	a.mu.Unlock()
	var deregisterErr error
	if a.opts.registrar != nil && instance != nil {
		ctx, cancel := context.WithTimeout(sctx, a.opts.registrarTimeout)
		defer cancel()
		deregisterErr = a.opts.registrar.Deregister(ctx, instance)
	}
	if a.cancel != nil {
		a.cancel()
	}
	return errors.Join(hookErr, deregisterErr)
}

func runHooks(ctx context.Context, hooks []func(context.Context) error) error {
	var err error
	for _, hook := range hooks {
		err = errors.Join(err, hook(ctx))
	}
	return err
}

func (a *App) buildInstance() (*registry.ServiceInstance, error) {
	endpoints := make([]string, 0, len(a.opts.endpoints))
	for _, e := range a.opts.endpoints {
		endpoints = append(endpoints, e.String())
	}
	if len(endpoints) == 0 {
		for _, srv := range a.opts.servers {
			if r, ok := srv.(transport.Endpointer); ok {
				e, err := r.Endpoint()
				if err != nil {
					return nil, err
				}
				endpoints = append(endpoints, e.String())
			}
		}
	}
	return &registry.ServiceInstance{
		ID:        a.opts.id,
		Name:      a.opts.name,
		Version:   a.opts.version,
		Metadata:  a.opts.metadata,
		Endpoints: endpoints,
	}, nil
}

type appKey struct{}

// NewContext returns a new Context that carries value.
func NewContext(ctx context.Context, s AppInfo) context.Context {
	return context.WithValue(ctx, appKey{}, s)
}

// FromContext returns the Transport value stored in ctx, if any.
func FromContext(ctx context.Context) (s AppInfo, ok bool) {
	s, ok = ctx.Value(appKey{}).(AppInfo)
	return
}
