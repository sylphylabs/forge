package metadata

import (
	"context"

	"github.com/sylphylabs/forge/metadata"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

// ServerStream is a server-side metadata middleware for streaming methods.
//
// Metadata is read from the transport request header once, when the stream
// opens, and is then visible for the whole stream lifecycle through the
// context of the stream handed to the handler.
func ServerStream(opts ...Option) middleware.StreamMiddleware {
	options := &options{
		prefix: []string{"x-md-"}, // x-md-global-, x-md-local
	}
	for _, o := range opts {
		o(options)
	}
	return func(handler middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			ctx := stream.Context()
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return handler(request, stream)
			}

			md := options.md.Clone()
			header := tr.RequestHeader()
			for _, k := range header.Keys() {
				if options.hasPrefix(k) {
					for _, v := range header.Values(k) {
						md.Add(k, v)
					}
				}
			}
			return handler(request, contextStream{
				ServerStream: stream,
				ctx:          metadata.NewServerContext(ctx, md),
			})
		}
	}
}

// contextStream overrides the context of the stream it embeds, leaving every
// other capability to the underlying stream.
type contextStream struct {
	middleware.ServerStream
	ctx context.Context
}

func (s contextStream) Context() context.Context { return s.ctx }
