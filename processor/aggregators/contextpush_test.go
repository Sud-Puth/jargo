package aggregators_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gojargo/jargo/frames"
	"github.com/gojargo/jargo/pipeline"
	"github.com/gojargo/jargo/processor/aggregators"
	"github.com/gojargo/jargo/utils/events"
)

// assistantHarness runs an assistant aggregator and records the context frames it
// pushes upstream, which is how it asks for generation to run, and every frame
// that reaches the end of the pipeline.
type assistantHarness struct {
	task      *pipeline.Worker
	assistant *aggregators.AssistantAggregator

	mu   sync.Mutex
	runs int
	down []frames.Frame
}

func newAssistantHarness(t *testing.T, convo *frames.LLMContext) *assistantHarness {
	t.Helper()

	h := &assistantHarness{}
	h.assistant = aggregators.New(convo).Assistant()
	h.task = pipeline.NewWorker(pipeline.New(h.assistant), pipeline.WorkerConfig{
		ReachedUpstreamFilter:   pipeline.AnyFrame,
		ReachedDownstreamFilter: pipeline.AnyFrame,
	})
	events.On(&h.task.Registry, pipeline.EventFrameReachedUpstream, func(_ context.Context, f frames.Frame) {
		if _, ok := f.(*frames.LLMContextFrame); ok {
			h.mu.Lock()
			h.runs++
			h.mu.Unlock()
		}
	})
	started := make(chan struct{})
	events.On(&h.task.Registry, pipeline.EventPipelineStarted, func(context.Context, *frames.StartFrame) {
		close(started)
	})
	events.On(&h.task.Registry, pipeline.EventFrameReachedDownstream, func(_ context.Context, f frames.Frame) {
		h.mu.Lock()
		h.down = append(h.down, f)
		h.mu.Unlock()
	})

	runDone := make(chan error, 1)
	go func() { runDone <- h.task.Run(context.Background()) }()
	t.Cleanup(func() {
		h.task.StopWhenDone()
		select {
		case <-runDone:
		case <-time.After(3 * time.Second):
			t.Error("task did not finish")
		}
	})
	// Started before a test drives it, so a pause takes the frames that follow
	// into the aggregator's queue rather than holding them ahead of the start.
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("the aggregator never started")
	}
	return h
}

// generations is how many times the aggregator has asked for generation to run.
func (h *assistantHarness) generations() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runs
}

// downstream is the frames that reached the end of the pipeline, by type name.
func (h *assistantHarness) downstream() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	names := make([]string, 0, len(h.down))
	for _, f := range h.down {
		name, _, _ := strings.Cut(f.Name(), "#")
		names = append(names, name)
	}
	return names
}
