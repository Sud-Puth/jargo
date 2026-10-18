package tts_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gojargo/jargo/frames"
	"github.com/gojargo/jargo/pipeline"
	"github.com/gojargo/jargo/service/tts"
	"github.com/gojargo/jargo/utils/events"
)

// hookSynth answers on its own receive loop, one chunk at a time and slowly, so
// an interruption lands while its context is still playing. It records which
// contexts the base reports interrupted or completed, and whether each context
// was still open when it was reported interrupted.
type hookSynth struct {
	mu          sync.Mutex
	host        tts.AudioContextHost
	requests    []string
	interrupted []string
	openOnHook  []bool
	completed   []string
	wg          sync.WaitGroup
}

var (
	_ tts.AudioContextInterrupter = (*hookSynth)(nil)
	_ tts.AudioContextCompleter   = (*hookSynth)(nil)
)

func (s *hookSynth) SampleRate() int { return orderingSampleRate }

func (s *hookSynth) SetAudioContextHost(h tts.AudioContextHost) {
	s.mu.Lock()
	s.host = h
	s.mu.Unlock()
}

func (s *hookSynth) RunTTS(_ context.Context, _, contextID string, _ func(frames.Frame) error) error {
	s.mu.Lock()
	host := s.host
	s.requests = append(s.requests, contextID)
	s.mu.Unlock()
	s.wg.Go(func() {
		for range 10 {
			time.Sleep(20 * time.Millisecond)
			if !host.AudioContextAvailable(contextID) {
				return
			}
			audio := frames.NewTTSAudioRawFrame(make([]byte, orderingChunkBytes), orderingSampleRate, 1)
			audio.ContextID = contextID
			host.AppendToAudioContext(contextID, audio)
		}
		host.AppendToAudioContext(contextID, frames.NewTTSStoppedFrame())
		host.RemoveAudioContext(contextID)
	})
	return nil
}

func (s *hookSynth) OnAudioContextInterrupted(_ context.Context, contextID string) {
	s.mu.Lock()
	host := s.host
	s.mu.Unlock()
	open := host.AudioContextAvailable(contextID)
	s.mu.Lock()
	s.interrupted = append(s.interrupted, contextID)
	s.openOnHook = append(s.openOnHook, open)
	s.mu.Unlock()
}

func (s *hookSynth) OnAudioContextCompleted(_ context.Context, contextID string) {
	s.mu.Lock()
	s.completed = append(s.completed, contextID)
	s.mu.Unlock()
}

// TestInterruptionReportsOpenContextsToProvider covers the provider hook an
// interruption runs: the context being played and the one queued behind it are
// each reported interrupted, while still open, and neither is reported
// completed. Speech requested after the interruption still plays.
func TestInterruptionReportsOpenContextsToProvider(t *testing.T) {
	synth := &hookSynth{}
	base := tts.New("HookTTS", synth)
	task := pipeline.NewWorker(pipeline.New(base), pipeline.WorkerConfig{
		ReachedDownstreamFilter: pipeline.AnyFrame,
	})
	var mu sync.Mutex
	audioFor := map[string]int{}
	events.On(&task.Registry, pipeline.EventFrameReachedDownstream, func(_ context.Context, f frames.Frame) {
		if a, ok := f.(*frames.TTSAudioRawFrame); ok {
			mu.Lock()
			audioFor[a.ContextID]++
			mu.Unlock()
		}
	})
	done := make(chan error, 1)
	go func() { done <- task.Run(context.Background()) }()

	task.QueueFrame(frames.NewTTSSpeakFrame("One."))
	task.QueueFrame(frames.NewTTSSpeakFrame("Two."))
	time.Sleep(80 * time.Millisecond)
	task.QueueFrame(frames.NewInterruptionFrame())
	time.Sleep(50 * time.Millisecond)
	task.QueueFrame(frames.NewTTSSpeakFrame("Three."))
	time.Sleep(400 * time.Millisecond)
	task.QueueFrame(frames.NewEndFrame())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pipeline did not end")
	}
	synth.wg.Wait()

	synth.mu.Lock()
	defer synth.mu.Unlock()
	if len(synth.requests) != 3 {
		t.Fatalf("requests = %v, want three", synth.requests)
	}
	first, second, third := synth.requests[0], synth.requests[1], synth.requests[2]
	interrupted := slices.Clone(synth.interrupted)
	slices.Sort(interrupted)
	want := []string{first, second}
	slices.Sort(want)
	if !slices.Equal(interrupted, want) {
		t.Fatalf("interrupted = %v, want the playing and the queued context %v", synth.interrupted, want)
	}
	for i, open := range synth.openOnHook {
		if !open {
			t.Errorf("context %s was already removed when reported interrupted", synth.interrupted[i])
		}
	}
	if slices.Contains(synth.completed, first) || slices.Contains(synth.completed, second) {
		t.Errorf("completed = %v, an interrupted context must not be reported completed", synth.completed)
	}
	if !slices.Contains(synth.completed, third) {
		t.Errorf("completed = %v, want the speech after the interruption %s", synth.completed, third)
	}
	mu.Lock()
	defer mu.Unlock()
	if audioFor[third] == 0 {
		t.Errorf("no audio played for the speech requested after the interruption")
	}
}
