package llm_test

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/gojargo/jargo/frames"
	"github.com/gojargo/jargo/processor"
	"github.com/gojargo/jargo/service/llm"
)

// TestInterruptionCancelsACallDispatchedJustBefore checks a call is tracked from
// the moment it is dispatched, not from the moment its goroutine runs. An
// interruption handled right after the calls were started must cancel it, or
// its result would ask for inference on a turn the user abandoned.
func TestInterruptionCancelsACallDispatchedJustBefore(t *testing.T) {
	// One processor keeps the call's goroutine from running before the
	// interruption is handled, which is the window being checked.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))

	svc := llm.New("FakeToolLLM", cancelOnInterruptionGen{})
	ran := make(chan struct{}, 1)
	svc.RegisterFunction("get_weather", func(ctx context.Context, p llm.FunctionCallParams) error {
		ran <- struct{}{}
		<-ctx.Done()
		return nil
	})
	canceled := make(chan []frames.ToolCall, 1)
	svc.OnFunctionCallsCanceled(func(_ context.Context, calls []frames.ToolCall) { canceled <- calls })

	ctx := context.Background()
	if err := svc.Setup(ctx, processor.Setup{}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Cleanup(ctx) }()

	convo := frames.NewLLMContext("")
	if err := svc.RunFunctionCalls(ctx, convo, []frames.ToolCall{
		{ID: "call_1", Name: "get_weather", Args: json.RawMessage(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessFrame(ctx, frames.NewInterruptionFrame(), processor.Downstream); err != nil {
		t.Fatal(err)
	}

	select {
	case calls := <-canceled:
		if len(calls) != 1 || calls[0].ID != "call_1" {
			t.Fatalf("canceled calls = %+v, want call_1", calls)
		}
	case <-time.After(time.Second):
		t.Fatal("the interruption did not cancel the call dispatched just before it")
	}
	select {
	case <-ran:
		t.Fatal("a call canceled before it started still ran its handler")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestFunctionUnregisteredBetweenQueueAndExecute checks the handler of a call is
// looked up again when the call starts. A function unregistered after the call
// was queued does not run; the call is answered as a missing function instead,
// so it still ends with an ordinary result.
func TestFunctionUnregisteredBetweenQueueAndExecute(t *testing.T) {
	svc := llm.New("FakeToolLLM", cancelOnInterruptionGen{}, llm.WithSequentialFunctionCalls())
	ran := make(chan string, 2)
	svc.RegisterFunction("swap", func(ctx context.Context, p llm.FunctionCallParams) error {
		// A tool set swapped by an earlier call of the same response.
		svc.UnregisterFunction("doomed_tool")
		ran <- "swap"
		return p.Result(ctx, "ok", nil)
	})
	svc.RegisterFunction("doomed_tool", func(ctx context.Context, p llm.FunctionCallParams) error {
		ran <- "doomed_tool"
		return p.Result(ctx, "should not be called", nil)
	})

	ctx := context.Background()
	if err := svc.Setup(ctx, processor.Setup{}); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Cleanup(ctx) }()

	if err := svc.RunFunctionCalls(ctx, frames.NewLLMContext(""), []frames.ToolCall{
		{ID: "call_1", Name: "swap", Args: json.RawMessage(`{}`)},
		{ID: "call_2", Name: "doomed_tool", Args: json.RawMessage(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}
	if got := <-ran; got != "swap" {
		t.Fatalf("first handler = %s, want swap", got)
	}
	select {
	case got := <-ran:
		t.Fatalf("handler unregistered before its call started still ran: %s", got)
	case <-time.After(200 * time.Millisecond):
	}
}
