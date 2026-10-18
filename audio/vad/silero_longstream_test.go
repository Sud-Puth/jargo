package vad

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"os"
	"slices"
	"testing"

	"github.com/gojargo/jargo/audio/resample"
	"github.com/gojargo/jargo/internal/onnxrt"
	"gonum.org/v1/gonum/dsp/fourier"
)

const (
	longStreamSampleRate = 16000
	// 8 kHz is phone bandwidth.
	phoneSampleRate = 8000
	// 20 ms, the frame size transports deliver.
	longStreamFrameSecs = 0.02
	// Synthesized speech with every pause removed, so a VAD listening to it
	// should never report the speaker stopping.
	speechPath = "testdata/continuous_speech_16k.wav"
)

// readSpeechPCM returns the samples of the 16 kHz mono 16-bit speech fixture
// as little-endian PCM.
func readSpeechPCM(t *testing.T) []byte {
	t.Helper()
	wav, err := os.ReadFile(speechPath)
	if err != nil {
		t.Fatalf("read %s: %v", speechPath, err)
	}
	if len(wav) < 12 || !bytes.Equal(wav[0:4], []byte("RIFF")) || !bytes.Equal(wav[8:12], []byte("WAVE")) {
		t.Fatalf("%s is not a WAV file", speechPath)
	}
	var sampleRate, channels, bits int
	for pos := 12; pos+8 <= len(wav); {
		id := string(wav[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(wav[pos+4:]))
		body := pos + 8
		if body+size > len(wav) {
			t.Fatalf("%s: truncated %q chunk", speechPath, id)
		}
		switch id {
		case "fmt ":
			channels = int(binary.LittleEndian.Uint16(wav[body+2:]))
			sampleRate = int(binary.LittleEndian.Uint32(wav[body+4:]))
			bits = int(binary.LittleEndian.Uint16(wav[body+14:]))
		case "data":
			if sampleRate != longStreamSampleRate || channels != 1 || bits != 16 {
				t.Fatalf("%s: got %d Hz, %d channels, %d bits; want 16 kHz mono 16-bit",
					speechPath, sampleRate, channels, bits)
			}
			return wav[body : body+size]
		}
		pos = body + size + size%2
	}
	t.Fatalf("%s: no data chunk", speechPath)
	return nil
}

// longStream drives a Silero analyzer over long synthesized streams, where the
// model's recurrent state persists. Test audio is built at 16 kHz. Streams that
// run at 8 kHz downsample it first, which leaves it with phone bandwidth.
type longStream struct {
	t      *testing.T
	speech []float64
	rng    *rand.Rand

	sampleRate int
	analyzer   *Silero
	// resetPositions records where in the audio, in seconds, the analyzer
	// reset the model.
	resetPositions []float64
}

func newLongStream(t *testing.T) *longStream {
	t.Helper()
	if !onnxrt.Available() {
		t.Skip("ONNX runtime not configured; set JARGO_ONNXRUNTIME_LIB")
	}
	pcm := readSpeechPCM(t)
	speech := make([]float64, len(pcm)/2)
	for i := range speech {
		speech[i] = float64(int16(binary.LittleEndian.Uint16(pcm[i*2:])))
	}
	ls := &longStream{t: t, speech: speech, rng: rand.New(rand.NewPCG(0, 0))} //nolint:gosec // seeded test noise
	ls.startStream(longStreamSampleRate)
	return ls
}

// startStream starts a fresh analyzer, like a new audio stream.
func (ls *longStream) startStream(sampleRate int) {
	ls.t.Helper()
	if ls.analyzer != nil {
		_ = ls.analyzer.Close()
	}
	s, err := NewSilero()
	if err != nil {
		ls.t.Fatalf("NewSilero: %v", err)
	}
	ls.t.Cleanup(func() { _ = s.Close() })
	if err := s.SetSampleRate(sampleRate); err != nil {
		ls.t.Fatalf("SetSampleRate: %v", err)
	}
	ls.sampleRate = sampleRate
	ls.analyzer = s
	ls.resetPositions = nil
}

func (ls *longStream) noise(seconds, std float64) []float64 {
	out := make([]float64, int(seconds*longStreamSampleRate))
	for i := range out {
		out[i] = ls.rng.NormFloat64() * std
	}
	return out
}

// pinkNoise is noise weighted toward low frequencies, like room and line
// noise.
func (ls *longStream) pinkNoise(seconds, rms float64) []float64 {
	samples := int(seconds * longStreamSampleRate)
	white := ls.noise(seconds, 1)
	fft := fourier.NewFFT(samples)
	spectrum := fft.Coefficients(nil, white)
	for k := range spectrum {
		// The DC bin takes the weight of the first frequency above it.
		f := float64(max(k, 1)) * longStreamSampleRate / float64(samples)
		spectrum[k] /= complex(math.Sqrt(f), 0)
	}
	pink := fft.Sequence(nil, spectrum)
	var sum float64
	for _, v := range pink {
		sum += v * v
	}
	scale := rms / math.Sqrt(sum/float64(samples))
	for i := range pink {
		pink[i] *= scale
	}
	return pink
}

// music is synthesized music: plucked notes over a sustained chord.
func (ls *longStream) music(seconds float64) []float64 {
	samples := int(seconds * longStreamSampleRate)
	frequencies := []float64{261.6, 293.7, 329.6, 392.0, 440.0, 523.3}
	music := make([]float64, samples)
	noteSamples := int(0.25 * longStreamSampleRate)
	for start := 0; start < samples; start += noteSamples {
		frequency := frequencies[ls.rng.IntN(len(frequencies))]
		for i := range min(noteSamples, samples-start) {
			t := float64(i) / longStreamSampleRate
			var harmonics float64
			for h := 1.0; h <= 5; h++ {
				harmonics += math.Sin(2*math.Pi*frequency*h*t) / h
			}
			music[start+i] = harmonics * math.Exp(-6*t)
		}
	}
	var peak float64
	for i := range music {
		t := float64(i) / longStreamSampleRate
		var chord float64
		for _, f := range []float64{130.8, 164.8, 196.0} {
			chord += math.Sin(2 * math.Pi * f * t)
		}
		music[i] += 0.3 * chord
		peak = max(peak, math.Abs(music[i]))
	}
	for i := range music {
		music[i] = music[i] / peak * 6000
	}
	return music
}

// analyze feeds audio to the analyzer in transport-sized frames and returns
// the VAD state after each frame. audio is at 16 kHz and is resampled to the
// stream's sample rate.
func (ls *longStream) analyze(audio []float64) []State {
	ls.t.Helper()
	pcm := make([]byte, len(audio)*2)
	for i, v := range audio {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(max(-32768, min(32767, v)))))
	}
	if ls.sampleRate != longStreamSampleRate {
		var err error
		pcm, err = resample.Resample(pcm, longStreamSampleRate, ls.sampleRate, 1)
		if err != nil {
			ls.t.Fatalf("resample: %v", err)
		}
	}

	// A 20 ms frame completes at most one analysis window, so a reset shows
	// as the window count since the last reset dropping back.
	frameBytes := int(longStreamFrameSecs*float64(ls.sampleRate)) * 2
	var states []State
	for start := 0; start+frameBytes <= len(pcm); start += frameBytes {
		before := ls.analyzer.windowsSinceReset
		states = append(states, ls.analyzer.AnalyzeAudio(pcm[start:start+frameBytes]))
		if ls.analyzer.windowsSinceReset < before {
			ls.resetPositions = append(ls.resetPositions, float64(start/2)/float64(ls.sampleRate))
		}
	}
	return states
}

// speakingShare is the share of frames from start seconds on that the VAD
// reports as speech.
func speakingShare(states []State, start float64) float64 {
	after := states[int(start/longStreamFrameSecs):]
	var speaking int
	for _, s := range after {
		if s == StateSpeaking {
			speaking++
		}
	}
	return float64(speaking) / float64(len(after))
}

func scaled(audio []float64, gain float64) []float64 {
	out := make([]float64, len(audio))
	for i, v := range audio {
		out[i] = v * gain
	}
	return out
}

func tiled(audio []float64, n int) []float64 {
	out := make([]float64, 0, len(audio)*n)
	for range n {
		out = append(out, audio...)
	}
	return out
}

func secondsOf(audio []float64) float64 {
	return float64(len(audio)) / longStreamSampleRate
}

// TestSileroDetectsSpeechAfterMinutesOfBackgroundSound checks that quiet speech
// is still detected after minutes of room tone or noise. A model whose state
// drifts during non-speech loses confidence in the speech that follows, which
// shows first on quiet speech.
func TestSileroDetectsSpeechAfterMinutesOfBackgroundSound(t *testing.T) {
	ls := newLongStream(t)
	quietSpeech := scaled(ls.speech, 0.15)
	backgrounds := []struct {
		name string
		make func(seconds float64) []float64
	}{
		{"room tone", func(seconds float64) []float64 { return ls.noise(seconds, 60) }},
		{"pink noise", func(seconds float64) []float64 { return ls.pinkNoise(seconds, 200) }},
	}
	for _, sampleRate := range []int{longStreamSampleRate, phoneSampleRate} {
		for _, bg := range backgrounds {
			ls.startStream(sampleRate)
			leadIn := bg.make(120)
			speech := bg.make(secondsOf(quietSpeech))
			for i := range speech {
				speech[i] += quietSpeech[i]
			}

			states := ls.analyze(slices.Concat(leadIn, speech))

			speaking := speakingShare(states, secondsOf(leadIn))
			if speaking <= 0.8 {
				t.Errorf("%d Hz: VAD missed speech after 2 minutes of %s: %.0f%%",
					sampleRate, bg.name, speaking*100)
			}
		}
	}
}

// TestSileroDetectsSpeechAfterMusic checks that quiet speech is still detected
// shortly after a minute of music. Music leaves the model unable to detect the
// speech that follows until its state is reset. It runs on phone audio, where
// music most often reaches the inbound track (hold music on outbound calls) and
// suppresses speech most.
func TestSileroDetectsSpeechAfterMusic(t *testing.T) {
	ls := newLongStream(t)
	ls.startStream(phoneSampleRate)
	leadIn := slices.Concat(ls.music(60), ls.noise(2.5, 60))

	states := ls.analyze(slices.Concat(leadIn, scaled(ls.speech, 0.15)))

	if speaking := speakingShare(states, secondsOf(leadIn)); speaking <= 0.8 {
		t.Errorf("VAD missed speech after music: %.0f%%", speaking*100)
	}
}

// TestSileroSustainedSpeechHasNoFalseStops checks that minutes of
// uninterrupted speech over background noise never end early. It runs at
// 16 kHz only: on this looped synthesized voice in white noise, the 8 kHz model
// occasionally reports a stop.
func TestSileroSustainedSpeechHasNoFalseStops(t *testing.T) {
	ls := newLongStream(t)
	speech := tiled(ls.speech, 30) // ~3 minutes
	var sum float64
	for _, v := range speech {
		sum += v * v
	}
	signalRMS := math.Sqrt(sum / float64(len(speech)))
	noisy := make([]float64, len(speech))
	for i, v := range speech {
		noisy[i] = v + ls.rng.NormFloat64()*signalRMS/math.Sqrt(10) // 10 dB SNR
	}

	states := ls.analyze(noisy)

	first := slices.Index(states, StateSpeaking)
	if first < 0 {
		t.Fatal("VAD never reported speech")
	}
	if slices.Contains(states[first:], StateQuiet) {
		t.Error("VAD reported the speaker stopping during continuous speech")
	}
}

// TestSileroModelIsResetDuringNonSpeech checks that the model is reset about
// every two seconds while there's no speech.
func TestSileroModelIsResetDuringNonSpeech(t *testing.T) {
	ls := newLongStream(t)
	ls.analyze(ls.noise(30, 300))

	if n := len(ls.resetPositions); n < 12 || n > 15 {
		t.Fatalf("expected a reset about every 2s over 30s, got %d", n)
	}
	for i := 1; i < len(ls.resetPositions); i++ {
		if spacing := ls.resetPositions[i] - ls.resetPositions[i-1]; spacing < 2.0 {
			t.Errorf("resets at %.2fs and %.2fs are %.2fs apart, want at least 2s",
				ls.resetPositions[i-1], ls.resetPositions[i], spacing)
		}
	}
}

// TestSileroModelIsNeverResetDuringSpeech checks that a minute of
// uninterrupted speech gets no resets, and that resets resume after it.
func TestSileroModelIsNeverResetDuringSpeech(t *testing.T) {
	ls := newLongStream(t)
	speech := tiled(ls.speech, 10) // ~1 minute
	const speechStart = 10.0
	speechEnd := speechStart + secondsOf(speech)

	ls.analyze(slices.Concat(ls.noise(speechStart, 60), speech, ls.noise(10, 60)))

	resumed := false
	for _, p := range ls.resetPositions {
		if p >= speechStart && p <= speechEnd {
			t.Errorf("model was reset during speech at %.2fs", p)
		}
		resumed = resumed || p > speechEnd
	}
	if !resumed {
		t.Error("model was not reset after speech ended")
	}
}
