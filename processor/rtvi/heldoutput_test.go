package rtvi_test

import (
	"slices"
	"testing"

	"github.com/gojargo/jargo/frames"
	"github.com/gojargo/jargo/processor/rtvi"
)

// leftOut builds text the TTS service left out of speech, marked as it marks it.
func leftOut(text string, typ frames.TextType) *frames.AggregatedTextFrame {
	f := frames.NewAggregatedTextFrame(text, typ)
	skip := true
	f.SkipTTS = &skip
	return f
}

// sentTexts is the text of every bot-output message, in order.
func sentTexts(msgs []rtvi.Message) []string {
	var out []string
	for _, d := range outputs(msgs) {
		out = append(out, d.Text)
	}
	return out
}

// TestTextTheTTSLeftOutIsSentWithoutWaitingForTheBotToSpeak checks text the
// TTS service left out of speech, such as a reply to text sent with
// audio_response false, has nothing to wait for.
func TestTextTheTTSLeftOutIsSentWithoutWaitingForTheBotToSpeak(t *testing.T) {
	msgs := outputHarness(t, rtvi.DefaultObserverParams(), "", leftOut("Hello there.", frames.TextTypeSentence))

	if got := sentTexts(msgs); !slices.Equal(got, []string{"Hello there."}) {
		t.Errorf("sent %q, want the text at once", got)
	}
}

// TestTextTheTTSLeftOutWaitsBehindTextQueuedBeforeIt checks text left out of
// speech keeps its place behind text still waiting for the bot to speak.
func TestTextTheTTSLeftOutWaitsBehindTextQueuedBeforeIt(t *testing.T) {
	msgs := outputHarness(t, rtvi.DefaultObserverParams(), "",
		segment("Here it is.", frames.TextTypeSentence), leftOut("print(1)", "code"), speaking())

	if got := sentTexts(msgs); !slices.Equal(got, []string{"Here it is.", "print(1)"}) {
		t.Errorf("sent %q, want both in order once the bot spoke", got)
	}
}

// TestAnInterruptionDropsQueuedTextWhoseSpeechItCutOff checks text queued for
// speech an interruption cut off before its audio started is not sent with the
// bot's next speech, nor holds back text that is not spoken.
func TestAnInterruptionDropsQueuedTextWhoseSpeechItCutOff(t *testing.T) {
	msgs := outputHarness(t, rtvi.DefaultObserverParams(), "",
		segment("Let me check.", frames.TextTypeSentence), frames.NewInterruptionFrame(),
		leftOut("Here it is.", frames.TextTypeSentence), speaking())

	if got := sentTexts(msgs); !slices.Equal(got, []string{"Here it is."}) {
		t.Errorf("sent %q, want only the text after the interruption", got)
	}
}

// TestAnInterruptionKeepsQueuedTextWhoseSpeechItCannotCutOff checks text for
// uninterruptible speech, whose audio still plays, is kept.
func TestAnInterruptionKeepsQueuedTextWhoseSpeechItCannotCutOff(t *testing.T) {
	hold := segment("Please hold.", frames.TextTypeSentence)
	hold.SetInterruptible(false)

	msgs := outputHarness(t, rtvi.DefaultObserverParams(), "", hold, frames.NewInterruptionFrame(), speaking())

	if got := sentTexts(msgs); !slices.Equal(got, []string{"Please hold."}) {
		t.Errorf("sent %q, want the uninterruptible text once the bot spoke", got)
	}
}

// TestBotOutputFollowsTheSpeechWithoutSpeakingMessages checks bot-output still
// waits for the bot's speech when the bot-speaking messages are turned off.
func TestBotOutputFollowsTheSpeechWithoutSpeakingMessages(t *testing.T) {
	params := rtvi.DefaultObserverParams()
	off := false
	params.BotSpeakingEnabled = &off

	msgs := outputHarness(t, params, "",
		segment("Let me check.", frames.TextTypeSentence), frames.NewInterruptionFrame(),
		segment("Here it is.", frames.TextTypeSentence), speaking(), frames.NewBotStoppedSpeakingFrame())

	if got := sentTexts(msgs); !slices.Equal(got, []string{"Here it is."}) {
		t.Errorf("sent %q, want only the text after the interruption", got)
	}
	for _, m := range msgs {
		if m.Type == rtvi.TypeBotStartedSpeaking || m.Type == rtvi.TypeBotStoppedSpeaking {
			t.Errorf("sent %s with bot-speaking messages turned off", m.Type)
		}
	}
}

// TestTextThatIsNotSpokenStartsASegment checks text that is not spoken (left
// out by the TTS service, of a type it skips, or a transcript spoken without a
// TTS service) gets one message saying "new".
func TestTextThatIsNotSpokenStartsASegment(t *testing.T) {
	msgs := outputHarness(t, rtvi.DefaultObserverParams(), "", speaking(),
		leftOut("Hello there.", frames.TextTypeSentence),
		frames.NewAggregatedTextFrame("print(1)", "code"),
		frames.NewTTSTextFrame("Here it is.", frames.TextTypeSentence))

	got := outputs(msgs)
	if len(got) != 3 {
		t.Fatalf("got %d bot-output messages, want 3: %v", len(got), types(msgs))
	}
	for _, d := range got {
		if d.SpokenStatus != rtvi.SpokenNew || d.SpokenProgress != nil {
			t.Errorf("%q: status %q, progress %v, want new and no progress", d.Text, d.SpokenStatus, d.SpokenProgress)
		}
	}
}
