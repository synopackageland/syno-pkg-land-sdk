package delegate

import "errors"

// NotSent reports whether a failed Client.Call definitely did not execute the
// requested operation: no request was sent, or the executor explicitly refused
// it before execution. It follows wrapped errors with errors.As. It is false
// for nil, unrelated errors and uncertain outcomes, including a partial write,
// cancellation after send, a lost reply or an unknown executor code.
//
// For a mutation with !NotSent(err), read back its state before deciding what
// to do; never blindly resend it. A true result does not fix an authorization
// or policy denial and is not itself a recommendation to retry.
func NotSent(err error) bool {
	var unsent *notSentError
	return errors.As(err, &unsent)
}

type notSentError struct{ error }

func (e *notSentError) Unwrap() error { return e.error }

func notSent(err error) error {
	if err == nil {
		return nil
	}
	return &notSentError{err}
}
