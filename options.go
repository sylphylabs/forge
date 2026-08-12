package forge

import (
	"context"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"time"

	"github.com/sylphylabs/forge/registry"
	"github.com/sylphylabs/forge/transport"
)

// Option is an application option.
//
// Options come in two shapes. Scalar options ([WithID], [WithName],
// [WithVersion], [WithContext], [WithLogger], [WithRegistrar], and the
// timeouts) set a single field: when the same option appears more than once,
// the one applied last wins. Collection options ([WithServer], [WithEndpoint],
// [WithSignal], [WithMetadata], and the lifecycle hooks) accumulate: every
// application of the option adds to what earlier applications contributed, so
// independent option lists — for example two [Suite] values — compose without
// overwriting each other.
type Option func(o *options)

// options is an application options.
type options struct {
	id        string
	name      string
	version   string
	metadata  map[string]string
	endpoints []*url.URL

	ctx  context.Context
	sigs []os.Signal

	logger           *slog.Logger
	registrar        registry.Registrar
	registrarTimeout time.Duration
	stopTimeout      time.Duration
	afterStopTimeout time.Duration
	servers          []transport.Server

	// Before and After funcs
	beforeStart []func(context.Context) error
	beforeStop  []func(context.Context) error
	afterStart  []func(context.Context) error
	afterStop   []func(context.Context) error
}

// WithID sets the service id.
func WithID(id string) Option {
	return func(o *options) { o.id = id }
}

// WithName sets the service name.
func WithName(name string) Option {
	return func(o *options) { o.name = name }
}

// WithVersion sets the service version.
func WithVersion(version string) Option {
	return func(o *options) { o.version = version }
}

// WithMetadata merges md into the service metadata. Keys from later applications
// win over earlier ones.
func WithMetadata(md map[string]string) Option {
	return func(o *options) {
		if o.metadata == nil {
			o.metadata = make(map[string]string, len(md))
		}
		maps.Copy(o.metadata, md)
	}
}

// WithEndpoint appends endpoints to the service endpoints.
func WithEndpoint(endpoints ...*url.URL) Option {
	return func(o *options) { o.endpoints = append(o.endpoints, endpoints...) }
}

// WithContext sets the service context.
func WithContext(ctx context.Context) Option {
	return func(o *options) { o.ctx = ctx }
}

// WithLogger sets the service logger.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) { o.logger = logger }
}

// WithServer appends transport servers to the application.
func WithServer(srv ...transport.Server) Option {
	return func(o *options) { o.servers = append(o.servers, srv...) }
}

// WithSignal appends exit signals to the set the application stops on. When
// no WithSignal option is given, the application stops on SIGTERM, SIGQUIT,
// and SIGINT.
func WithSignal(sigs ...os.Signal) Option {
	return func(o *options) { o.sigs = append(o.sigs, sigs...) }
}

// WithRegistrar sets the service registrar.
func WithRegistrar(r registry.Registrar) Option {
	return func(o *options) { o.registrar = r }
}

// WithRegistrarTimeout sets the registrar timeout.
func WithRegistrarTimeout(t time.Duration) Option {
	return func(o *options) { o.registrarTimeout = t }
}

// WithStopTimeout sets the app stop timeout.
func WithStopTimeout(t time.Duration) Option {
	return func(o *options) { o.stopTimeout = t }
}

// WithAfterStopTimeout sets the total time allowed for all AfterStop hooks.
// A non-positive duration disables the deadline.
func WithAfterStopTimeout(t time.Duration) Option {
	return func(o *options) { o.afterStopTimeout = t }
}

// Before and Afters

// WithBeforeStart registers a func to run before the app starts.
func WithBeforeStart(fn func(context.Context) error) Option {
	return func(o *options) {
		o.beforeStart = append(o.beforeStart, fn)
	}
}

// WithBeforeStop registers a func to run before the app stops.
func WithBeforeStop(fn func(context.Context) error) Option {
	return func(o *options) {
		o.beforeStop = append(o.beforeStop, fn)
	}
}

// WithAfterStart registers a func to run after the app starts.
func WithAfterStart(fn func(context.Context) error) Option {
	return func(o *options) {
		o.afterStart = append(o.afterStart, fn)
	}
}

// WithAfterStop registers a func to run after the app stops.
func WithAfterStop(fn func(context.Context) error) Option {
	return func(o *options) {
		o.afterStop = append(o.afterStop, fn)
	}
}
