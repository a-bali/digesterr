// Package metrics: status-carrying errors.
//
// Every metric recording site needs to report the real HTTP status behind a
// failed outbound request. Historically each site hardcoded 500 whenever err
// was non-nil, which made a 429 indistinguishable from a genuine server error
// and hid rate limiting from the metrics UI entirely.
//
// Producers attach the real code with StatusError; recording sites read it
// back with StatusFor. The type lives here rather than in a provider package
// because metrics is a stdlib-only leaf imported by feed, digest and httpapi.
package metrics

import (
	"errors"
	"fmt"
)

// StatusError attaches an HTTP status code to a failure.
//
// Msg carries the human-readable message; for upstream API failures the callers
// build it so the text stays identical to the plain error it replaced.
type StatusError struct {
	Code int
	Msg  string
}

func (e *StatusError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("request failed with status %d", e.Code)
}

// StatusErrorf builds a StatusError with a formatted message.
func StatusErrorf(code int, format string, args ...any) *StatusError {
	return &StatusError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// WrapStatus attaches a status code to err, preserving the original message so
// existing log greps and error string assertions keep working.
func WrapStatus(code int, err error) error {
	if err == nil {
		return nil
	}
	return &StatusError{Code: code, Msg: err.Error()}
}

// StatusCodeOf returns the HTTP status carried by err, or 0 when the error
// carries no status (transport failure, context cancellation, parse error).
func StatusCodeOf(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code
	}
	return 0
}

// StatusFor maps an operation error to the status code recording should report:
// the real HTTP status when the error carries one, 200 when there was no error,
// and 500 only for a genuine processing failure with no associated status.
//
// This is the single place that decides how a failure is bucketed, so 4xx and
// 5xx stay distinguishable in metrics.
func StatusFor(err error) int {
	if err == nil {
		return 200
	}
	if code := StatusCodeOf(err); code != 0 {
		return code
	}
	return 500
}
