package turn

import (
	"fmt"
	"math"
	"testing"

	"github.com/gojargo/jargo/internal/onnxrt"
)

// TestSmartTurnModelMatchesReference runs the embedded smart-turn-v3 model over
// features computed for the reference signal and checks the completion
// probability against the Python reference. It needs the ONNX runtime.
//
// The tolerance is wide because of what this test can actually pin down. The
// features are jargo's own arithmetic, and TestComputeLogMel holds them to 1e-6
// against the same reference; everything after that is the runtime's inference,
// which picks its kernels from what the host CPU offers. The same model over the
// same features drifts by a few thousandths between machines, so a tolerance
// tight enough to catch that would fail on the next processor rather than on a
// defect. What is left to check here is that the model is wired to the features
// correctly, and a wiring error moves the probability by tenths, not
// thousandths.
func TestSmartTurnModelMatchesReference(t *testing.T) {
	if !onnxrt.Available() {
		t.Skip("ONNX runtime not configured; set JARGO_ONNXRUNTIME_LIB")
	}

	s, err := NewSmartTurnV3()
	if err != nil {
		t.Fatalf("NewSmartTurnV3: %v", err)
	}
	defer func() { _ = s.Close() }()

	features := computeLogMel(refAudio())
	prob, err := s.runModel(features)
	if err != nil {
		t.Fatalf("runModel: %v", err)
	}

	const (
		want = 0.980147
		tol  = 1e-2
	)
	if math.Abs(prob-want) > tol {
		t.Errorf("smart-turn probability = %v, want %v (tol %v)", prob, want, tol)
	}
	// And the decision the pipeline actually takes from it.
	if prob <= 0.5 {
		t.Errorf("smart-turn probability = %v, want the turn read as complete", prob)
	}
}

// TestSmartTurnV3SharedModel verifies that multiple SmartTurnV3 analyzers sharing
// a single SmartTurnModel evaluate identical probabilities to dedicated sessions,
// and closing one analyzer does not close the shared session.
func TestSmartTurnV3SharedModel(t *testing.T) {
	if !onnxrt.Available() {
		t.Skip("ONNX runtime not configured; set JARGO_ONNXRUNTIME_LIB")
	}

	model, err := NewSmartTurnModel()
	if err != nil {
		t.Fatalf("NewSmartTurnModel: %v", err)
	}
	defer func() { _ = model.Close() }()

	features := computeLogMel(refAudio())
	const (
		want = 0.980147
		tol  = 1e-2
	)

	// Analyzer 1 using shared model
	s1, err := NewSmartTurnV3WithModel(model)
	if err != nil {
		t.Fatalf("NewSmartTurnV3WithModel s1: %v", err)
	}
	prob1, err := s1.runModel(features)
	if err != nil {
		t.Fatalf("s1.runModel: %v", err)
	}
	if math.Abs(prob1-want) > tol {
		t.Errorf("s1 probability = %v, want %v", prob1, want)
	}

	// Close s1; this should NOT close the shared model
	if err := s1.Close(); err != nil {
		t.Fatalf("s1.Close: %v", err)
	}

	// Analyzer 2 using the same shared model must still function properly
	s2, err := NewSmartTurnV3WithModel(model)
	if err != nil {
		t.Fatalf("NewSmartTurnV3WithModel s2: %v", err)
	}
	defer func() { _ = s2.Close() }()

	prob2, err := s2.runModel(features)
	if err != nil {
		t.Fatalf("s2.runModel: %v", err)
	}
	if math.Abs(prob2-want) > tol {
		t.Errorf("s2 probability = %v, want %v", prob2, want)
	}
	if math.Abs(prob1-prob2) > 1e-6 {
		t.Errorf("s1 and s2 probabilities diverged: %v vs %v", prob1, prob2)
	}
}

// TestSmartTurnV3SharedModelConcurrency verifies that multiple concurrent
// goroutines can run inference on SmartTurnV3 backed by one SmartTurnModel
// without data races or memory corruption.
func TestSmartTurnV3SharedModelConcurrency(t *testing.T) {
	if !onnxrt.Available() {
		t.Skip("ONNX runtime not configured; set JARGO_ONNXRUNTIME_LIB")
	}

	model, err := NewSmartTurnModel()
	if err != nil {
		t.Fatalf("NewSmartTurnModel: %v", err)
	}
	defer func() { _ = model.Close() }()

	const (
		numWorkers = 20
		want       = 0.980147
		tol        = 1e-2
	)
	features := computeLogMel(refAudio())

	errs := make(chan error, numWorkers)
	for w := 0; w < numWorkers; w++ {
		go func() {
			s, err := NewSmartTurnV3WithModel(model)
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = s.Close() }()

			prob, err := s.runModel(features)
			if err != nil {
				errs <- err
				return
			}
			if math.Abs(prob-want) > tol {
				errs <- fmt.Errorf("probability = %v, want %v", prob, want)
				return
			}
			errs <- nil
		}()
	}

	for w := 0; w < numWorkers; w++ {
		if err := <-errs; err != nil {
			t.Errorf("worker failed: %v", err)
		}
	}
}
