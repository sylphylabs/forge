package kratos

import (
	"context"
	"errors"
	"maps"
	"os"
	"os/signal"
	"slices"
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
	opts               options
	ctx                context.Context
	cancel             context.CancelFunc
	mu                 sync.Mutex
	instance           *registry.ServiceInstance
	endpoints          []string
	registered         bool
	stopRequested      bool
	registrationClosed bool
	stopOnce           sync.Once
	stopErr            error
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
	o.metadata = maps.Clone(o.metadata)
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
func (a *App) Metadata() map[string]string { return maps.Clone(a.opts.metadata) }

// Endpoint returns endpoints.
func (a *App) Endpoint() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.endpoints == nil && a.instance != nil {
		return slices.Clone(a.instance.Endpoints)
	}
	return slices.Clone(a.endpoints)
}

// Run executes all OnStart hooks registered with the application's Lifecycle.
func (a *App) Run() error {
	sctx := NewContext(a.ctx, a)
	eg, ctx := errgroup.WithContext(sctx)
	octx := NewContext(a.opts.ctx, a)
	// errgroup returns only its first error; retain later cleanup errors too.
	var lifecycleErrMu sync.Mutex
	var lifecycleErr error
	recordLifecycleErr := func(err error) error {
		if err == nil {
			return err
		}
		if err == context.Canceled && a.shutdownRequested() {
			return err
		}
		lifecycleErrMu.Lock()
		lifecycleErr = errors.Join(lifecycleErr, err)
		lifecycleErrMu.Unlock()
		return err
	}
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
			return recordLifecycleErr(server.Stop(stopCtx))
		})
	}
	finish := func(startupErr error) error {
		if startupErr != nil {
			startupErr = errors.Join(startupErr, a.Stop())
		}
		waitErr := eg.Wait()
		lifecycleErrMu.Lock()
		runErr := lifecycleErr
		lifecycleErrMu.Unlock()
		if runErr == nil && waitErr != context.Canceled {
			runErr = waitErr
		}
		afterCtx := context.WithoutCancel(sctx)
		if a.opts.afterStopTimeout > 0 {
			var cancel context.CancelFunc
			afterCtx, cancel = context.WithTimeout(afterCtx, a.opts.afterStopTimeout)
			defer cancel()
		}
		return errors.Join(startupErr, runErr, runHooks(afterCtx, a.opts.afterStop))
	}

	instance, err := a.buildInstance()
	if err != nil {
		return finish(err)
	}
	a.mu.Lock()
	a.instance = instance
	a.endpoints = slices.Clone(instance.Endpoints)
	a.mu.Unlock()

	for _, fn := range a.opts.beforeStart {
		if err = fn(sctx); err != nil {
			return finish(err)
		}
	}
	wg := sync.WaitGroup{}
	for _, srv := range a.opts.servers {
		server := srv
		wg.Add(1)
		eg.Go(func() error {
			wg.Done() // here is to ensure server start has begun running before register, so defer is not needed
			return recordLifecycleErr(server.Start(octx))
		})
	}
	wg.Wait()
	if a.opts.registrar != nil {
		rctx, rcancel := context.WithTimeout(ctx, a.opts.registrarTimeout)
		defer rcancel()
		if err = a.opts.registrar.Register(rctx, instance); err != nil {
			return finish(err)
		}
		a.mu.Lock()
		registrationClosed := a.registrationClosed
		if !registrationClosed {
			a.registered = true
		}
		a.mu.Unlock()
		if registrationClosed {
			dctx := context.WithoutCancel(sctx)
			if err = a.deregister(dctx, instance); err != nil {
				recordLifecycleErr(err)
			}
			return finish(nil)
		}
	}
	for _, fn := range a.opts.afterStart {
		if err = fn(sctx); err != nil {
			return finish(err)
		}
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, a.opts.sigs...)
	defer signal.Stop(c)
	eg.Go(func() error {
		select {
		case <-ctx.Done():
			return recordLifecycleErr(a.Stop())
		case <-c:
			return recordLifecycleErr(a.Stop())
		}
	})
	return finish(nil)
}

// Stop gracefully stops the application.
func (a *App) Stop() error {
	a.stopOnce.Do(func() {
		a.stopErr = a.stop()
	})
	return a.stopErr
}

func (a *App) stop() error {
	a.mu.Lock()
	a.stopRequested = true
	a.mu.Unlock()

	sctx := context.WithoutCancel(NewContext(a.ctx, a))
	if a.opts.stopTimeout > 0 {
		var cancel context.CancelFunc
		sctx, cancel = context.WithTimeout(sctx, a.opts.stopTimeout)
		defer cancel()
	}
	hookErr := runHooks(sctx, a.opts.beforeStop)

	a.mu.Lock()
	a.registrationClosed = true
	instance := a.instance
	registered := a.registered
	a.mu.Unlock()
	var deregisterErr error
	if a.opts.registrar != nil && instance != nil && registered {
		deregisterErr = a.deregister(sctx, instance)
	}
	if a.cancel != nil {
		a.cancel()
	}
	return errors.Join(hookErr, deregisterErr)
}

func (a *App) deregister(ctx context.Context, instance *registry.ServiceInstance) error {
	ctx, cancel := context.WithTimeout(ctx, a.opts.registrarTimeout)
	defer cancel()
	return a.opts.registrar.Deregister(ctx, instance)
}

func (a *App) shutdownRequested() bool {
	a.mu.Lock()
	requested := a.stopRequested
	a.mu.Unlock()
	return requested || a.ctx.Err() != nil
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
		Metadata:  maps.Clone(a.opts.metadata),
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
