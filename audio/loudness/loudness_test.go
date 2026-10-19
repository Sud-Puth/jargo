package loudness_test

import (
	"encoding/binary"
	"math"
	"strconv"
	"testing"

	"github.com/gojargo/jargo/audio/loudness"
)

// referenceCase is one signal measured by the reference implementation.
type referenceCase struct {
	kind string
	rate int
	n    int
	amp  int
	p    int
	// lufs is the integrated loudness the reference meter reported.
	lufs float64
	// want is the volume the reference computed from it.
	want float64
}

// referenceCases were produced by the reference loudness meter (libloudness,
// in its integrated, histogram mode) through the same call the reference volume
// makes: samples scaled to [-1, 1) as float32, and the loudness normalized from
// the -110..-10 LUFS range.
//
// They are what makes this a port rather than an interpretation. A filter
// coefficient or a gating bound that is subtly wrong still produces plausible
// numbers, and only a comparison against the reference catches it. The window
// lengths are the ones the Tracker measures (400 ms rounded up to a whole
// sample), plus longer ones that exercise overlapping blocks and the relative
// gate. At 11025 Hz a 400 ms window falls two samples short of four rounded
// 100 ms sub-blocks, so the reference finds no gating block in it and reads 0.
//
//nolint:gochecknoglobals // reference vectors
var referenceCases = []referenceCase{
	{kind: "silence", rate: 8000, n: 3200, amp: 0, p: 0, lufs: math.Inf(-1), want: 0.0},
	{kind: "noise", rate: 8000, n: 3200, amp: 30, p: 1, lufs: -63.15000000000002, want: 0.4684999999999998},
	{kind: "noise", rate: 8000, n: 3200, amp: 300, p: 1, lufs: -43.15000000000002, want: 0.6684999999999998},
	{kind: "noise", rate: 8000, n: 3200, amp: 3000, p: 1, lufs: -23.35, want: 0.8665},
	{kind: "noise", rate: 8000, n: 3200, amp: 20000, p: 1, lufs: -8.649999999999995, want: 1.0},
	{kind: "saw", rate: 8000, n: 3200, amp: 1000, p: 37, lufs: -35.550000000000004, want: 0.7444999999999999},
	{kind: "silence", rate: 11025, n: 4410, amp: 0, p: 0, lufs: math.Inf(-1), want: 0.0},
	{kind: "noise", rate: 11025, n: 4410, amp: 30, p: 1, lufs: math.Inf(-1), want: 0.0},
	{kind: "noise", rate: 11025, n: 4410, amp: 300, p: 1, lufs: math.Inf(-1), want: 0.0},
	{kind: "noise", rate: 11025, n: 4410, amp: 3000, p: 1, lufs: math.Inf(-1), want: 0.0},
	{kind: "noise", rate: 11025, n: 4410, amp: 20000, p: 1, lufs: math.Inf(-1), want: 0.0},
	{kind: "saw", rate: 11025, n: 4410, amp: 1000, p: 37, lufs: math.Inf(-1), want: 0.0},
	{kind: "silence", rate: 16000, n: 6400, amp: 0, p: 0, lufs: math.Inf(-1), want: 0.0},
	{kind: "noise", rate: 16000, n: 6400, amp: 30, p: 1, lufs: -62.65000000000001, want: 0.47349999999999987},
	{kind: "noise", rate: 16000, n: 6400, amp: 300, p: 1, lufs: -42.65000000000001, want: 0.6735},
	{kind: "noise", rate: 16000, n: 6400, amp: 3000, p: 1, lufs: -22.649999999999995, want: 0.8735},
	{kind: "noise", rate: 16000, n: 6400, amp: 20000, p: 1, lufs: -7.950000000000002, want: 1.0},
	{kind: "saw", rate: 16000, n: 6400, amp: 1000, p: 37, lufs: -35.05000000000001, want: 0.7494999999999998},
	{kind: "silence", rate: 22050, n: 8820, amp: 0, p: 0, lufs: math.Inf(-1), want: 0.0},
	{kind: "noise", rate: 22050, n: 8820, amp: 30, p: 1, lufs: -62.45000000000002, want: 0.4754999999999998},
	{kind: "noise", rate: 22050, n: 8820, amp: 300, p: 1, lufs: -42.550000000000004, want: 0.6744999999999999},
	{kind: "noise", rate: 22050, n: 8820, amp: 3000, p: 1, lufs: -22.549999999999986, want: 0.8745000000000002},
	{kind: "noise", rate: 22050, n: 8820, amp: 20000, p: 1, lufs: -7.849999999999997, want: 1.0},
	{kind: "saw", rate: 22050, n: 8820, amp: 1000, p: 37, lufs: -34.75000000000001, want: 0.7525},
	{kind: "silence", rate: 24000, n: 9600, amp: 0, p: 0, lufs: math.Inf(-1), want: 0.0},
	{kind: "noise", rate: 24000, n: 9600, amp: 30, p: 1, lufs: -62.45000000000002, want: 0.4754999999999998},
	{kind: "noise", rate: 24000, n: 9600, amp: 300, p: 1, lufs: -42.45, want: 0.6755},
	{kind: "noise", rate: 24000, n: 9600, amp: 3000, p: 1, lufs: -22.45, want: 0.8755},
	{kind: "noise", rate: 24000, n: 9600, amp: 20000, p: 1, lufs: -7.849999999999997, want: 1.0},
	{kind: "saw", rate: 24000, n: 9600, amp: 1000, p: 37, lufs: -34.75000000000001, want: 0.7525},
	{kind: "silence", rate: 44100, n: 17640, amp: 0, p: 0, lufs: math.Inf(-1), want: 0.0},
	{kind: "noise", rate: 44100, n: 17640, amp: 30, p: 1, lufs: -62.25, want: 0.4775},
	{kind: "noise", rate: 44100, n: 17640, amp: 300, p: 1, lufs: -42.35, want: 0.6765000000000001},
	{kind: "noise", rate: 44100, n: 17640, amp: 3000, p: 1, lufs: -22.349999999999998, want: 0.8765000000000001},
	{kind: "noise", rate: 44100, n: 17640, amp: 20000, p: 1, lufs: -7.649999999999999, want: 1.0},
	{kind: "saw", rate: 44100, n: 17640, amp: 1000, p: 37, lufs: -33.95, want: 0.7605},
	{kind: "silence", rate: 48000, n: 19200, amp: 0, p: 0, lufs: math.Inf(-1), want: 0.0},
	{kind: "noise", rate: 48000, n: 19200, amp: 30, p: 1, lufs: -62.25, want: 0.4775},
	{kind: "noise", rate: 48000, n: 19200, amp: 300, p: 1, lufs: -42.35, want: 0.6765000000000001},
	{kind: "noise", rate: 48000, n: 19200, amp: 3000, p: 1, lufs: -22.349999999999998, want: 0.8765000000000001},
	{kind: "noise", rate: 48000, n: 19200, amp: 20000, p: 1, lufs: -7.649999999999999, want: 1.0},
	{kind: "saw", rate: 48000, n: 19200, amp: 1000, p: 37, lufs: -33.85000000000001, want: 0.7615},
	{kind: "noise", rate: 16000, n: 16000, amp: 3000, p: 7, lufs: -22.79257550864185, want: 0.8720742449135815},
	{kind: "quietloud", rate: 16000, n: 32000, amp: 3000, p: 7, lufs: -23.44399470054838, want: 0.8655600529945161},
	{kind: "noise", rate: 48000, n: 48000, amp: 3000, p: 7, lufs: -22.37833724608109, want: 0.8762166275391892},
	{kind: "quietloud", rate: 48000, n: 96000, amp: 3000, p: 7, lufs: -23.132411468045024, want: 0.8686758853195498},
}

// lcgNoise builds n samples of pseudo-random noise within ±amp, from integer
// arithmetic alone so the reference vectors were built from the same samples.
func lcgNoise(n, amp, seed int) []int16 {
	x := seed
	out := make([]int16, n)
	for i := range out {
		x = (x*1103515245 + 12345) % (1 << 31)
		out[i] = int16((x>>16)%(2*amp+1) - amp)
	}
	return out
}

// saw builds n samples of a sawtooth within ±amp, rising by step a sample.
func saw(n, amp, step int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16((i*step)%(2*amp) - amp)
	}
	return out
}

// referenceSignal builds the PCM a reference case was measured on.
func referenceSignal(c referenceCase) []byte {
	var s []int16
	switch c.kind {
	case "silence":
		s = make([]int16, c.n)
	case "noise":
		s = lcgNoise(c.n, c.amp, c.p)
	case "saw":
		s = saw(c.n, c.amp, c.p)
	case "quietloud":
		s = append(lcgNoise(c.n/2, 10, c.p), lcgNoise(c.n-c.n/2, c.amp, c.p+1)...)
	}
	return pcmOf(s)
}

func pcmOf(samples []int16) []byte {
	pcm := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(s))
	}
	return pcm
}

func caseName(c referenceCase) string {
	return c.kind + "/" + strconv.Itoa(c.rate) + "Hz/" + strconv.Itoa(c.n) + "x" + strconv.Itoa(c.amp)
}

// TestVolumeMatchesReference checks the volume against the reference.
func TestVolumeMatchesReference(t *testing.T) {
	for _, c := range referenceCases {
		t.Run(caseName(c), func(t *testing.T) {
			got := loudness.Volume(referenceSignal(c), c.rate)
			if math.Abs(got-c.want) > 1e-12 {
				t.Errorf("Volume() = %.15f, want %.15f (reference)", got, c.want)
			}
		})
	}
}

// TestMeterMatchesReference checks the integrated loudness itself, before it is
// normalized and clamped into a volume.
func TestMeterMatchesReference(t *testing.T) {
	for _, c := range referenceCases {
		t.Run(caseName(c), func(t *testing.T) {
			m := loudness.NewMeter(c.rate)
			m.AddPCM(referenceSignal(c))
			got := m.LoudnessGlobal()
			if math.IsInf(c.lufs, -1) {
				if !math.IsInf(got, -1) {
					t.Errorf("LoudnessGlobal() = %v, want -Inf (reference)", got)
				}
				return
			}
			if math.Abs(got-c.lufs) > 1e-10 {
				t.Errorf("LoudnessGlobal() = %.15f, want %.15f (reference)", got, c.lufs)
			}
		})
	}
}

// Feeding a meter in chunks measures the same as feeding it all at once: the
// gating blocks run across the chunk boundaries.
func TestMeterChunkedMatchesWhole(t *testing.T) {
	const rate = 16000
	pcm := pcmOf(append(lcgNoise(rate/2, 10, 7), lcgNoise(rate*3/2, 3000, 8)...))

	whole := loudness.NewMeter(rate)
	whole.AddPCM(pcm)

	chunked := loudness.NewMeter(rate)
	for i := 0; i < len(pcm); i += 640 {
		chunked.AddPCM(pcm[i:min(i+640, len(pcm))])
	}

	if a, b := whole.LoudnessGlobal(), chunked.LoudnessGlobal(); a != b {
		t.Errorf("chunked loudness %v, want the whole buffer's %v", b, a)
	}
}

// Audio shorter than a gating block is not a measurement, and reads 0.
func TestVolumeOfLessThanAGatingBlockIsZero(t *testing.T) {
	const rate = 16000
	if got := loudness.Volume(pcmOf(lcgNoise(rate*4/10-1, 20000, 1)), rate); got != 0 {
		t.Errorf("Volume() of 400 ms less a sample = %v, want 0", got)
	}
}

// A sample rate the meter cannot measure at reads 0 rather than a number.
func TestVolumeAtAnUnsupportedRateIsZero(t *testing.T) {
	for _, rate := range []int{0, 15, 2822401} {
		if got := loudness.Volume(pcmOf(lcgNoise(8000, 20000, 1)), rate); got != 0 {
			t.Errorf("Volume() at %d Hz = %v, want 0", rate, got)
		}
		if m := loudness.NewMeter(rate); m != nil {
			t.Errorf("NewMeter(%d) = %v, want nil", rate, m)
		}
	}
}

// sine builds 400 ms of a tone at amp, as mono 16-bit PCM.
func sine(rate, freq, amp int) []byte {
	n := rate * 4 / 10
	samples := make([]int16, n)
	for i := range samples {
		samples[i] = int16(float64(amp) * math.Sin(2*math.Pi*float64(freq)*float64(i)/float64(rate)))
	}
	return pcmOf(samples)
}

// TestVolumeIsWeightedNotRaw checks the measurement is weighted for how the ear
// hears rather than being raw amplitude: the same amplitude down at a rumble
// reads quieter than up where speech sits.
func TestVolumeIsWeightedNotRaw(t *testing.T) {
	const rate, amp = 16000, 3000
	low := loudness.Volume(sine(rate, 30, amp), rate)
	speech := loudness.Volume(sine(rate, 440, amp), rate)
	high := loudness.Volume(sine(rate, 3000, amp), rate)

	if !(low < speech && speech < high) {
		t.Errorf("volumes at one amplitude: 30Hz=%.4f 440Hz=%.4f 3kHz=%.4f, want increasing", low, speech, high)
	}
}

func TestSmooth(t *testing.T) {
	// A reading is pulled toward the new value by the factor, not replaced.
	if got := loudness.Smooth(1, 0, 0.2); math.Abs(got-0.2) > 1e-12 {
		t.Errorf("Smooth(1, 0, 0.2) = %v, want 0.2", got)
	}
	if got := loudness.Smooth(0, 1, 0.2); math.Abs(got-0.8) > 1e-12 {
		t.Errorf("Smooth(0, 1, 0.2) = %v, want 0.8", got)
	}
}

func TestNormalizeClamps(t *testing.T) {
	if got := loudness.Normalize(math.Inf(-1), -110, -10); got != 0 {
		t.Errorf("Normalize(-Inf) = %v, want 0", got)
	}
	if got := loudness.Normalize(0, -110, -10); got != 1 {
		t.Errorf("Normalize(0) = %v, want 1", got)
	}
	if got := loudness.Normalize(-60, -110, -10); math.Abs(got-0.5) > 1e-12 {
		t.Errorf("Normalize(-60) = %v, want 0.5", got)
	}
}
