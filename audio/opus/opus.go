// Package opus wraps the Opus codec for jargo's audio path: it decodes Opus
// packets to PCM on the way in and encodes PCM to Opus packets on the way out.
//
// The codec runs at 48 kHz, the rate WebRTC negotiates for Opus. Decoding
// produces 48 kHz PCM, while the encoder takes PCM at whatever rate it was
// configured for and converts it itself. Encoding produces 20 ms frames, the
// standard WebRTC packetization.
//
// Both the decoder and the encoder are pure Go, so the package needs no cgo:
// the decoder handles every Opus mode, and the encoder emits wideband SILK
// (see encoder.go), which is the mode tuned for speech.
package opus

import (
	"encoding/binary"
	"fmt"
	"time"

	pion "github.com/pion/opus"
)

const (
	// SampleRate is the codec sample rate in Hz. Opus runs at 48 kHz.
	SampleRate = 48000
	// FrameDuration is the duration of one encoded packet.
	FrameDuration = 20 * time.Millisecond
	// FrameSamples is the number of samples per channel in one 20 ms frame.
	FrameSamples = SampleRate / 1000 * 20 // 960

	// maxFrameSamples is the largest decoded frame (120 ms) per channel.
	maxFrameSamples = SampleRate / 1000 * 120 // 5760
	// maxPacketBytes bounds an encoded Opus packet.
	maxPacketBytes = 1500
)

// FrameBytes is the size in bytes of one 20 ms S16LE frame for channels.
func FrameBytes(channels int) int { return FrameSamples * channels * 2 }

// FrameBytesAt is the size in bytes of one 20 ms S16LE frame for channels at
// sampleRate, which is what Encode takes from an Encoder fed at that rate.
func FrameBytesAt(sampleRate, channels int) int {
	return sampleRate / framesPerSecond * channels * 2
}

// framesPerSecond is how many 20 ms frames make up one second.
const framesPerSecond = int(time.Second / FrameDuration)

// EncoderConfig configures an Encoder.
type EncoderConfig struct {
	// Channels is the number of channels in the PCM handed to Encode.
	Channels int
	// SampleRate is the rate in Hz of the PCM handed to Encode; 0 means
	// SampleRate (48 kHz). The encoder converts it to the rate the codec runs
	// at, so a stream produced at its own rate is encoded without first being
	// brought to 48 kHz. It must divide into whole 20 ms frames.
	SampleRate int
	// Bitrate is the target bitrate in bits per second; 0 uses the codec
	// default. Speech is transparent well below the codec's fullband range, so
	// raising this past ~32 kbps for a single voice mostly costs bandwidth.
	Bitrate int
}

// Decoder decodes Opus packets into signed 16-bit little-endian PCM.
type Decoder struct {
	dec      pion.Decoder
	channels int
	buf      []int16
}

// NewDecoder builds a Decoder that outputs channels-channel 48 kHz PCM. It
// decodes all Opus modes (SILK, CELT and hybrid).
func NewDecoder(channels int) (*Decoder, error) {
	d, err := pion.NewDecoderWithOutput(SampleRate, channels)
	if err != nil {
		return nil, fmt.Errorf("new opus decoder: %w", err)
	}
	return &Decoder{
		dec:      d,
		channels: channels,
		buf:      make([]int16, maxFrameSamples*channels),
	}, nil
}

// Decode decodes one Opus packet into interleaved S16LE PCM bytes.
func (d *Decoder) Decode(packet []byte) ([]byte, error) {
	n, err := d.dec.DecodeToInt16(packet, d.buf)
	if err != nil {
		return nil, fmt.Errorf("opus decode: %w", err)
	}
	total := n * d.channels
	out := make([]byte, total*2)
	for i := range total {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(d.buf[i]))
	}
	return out, nil
}
