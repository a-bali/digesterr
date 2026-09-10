package feed

import (
	"errors"
	"fmt"
)

// StatusError carries the real HTTP status code of a failed outbound request so
// callers can distinguish a 4xx (rate limit, auth) from a genuine 5xx failure.
// Metric recording uses it to stop masking every failure as a 500, and the
// Twitter client uses it to recognise a 429.
//
// The message is unchanged from the plain fmt.Errorf it replaces, so existing
// log greps and operator habits keep working.
type StatusError struct {
	Code  int
	Query string
	Body  string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("Twitter API returned status %d for %s: %s", e.Code, e.Query, e.Body)
}

// StatusCodeOf returns the HTTP status carried by err, or 0 when the error
// carries no status (transport failure, context cancellation, ...).
func StatusCodeOf(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code
	}
	return 0
}

// RecordedStatus maps an operation error to the status code metric recording
// should report: the real HTTP status when the error carries one, 500 for a
// genuine processing failure, and 200 when there was no error at all.
func RecordedStatus(err error) int {
	if err == nil {
		return 200
	}
	if code := StatusCodeOf(err); code != 0 {
		return code
	}
	return 500
}
