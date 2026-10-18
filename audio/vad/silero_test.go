package vad

import (
	"encoding/binary"
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

// speechFrames returns the first n frames of 512 samples of the 16 kHz speech
// fixture.
func speechFrames(t *testing.T, n int) [][]byte {
	t.Helper()
	pcm := readSpeechPCM(t)
	frames := make([][]byte, n)
	for f := range n {
		frames[f] = pcm[f*512*2 : (f+1)*512*2]
	}
	return frames
}

// TestSileroMatchesReference verifies the Silero confidence sequence against
// reference values, confirming the context and recurrent-state management
// match. The values come from running the embedded model in a standalone
// onnxruntime script over the same frames, zeroing the state and the 64-sample
// context at the start and carrying both from one frame to the next;
// regenerate them that way whenever the model changes. It needs the ONNX
// runtime.
func TestSileroMatchesReference(t *testing.T) {
	if !onnxrt.Available() {
		t.Skip("ONNX runtime not configured; set JARGO_ONNXRUNTIME_LIB")
	}

	const tol = 1e-3
	toneWant := []float64{0.002657, 0.001875, 0.001425, 0.001144, 0.000907, 0.000863, 0.000693, 0.000712}
	silenceWant := []float64{0.001670, 0.006884, 0.008911, 0.007857, 0.005907, 0.005961, 0.005853, 0.005640}
	speechWant := []float64{
		0.423911, 0.939346, 0.951095, 0.988661, 0.977613, 0.985255, 0.999242, 0.999576,
		0.999373, 0.999875, 0.999624, 0.999816, 0.999824, 0.999856, 0.999968, 0.999962,
	}
	cases := []struct {
		name   string
		frames [][]byte
		want   []float64
	}{
		{"tone", toneFrames(8), toneWant},
		{"silence", silenceFrames(8), silenceWant},
		{"speech", speechFrames(t, 16), speechWant},
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

// TestSileroVoiceConfidenceSilence checks that voiceConfidence scores a raw
// 16-bit buffer and that silence is not confidently classified as speech.
func TestSileroVoiceConfidenceSilence(t *testing.T) {
	if !onnxrt.Available() {
		t.Skip("ONNX runtime not configured; set JARGO_ONNXRUNTIME_LIB")
	}
	s, err := NewSilero()
	if err != nil {
		t.Fatalf("NewSilero: %v", err)
	}
	defer func() { _ = s.Close() }()
	if err := s.SetSampleRate(16000); err != nil {
		t.Fatalf("SetSampleRate: %v", err)
	}

	got := s.voiceConfidence(make([]byte, 512*2))
	if got < 0 || got >= 0.5 {
		t.Errorf("silence confidence = %v, want in [0, 0.5)", got)
	}
}

// TestPCMToFloat32 checks that the 16-bit to float conversion feeds the model
// samples normalized by 32768.
func TestPCMToFloat32(t *testing.T) {
	samples := []int16{0, 16384, -32768, 32767}
	pcm := make([]byte, len(samples)*2)
	for i, v := range samples {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}

	got := pcmToFloat32(pcm)
	for i, v := range samples {
		if want := float32(v) / 32768.0; got[i] != want {
			t.Errorf("sample %d = %v, want %v", i, got[i], want)
		}
	}
}
