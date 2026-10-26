package wsservice

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

// messages reads the given messages in turn, then ends the connection, or, with
// hang, waits until the read is called off.
func messages(hang bool, items ...string) func(ctx context.Context) ([]byte, error) {
	return func(ctx context.Context) ([]byte, error) {
		if len(items) > 0 {
			msg := items[0]
			items = items[1:]
			return []byte(msg), nil
		}
		if hang {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return nil, io.EOF
	}
}

// confirmOn handles a message by recording it, and confirms on confirmation.
func confirmOn(confirmation string, handled *[]string) func([]byte) (bool, error) {
	return func(msg []byte) (bool, error) {
		*handled = append(*handled, string(msg))
		return string(msg) == confirmation, nil
	}
}

// TestReceiveUntilConfirmedStopsAfterConfirmation checks the messages up to the
// confirmation are handled, and the rest are left for the caller.
func TestReceiveUntilConfirmedStopsAfterConfirmation(t *testing.T) {
	var handled []string
	read := messages(false, "hello", "connected", "after")

	if err := ReceiveUntilConfirmed(t.Context(), read, confirmOn("connected", &handled), time.Second); err != nil {
		t.Fatalf("ReceiveUntilConfirmed = %v, want confirmed", err)
	}

	if want := []string{"hello", "connected"}; !slices.Equal(handled, want) {
		t.Errorf("handled %q, want %q", handled, want)
	}
	if rest, err := read(t.Context()); err != nil || string(rest) != "after" {
		t.Errorf("next message = %q, %v, want the one after the confirmation", rest, err)
	}
}

// TestReceiveUntilConfirmedReportsAConnectionClosedFirst checks that a
// connection closing before it is confirmed returns the read's error.
func TestReceiveUntilConfirmedReportsAConnectionClosedFirst(t *testing.T) {
	var handled []string

	err := ReceiveUntilConfirmed(t.Context(), messages(false, "hello"), confirmOn("connected", &handled), time.Second)

	if !errors.Is(err, io.EOF) {
		t.Errorf("ReceiveUntilConfirmed = %v, want the read's error", err)
	}
	if want := []string{"hello"}; !slices.Equal(handled, want) {
		t.Errorf("handled %q, want %q", handled, want)
	}
}

// TestReceiveUntilConfirmedTimesOut checks a missing confirmation returns an
// error wrapping ErrNotConfirmed.
func TestReceiveUntilConfirmedTimesOut(t *testing.T) {
	var handled []string

	err := ReceiveUntilConfirmed(t.Context(), messages(true, "hello"), confirmOn("connected", &handled),
		50*time.Millisecond)

	if !errors.Is(err, ErrNotConfirmed) || !strings.Contains(err.Error(), "did not confirm the connection") {
		t.Errorf("ReceiveUntilConfirmed = %v, want ErrNotConfirmed", err)
	}
}

// errRejected is the error a handler rejecting a message returns.
//
//nolint:gochecknoglobals // sentinel error
var errRejected = errors.New("rejected")

// TestReceiveUntilConfirmedPropagatesHandlerErrors checks that an error raised
// while handling a message reaches the caller.
func TestReceiveUntilConfirmedPropagatesHandlerErrors(t *testing.T) {
	reject := func([]byte) (bool, error) { return false, errRejected }

	if err := ReceiveUntilConfirmed(t.Context(), messages(false, "error"), reject, time.Second); !errors.Is(
		err, errRejected) {
		t.Errorf("ReceiveUntilConfirmed = %v, want the handler's error", err)
	}
}

// TestUnconfirmedConnectionsEndTheRetrying checks that a connection the server
// never confirms counts as failing right after it was made, however long the
// wait for the confirmation took, so the retrying stops after a few in a row.
func TestUnconfirmedConnectionsEndTheRetrying(t *testing.T) {
	t.Parallel()

	clk := newClock()
	h := &handler{receive: func(int) error {
		// The wait for the confirmation outlasts what counts as quick.
		clk.advance(10 * time.Second)
		return ErrNotConfirmed
	}}
	rec := newRecorder()
	b := newTestBase(t, h, clk)

	b.ReceiveTaskHandler(t.Context(), rec.report)

	if receives, _, _ := h.counts(); receives != b.tracker.MaxConsecutiveFailures() {
		t.Errorf("read %d times, want %d: one per unconfirmed connection tolerated",
			receives, b.tracker.MaxConsecutiveFailures())
	}
	reported := rec.all()
	if len(reported) != 1 || !strings.Contains(reported[0], "failed 3 times immediately after connecting") {
		t.Errorf("reported %v, want a single report saying the retrying stopped", reported)
	}
}
