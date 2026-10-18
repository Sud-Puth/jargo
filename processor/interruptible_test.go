package processor_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gojargo/jargo/clock"
	"github.com/gojargo/jargo/frames"
	"github.com/gojargo/jargo/processor"
)

// markerFrame is a data frame whose type is uninterruptible by default.
type markerFrame struct {
	frames.BaseDataFrame
	frames.UninterruptibleMixin
	Text string
}

func newMarkerFrame(text string) *markerFrame {
	return &markerFrame{BaseDataFrame: frames.NewBaseDataFrame("markerFrame"), Text: text}
}

// delayer holds every frame that is not a system frame for a while before
// pushing it on, so an interruption lands while it is being processed.
type delayer struct {
	*processor.Base
}

func newDelayer() *delayer {
	d := &delayer{}
	d.Base = processor.New("Delayer", d)
	return d
}

func (d *delayer) ProcessFrame(ctx context.Context, f frames.Frame, dir processor.Direction) error {
	if err := d.Base.ProcessFrame(ctx, f, dir); err != nil {
		return err
	}
	if _, system := f.(frames.SystemFrame); !system {
		select {
		case <-time.After(400 * time.Millisecond):
		case <-ctx.Done():
			return nil
		}
	}
	return d.PushFrame(ctx, f, dir)
}

// TestInterruptibleFlagDecidesForAFrame checks a plain frame marked
// uninterruptible survives an interruption, and a frame of an uninterruptible
// type marked interruptible does not.
func TestInterruptibleFlagDecidesForAFrame(t *testing.T) {
	ctx := t.Context()
	d, s := newDelayer(), newSink("Sink")
	linkChain(t, ctx, processor.Setup{Clock: clock.NewSystem()}, d, s)

	kept := frames.NewTextFrame("kept")
	kept.SetInterruptible(false)
	dropped := newMarkerFrame("dropped")
	dropped.SetInterruptible(true)

	queue(t, ctx, d, frames.NewStartFrame())
	queue(t, ctx, d, kept)
	queue(t, ctx, d, dropped)
	time.Sleep(100 * time.Millisecond)
	queue(t, ctx, d, frames.NewInterruptionFrame())

	isText := func(f frames.Frame) bool { _, ok := f.(*frames.TextFrame); return ok }
	isMarker := func(f frames.Frame) bool { _, ok := f.(*markerFrame); return ok }
	if !waitFor(t, func() bool { return s.counts(isText) == 1 }) {
		t.Fatal("the frame marked uninterruptible did not survive the interruption")
	}
	time.Sleep(600 * time.Millisecond)
	if n := s.counts(isMarker); n != 0 {
		t.Fatalf("the frame marked interruptible survived the interruption (%d)", n)
	}
	if n := s.counts(isInterruption); n != 1 {
		t.Fatalf("got %d interruptions downstream, want 1", n)
	}
}

// TestInterruptibleFrames checks an interruption cancels an interruptible frame
// being processed: only the interruption comes out.
func TestInterruptibleFrames(t *testing.T) {
	ctx := t.Context()
	d, s := newDelayer(), newSink("Sink")
	linkChain(t, ctx, processor.Setup{Clock: clock.NewSystem()}, d, s)

	queue(t, ctx, d, frames.NewStartFrame())
	queue(t, ctx, d, frames.NewTextFrame("interrupted"))
	time.Sleep(100 * time.Millisecond)
	queue(t, ctx, d, frames.NewInterruptionFrame())

	if !waitFor(t, func() bool { return s.counts(isInterruption) == 1 }) {
		t.Fatal("the interruption did not come out")
	}
	time.Sleep(600 * time.Millisecond)
	if n := s.counts(isTextFrame); n != 0 {
		t.Fatalf("the interrupted frame came out (%d)", n)
	}
}

// TestUninterruptibleFrames checks an interruption leaves an uninterruptible
// frame being processed to finish: the interruption comes out, then the frame.
func TestUninterruptibleFrames(t *testing.T) {
	ctx := t.Context()
	d, s := newDelayer(), newSink("Sink")
	linkChain(t, ctx, processor.Setup{Clock: clock.NewSystem()}, d, s)

	queue(t, ctx, d, frames.NewStartFrame())
	queue(t, ctx, d, newMarkerFrame("kept"))
	time.Sleep(100 * time.Millisecond)
	queue(t, ctx, d, frames.NewInterruptionFrame())

	isMarker := func(f frames.Frame) bool { _, ok := f.(*markerFrame); return ok }
	if !waitFor(t, func() bool { return s.counts(isMarker) == 1 }) {
		t.Fatal("the uninterruptible frame did not survive the interruption")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var order []string
	for _, f := range s.got {
		switch f.(type) {
		case *frames.InterruptionFrame:
			order = append(order, "interruption")
		case *markerFrame:
			order = append(order, "marker")
		}
	}
	if len(order) != 2 || order[0] != "interruption" {
		t.Fatalf("got %v downstream, want the interruption ahead of the frame", order)
	}
}

// stallFirst holds the first text frame until it is interrupted, then pushes
// every frame after it straight on, so a frame handled after the interruption
// shows up downstream at once.
type stallFirst struct {
	*processor.Base
	entered chan struct{}
	once    sync.Once
}

func (p *stallFirst) ProcessFrame(ctx context.Context, f frames.Frame, dir processor.Direction) error {
	if err := p.Base.ProcessFrame(ctx, f, dir); err != nil {
		return err
	}
	if tf, ok := f.(*frames.TextFrame); ok && tf.Text == "interrupted" {
		p.once.Do(func() { close(p.entered) })
		<-ctx.Done()
		return nil
	}
	return p.PushFrame(ctx, f, dir)
}

// TestFramesQueuedBehindAnInterruptedFrameAreDropped checks the frames waiting
// behind an interrupted one are not processed: the interruption stops the
// process goroutine before it takes another frame.
func TestFramesQueuedBehindAnInterruptedFrameAreDropped(t *testing.T) {
	ctx := t.Context()
	p := &stallFirst{entered: make(chan struct{})}
	p.Base = processor.New("StallFirst", p)
	s := newSink("Sink")
	linkChain(t, ctx, processor.Setup{Clock: clock.NewSystem()}, p, s)

	queue(t, ctx, p, frames.NewStartFrame())
	queue(t, ctx, p, frames.NewTextFrame("interrupted"))
	<-p.entered
	for _, text := range []string{"B", "C", "D"} {
		queue(t, ctx, p, frames.NewTextFrame(text))
	}
	// The input queue serves system frames first, so the frames above are given
	// time to reach the in-order queue before the interruption overtakes them.
	time.Sleep(50 * time.Millisecond)
	queue(t, ctx, p, frames.NewInterruptionFrame())

	if !waitFor(t, func() bool { return s.counts(isInterruption) == 1 }) {
		t.Fatal("the interruption did not come out")
	}
	time.Sleep(100 * time.Millisecond)
	if n := s.counts(isTextFrame); n != 0 {
		t.Fatalf("%d frames queued behind the interrupted one came out", n)
	}

	// The processor carries on with what arrives after the interruption.
	queue(t, ctx, p, frames.NewTextFrame("after"))
	if !waitFor(t, func() bool { return s.counts(isTextFrame) == 1 }) {
		t.Fatal("the frame sent after the interruption did not come out")
	}
}

// canceled returns the context's error when it is interrupted mid-frame, the
// way a call cut off by its context does.
type canceled struct {
	*processor.Base
	entered chan struct{}
}

func (c *canceled) ProcessFrame(ctx context.Context, f frames.Frame, dir processor.Direction) error {
	if err := c.Base.ProcessFrame(ctx, f, dir); err != nil {
		return err
	}
	if _, ok := f.(*frames.TextFrame); ok {
		close(c.entered)
		<-ctx.Done()
		return fmt.Errorf("request aborted: %w", ctx.Err())
	}
	return c.PushFrame(ctx, f, dir)
}

// TestAnInterruptedFrameIsNotReportedAsAnError checks a frame that fails only
// because the interruption canceled it raises no error.
func TestAnInterruptedFrameIsNotReportedAsAnError(t *testing.T) {
	c := &canceled{entered: make(chan struct{})}
	c.Base = processor.New("Canceled", c)
	up, down := linkAndStart(t, c)
	ctx := context.Background()

	queue(t, ctx, c, frames.NewTextFrame("interrupted"))
	<-c.entered
	queue(t, ctx, c, frames.NewInterruptionFrame())
	mustReceive[*frames.InterruptionFrame](t, down.got, "InterruptionFrame")

	// A system frame behind the interruption marks the point by which an error
	// for the canceled frame would have gone upstream.
	queue(t, ctx, c, frames.NewUserStartedSpeakingFrame())
	mustReceive[*frames.UserStartedSpeakingFrame](t, down.got, "UserStartedSpeakingFrame")
	select {
	case f := <-up.got:
		t.Fatalf("got %s upstream for a canceled frame", f.Name())
	default:
	}
}

func isTextFrame(f frames.Frame) bool {
	_, ok := f.(*frames.TextFrame)
	return ok
}
