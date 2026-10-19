package loudness

import (
	"encoding/binary"
	"math"
	"strconv"
	"testing"
)

// tone builds ms milliseconds of a 440 Hz tone at amp, as mono 16-bit PCM.
func tone(ms, rate int, amp float64) []byte {
	n := ms * rate / 1000
	pcm := make([]byte, n*2)
	for i := range n {
		s := int16(amp * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(s))
	}
	return pcm
}

// TestTrackerReadsZeroUntilTheWindowFills checks a partly filled window measures
// nothing. Loudness is measured over a gating block, and audio shorter than one
// is not a reading at all: reporting it would let a fraction of a block decide
// whether someone is speaking.
func TestTrackerReadsZeroUntilTheWindowFills(t *testing.T) {
	const rate = 16000
	var tr Tracker

	// A fresh tracker has nothing to measure.
	if got := tr.Volume(); got != 0 {
		t.Errorf("a fresh tracker reads %v, want 0", got)
	}

	// Four 100 ms chunks make up the 400 ms window; only the last completes it.
	chunk := tone(100, rate, 8000)
	for i := range 3 {
		tr.Update(chunk, rate)
		if got := tr.Volume(); got != 0 {
			t.Fatalf("after %d ms the tracker reads %v, want 0", (i+1)*100, got)
		}
	}

	tr.Update(chunk, rate)
	full := tr.Volume()
	if full <= 0 {
		t.Fatalf("a full window of a loud tone reads %v, want a positive volume", full)
	}
}

// TestTrackerWindowSlides checks the window holds the most recent audio rather
// than growing without bound, so the volume follows the stream.
func TestTrackerWindowSlides(t *testing.T) {
	const rate = 16000
	var tr Tracker

	loud := tone(400, rate, 12000)
	tr.Update(loud, rate)
	wasLoud := tr.Volume()
	if wasLoud <= 0 {
		t.Fatalf("a full window of a loud tone reads %v, want a positive volume", wasLoud)
	}

	// A full window of silence pushes the tone out entirely.
	tr.Update(make([]byte, 400*rate/1000*2), rate)
	if got := tr.Volume(); got >= wasLoud {
		t.Errorf("after a window of silence the tracker reads %v, want below %v", got, wasLoud)
	}
}

// TestTrackerRereadsAreCached checks measuring happens on read and only once per
// update, since it costs a few hundred microseconds.
func TestTrackerRereadsAreCached(t *testing.T) {
	const rate = 16000
	var tr Tracker
	tr.Update(tone(400, rate, 8000), rate)

	first := tr.Volume()
	if first != tr.Volume() {
		t.Error("two reads of the same window disagree")
	}

	// Reading does not consume the window: more audio keeps it measurable.
	tr.Update(tone(100, rate, 8000), rate)
	if got := tr.Volume(); got <= 0 {
		t.Errorf("after more audio the tracker reads %v, want a positive volume", got)
	}
}

// TestTrackerResetsOnASampleRateChange checks the window is discarded when the
// rate changes, since what it holds was measured against a different one.
func TestTrackerResetsOnASampleRateChange(t *testing.T) {
	var tr Tracker
	tr.Update(tone(400, 16000, 8000), 16000)
	if tr.Volume() <= 0 {
		t.Fatal("the window did not fill at 16 kHz")
	}

	// The same audio at a new rate starts a new window, which is not yet full.
	tr.Update(tone(100, 8000, 8000), 8000)
	if got := tr.Volume(); got != 0 {
		t.Errorf("after the rate changed the tracker reads %v, want 0", got)
	}
}

// TestTrackerReset checks an explicit reset clears both the window and the
// reading taken from it.
func TestTrackerReset(t *testing.T) {
	const rate = 16000
	var tr Tracker
	tr.Update(tone(400, rate, 8000), rate)
	if tr.Volume() <= 0 {
		t.Fatal("the window did not fill")
	}

	tr.Reset()
	if got := tr.Volume(); got != 0 {
		t.Errorf("after a reset the tracker reads %v, want 0", got)
	}
}

// feed gives the tracker audio in 20 ms chunks, reading the volume after each.
func feed(tr *Tracker, pcm []byte, rate int) {
	chunk := rate / 50 * 2
	for i := 0; i < len(pcm); i += chunk {
		tr.Update(pcm[i:min(i+chunk, len(pcm))], rate)
		tr.Volume()
	}
}

// TestTrackerWindowIsBounded checks the window never holds more than 400 ms,
// however much audio is fed through it.
func TestTrackerWindowIsBounded(t *testing.T) {
	const rate = 16000
	var tr Tracker
	feed(&tr, tone(5000, rate, 8000), rate)
	if got, want := len(tr.buf), 400*rate/1000*2; got != want {
		t.Errorf("the window holds %d bytes, want %d", got, want)
	}
}

// TestTrackerChunkLargerThanWindow checks a chunk longer than the window leaves
// only its most recent 400 ms behind, and is measured.
func TestTrackerChunkLargerThanWindow(t *testing.T) {
	const rate = 16000
	var tr Tracker
	tr.Update(tone(3000, rate, 8000), rate)
	if tr.Volume() <= 0 {
		t.Error("a chunk longer than the window reads silent")
	}
	if got, want := len(tr.buf), 400*rate/1000*2; got != want {
		t.Errorf("the window holds %d bytes, want %d", got, want)
	}
}

// TestTrackerSupportedSampleRates checks a full window measures at the rates a
// pipeline runs at.
func TestTrackerSupportedSampleRates(t *testing.T) {
	for _, rate := range []int{8000, 16000, 22050, 24000, 44100, 48000} {
		var tr Tracker
		feed(&tr, tone(1000, rate, 8000), rate)
		if got := tr.Volume(); got <= 0 {
			t.Errorf("at %d Hz a full window of a loud tone reads %v, want a positive volume", rate, got)
		}
	}
}

// TestTrackerWindowIsNeverShortOfAGatingBlock checks rates where 400 ms is not a
// whole number of samples. Rounding the window down would leave it a sample
// short of a gating block, which is not a measurement.
func TestTrackerWindowIsNeverShortOfAGatingBlock(t *testing.T) {
	for _, rate := range []int{7999, 11999, 16001, 44101} {
		var tr Tracker
		feed(&tr, tone(1000, rate, 8000), rate)
		if secs := float64(len(tr.buf)) / 2 / float64(rate); secs < windowSecs {
			t.Errorf("at %d Hz the window holds %v s, want at least %v", rate, secs, windowSecs)
		}
		if got := tr.Volume(); got <= 0 {
			t.Errorf("at %d Hz a full window of a loud tone reads %v, want a positive volume", rate, got)
		}
	}
}

// TestTrackerMeasuresWithoutAllocating checks a stream at a steady rate costs no
// garbage once its window is full: every frame of every stream is measured, so
// an allocation per measurement is garbage at the frame rate times the number
// of streams.
func TestTrackerMeasuresWithoutAllocating(t *testing.T) {
	for _, rate := range []int{16000, 48000} {
		var tr Tracker
		feed(&tr, tone(1000, rate, 8000), rate)

		frame := tone(32, rate, 8000) // one 32 ms VAD frame
		allocs := testing.AllocsPerRun(100, func() {
			tr.Update(frame, rate)
			tr.Volume()
		})
		if allocs != 0 {
			t.Errorf("at %d Hz measuring a frame allocates %v times, want 0", rate, allocs)
		}
	}
}

// BenchmarkTrackerFrame measures the cost of tracking one 32 ms frame, the
// work done for every frame of every stream.
func BenchmarkTrackerFrame(b *testing.B) {
	for _, rate := range []int{16000, 48000} {
		b.Run(strconv.Itoa(rate), func(b *testing.B) {
			var tr Tracker
			feed(&tr, tone(1000, rate, 8000), rate)
			frame := tone(32, rate, 8000)
			b.ReportAllocs()
			for b.Loop() {
				tr.Update(frame, rate)
				tr.Volume()
			}
		})
	}
}
