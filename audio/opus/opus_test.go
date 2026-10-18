package opus_test

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/gojargo/jargo/audio/opus"
)

// sineFrame builds one 20 ms mono frame of S16LE PCM at the given frequency.
func sineFrame(freq float64) []byte {
	pcm := make([]byte, opus.FrameBytes(1))
	for i := range opus.FrameSamples {
		v := math.Sin(2*math.Pi*freq*float64(i)/float64(opus.SampleRate)) * 0.3 * math.MaxInt16
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(v)))
	}
	return pcm
}

func TestEncodeProducesPacket(t *testing.T) {
	enc, err := opus.NewEncoder(opus.EncoderConfig{Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := enc.Encode(sineFrame(440))
	if err != nil {
		t.Fatal(err)
	}
	if len(packet) == 0 {
		t.Fatal("encoded packet is empty")
	}
}

func TestEncodeRejectsWrongFrameSize(t *testing.T) {
	enc, err := opus.NewEncoder(opus.EncoderConfig{Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Encode(make([]byte, opus.FrameBytes(1)-2)); err == nil {
		t.Fatal("expected an error for a short frame")
	}
}

func TestRoundTrip(t *testing.T) {
	enc, err := opus.NewEncoder(opus.EncoderConfig{Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	dec, err := opus.NewDecoder(1)
	if err != nil {
		t.Fatal(err)
	}

	packet, err := enc.Encode(sineFrame(440))
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := dec.Decode(packet)
	if err != nil {
		t.Fatal(err)
	}
	// A 20 ms frame decodes to 960 mono samples (1920 bytes).
	if got, want := len(pcm), opus.FrameBytes(1); got != want {
		t.Fatalf("decoded PCM = %d bytes, want %d", got, want)
	}
}

// toneAt builds n samples of mono S16LE PCM at rate carrying a freq Hz tone,
// continuing from phase.
func toneAt(rate, n int, freq float64, phase *float64) []byte {
	pcm := make([]byte, n*2)
	step := 2 * math.Pi * freq / float64(rate)
	for i := range n {
		v := math.Sin(*phase) * 0.3 * math.MaxInt16
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(v)))
		*phase += step
	}
	return pcm
}

// TestEncodeAtAnotherRate covers an encoder fed below the codec's rate. It takes
// 20 ms frames at that rate, and what it encodes decodes to the same tone at
// 48 kHz rather than one shifted by the ratio between the two rates.
func TestEncodeAtAnotherRate(t *testing.T) {
	const rate = 24000
	enc, err := opus.NewEncoder(opus.EncoderConfig{Channels: 1, SampleRate: rate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Encode(make([]byte, opus.FrameBytes(1))); err == nil {
		t.Fatal("a 48 kHz frame was accepted by an encoder fed at 24 kHz")
	}
	dec, err := opus.NewDecoder(1)
	if err != nil {
		t.Fatal(err)
	}

	frame := opus.FrameBytesAt(rate, 1)
	if frame != 960 {
		t.Fatalf("a 20 ms frame at 24 kHz is %d bytes, want 960", frame)
	}
	var phase float64
	var decoded []int16
	for i := range 50 {
		packet, err := enc.Encode(toneAt(rate, frame/2, 440, &phase))
		if err != nil {
			t.Fatal(err)
		}
		pcm, err := dec.Decode(packet)
		if err != nil {
			t.Fatal(err)
		}
		if i < 5 {
			continue // the codec and the resampler warm up
		}
		for j := 0; j+1 < len(pcm); j += 2 {
			decoded = append(decoded, int16(binary.LittleEndian.Uint16(pcm[j:])))
		}
	}

	crossings := 0
	for i := 1; i < len(decoded); i++ {
		if decoded[i-1] < 0 && decoded[i] >= 0 {
			crossings++
		}
	}
	pitch := float64(crossings) * opus.SampleRate / float64(len(decoded))
	if pitch < 400 || pitch > 480 {
		t.Errorf("the tone decodes at %.0f Hz, want about 440 Hz", pitch)
	}
}

func TestNewEncoderRejectsARateWithoutWholeFrames(t *testing.T) {
	if _, err := opus.NewEncoder(opus.EncoderConfig{Channels: 1, SampleRate: 11025}); err == nil {
		t.Fatal("expected an error for a rate that does not divide into 20 ms frames")
	}
}
