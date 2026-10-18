package rtc_test

import (
	"context"
	"encoding/binary"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/gojargo/jargo/audio/opus"
	"github.com/gojargo/jargo/frames"
	"github.com/gojargo/jargo/pipeline"
	"github.com/gojargo/jargo/processor"
	"github.com/gojargo/jargo/transport"
	"github.com/gojargo/jargo/transport/rtc"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// The codec runs at 48 kHz whatever the pipeline does, so the transport converts
// on the way in, to the rate the pipeline reads at, and on the way out, from the
// rate the pipeline writes at. The test below runs the pipeline at 16 kHz in and
// 24 kHz out, the rates of a typical speech recognizer and TTS service.

const (
	inRate  = 16000
	outRate = 24000
	toneHz  = 440
)

// rateRecorder records the shape of the audio frames reaching it and echoes them
// back out at the rate they arrived at.
type rateRecorder struct {
	*processor.Base

	mu      sync.Mutex
	rates   map[int]int
	samples int
}

func newRateRecorder() *rateRecorder {
	r := &rateRecorder{rates: map[int]int{}}
	r.Base = processor.New("RateRecorder", r)
	return r
}

func (r *rateRecorder) ProcessFrame(ctx context.Context, f frames.Frame, dir processor.Direction) error {
	if err := r.Base.ProcessFrame(ctx, f, dir); err != nil {
		return err
	}
	if in, ok := f.(*frames.InputAudioRawFrame); ok {
		r.mu.Lock()
		r.rates[in.SampleRate]++
		r.samples += len(in.Audio) / 2
		r.mu.Unlock()
		out := frames.NewOutputAudioRawFrame(in.Audio, in.SampleRate, in.NumChannels)
		return r.PushFrame(ctx, out, processor.Downstream)
	}
	return r.PushFrame(ctx, f, dir)
}

func (r *rateRecorder) snapshot() (map[int]int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.rates), r.samples
}

// listener decodes what the client hears, keeping only the audible part of it:
// the sender fills the gaps with silence, which says nothing about the pitch.
type listener struct {
	mu     sync.Mutex
	heard  []int16
	want   int
	enough chan struct{}
	once   sync.Once
}

func newListener(want int) *listener {
	return &listener{want: want, enough: make(chan struct{})}
}

func (l *listener) listen(t *testing.T, tr *webrtc.TrackRemote) {
	t.Helper()
	dec, err := opus.NewDecoder(1)
	if err != nil {
		t.Error(err)
		return
	}
	for {
		pkt, _, err := tr.ReadRTP()
		if err != nil {
			return
		}
		if len(pkt.Payload) == 0 {
			continue
		}
		pcm, err := dec.Decode(pkt.Payload)
		if err != nil || peak(pcm) < 3000 {
			continue
		}
		l.mu.Lock()
		for i := 0; i+1 < len(pcm); i += 2 {
			l.heard = append(l.heard, int16(binary.LittleEndian.Uint16(pcm[i:])))
		}
		if len(l.heard) >= l.want {
			l.once.Do(func() { close(l.enough) })
		}
		l.mu.Unlock()
	}
}

func (l *listener) pitch() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return dominantPitch(l.heard, opus.SampleRate)
}

// connectClient negotiates a plain Pion client peer with server and returns the
// track it sends Opus on. Whatever the server sends back goes to onTrack.
func connectClient(
	t *testing.T, server *rtc.Connection, onTrack func(*webrtc.TrackRemote),
) *webrtc.TrackLocalStaticSample {
	t.Helper()
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		t.Fatal(err)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m))
	client, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	clientTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		"audio", "client",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AddTrack(clientTrack); err != nil {
		t.Fatal(err)
	}
	client.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) { go onTrack(tr) })

	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gatherDone := webrtc.GatheringCompletePromise(client)
	if err := client.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gatherDone
	answer, err := server.Answer(*client.LocalDescription())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetRemoteDescription(answer); err != nil {
		t.Fatal(err)
	}
	return clientTrack
}

// sendTone streams a 440 Hz tone on track, a 20 ms Opus packet at a time, until
// stop is closed. The returned channel closes once it has stopped.
func sendTone(t *testing.T, track *webrtc.TrackLocalStaticSample, stop <-chan struct{}) <-chan struct{} {
	t.Helper()
	enc, err := opus.NewEncoder(opus.EncoderConfig{Channels: 1})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(opus.FrameDuration)
		defer ticker.Stop()
		phase := 0.0
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				packet, err := enc.Encode(sine(&phase))
				if err != nil {
					return
				}
				_ = track.WriteSample(media.Sample{Data: packet, Duration: opus.FrameDuration})
			}
		}
	}()
	return done
}

// TestWebRTCConvertsToThePipelineRates sends a tone into a pipeline running at
// 16 kHz in and 24 kHz out, which echoes it back. The pipeline has to receive
// the tone at 16 kHz, labeled as such, and the client has to hear the echo at
// the pitch it sent. A transport that ignored the rates would label 48 kHz audio
// as the pipeline's, and send 24 kHz audio as though it were 48 kHz, which the
// client hears an octave up.
func TestWebRTCConvertsToThePipelineRates(t *testing.T) {
	server, err := rtc.NewConnection(rtc.WithICEServers())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	ear := newListener(opus.SampleRate) // one second of echoed tone
	clientTrack := connectClient(t, server, func(tr *webrtc.TrackRemote) { ear.listen(t, tr) })

	params := transport.DefaultParams()
	params.AudioInSampleRate = inRate
	params.AudioOutSampleRate = outRate
	tr := rtc.NewTransport(server, params)
	rec := newRateRecorder()
	task := pipeline.NewWorker(pipeline.New(tr.Input(), rec, tr.Output()), pipeline.WorkerConfig{
		Params: pipeline.Params{AudioInSampleRate: inRate, AudioOutSampleRate: outRate},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	taskDone := make(chan struct{})
	go func() { _ = task.Run(ctx); close(taskDone) }()

	stop := make(chan struct{})
	sent := sendTone(t, clientTrack, stop)
	select {
	case <-ear.enough:
	case <-time.After(20 * time.Second):
		t.Fatal("did not hear a second of echoed audio within 20s")
	}
	close(stop)
	<-sent

	rates, samples := rec.snapshot()
	if len(rates) != 1 || rates[inRate] == 0 {
		t.Errorf("the pipeline received audio labeled %v, want only %d Hz", rates, inRate)
	}
	// Each 20 ms frame decodes to 960 samples at 48 kHz, and has to reach the
	// pipeline as about 320 at 16 kHz.
	if frames := rates[inRate]; frames > 0 {
		if per := samples / frames; per < 300 || per > 340 {
			t.Errorf("the pipeline received %d samples per frame, want about %d at %d Hz",
				per, inRate/50, inRate)
		}
	}

	if pitch := ear.pitch(); pitch < toneHz*0.85 || pitch > toneHz*1.15 {
		t.Errorf("the echo plays at %.0f Hz, want about %d Hz: the rates were not converted", pitch, toneHz)
	}

	cancel()
	select {
	case <-taskDone:
	case <-time.After(3 * time.Second):
		t.Fatal("task did not shut down")
	}
}

// peak is the largest absolute sample in S16LE PCM.
func peak(pcm []byte) int {
	most := 0
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int(int16(binary.LittleEndian.Uint16(pcm[i:])))
		if v < 0 {
			v = -v
		}
		most = max(most, v)
	}
	return most
}

// dominantPitch estimates the frequency of a tone from its rising zero
// crossings.
func dominantPitch(samples []int16, rate int) float64 {
	if len(samples) < 2 {
		return 0
	}
	crossings := 0
	for i := 1; i < len(samples); i++ {
		if samples[i-1] < 0 && samples[i] >= 0 {
			crossings++
		}
	}
	return float64(crossings) * float64(rate) / float64(len(samples))
}
