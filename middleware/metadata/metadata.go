package metadata

import (
	"context"
	"net/url"
	"strings"

	"github.com/sylphylabs/forge/metadata"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

// encodeValue percent-escapes v when it cannot travel as a header value.
//
// gRPC admits only printable ASCII (0x20 to 0x7E) in a non-binary header and
// fails the whole RPC otherwise, so a value carrying a name, an address, or a
// stray control byte has to be escaped to survive the wire. A value already
// within that range is written unchanged, which keeps the common case free of
// both the escaping cost and a decode dependency in the peer.
//
// '%' is escaped even though it is printable: it is the escape marker itself,
// so leaving it bare would make an encoded value ambiguous on read.
func encodeValue(v string) string {
	for i := range len(v) {
		if c := v[i]; c < 0x20 || c > 0x7E || c == '%' {
			return url.PathEscape(v)
		}
	}
	return v
}

// decodeValue reverses [encodeValue].
//
// A value with no '%' cannot have been escaped and is returned as is. A value
// that fails to unescape is returned unchanged rather than rejected, because a
// bare '%' is what a peer that does not encode would send; treating that as an
// error would turn a version skew into a failed request.
func decodeValue(v string) string {
	if !strings.ContainsRune(v, '%') {
		return v
	}
	if decoded, err := url.PathUnescape(v); err == nil {
		return decoded
	}
	return v
}

// Option is metadata option.
type Option func(*options)

type options struct {
	prefix []string
	md     metadata.Metadata
}

func (o *options) hasPrefix(key string) bool {
	k := strings.ToLower(key)
	for _, prefix := range o.prefix {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// WithConstants with constant metadata key value.
func WithConstants(md metadata.Metadata) Option {
	return func(o *options) {
		o.md = md
	}
}

// WithPropagatedPrefix with propagated key prefix.
func WithPropagatedPrefix(prefix ...string) Option {
	return func(o *options) {
		o.prefix = prefix
	}
}

// Server is middleware server-side metadata.
func Server(opts ...Option) middleware.UnaryMiddleware {
	options := &options{
		prefix: []string{"x-md-"}, // x-md-global-, x-md-local
	}
	for _, o := range opts {
		o(options)
	}
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (reply any, err error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return handler(ctx, req)
			}

			md := options.md.Clone()
			header := tr.RequestHeader()
			for _, k := range header.Keys() {
				if options.hasPrefix(k) {
					for _, v := range header.Values(k) {
						md.Add(k, decodeValue(v))
					}
				}
			}
			ctx = metadata.NewServerContext(ctx, md)
			return handler(ctx, req)
		}
	}
}

// Client is middleware client-side metadata.
func Client(opts ...Option) middleware.UnaryMiddleware {
	options := &options{
		prefix: []string{"x-md-global-"},
	}
	for _, o := range opts {
		o(options)
	}
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (reply any, err error) {
			tr, ok := transport.FromClientContext(ctx)
			if !ok {
				return handler(ctx, req)
			}

			header := tr.RequestHeader()
			// x-md-local-
			for k, vList := range options.md {
				for _, v := range vList {
					header.Add(k, encodeValue(v))
				}
			}
			if md, ok := metadata.FromClientContext(ctx); ok {
				for k, vList := range md {
					for _, v := range vList {
						header.Add(k, encodeValue(v))
					}
				}
			}
			// x-md-global-
			if md, ok := metadata.FromServerContext(ctx); ok {
				for k, vList := range md {
					if options.hasPrefix(k) {
						for _, v := range vList {
							header.Add(k, encodeValue(v))
						}
					}
				}
			}
			return handler(ctx, req)
		}
	}
}
