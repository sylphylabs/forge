// Package backstop converts a panic caught at a transport boundary into the
// generic internal error every transport puts on the wire for one.
//
// The backstop is not middleware and cannot be removed or replaced: it is the
// transport's guarantee that a panicking handler never kills the process and
// never discloses the panic value. Applications that want to classify or
// enrich a recovered panic use middleware/recovery, which runs inside the
// backstop and handles the panic first.
package backstop

import (
	"context"
	"runtime"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/log"
)

// ErrPanic is the error a client observes when the transport backstop caught a
// panic. The message is deliberately generic: a panic value is diagnostic
// text written for an operator, so it goes to the log and never to the wire.
var ErrPanic = errors.MustDefine(errors.KindInternal, errors.Domain, "PANIC").
	Msg("internal error")

// Recovered logs a recovered panic value with its stack and returns the error
// the transport encodes in its place. transport names the boundary in the
// log record, matching each transport's log prefix ("[HTTP]", "[gRPC]",
// "[Message]").
func Recovered(ctx context.Context, transport string, rec any) *errors.Error {
	buf := make([]byte, 64<<10) //nolint:mnd
	n := runtime.Stack(buf, false)
	log.ErrorContext(ctx, transport+" panic recovered",
		"panic", rec,
		"stack", string(buf[:n]),
	)
	return ErrPanic
}
