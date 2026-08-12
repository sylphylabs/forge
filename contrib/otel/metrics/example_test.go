package metrics_test

// The examples in this file mirror the "HTTP metrics" snippet in
// docs/agent/observability.md so that the guide cannot drift from the API
// without breaking the build. When one of these stops compiling, fix the
// guide together with the example.

import (
	"context"
	"fmt"

	metricnoop "go.opentelemetry.io/otel/metric/noop"

	"github.com/sylphylabs/forge/contrib/otel/metrics"
	forgehttp "github.com/sylphylabs/forge/transport/http"
)

// Example_httpMetrics mirrors "HTTP metrics": the server instrument is a
// FilterFunc for WithFilter, the client instrument is a RoundTripperWrapper
// for WithRoundTripperWrapper, and both constructors return an error that
// must not be discarded.
func Example_httpMetrics() {
	provider := metricnoop.NewMeterProvider()
	ctx := context.Background()
	endpoint := "http://127.0.0.1:8000"

	serverMetrics, err := metrics.NewHTTPServerFilter(provider)
	if err != nil {
		fmt.Println(err)
		return
	}
	srv := forgehttp.NewServer(forgehttp.WithFilter(serverMetrics))
	_ = srv

	clientMetrics, err := metrics.NewHTTPClientWrapper(provider)
	if err != nil {
		fmt.Println(err)
		return
	}
	client, err := forgehttp.NewClient(ctx,
		forgehttp.WithTarget(endpoint),
		forgehttp.WithRoundTripperWrapper(clientMetrics),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer client.Close()

	fmt.Println("instrumented")
	// Output: instrumented
}

// Example_knownMethods mirrors the guide's cardinality note: unrecognized
// HTTP methods collapse to _OTHER; extend the recognized set explicitly.
func Example_knownMethods() {
	provider := metricnoop.NewMeterProvider()

	serverMetrics, err := metrics.NewHTTPServerFilter(provider,
		metrics.WithHTTPServerKnownMethods("GET", "POST", "PURGE"),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	_ = serverMetrics

	clientMetrics, err := metrics.NewHTTPClientWrapper(provider,
		metrics.WithHTTPClientKnownMethods("GET", "POST", "PURGE"),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	_ = clientMetrics

	fmt.Println("configured")
	// Output: configured
}
