package transport_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gojargo/jargo/audio/dtmf"
	"github.com/gojargo/jargo/frames"
	"github.com/gojargo/jargo/pipeline"
	"github.com/gojargo/jargo/transport"
)

// startFakeOutput runs o in a task and returns the task plus a stop.
func startFakeOutput(t *testing.T, o *fakeOutput) (*pipeline.Worker, func()) {
	t.Helper()
	task := pipeline.NewWorker(pipeline.New(o), pipeline.WorkerConfig{})
	runDone := make(chan error, 1)
	go func() { runDone <- task.Run(context.Background()) }()
	return task, func() {
		task.StopWhenDone()
		select {
		case <-runDone:
		case <-time.After(3 * time.Second):
			t.Error("the output task did not shut down")
		}
	}
}

// collectPCM drains writes until it has at least n bytes or gives up.
func collectPCM(t *testing.T, o *fakeOutput, n int) []byte {
	t.Helper()
	var pcm []byte
	deadline := time.After(3 * time.Second)
	for len(pcm) < n {
		select {
		case chunk := <-o.writes:
			pcm = append(pcm, chunk...)
		case <-deadline:
			t.Fatalf("only %d of %d bytes were written", len(pcm), n)
		}
	}
	return pcm
}

// TestOutputDTMFIsSoundedAsAudio checks a keypress reaches a transport that
// cannot signal one natively as the tone for that key. Without this a bot cannot
// answer an IVR at all: the frame was defined and nothing ever played it.
func TestOutputDTMFIsSoundedAsAudio(t *testing.T) {
	params := transport.DefaultParams()
	params.AudioOutSampleRate = 16000
	o := newFakeOutput(params)
	task, stop := startFakeOutput(t, o)
	defer stop()

	task.QueueFrame(frames.NewOutputDTMFFrame(frames.KeypadSeven))

	want, err := dtmf.Tone(frames.KeypadSeven, params.AudioOutSampleRate)
	if err != nil {
		t.Fatalf("Tone: %v", err)
	}
	got := collectPCM(t, o, len(want))
	if len(got) < len(want) {
		t.Fatalf("wrote %d bytes, want at least the %d of one tone", len(got), len(want))
	}
}

// TestOutputDTMFSequenceKeepsItsOrder checks a run of keys is sounded in the
// order it was pressed, since a number entered out of order is a different
// number.
func TestOutputDTMFSequenceKeepsItsOrder(t *testing.T) {
	params := transport.DefaultParams()
	params.AudioOutSampleRate = 16000
	o := newFakeOutput(params)
	task, stop := startFakeOutput(t, o)
	defer stop()

	keys := []frames.KeypadEntry{frames.KeypadOne, frames.KeypadTwo, frames.KeypadPound}
	task.QueueFrame(frames.NewOutputDTMFSequenceFrame(keys))

	one, err := dtmf.Tone(frames.KeypadOne, params.AudioOutSampleRate)
	if err != nil {
		t.Fatalf("Tone: %v", err)
	}
	got := collectPCM(t, o, len(one)*len(keys))
	if len(got) < len(one)*len(keys) {
		t.Fatalf("wrote %d bytes, want the %d of three tones", len(got), len(one)*len(keys))
	}
}

// nativeDTMFOutput reports that it signals keypresses itself, so the base must
// hand them over rather than sounding them.
type nativeDTMFOutput struct {
	*transport.BaseOutput
	keys chan []frames.KeypadEntry
	pcm  chan []byte
}

func newNativeDTMFOutput(p transport.Params) *nativeDTMFOutput {
	o := &nativeDTMFOutput{
		keys: make(chan []frames.KeypadEntry, 8),
		pcm:  make(chan []byte, 64),
	}
	o.BaseOutput = transport.NewBaseOutput("NativeDTMFOutput", p, o)
	return o
}

func (o *nativeDTMFOutput) SupportsNativeDTMF() bool { return true }

func (o *nativeDTMFOutput) WriteDTMFNative(_ context.Context, f frames.DTMFOutput) error {
	select {
	case o.keys <- f.Keys():
	default:
	}
	return nil
}

func (o *nativeDTMFOutput) WriteAudio(ctx context.Context, f frames.OutputAudioFrame) (bool, error) {
	cp := append([]byte(nil), f.AudioData().Audio...)
	select {
	case o.pcm <- cp:
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// TestNativeDTMFBypassesTheAudioPath checks a transport whose protocol carries
// keypresses is handed them, and does not also hear them as audio. Sounding the
// tone as well would put it into anything recording the call.
func TestNativeDTMFBypassesTheAudioPath(t *testing.T) {
	params := transport.DefaultParams()
	params.AudioOutSampleRate = 16000
	o := newNativeDTMFOutput(params)
	task, stop := startFakeOutput2(t, o)
	defer stop()

	task.QueueFrame(frames.NewOutputDTMFFrame(frames.KeypadFour))

	select {
	case keys := <-o.keys:
		if frames.KeypadString(keys) != "4" {
			t.Errorf("keys = %q, want 4", frames.KeypadString(keys))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the transport was never handed the keypress")
	}

	select {
	case pcm := <-o.pcm:
		t.Errorf("wrote %d bytes of audio for a natively signaled keypress", len(pcm))
	case <-time.After(300 * time.Millisecond):
	}
}

// TestUrgentDTMFGoesOutAtOnce checks the urgent frame is sent without waiting
// behind the audio already queued, which is what a keypress answering a prompt
// that is still playing needs.
func TestUrgentDTMFGoesOutAtOnce(t *testing.T) {
	params := transport.DefaultParams()
	params.AudioOutSampleRate = 16000
	o := newNativeDTMFOutput(params)
	task, stop := startFakeOutput2(t, o)
	defer stop()

	task.QueueFrame(frames.NewOutputDTMFUrgentFrame(frames.KeypadStar))

	select {
	case keys := <-o.keys:
		if frames.KeypadString(keys) != "*" {
			t.Errorf("keys = %q, want *", frames.KeypadString(keys))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the urgent keypress never reached the transport")
	}
}

// pacedDTMFOutput takes a chunk as long as it plays, like a network transport
// emulating a device, and counts writes that land while another is in flight.
type pacedDTMFOutput struct {
	*transport.BaseOutput
	inside  atomic.Int32
	overlap atomic.Int32
	writes  atomic.Int32
}

func (o *pacedDTMFOutput) WriteAudio(ctx context.Context, f frames.OutputAudioFrame) (bool, error) {
	if o.inside.Add(1) > 1 {
		o.overlap.Add(1)
	}
	defer o.inside.Add(-1)
	o.writes.Add(1)
	a := f.AudioData()
	d := time.Duration(len(a.Audio)/2) * time.Second / time.Duration(a.SampleRate)
	select {
	case <-time.After(d):
		return true, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// TestUrgentDTMFIsNotWrittenOverQueuedAudio checks the tones of an urgent
// keypress are not handed to the transport while the audio loop is in the middle
// of writing a chunk. A transport encodes what it writes (a serializer resamples
// it, say), and two writes at once would drive that encoder from two goroutines.
func TestUrgentDTMFIsNotWrittenOverQueuedAudio(t *testing.T) {
	params := transport.DefaultParams()
	params.AudioOutSampleRate = 16000
	o := &pacedDTMFOutput{}
	o.BaseOutput = transport.NewBaseOutput("PacedOutput", params, o)
	task := pipeline.NewWorker(pipeline.New(o), pipeline.WorkerConfig{})
	runDone := make(chan error, 1)
	go func() { runDone <- task.Run(context.Background()) }()
	defer func() {
		task.Cancel(context.Background(), "")
		<-runDone
	}()

	// One second of audio keeps the audio loop writing while the keypress lands.
	task.QueueFrame(frames.NewTTSAudioRawFrame(make([]byte, 32000), 16000, 1))
	deadline := time.Now().Add(3 * time.Second)
	for o.writes.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the audio loop never started writing")
		}
		time.Sleep(5 * time.Millisecond)
	}
	before := o.writes.Load()
	task.QueueFrame(frames.NewOutputDTMFUrgentFrame(frames.KeypadOne))

	// Wait for the tone to be written and for the queued audio to go on.
	time.Sleep(600 * time.Millisecond)
	if o.writes.Load() <= before+1 {
		t.Fatalf("only %d writes after the keypress", o.writes.Load()-before)
	}
	if n := o.overlap.Load(); n != 0 {
		t.Errorf("%d writes were handed to the transport while another was in flight", n)
	}
}

// startFakeOutput2 runs a native-DTMF output in a task.
func startFakeOutput2(t *testing.T, o *nativeDTMFOutput) (*pipeline.Worker, func()) {
	t.Helper()
	task := pipeline.NewWorker(pipeline.New(o), pipeline.WorkerConfig{})
	runDone := make(chan error, 1)
	go func() { runDone <- task.Run(context.Background()) }()
	return task, func() {
		task.StopWhenDone()
		select {
		case <-runDone:
		case <-time.After(3 * time.Second):
			t.Error("the output task did not shut down")
		}
	}
}
