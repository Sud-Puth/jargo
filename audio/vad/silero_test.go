package vad

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/gojargo/jargo/internal/onnxrt"
)

// toneFrames builds n frames of 512 mono 16-bit samples of a continuous 300 Hz
// sine at amplitude 0.5; silenceFrames builds n frames of zeros.
func toneFrames(n int) [][]byte {
	frames := make([][]byte, n)
	for f := range n {
		buf := make([]byte, 512*2)
		for i := range 512 {
			global := f*512 + i
			v := 0.5 * math.Sin(2*math.Pi*300*float64(global)/16000)
			binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(v*32767)))
		}
		frames[f] = buf
	}
	return frames
}

func silenceFrames(n int) [][]byte {
	frames := make([][]byte, n)
	for f := range n {
		frames[f] = make([]byte, 512*2)
	}
	return frames
}

// TestSileroMatchesReference verifies the Silero confidence sequence against
// values produced by the reference Python implementation, confirming the
// context and recurrent-state management match. It needs the ONNX runtime.
func TestSileroMatchesReference(t *testing.T) {
	if !onnxrt.Available() {
		t.Skip("ONNX runtime not configured; set JARGO_ONNXRUNTIME_LIB")
	}

	const tol = 1e-3
	toneWant := []float64{0.118424, 0.091276, 0.038707, 0.023131, 0.010957, 0.006557, 0.003468, 0.002861}
	silenceWant := []float64{0.023829, 0.011602, 0.006769, 0.003017, 0.002068, 0.001625, 0.001439, 0.001337}
	cases := []struct {
		name   string
		frames [][]byte
		want   []float64
	}{
		{"tone", toneFrames(8), toneWant},
		{"silence", silenceFrames(8), silenceWant},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := NewSilero()
			if err != nil {
				t.Fatalf("NewSilero: %v", err)
			}
			defer func() { _ = s.Close() }()
			if err := s.SetSampleRate(16000); err != nil {
				t.Fatalf("SetSampleRate: %v", err)
			}

			for i, frame := range c.frames {
				got := s.voiceConfidence(frame)
				if math.Abs(got-c.want[i]) > tol {
					t.Errorf("frame %d confidence = %v, want %v (tol %v)", i, got, c.want[i], tol)
				}
			}
		})
	}
}

// TestSileroSharedModel verifies that multiple Silero analyzers sharing a single
// SileroModel produce the exact same confidence sequence as dedicated sessions,
// and that closing an analyzer does not invalidate the shared session.
func TestSileroSharedModel(t *testing.T) {
	if !onnxrt.Available() {
		t.Skip("ONNX runtime not configured; set JARGO_ONNXRUNTIME_LIB")
	}

	model, err := NewSileroModel()
	if err != nil {
		t.Fatalf("NewSileroModel: %v", err)
	}
	defer func() { _ = model.Close() }()

	const tol = 1e-3
	toneWant := []float64{0.118424, 0.091276, 0.038707, 0.023131, 0.010957, 0.006557, 0.003468, 0.002861}
	frames := toneFrames(8)

	// Analyzer 1 using shared model
	s1, err := NewSilero(WithModel(model))
	if err != nil {
		t.Fatalf("NewSilero with shared model: %v", err)
	}
	if err := s1.SetSampleRate(16000); err != nil {
		t.Fatalf("SetSampleRate: %v", err)
	}

	for i, frame := range frames {
		got := s1.voiceConfidence(frame)
		if math.Abs(got-toneWant[i]) > tol {
			t.Errorf("s1 frame %d confidence = %v, want %v", i, got, toneWant[i])
		}
	}

	// Close s1; this should NOT close the shared model
	if err := s1.Close(); err != nil {
		t.Fatalf("s1.Close: %v", err)
	}

	// Analyzer 2 using the same shared model must still function properly
	s2, err := NewSilero(WithModel(model))
	if err != nil {
		t.Fatalf("NewSilero s2 with shared model: %v", err)
	}
	defer func() { _ = s2.Close() }()
	if err := s2.SetSampleRate(16000); err != nil {
		t.Fatalf("SetSampleRate: %v", err)
	}

	for i, frame := range frames {
		got := s2.voiceConfidence(frame)
		if math.Abs(got-toneWant[i]) > tol {
			t.Errorf("s2 frame %d confidence = %v, want %v", i, got, toneWant[i])
		}
	}
}

// TestSileroSharedModelConcurrency verifies that multiple concurrent goroutines
// can run inference through separate Silero analyzers backed by one SileroModel
// without data races or corruption.
func TestSileroSharedModelConcurrency(t *testing.T) {
	if !onnxrt.Available() {
		t.Skip("ONNX runtime not configured; set JARGO_ONNXRUNTIME_LIB")
	}

	model, err := NewSileroModel()
	if err != nil {
		t.Fatalf("NewSileroModel: %v", err)
	}
	defer func() { _ = model.Close() }()

	const (
		numWorkers = 20
		numFrames  = 8
		tol        = 1e-3
	)
	toneWant := []float64{0.118424, 0.091276, 0.038707, 0.023131, 0.010957, 0.006557, 0.003468, 0.002861}

	errs := make(chan error, numWorkers)
	for w := 0; w < numWorkers; w++ {
		go func() {
			s, err := NewSilero(WithModel(model))
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = s.Close() }()

			if err := s.SetSampleRate(16000); err != nil {
				errs <- err
				return
			}

			frames := toneFrames(numFrames)
			for i, frame := range frames {
				got := s.voiceConfidence(frame)
				if math.Abs(got-toneWant[i]) > tol {
					errs <- fmt.Errorf("frame %d confidence = %v, want %v", i, got, toneWant[i])
					return
				}
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
