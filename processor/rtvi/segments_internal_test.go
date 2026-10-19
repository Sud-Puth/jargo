package rtvi

import (
	"maps"
	"slices"
	"testing"

	"github.com/gojargo/jargo/frames"
)

// Tests for how the observer keeps a skipped segment from the client, its words
// and its progress included. They drive the observer's frame mapping directly,
// as it runs once a frame has gone through the output transport, so what the
// observer remembers about skipped segments can be checked too.

// segmentObserver is an observer that skips the "status" type, with the bot
// already speaking so text is reported at once instead of held.
type segmentObserver struct {
	*Observer
	sent []Message
}

func newSegmentObserver() *segmentObserver {
	params := DefaultObserverParams()
	params.SkipAggregatorTypes = []frames.AggregationType{"status"}
	o := &segmentObserver{Observer: NewObserverWithParams(nil, params)}
	o.botSpeaking = true
	return o
}

func (o *segmentObserver) push(f frames.Frame) {
	o.sent = append(o.sent, o.messagesFor(f)...)
}

// texts is the text of every message sent that carries any: the bot's output
// and the captions for what it spoke.
func (o *segmentObserver) texts() []string {
	var out []string
	for _, m := range o.sent {
		switch d := m.Data.(type) {
		case BotOutputData:
			out = append(out, d.Text)
		case TextData:
			out = append(out, d.Text)
		}
	}
	return out
}

func (o *segmentObserver) skippedIDs() []uint64 {
	o.outputMu.Lock()
	defer o.outputMu.Unlock()
	return slices.Sorted(maps.Keys(o.skippedSegmentIDs))
}

// statusSegment is a segment the synthesizer will speak. All the segments of
// one response share a synthesis context.
func statusSegment(text string, by frames.AggregationType) *frames.AggregatedTextFrame {
	f := frames.NewAggregatedTextFrame(text, by)
	f.ContextID = "turn"
	f.WillBeSpoken = true
	return f
}

// word is one word spoken in seg.
func word(text string, seg *frames.AggregatedTextFrame) *frames.TTSTextFrame {
	f := frames.NewTTSTextFrame(text, frames.AggregationWord)
	f.ContextID = "turn"
	f.SegmentID = seg.ID()
	return f
}

// progress is how far through seg the bot has spoken.
func progress(seg *frames.AggregatedTextFrame, accumulated, remaining string) *frames.AggregatedTextProgressFrame {
	return frames.NewAggregatedTextProgressFrame(seg.ID(), "turn", seg.Text, seg.AggregatedBy, accumulated, remaining)
}

func TestASkippedSegmentKeepsItsProgressAndWordsFromTheClient(t *testing.T) {
	o := newSegmentObserver()
	status := statusSegment("One moment, please.", "status")
	o.push(status)
	o.push(word("One", status))
	o.push(progress(status, "One", " moment, please."))

	if len(o.sent) != 0 {
		t.Fatalf("sent %+v, want nothing of a skipped segment", o.sent)
	}
}

func TestASegmentAfterASkippedOneIsSent(t *testing.T) {
	o := newSegmentObserver()
	status := statusSegment("One moment, please.", "status")
	sentence := statusSegment("That sounds fun.", frames.AggregationSentence)
	o.push(status)
	o.push(word("One", status))
	o.push(sentence)
	o.push(word("That", sentence))

	if got, want := o.texts(), []string{"That sounds fun.", "That"}; !slices.Equal(got, want) {
		t.Fatalf("sent %q, want %q", got, want)
	}
}

func TestWordsFollowTheirSegmentWhenTheSegmentsComeFirst(t *testing.T) {
	// A turn's segments can all arrive before the first one is spoken.
	o := newSegmentObserver()
	status := statusSegment("One moment, please.", "status")
	sentence := statusSegment("That sounds fun.", frames.AggregationSentence)
	o.push(status)
	o.push(sentence)
	o.push(word("One", status))
	o.push(word("That", sentence))

	if got, want := o.texts(), []string{"That sounds fun.", "That"}; !slices.Equal(got, want) {
		t.Fatalf("sent %q, want %q", got, want)
	}
}

func TestASkippedSegmentIsForgottenOnceSpoken(t *testing.T) {
	o := newSegmentObserver()
	status := statusSegment("One moment, please.", "status")
	o.push(status)
	o.push(progress(status, "One moment, please.", ""))

	if got := o.skippedIDs(); len(got) != 0 {
		t.Fatalf("still remembering %v after the segment was spoken", got)
	}
}

func TestASkippedSegmentCutOffIsForgottenWhenTheBotStops(t *testing.T) {
	o := newSegmentObserver()
	status := statusSegment("One moment, please.", "status")
	o.push(status)
	o.push(frames.NewInterruptionFrame())
	// A word the output transport pushed before it handled the interruption.
	o.push(word("One", status))
	o.push(frames.NewBotStoppedSpeakingFrame())

	if slices.Contains(o.texts(), "One") {
		t.Fatal("a word of the skipped segment was sent")
	}
	if got := o.skippedIDs(); len(got) != 0 {
		t.Fatalf("still remembering %v after the bot stopped", got)
	}
}

func TestASkippedSegmentCutOffInAPauseIsForgottenWhenTheBotSpeaks(t *testing.T) {
	o := newSegmentObserver()
	status := statusSegment("One moment, please.", "status")
	o.push(status)
	o.push(frames.NewBotStoppedSpeakingFrame())
	if got := o.skippedIDs(); !slices.Equal(got, []uint64{status.ID()}) {
		t.Fatalf("remembering %v, want the skipped segment %d", got, status.ID())
	}

	o.push(frames.NewInterruptionFrame())
	o.push(frames.NewBotStartedSpeakingFrame())
	if got := o.skippedIDs(); len(got) != 0 {
		t.Fatalf("still remembering %v after the bot spoke again", got)
	}
}

func TestASkippedSegmentSpokenInOnePieceIsForgotten(t *testing.T) {
	o := newSegmentObserver()
	status := statusSegment("One moment, please.", "status")
	spoken := frames.NewTTSTextFrame(status.Text, "status")
	spoken.SegmentID = status.ID()
	o.push(status)
	o.push(spoken)

	if len(o.sent) != 0 {
		t.Fatalf("sent %+v, want nothing of a skipped segment", o.sent)
	}
	if got := o.skippedIDs(); len(got) != 0 {
		t.Fatalf("still remembering %v after the segment was spoken", got)
	}
}
