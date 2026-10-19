package pipeline

import (
	"runtime"
	"testing"
	"time"

	"github.com/gojargo/jargo/frames"
	"github.com/gojargo/jargo/processor"
)

// countingObserver counts the pushes it is told about.
type countingObserver struct{ pushes chan struct{} }

func (o *countingObserver) OnPushFrame(processor.FramePushed) { o.pushes <- struct{}{} }

// TestAFrameIsForgottenOnceThePipelineLetsGoOfIt checks the proxy tracks the
// frames in flight, not every frame ever pushed.
func TestAFrameIsForgottenOnceThePipelineLetsGoOfIt(t *testing.T) {
	o := &countingObserver{pushes: make(chan struct{}, 16)}
	p := newObserverProxy([]processor.Observer{o})
	p.start()
	t.Cleanup(p.stop)

	push := func(f frames.Frame) {
		p.OnPushFrame(processor.FramePushed{Frame: f, Direction: processor.Downstream})
	}
	for range 10 {
		push(frames.NewTextFrame("hello"))
	}
	if n := p.pushedFrames(); n != 10 {
		t.Fatalf("tracking %d frames, want 10", n)
	}

	// The observer holds on to the last push it handled, so end with a frame
	// the test keeps and wait for every push to be handed over.
	kept := frames.NewTextFrame("kept")
	push(kept)
	for range 11 {
		<-o.pushes
	}

	deadline := time.Now().Add(5 * time.Second)
	for p.pushedFrames() > 1 && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	if n := p.pushedFrames(); n != 1 {
		t.Fatalf("tracking %d frames after they were let go, want only the one kept", n)
	}
	runtime.KeepAlive(kept)
}

// selfRemovingObserver removes itself from the proxy on the first text frame it
// is told about, and counts every push it hears.
type selfRemovingObserver struct {
	p       *observerProxy
	removed chan struct{}
	pushes  chan struct{}
}

func (o *selfRemovingObserver) OnPushFrame(d processor.FramePushed) {
	o.pushes <- struct{}{}
	if _, ok := d.Frame.(*frames.TextFrame); ok {
		o.p.remove(o)
		close(o.removed)
	}
}

// TestAnObserverCanBeRemovedFromItsOwnCallback checks an observer removing
// itself from one of its own callbacks does not wait for itself, and hears
// nothing more once the callback has returned.
func TestAnObserverCanBeRemovedFromItsOwnCallback(t *testing.T) {
	o := &selfRemovingObserver{removed: make(chan struct{}), pushes: make(chan struct{}, 16)}
	p := newObserverProxy([]processor.Observer{o})
	o.p = p
	p.start()
	defer p.stop()

	p.OnPushFrame(processor.FramePushed{Frame: frames.NewTextFrame("hi")})
	select {
	case <-o.removed:
	case <-time.After(time.Second):
		t.Fatal("an observer removing itself from its own callback deadlocked")
	}
	<-o.pushes

	p.OnPushFrame(processor.FramePushed{Frame: frames.NewTextFrame("after")})
	select {
	case <-o.pushes:
		t.Error("a removed observer was still told about a frame")
	case <-time.After(100 * time.Millisecond):
	}
}
