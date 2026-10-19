// Package loudness measures how loud audio is, to ITU-R BS.1770 (the EBU R128
// standard), and normalizes the result to a 0..1 scale.
//
// It is what a voice detector gates on when it is asked for a minimum volume:
// loudness weighted for how the ear actually hears, rather than raw amplitude,
// so a low rumble and a spoken word of the same amplitude are not treated alike.
//
// It is its own package, rather than sitting with the other audio helpers, so
// that audio/vad can use it. The audio package refers to frames, and frames
// refers to audio/vad, so anything audio/vad depends on has to stay clear of
// both.
package loudness

import (
	"encoding/binary"
	"math"
)

// Gating thresholds from the standard: a gating block is ignored below the
// absolute threshold, and below the relative one, which sits this far under
// the mean of the blocks that passed the absolute one.
const (
	relativeGateLU   = -10.0
	absoluteGateLUFS = -70.0
)

// Block geometry. Audio is summed in 100 ms sub-blocks, and a gating block is
// the last four of them, so consecutive blocks overlap by three quarters.
const (
	subblocksInS     = 10
	momentarySubs    = 4
	shorttermSubs    = 30
	momentaryBlockMs = momentarySubs * 1000 / subblocksInS
)

// minDurationSecs is the shortest audio Volume measures, a gating block.
const minDurationSecs = 0.4

// The sample rates a Meter can measure at.
const (
	minSampleRate = 16
	maxSampleRate = 2822400
)

// The integrated measurement counts gating blocks into a histogram of 0.1 LU
// bins starting at the absolute gate, and averages the bins' center energies
// rather than the blocks' own. It is what keeps the measurement in constant
// memory however long the audio runs, and it is why loudness reads in steps
// of a tenth of a LU.
const (
	histogramSize = 1000
	binSizeLU     = 0.1
)

// Volume normalization. Loudness of speech-like audio runs from about -110 LUFS
// (quiet) to -10 LUFS (loud), and volume maps that range onto 0..1.
const (
	// SmoothingFactor weights a new volume reading against the running one.
	SmoothingFactor = 0.2
	loudnessFloor   = -110.0
	loudnessCeiling = -10.0
)

// The conversions to float64 around products in this file are not redundant: they
// round each product on its own, which stops the compiler fusing it with the
// addition that follows into one multiply-add. A fused operation rounds once
// instead of twice, and the measurement is meant to match the reference to the
// last bit wherever it can.

//nolint:gochecknoglobals // constants derived once from the ones above
var (
	absoluteGate       = loudnessToEnergy(absoluteGateLUFS)
	relativeGateFactor = math.Pow(10, relativeGateLU/10)
	histogramEnergies  = func() (e [histogramSize]float64) {
		for i := range e {
			e[i] = loudnessToEnergy(binFloor(i) + binSizeLU/2)
		}
		return e
	}()
	histogramMaxEnergy = loudnessToEnergy(binFloor(histogramSize - 1))
)

// binFloor is the loudness at the bottom of histogram bin i.
func binFloor(i int) float64 { return float64(float64(i)*binSizeLU) + absoluteGateLUFS }

// energyToLoudness converts a mean square to LUFS.
func energyToLoudness(energy float64) float64 { return float64(10*math.Log10(energy)) - 0.691 }

// loudnessToEnergy converts LUFS to a mean square.
func loudnessToEnergy(lufs float64) float64 { return math.Pow(10, (lufs+0.691)/10) }

// reduce sums xs onto init four at a time, each four added pairwise, and the
// remainder one by one. The order is part of the result, since floating-point
// addition is not associative, and it is the order the reference sums in.
func reduce(xs []float64, init float64) float64 {
	for len(xs) >= 4 {
		init += (xs[0] + xs[1]) + (xs[2] + xs[3])
		xs = xs[4:]
	}
	for _, x := range xs {
		init += x
	}
	return init
}

// kFilter is the K-weighting filter: a high shelf lifting the treble for the
// head's acoustic response, cascaded with a high pass discarding the rumble
// below speech, as one fourth-order section.
type kFilter struct {
	b, a [5]float64
	v    [5]float64
}

// newKFilter designs the filter for a sample rate, from the analog prototype
// the standard's 48 kHz coefficients were derived from.
func newKFilter(rate float64) kFilter {
	const (
		f0 = 1681.974450955533
		g  = 3.999843853973347
		q  = 0.7071752369554196
	)
	vh := math.Pow(10, g/20)
	vb := math.Pow(vh, 0.4996667741545416)

	k := math.Tan(math.Pi * f0 / rate)
	kk := float64(k * k)
	a0 := 1 + k/q + kk
	pb := [3]float64{(vh + vb*k/q + kk) / a0, 2 * (kk - vh) / a0, (vh - vb*k/q + kk) / a0}
	pa := [3]float64{1, 2 * (kk - 1) / a0, (1 - k/q + kk) / a0}
	rb := [3]float64{1, -2, 1}

	var f kFilter
	f.b[0] = pb[0] * rb[0]
	f.b[1] = float64(pb[0]*rb[1]) + float64(pb[1]*rb[0])
	f.b[2] = float64(pb[0]*rb[2]) + float64(pb[1]*rb[1]) + float64(pb[2]*rb[0])
	f.b[3] = float64(pb[1]*rb[2]) + float64(pb[2]*rb[1])
	f.b[4] = pb[2] * rb[2]

	const (
		f02 = 38.13547087602444
		q2  = 0.5003270373238773
	)
	k = math.Tan(math.Pi * f02 / rate)
	kk = float64(k * k)
	ra := [3]float64{
		1,
		2 * (kk - 1) / (1 + k/q2 + kk),
		(1 - k/q2 + kk) / (1 + k/q2 + kk),
	}

	f.a[0] = pa[0] * ra[0]
	f.a[1] = float64(pa[0]*ra[1]) + float64(pa[1]*ra[0])
	f.a[2] = float64(pa[0]*ra[2]) + float64(pa[1]*ra[1]) + float64(pa[2]*ra[0])
	f.a[3] = float64(pa[1]*ra[2]) + float64(pa[2]*ra[1])
	f.a[4] = pa[2] * ra[2]
	return f
}

// apply filters one sample.
func (f *kFilter) apply(src float64) float64 {
	f.v[4] = f.v[3]
	f.v[3] = f.v[2]
	f.v[2] = f.v[1]
	f.v[1] = f.v[0]
	f.v[0] = src - float64(f.a[1]*f.v[1]) - float64(f.a[2]*f.v[2]) -
		float64(f.a[3]*f.v[3]) - float64(f.a[4]*f.v[4])
	return float64(f.b[0]*f.v[0]) + float64(f.b[1]*f.v[1]) + float64(f.b[2]*f.v[2]) +
		float64(f.b[3]*f.v[3]) + float64(f.b[4]*f.v[4])
}

// Meter measures the integrated loudness of one channel of audio, at one
// sample rate. Feed it with AddPCM and read it with LoudnessGlobal.
//
// A Meter holds every buffer it needs, so measuring through one allocates
// nothing; Reset readies it for the next measurement. It is not safe for
// concurrent use.
type Meter struct {
	rate int
	// samplesIn100ms is the length of a sub-block, rounded to the nearest
	// sample.
	samplesIn100ms int
	// neededFrames is how much more audio completes the next gating block: four
	// sub-blocks for the first one, and one for each after it.
	neededFrames int
	filter       kFilter

	// audioData is a ring of the squared, filtered samples, one momentary block
	// long rounded up to whole sub-blocks.
	audioData      []float64
	audioDataIndex int
	calcAudioIndex int

	// subblocks is a ring of the most recent sub-block sums, oldest first from
	// subblocksStart.
	subblocks      [shorttermSubs]float64
	subblocksStart int
	subblocksLen   int

	histogram [histogramSize]uint64
}

// NewMeter returns a meter for a sample rate, or nil for a rate it cannot
// measure at (below 16 Hz or above 2822400 Hz).
func NewMeter(sampleRate int) *Meter {
	if sampleRate < minSampleRate || sampleRate > maxSampleRate {
		return nil
	}
	m := &Meter{}
	m.init(sampleRate)
	return m
}

// init sizes the meter for a sample rate and clears it.
func (m *Meter) init(sampleRate int) {
	m.rate = sampleRate
	m.samplesIn100ms = (sampleRate + subblocksInS/2) / subblocksInS
	frames := sampleRate * momentaryBlockMs / 1000
	if rem := frames % m.samplesIn100ms; rem != 0 {
		frames += m.samplesIn100ms - rem
	}
	if cap(m.audioData) < frames {
		m.audioData = make([]float64, frames)
	}
	m.audioData = m.audioData[:frames]
	m.Reset()
}

// Reset clears everything the meter has measured, keeping its sample rate.
func (m *Meter) Reset() {
	m.filter = newKFilter(float64(m.rate))
	m.neededFrames = m.samplesIn100ms * momentarySubs
	m.audioDataIndex = 0
	m.calcAudioIndex = 0
	m.subblocksStart = 0
	m.subblocksLen = 0
	clear(m.histogram[:])
}

// AddPCM measures mono 16-bit little-endian PCM, appending it to what the
// meter has measured so far. Samples are scaled to [-1, 1) first.
func (m *Meter) AddPCM(pcm []byte) {
	frames := len(pcm) / 2
	src := 0
	for frames > 0 {
		if frames >= m.neededFrames {
			m.filterPCM(pcm, src, m.neededFrames)
			src += m.neededFrames
			frames -= m.neededFrames
			// A gating block is complete.
			m.calcSubBlocks()
			m.addIntegrationBlock()
			// Every block after the first needs only one more sub-block.
			m.neededFrames = m.samplesIn100ms
			if m.audioDataIndex == len(m.audioData) {
				m.audioDataIndex = 0
			}
			if m.calcAudioIndex == len(m.audioData) {
				m.calcAudioIndex = 0
			}
		} else {
			m.filterPCM(pcm, src, frames)
			m.neededFrames -= frames
			frames = 0
		}
	}
}

// filterPCM weights frames samples of pcm, from sample offset on, and stores
// their squares in the ring.
func (m *Meter) filterPCM(pcm []byte, offset, frames int) {
	audio := m.audioData[m.audioDataIndex : m.audioDataIndex+frames]
	for i := range audio {
		s := float32(int16(binary.LittleEndian.Uint16(pcm[(offset+i)*2:]))) / 32768
		v := m.filter.apply(float64(s))
		audio[i] = v * v
	}
	m.audioDataIndex += frames
}

// calcSubBlocks sums every sub-block completed since the last call.
func (m *Meter) calcSubBlocks() {
	for ; m.calcAudioIndex+m.samplesIn100ms <= m.audioDataIndex; m.calcAudioIndex += m.samplesIn100ms {
		sum := reduce(m.audioData[m.calcAudioIndex:m.calcAudioIndex+m.samplesIn100ms], 0)
		if m.subblocksLen < shorttermSubs {
			m.subblocks[(m.subblocksStart+m.subblocksLen)%shorttermSubs] = sum
			m.subblocksLen++
		} else {
			m.subblocks[m.subblocksStart] = sum
			m.subblocksStart = (m.subblocksStart + 1) % shorttermSubs
		}
	}
}

// addIntegrationBlock counts the gating block made of the last four
// sub-blocks, unless it falls below the absolute gate.
func (m *Meter) addIntegrationBlock() {
	var last [momentarySubs]float64
	for i := range last {
		last[i] = m.subblocks[(m.subblocksStart+m.subblocksLen-1-i)%shorttermSubs]
	}
	energy := reduce(last[:], 0) / float64(momentarySubs*m.samplesIn100ms)
	if energy >= absoluteGate {
		m.histogram[histogramIndex(energy)]++
	}
}

// histogramIndex is the bin a gating block's energy is counted in.
func histogramIndex(energy float64) int {
	switch {
	case energy >= histogramMaxEnergy:
		return histogramSize - 1
	case energy <= absoluteGate:
		return 0
	default:
		return int((energyToLoudness(energy) - absoluteGateLUFS) / binSizeLU)
	}
}

// binEnergy is the energy of count blocks in bin j.
func binEnergy(count uint64, j int) float64 {
	return float64(float64(count) * histogramEnergies[j])
}

// relativeThresholdIndex is the first bin at or above a relative threshold.
func relativeThresholdIndex(threshold float64) int {
	if threshold < absoluteGate {
		return 0
	}
	i := histogramIndex(threshold)
	if threshold > histogramEnergies[i] {
		i++
	}
	return i
}

// LoudnessGlobal is the gated integrated loudness of everything measured, in
// LUFS. It is negative infinity when no gating block passed the gates, which
// is to say when there was nothing there to measure, or not a gating block's
// worth of audio to measure it over.
func (m *Meter) LoudnessGlobal() float64 {
	// Mean of the blocks that passed the absolute gate.
	var count uint64
	for _, c := range m.histogram {
		count += c
	}
	if count == 0 {
		return math.Inf(-1)
	}
	var sum float64
	for j := 0; j < histogramSize; j += 4 {
		v1 := binEnergy(m.histogram[j], j) + binEnergy(m.histogram[j+1], j+1)
		v2 := binEnergy(m.histogram[j+2], j+2) + binEnergy(m.histogram[j+3], j+3)
		sum += v1 + v2
	}
	mean := sum / float64(count)

	// Mean of the blocks that also pass the gate relative to it.
	var gatedCount uint64
	var gated float64
	for j := relativeThresholdIndex(mean * relativeGateFactor); j < histogramSize; j++ {
		gated += binEnergy(m.histogram[j], j)
		gatedCount += m.histogram[j]
	}
	if gatedCount == 0 {
		return math.Inf(-1)
	}
	return energyToLoudness(gated / float64(gatedCount))
}

// Volume measures how loud a chunk of 16-bit mono PCM is, on a 0..1 scale where
// 0 is silent, from its integrated loudness.
//
// The chunk must hold at least 400 ms of audio, the length of a gating block;
// a shorter one is not a measurement and reads 0, as does a sample rate the
// meter cannot measure at. Each call builds a meter of its own; a Tracker
// measures through one it keeps.
func Volume(pcm []byte, sampleRate int) float64 {
	m := NewMeter(sampleRate)
	if m == nil {
		return 0
	}
	return m.volume(pcm)
}

// volume clears the meter, measures pcm through it and normalizes the result.
func (m *Meter) volume(pcm []byte) float64 {
	m.Reset()
	if float64(len(pcm)/2)/float64(m.rate) < minDurationSecs {
		return 0
	}
	m.AddPCM(pcm)
	// Below the absolute gate loudness measures as negative infinity, which
	// normalizes to 0.
	return Normalize(m.LoudnessGlobal(), loudnessFloor, loudnessCeiling)
}

// Normalize maps a value from a range onto 0..1, clamped to it.
func Normalize(value, low, high float64) float64 {
	if high == low {
		return 0
	}
	n := (value - low) / (high - low)
	switch {
	case math.IsNaN(n) || n < 0:
		return 0
	case n > 1:
		return 1
	}
	return n
}

// Smooth blends a new reading into the running one, weighting the new reading by
// factor.
func Smooth(value, previous, factor float64) float64 {
	return previous + factor*(value-previous)
}
