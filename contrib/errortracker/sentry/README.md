# Sentry middleware for Kratos
This middleware helps you to catch panics and report them to [sentry](https://sentry.io/)

## Quick Start
You could check the full demo in example folder.
```go
// Step 1:
// init sentry in the entry of your application
import "github.com/getsentry/sentry-go"

sentry.Init(sentry.ClientOptions{
	Dsn: "<your dsn>",
	AttachStacktrace: true, // recommended
})

// Step 2:
// set middleware
import (
	"context"

	ksentry "github.com/openkratos/kratos/contrib/errortracker/sentry"
	"github.com/openkratos/kratos/contrib/otel/tracing"
)

// Build one generated service plan for HTTP and gRPC.
plan := helloworld.GreeterMiddleware{
	Unary: []middleware.UnaryMiddleware{
		recovery.Recovery(),
		tracing.Server(),
		ksentry.Server(
			ksentry.WithTags(map[string]string{
				"tag": "some-custom-constant-tag",
			}),
			ksentry.WithContextTags(func(ctx context.Context) map[string]string {
				return map[string]string{"trace_id": tracing.TraceID(ctx)}
			}),
		), // place after Recovery so Sentry observes the recovered panic
		logging.Server(logger),
	},
}

httpService, err := helloworld.WrapGreeterHTTPServer(service, plan)
if err != nil {
	return err
}
helloworld.RegisterGreeterHTTPServer(httpServer, httpService)

grpcService, err := helloworld.WrapGreeterGRPCServer(service, plan)
if err != nil {
	return err
}
helloworld.RegisterGreeterServer(grpcServer, grpcService)

// Then, the framework will report events to Sentry when your trigger panics.
// Or you can push events to Sentry manually.
```

## Reference
* [https://docs.sentry.io/platforms/go/](https://docs.sentry.io/platforms/go/)
