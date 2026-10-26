package deepgram

import (
	"errors"
	"fmt"

	"github.com/gojargo/jargo/service/wsutil"
)

// refusalError is a WebSocket handshake Deepgram refused, described by the
// reason Deepgram gave.
type refusalError struct {
	description string
	err         error
}

// Error implements error.
func (e *refusalError) Error() string { return e.description }

// Unwrap returns the dial failure, so the refusal is still classified by the
// status it carries.
func (e *refusalError) Unwrap() error { return e.err }

// describeRefusal describes Deepgram refusing a WebSocket handshake, if that is
// what failed.
//
// Deepgram explains a refusal (an invalid setting, a bad API key, a model the
// project cannot use) in the dg-error response header, which the dial error's
// own message leaves out. It returns the empty string when err is not a refusal
// carrying a reason.
func describeRefusal(err error) string {
	he, ok := errors.AsType[*wsutil.HandshakeError](err)
	if !ok {
		return ""
	}
	reason := he.Header.Get("dg-error")
	if reason == "" {
		return ""
	}
	return fmt.Sprintf("Deepgram refused the connection (HTTP %d): %s", he.StatusCode, reason)
}

// dialError is the error a failed dial is reported as: Deepgram's own
// description of a refusal when it gave one, and otherwise the dial error under
// prefix.
func dialError(prefix string, err error) error {
	if description := describeRefusal(err); description != "" {
		return &refusalError{description: description, err: err}
	}
	return fmt.Errorf("%s: dial: %w", prefix, err)
}
