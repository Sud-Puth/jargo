package tts

import (
	"context"
	"testing"

	"github.com/gojargo/jargo/frames"
)

// TestSerialQueueResetKeepsUninterruptibleFrames checks an interruption drops
// the interruptible audio contexts and frames queued for serialization, and
// keeps what is uninterruptible: frames that must not be lost mid-flight, the
// contexts of uninterruptible speech, and the sentinel a graceful end waits on.
func TestSerialQueueResetKeepsUninterruptibleFrames(t *testing.T) {
	q := newSerialQueue()
	result := frames.NewFunctionCallResultFrame("1", "f", nil, "done")
	q.push(serialItem{contextID: "dropped"})
	q.push(serialItem{contextID: "kept", uninterruptible: true})
	q.push(serialItem{frame: frames.NewTextFrame("dropped")})
	q.push(serialItem{frame: result})
	q.push(serialItem{end: true})

	q.reset()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, ok := q.get(t.Context()); !ok || got.contextID != "kept" {
		t.Fatalf("reset kept %+v first, want the uninterruptible context", got)
	}
	if got, ok := q.get(t.Context()); !ok || got.frame != result {
		t.Fatalf("reset kept %+v next, want the function call result", got)
	}
	if got, ok := q.get(t.Context()); !ok || !got.end {
		t.Fatalf("reset kept %+v last, want the shutdown sentinel", got)
	}
	if it, ok := q.get(ctx); ok {
		t.Fatalf("reset kept %+v as well", it)
	}
}
