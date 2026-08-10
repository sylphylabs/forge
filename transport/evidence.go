package transport

import "errors"

// notSent marks an error as proof that the request it reports never left this
// process. It carries no message of its own: the wrapped error already says
// what went wrong, and this type says only where the failure fell relative to
// the wire.
type notSent struct {
	err error
}

func (e *notSent) Error() string { return e.err.Error() }

// Unwrap keeps errors.Is and errors.As reaching the underlying error, so a
// marked error classifies exactly as it would unmarked.
func (e *notSent) Unwrap() error { return e.err }

// MarkNotSent records that the request reporting err was never written to the
// wire, making the failure safe to retry regardless of whether the operation
// is idempotent. It returns err unchanged when err is nil or already marked.
//
// A transport MUST mark only what it can prove. "The connection never opened"
// and "no connection was available to write to" are proof; "the request was
// written and no response came back" is not — a server may have executed it
// and lost the reply. When a transport cannot tell the two apart, it leaves
// the error unmarked: the absence of the mark reads as "the request may have
// executed", which is the safe assumption.
func MarkNotSent(err error) error {
	if err == nil {
		return nil
	}
	if WasNotSent(err) {
		return err
	}
	return &notSent{err: err}
}

// WasNotSent reports whether err carries the [MarkNotSent] evidence anywhere
// in its chain, meaning a transport proved the request never reached a server.
//
// A false result is not proof of the opposite. It means no transport claimed
// the request was withheld — either because it was sent, or because the
// transport could not tell.
func WasNotSent(err error) bool {
	if err == nil {
		return false
	}
	var e *notSent
	return errors.As(err, &e)
}
