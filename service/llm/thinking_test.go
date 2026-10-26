package llm_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/gojargo/jargo/service/llm"
)

// thinkingLogs routes the default logger to a buffer at INFO and above until
// the test ends, and returns what it logged, one line per entry.
func thinkingLogs(t *testing.T) func() []string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() []string {
		out := strings.TrimSpace(buf.String())
		if out == "" {
			return nil
		}
		return strings.Split(out, "\n")
	}
}

// TestThinkingDefaultIsReportedOncePerModel checks that the developer hears what
// was sent, and when to change it, once per model rather than on every request.
func TestThinkingDefaultIsReportedOncePerModel(t *testing.T) {
	lines := thinkingLogs(t)
	svc := llm.New("FakeLLM", &fakeGen{})

	for _, model := range []string{"m-1", "m-1", "m-2", "m-2"} {
		svc.ReportThinkingDefault(model, `thinking={"type":"disabled"}`, true, "P", "Thinking in p.Config")
	}

	got := lines()
	if len(got) != 2 {
		t.Fatalf("logged %d lines, want 2:\n%s", len(got), strings.Join(got, "\n"))
	}
	for i, model := range []string{"m-1", "m-2"} {
		if !strings.Contains(got[i], "level=INFO") || !strings.Contains(got[i], "model="+model+" ") ||
			!strings.Contains(got[i], "multistep reasoning") ||
			!strings.Contains(got[i], `configure="Thinking in p.Config"`) {
			t.Errorf("line %d = %s, want the setting sent to %s and what to configure", i, got[i], model)
		}
	}
}

// TestAModelThatCanThinkWithoutADefaultIsWarnedAbout checks that a model that
// may think before replying is reported as a warning, and one that cannot
// think is not reported at all.
func TestAModelThatCanThinkWithoutADefaultIsWarnedAbout(t *testing.T) {
	lines := thinkingLogs(t)
	svc := llm.New("FakeLLM", &fakeGen{})

	svc.ReportThinkingDefault("thinker", "", true, "P", "Thinking in p.Config")
	svc.ReportThinkingDefault("plain", "", false, "P", "Thinking in p.Config")

	got := lines()
	if len(got) != 1 {
		t.Fatalf("logged %d lines, want 1:\n%s", len(got), strings.Join(got, "\n"))
	}
	if !strings.Contains(got[0], "level=WARN") || !strings.Contains(got[0], "no thinking default") ||
		!strings.Contains(got[0], "model=thinker") || !strings.Contains(got[0], "provider=P") {
		t.Errorf("line = %s, want a warning about thinker", got[0])
	}
}

// TestUseProviderThinkingDefaultIsReportedAtDebug checks a model left to its
// provider's default on purpose is reported once, at DEBUG, and a model that
// cannot think is not reported at all.
func TestUseProviderThinkingDefaultIsReportedAtDebug(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	svc := llm.New("FakeLLM", &fakeGen{})
	if svc.UseProviderThinkingDefault() {
		t.Fatal("use_provider_thinking_default is on before anything set it")
	}
	svc.SetUseProviderThinkingDefault(true)

	svc.ReportThinkingDefault("thinker", "", true, "P", "Thinking in p.Config")
	svc.ReportThinkingDefault("thinker", "", true, "P", "Thinking in p.Config")
	svc.ReportThinkingDefault("plain", "", false, "P", "Thinking in p.Config")

	got := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(got) != 1 {
		t.Fatalf("logged %d lines, want 1:\n%s", len(got), buf.String())
	}
	if !strings.Contains(got[0], "level=DEBUG") ||
		!strings.Contains(got[0], "use_provider_thinking_default is set") ||
		!strings.Contains(got[0], "model=thinker") {
		t.Errorf("line = %s, want thinker reported at DEBUG", got[0])
	}
}
