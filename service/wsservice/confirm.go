package wsservice

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrNotConfirmed is wrapped by the error ReceiveUntilConfirmed returns when the
// server does not confirm a connection in time. The receive loop counts a
// connection that ends this way as failing right after it was made, so it gives
// up after a few in a row rather than retrying forever.
//
//nolint:gochecknoglobals // sentinel error
var ErrNotConfirmed = errors.New("wsservice: the server did not confirm the connection")

// ReceiveUntilConfirmed handles messages until the server confirms the
// connection.
//
// It is for services whose server confirms a connection with a message after the
// handshake. read reads the next message; handle handles one and reports
// whether it was the confirmation. It returns nil once the connection is
// confirmed, leaving the connection positioned after the confirmation for the
// caller to keep reading, and the read's error if the connection ended first. If
// the confirmation does not arrive within timeout, it returns an error wrapping
// ErrNotConfirmed.
func ReceiveUntilConfirmed(
	ctx context.Context,
	read func(ctx context.Context) ([]byte, error),
	handle func(msg []byte) (confirmed bool, err error),
	timeout time.Duration,
) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		msg, err := read(waitCtx)
		if err != nil {
			if waitCtx.Err() != nil && ctx.Err() == nil {
				return fmt.Errorf("%w within %s", ErrNotConfirmed, timeout)
			}
			return err
		}
		confirmed, err := handle(msg)
		if err != nil {
			return err
		}
		if confirmed {
			return nil
		}
	}
}
