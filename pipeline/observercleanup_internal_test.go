package pipeline

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gojargo/jargo/frames"
	"github.com/gojargo/jargo/processor"
)

// cleanupRecorder records its cleanup, and runs onText for every text frame.
type cleanupRecorder struct {
	cleanedUp atomic.Bool
	onText    func(o *cleanupRecorder)
}

func (o *cleanupRecorder) OnPushFrame(data processor.FramePushed) {
	if _, ok := data.Frame.(*frames.TextFrame); ok && o.onText != nil {
		o.onText(o)
	}
}

func (o *cleanupRecorder) Cleanup(context.Context) error {
	o.cleanedUp.Store(true)
	return nil
}

// errCleanupFailed is what failingCleanup reports.
//
//nolint:gochecknoglobals // sentinel error
var errCleanupFailed = errors.New("cleanup failed")

// failingCleanup fails to clean up.
type failingCleanup struct{}

func (failingCleanup) OnPushFrame(processor.FramePushed) {}

func (failingCleanup) Cleanup(context.Context) error { return errCleanupFailed }

// TestARemovedObserverIsCleanedUp checks the proxy lets a removed observer
// release what it holds: the pipeline cleans up only the observers still
// registered when it stops.
func TestARemovedObserverIsCleanedUp(t *testing.T) {
	o := &cleanupRecorder{}
	p := newObserverProxy([]processor.Observer{o})
	p.start()
	t.Cleanup(p.stop)

	p.remove(o)

	if !o.cleanedUp.Load() {
		t.Error("the removed observer was not cleaned up")
	}
}

// TestAnObserverRemovedFromItsOwnCallbackIsCleanedUp checks an observer that
// removes itself is cleaned up too.
func TestAnObserverRemovedFromItsOwnCallbackIsCleanedUp(t *testing.T) {
	removed := make(chan struct{})
	var p *observerProxy
	o := &cleanupRecorder{onText: func(o *cleanupRecorder) {
		p.remove(o)
		close(removed)
	}}
	p = newObserverProxy([]processor.Observer{o})
	p.start()
	t.Cleanup(p.stop)

	p.OnPushFrame(processor.FramePushed{Frame: frames.NewTextFrame("hello"), Direction: processor.Downstream})

	select {
	case <-removed:
	case <-time.After(time.Second):
		t.Fatal("the observer never removed itself")
	}
	if !o.cleanedUp.Load() {
		t.Error("the observer that removed itself was not cleaned up")
	}
}

// TestAnObserverFailingCleanupDoesNotStopTheOthers checks stopping is best
// effort: the observers after one whose cleanup fails are still cleaned up.
func TestAnObserverFailingCleanupDoesNotStopTheOthers(t *testing.T) {
	later := &cleanupRecorder{}
	p := newObserverProxy([]processor.Observer{failingCleanup{}, later})
	p.start()

	p.stop()

	if !later.cleanedUp.Load() {
		t.Error("the observer after the failing one was not cleaned up")
	}
}
