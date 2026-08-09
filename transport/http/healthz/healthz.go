// Package healthz serves a readiness probe over HTTP for anything that
// implements transport.Healthzer — one server, or an App aggregating all of
// its servers. Nothing registers the handler automatically; mount it where
// the probe should live:
//
//	httpSrv.Handle("/healthz", healthz.NewHandler(app))
package healthz

import (
	"net/http"

	"github.com/sylphylabs/forge/transport"
)

// NewHandler returns a handler that answers 200 with body "ok" while
// h.Healthz() reports true and 503 with body "unavailable" once it reports
// false, so load balancers and orchestrators stop routing before draining
// begins.
func NewHandler(h transport.Healthzer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if h.Healthz() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("unavailable"))
	})
}
