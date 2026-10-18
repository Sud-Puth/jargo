package processor_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gojargo/jargo/clock"
	"github.com/gojargo/jargo/frames"
	"github.com/gojargo/jargo/processor"
)

// stubborn ignores its context while handling the text frame "stuck", the way a
// blocking call with no deadline does, and records how many goroutines are
// inside ProcessFrame at once.
type stubborn struct {
	*processor.Base
	stuckFor          time.Duration
	entered           chan struct{}
	active, maxActive atomic.Int32
	handled           atomic.Int32
}

func (s *stubborn) ProcessFrame(ctx context.Context, f frames.Frame, dir processor.Direction) error {
	if err := s.Base.ProcessFrame(ctx, f, dir); err != nil {
		return err
	}
	tf, ok := f.(*frames.TextFrame)
	if !ok {
		return s.PushFrame(ctx, f, dir)
	}
	n := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		m := s.maxActive.Load()
		if n <= m || s.maxActive.CompareAndSwap(m, n) {
			break
		}
	}
	if tf.Text == "stuck" {
		close(s.entered)
		time.Sleep(s.stuckFor)
		return nil
	}
	s.handled.Add(1)
	time.Sleep(50 * time.Millisecond)
	return nil
}

// TestInterruptionWaitsForAProcessFrameThatIgnoresItsContext checks an
// interruption never gives up on the process goroutine it canceled, however
// long that takes, so a second one never handles frames alongside it.
func TestInterruptionWaitsForAProcessFrameThatIgnoresItsContext(t *testing.T) {
	if testing.Short() {
		t.Skip("holds a frame for several seconds")
	}
	s := &stubborn{stuckFor: 3500 * time.Millisecond, entered: make(chan struct{})}
	s.Base = processor.New("Stubborn", s)
	ctx := t.Context()
	linkChain(t, ctx, processor.Setup{Clock: clock.NewSystem()}, s, newCapture())

	queue(t, ctx, s, frames.NewStartFrame())
	queue(t, ctx, s, frames.NewTextFrame("stuck"))
	<-s.entered
	queue(t, ctx, s, frames.NewInterruptionFrame())
	// Sent once any bound on the wait would have run out, so a goroutine
	// started then would take them while the stuck one is still inside.
	time.Sleep(3100 * time.Millisecond)
	for range 4 {
		queue(t, ctx, s, frames.NewTextFrame("after"))
	}

	if !waitFor(t, func() bool { return s.handled.Load() == 4 }) {
		t.Fatalf("handled %d of the frames sent after the interruption, want 4", s.handled.Load())
	}
	if n := s.maxActive.Load(); n != 1 {
		t.Fatalf("ProcessFrame ran on %d goroutines at once", n)
	}
}

// selfInterrupter interrupts itself from its own frame handling when it sees
// the text frame "interrupt", then carries on.
type selfInterrupter struct {
	*processor.Base
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	seen    []string
}

func (p *selfInterrupter) ProcessFrame(ctx context.Context, f frames.Frame, dir processor.Direction) error {
	if err := p.Base.ProcessFrame(ctx, f, dir); err != nil {
		return err
	}
	if tf, ok := f.(*frames.TextFrame); ok {
		p.mu.Lock()
		p.seen = append(p.seen, tf.Text)
		p.mu.Unlock()
		if tf.Text == "interrupt" {
			close(p.entered)
			<-p.release
			p.StartInterruption()
		}
	}
	return p.PushFrame(ctx, f, dir)
}

// TestStartInterruptionFromTheProcessGoroutine checks a processor interrupting
// itself from inside its own frame handling neither deadlocks nor starts a
// second goroutine: the frames queued behind are dropped and it carries on.
func TestStartInterruptionFromTheProcessGoroutine(t *testing.T) {
	p := &selfInterrupter{entered: make(chan struct{}), release: make(chan struct{})}
	p.Base = processor.New("SelfInterrupter", p)
	_, down := linkAndStart(t, p)
	ctx := context.Background()

	queue(t, ctx, p, frames.NewTextFrame("interrupt"))
	<-p.entered
	queue(t, ctx, p, frames.NewTextFrame("dropped"))
	time.Sleep(50 * time.Millisecond)
	close(p.release)

	if tf := mustReceive[*frames.TextFrame](t, down.got, "TextFrame"); tf.Text != "interrupt" {
		t.Fatalf("got %q downstream, want the frame that interrupted", tf.Text)
	}
	queue(t, ctx, p, frames.NewTextFrame("after"))
	if tf := mustReceive[*frames.TextFrame](t, down.got, "TextFrame"); tf.Text != "after" {
		t.Fatalf("got %q downstream, want the frame sent after the interruption", tf.Text)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, text := range p.seen {
		if text == "dropped" {
			t.Fatal("the frame queued behind the interruption was handled")
		}
	}
}
