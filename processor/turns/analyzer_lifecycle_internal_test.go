package turns

import (
	"testing"

	"github.com/gojargo/jargo/audio/turn"
	"github.com/gojargo/jargo/processor"
)

// cleanupCountingAnalyzer is a turn.Analyzer that counts how often it was asked
// to release what it holds, standing in for a model with a native session.
type cleanupCountingAnalyzer struct {
	slowAnalyzer
	cleanups int
}

func (a *cleanupCountingAnalyzer) Cleanup() error {
	a.cleanups++
	return nil
}

// Cleaning up the turn analyzer stop strategy cleans up its analyzer, which is
// the only point the analyzer's native session is released. Without it every
// pipeline leaked one model session.
func TestTurnAnalyzerStopCleanupCleansUpTheAnalyzer(t *testing.T) {
	a := &cleanupCountingAnalyzer{}
	c := NewUserTurnController(UserTurnStrategies{
		Stop: []StopStrategy{NewTurnAnalyzerStop(TurnAnalyzerConfig{Analyzer: a})},
	}, 0)
	if err := c.Setup(t.Context(), processor.Setup{}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if a.cleanups != 0 {
		t.Fatalf("the analyzer was cleaned up %d times before the strategy was", a.cleanups)
	}

	c.Cleanup()

	if a.cleanups != 1 {
		t.Errorf("the analyzer was cleaned up %d times, want 1", a.cleanups)
	}
}

// Replacing the strategies cleans up the analyzer of the chain that goes, and
// leaves the one that arrives alone.
func TestUpdatingStrategiesCleansUpTheReplacedAnalyzer(t *testing.T) {
	old, replacement := &cleanupCountingAnalyzer{}, &cleanupCountingAnalyzer{}
	c := NewUserTurnController(UserTurnStrategies{
		Stop: []StopStrategy{NewTurnAnalyzerStop(TurnAnalyzerConfig{Analyzer: old})},
	}, 0)
	if err := c.Setup(t.Context(), processor.Setup{}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := c.UpdateStrategies(UserTurnStrategies{
		Stop: []StopStrategy{NewTurnAnalyzerStop(TurnAnalyzerConfig{Analyzer: replacement})},
	})
	if err != nil {
		t.Fatalf("update strategies: %v", err)
	}

	if old.cleanups != 1 {
		t.Errorf("the replaced analyzer was cleaned up %d times, want 1", old.cleanups)
	}
	if replacement.cleanups != 0 {
		t.Errorf("the arriving analyzer was cleaned up %d times, want 0", replacement.cleanups)
	}
}

// A strategy whose analyzer could not be built has nothing to release, and
// cleaning it up must not fail on the missing analyzer.
func TestTurnAnalyzerStopCleanupWithoutAnAnalyzer(t *testing.T) {
	s := &TurnAnalyzerStop{analyzerErr: errNotSetUp}
	s.Cleanup()
}

var _ turn.Analyzer = (*cleanupCountingAnalyzer)(nil)
