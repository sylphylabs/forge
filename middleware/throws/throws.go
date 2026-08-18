// Package throws asserts at runtime that the error identities leaving a
// method are the ones its Protobuf throws declarations promised.
//
// A throws declaration (ADR-0013) makes a method's OpenAPI document list
// exact error responses. The declaration alone is only a documented promise:
// nothing stops a handler — or a middleware in the method's generated plan —
// from returning an identity the document never mentions. Generated wrappers
// close that gap: protoc-gen-go-middleware compiles each declared method's
// identity set into its _middleware.pb.go file and checks every error the
// composed handler returns against it (ADR-0014).
//
// A method with no declaration — neither its own nor a service-level one —
// does not participate; its wrapper is generated exactly as before. Once a
// method declares anything, every error it returns is checked against the
// effective set:
//
//   - the declared identities (method ∪ service declarations);
//   - the framework domain [errors.Domain] — rate limits, timeouts, circuit
//     breakers, validation, panic backstops raise operational identities on
//     any method, and documenting them per-method would be noise;
//   - undeclared local identities that were never registered as contract:
//     [errors.PublicOf] projects them to a bare internal error anyway, so
//     they cannot contradict the document.
//
// A remote error is not exempt: [errors.PublicOf] passes a remote identity
// through verbatim, so an undeclared remote identity on the wire is exactly
// the violation the assertion exists to catch — a business layer that
// forwarded a foreign failure instead of translating it once.
//
// The default mode observes: a violation is logged with the method, the
// identity, and the declaration line that would fix it, and the error passes
// unchanged — the assertion guards documentation honesty, not disclosure
// safety, and breaking production traffic over a stale document punishes the
// wrong party. [Strict] upgrades chosen methods to mark violating errors
// undisclosed ([errors.Undisclose]), so the single disclosure gate projects
// them as internal failures. [FailUndeclared] — or FORGE_THROWS=fail in the
// environment — replaces the violating error with [ErrUndeclared] so an
// integration test fails loudly.
package throws

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/sylphylabs/forge/errors"
)

// ErrUndeclared reports an error identity that left a declared method without
// being declared, in fail mode. It wraps the violating error, so a test that
// hits it sees both the verdict and the failure that earned it.
var ErrUndeclared = errors.MustDefine(errors.KindInternal, errors.Domain, "THROWS_UNDECLARED")

// EnvVar is the environment variable that upgrades observe mode to fail mode
// for every wrapper constructed while it is set to [EnvFail]. It is read at
// wrapper construction, never on the request path, and exists so a CI job can
// turn every observed violation into a test failure without changing wiring
// code.
const (
	EnvVar  = "FORGE_THROWS"
	EnvFail = "fail"
)

// Identity is one declared error identity: the (domain, reason) pair that
// [errors.MustDefine] registers and transports put on the wire.
type Identity struct {
	Domain string
	Reason string
}

// Declaration is the compiled throws declaration of one method: its full
// method name and the identity set the method's OpenAPI document promises.
//
// Generated _middleware.pb.go files construct one per declared method with
// [Declare] at package initialization. The set is static data — asserting
// against it reads no descriptors and performs no reflection.
type Declaration struct {
	method   string
	declared map[Identity]struct{}

	mu       sync.Mutex
	observed map[Identity]struct{}
}

// Declare compiles one method's declared identity set. The method name is the
// full Protobuf name, "package.Service/Method".
//
// It is intended to be called from generated code; the identities are the
// resolved union of the method's and its service's throws declarations.
func Declare(method string, identities ...Identity) *Declaration {
	declared := make(map[Identity]struct{}, len(identities))
	for _, identity := range identities {
		declared[identity] = struct{}{}
	}
	return &Declaration{
		method:   method,
		declared: declared,
		observed: make(map[Identity]struct{}, len(identities)),
	}
}

// Method returns the full Protobuf method name the declaration belongs to.
func (d *Declaration) Method() string { return d.method }

// Declared returns the declared identities, sorted by domain then reason.
func (d *Declaration) Declared() []Identity {
	identities := make([]Identity, 0, len(d.declared))
	for identity := range d.declared {
		identities = append(identities, identity)
	}
	sortIdentities(identities)
	return identities
}

// Unobserved returns the declared identities that have not yet been seen
// leaving the method through any wrapper built from this declaration, sorted
// by domain then reason.
//
// It is the reverse observation: a declaration that never fires may be stale
// documentation. An integration suite that exercises a service's failure
// paths can assert this is empty; production code can expose it as
// diagnostics. An empty result proves only that each identity fired at least
// once since process start, nothing stronger.
func (d *Declaration) Unobserved() []Identity {
	d.mu.Lock()
	defer d.mu.Unlock()
	identities := make([]Identity, 0, len(d.declared))
	for identity := range d.declared {
		if _, ok := d.observed[identity]; !ok {
			identities = append(identities, identity)
		}
	}
	sortIdentities(identities)
	return identities
}

func (d *Declaration) markObserved(identity Identity) {
	d.mu.Lock()
	if _, ok := d.observed[identity]; !ok {
		d.observed[identity] = struct{}{}
	}
	d.mu.Unlock()
}

func sortIdentities(identities []Identity) {
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].Domain != identities[j].Domain {
			return identities[i].Domain < identities[j].Domain
		}
		return identities[i].Reason < identities[j].Reason
	})
}

// mode is what an assertion does about a violation.
type mode uint8

const (
	modeObserve mode = iota // log and pass the error unchanged
	modeFail                // replace with ErrUndeclared, for tests
	modeStrict              // mark undisclosed; PublicOf projects internal
)

type options struct {
	strict    []string
	strictAll bool
	fail      []string
	failAll   bool
	logger    *slog.Logger
	lookupEnv func(string) string
}

// Option configures assertion behavior for one generated wrapper. Options are
// applied at wrapper construction; nothing is reconfigured on the request
// path.
type Option func(*options)

// Strict makes violations on the named methods undisclosable instead of
// observed: the violating error is marked with [errors.Undisclose], and the
// disclosure gate projects it as an internal failure carrying only its trace
// ID. The error itself is not rewritten — logging and metrics inside the
// process still observe its real classification and identity.
//
// Methods are named by their RPC name as spelled in the .proto file, for
// example "GetBook". With no arguments, every declared method of the wrapper
// is strict. Naming a method the wrapper has no declaration for fails wrapper
// construction.
func Strict(methods ...string) Option {
	return func(o *options) {
		if len(methods) == 0 {
			o.strictAll = true
			return
		}
		o.strict = append(o.strict, methods...)
	}
}

// FailUndeclared upgrades observed violations on the named methods to hard
// failures: the violating error is replaced by [ErrUndeclared] wrapping it,
// so an integration test asserting a successful — or a declared — response
// fails loudly. With no arguments it applies to every declared method of the
// wrapper. Naming an undeclared method fails wrapper construction.
//
// Setting FORGE_THROWS=fail in the environment has the same effect on every
// wrapper constructed while it is set, without touching wiring code.
//
// [Strict] takes precedence on a method configured with both: a strict method
// already refuses to disclose the violation, which is the stronger verdict at
// the boundary.
func FailUndeclared(methods ...string) Option {
	return func(o *options) {
		if len(methods) == 0 {
			o.failAll = true
			return
		}
		o.fail = append(o.fail, methods...)
	}
}

// WithLogger sets the logger violations are reported to. The default is
// [slog.Default] at the time the wrapper is constructed.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) {
		if logger != nil {
			o.logger = logger
		}
	}
}

// withEnv overrides the environment lookup, for tests.
func withEnv(lookup func(string) string) Option {
	return func(o *options) { o.lookupEnv = lookup }
}

// Config is the assertion configuration of one generated wrapper: each
// declared method's mode, resolved once at construction.
type Config struct {
	logger *slog.Logger
	modes  map[string]mode // keyed by full method name
}

// NewConfig resolves options against the wrapper's declarations. It is called
// by generated wrapper constructors; a method named by an option that none of
// the declarations covers is a configuration error and fails construction,
// the same way a nil middleware does.
func NewConfig(opts []Option, declarations ...*Declaration) (*Config, error) {
	o := options{lookupEnv: os.Getenv}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	logger := o.logger
	if logger == nil {
		logger = slog.Default()
	}

	byRPCName := make(map[string]string, len(declarations))
	config := &Config{logger: logger, modes: make(map[string]mode, len(declarations))}
	for _, declaration := range declarations {
		rpc := declaration.method
		if i := strings.LastIndexByte(rpc, '/'); i >= 0 {
			rpc = rpc[i+1:]
		}
		byRPCName[rpc] = declaration.method
		config.modes[declaration.method] = modeObserve
	}

	if o.failAll || o.lookupEnv(EnvVar) == EnvFail {
		for method := range config.modes {
			config.modes[method] = modeFail
		}
	}
	for _, rpc := range o.fail {
		method, ok := byRPCName[rpc]
		if !ok {
			return nil, fmt.Errorf("throws: FailUndeclared names method %q, which has no throws declaration in this wrapper", rpc)
		}
		config.modes[method] = modeFail
	}
	if o.strictAll {
		for method := range config.modes {
			config.modes[method] = modeStrict
		}
	}
	for _, rpc := range o.strict {
		method, ok := byRPCName[rpc]
		if !ok {
			return nil, fmt.Errorf("throws: Strict names method %q, which has no throws declaration in this wrapper", rpc)
		}
		config.modes[method] = modeStrict
	}
	return config, nil
}

// Assert is one method's compiled assertion: it checks an error leaving the
// method and returns the error the wrapper must propagate.
type Assert func(ctx context.Context, err error) error

// Asserter returns the assertion for one declared method under this
// configuration. Generated wrapper constructors call it once per declared
// method and store the result; the request path only invokes the returned
// function.
func (c *Config) Asserter(declaration *Declaration) Assert {
	methodMode := c.modes[declaration.method]
	logger := c.logger
	return func(ctx context.Context, err error) error {
		if err == nil {
			return nil
		}
		// The identity that will leave the process is the one the transport
		// encoders select: both errors.PublicOf (HTTP) and errors.FromError
		// (gRPC projection) pick the first *Error errors.As reaches, which is
		// exactly what FromError exposes.
		e := errors.FromError(err)
		identity := Identity{Domain: e.Domain(), Reason: e.Reason()}
		if _, ok := declaration.declared[identity]; ok {
			declaration.markObserved(identity)
			return err
		}
		if identity.Domain == errors.Domain {
			// Framework operational identities may surface on any method.
			return err
		}
		if errors.IsUndisclosed(e) {
			// Already sentenced: it projects as an internal failure.
			return err
		}
		if !e.IsRemote() && !errors.IsContract(identity.Domain, identity.Reason) {
			// An undeclared local identity projects as a bare internal error;
			// it cannot contradict the document.
			return err
		}

		// Violation: a contract or remote identity the document never
		// promised is about to leave the process fully disclosed. Log the
		// original error before any projection decision, per ADR-0012.
		logger.LogAttrs(ctx, slog.LevelWarn, "throws: undeclared error identity",
			slog.String("method", declaration.method),
			slog.String("domain", identity.Domain),
			slog.String("reason", identity.Reason),
			slog.String("error_kind", e.Kind().String()),
			slog.Bool("remote", e.IsRemote()),
			slog.String("fix", fmt.Sprintf(
				"declare %s in the (throws) option of %s, or translate the error before it leaves the method",
				identity.Reason, declaration.method,
			)),
			slog.Any("error", err),
		)
		switch methodMode {
		case modeStrict:
			return errors.Undisclose(err)
		case modeFail:
			return ErrUndeclared.
				Msgf("undeclared error identity %s/%s left %s", identity.Domain, identity.Reason, declaration.method).
				Wrap(err)
		default:
			return err
		}
	}
}
