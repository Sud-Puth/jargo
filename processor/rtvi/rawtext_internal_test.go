package rtvi

import (
	"slices"
	"testing"

	"github.com/gojargo/jargo/frames"
)

// Tests for the raw text messages the observer sends for the STT, the LLM and
// the TTS. They drive the observer's frame mapping directly, with the bot
// already speaking so text is sent right away instead of held.

func rawTextObserver() *segmentObserver {
	params := DefaultObserverParams()
	params.STTRawTextEnabled = true
	params.LLMRawTextEnabled = true
	params.TTSRawTextEnabled = true
	o := &segmentObserver{Observer: NewObserverWithParams(nil, params)}
	o.botSpeaking = true
	return o
}

func (o *segmentObserver) pushOneOfEach() {
	o.push(frames.NewTranscriptionFrame("Hello.", "user", "now"))
	o.push(frames.NewLLMTextFrame("Hi"))
	segment := statusSegment("Hi there.", frames.TextTypeSentence)
	o.push(segment)
	o.push(word("Hi", segment))
}

func (o *segmentObserver) rawText() []Message {
	var out []Message
	for _, m := range o.sent {
		switch m.Type {
		case TypeSTTRawText, TypeLLMRawText, TypeTTSRawText:
			out = append(out, m)
		}
	}
	return out
}

func TestRawTextIsKeptFromTheClientByDefault(t *testing.T) {
	o := rawTextObserver()
	o.params = DefaultObserverParams()
	o.pushOneOfEach()

	if got := o.rawText(); len(got) != 0 {
		t.Fatalf("sent %+v, want no raw text", got)
	}
}

func TestATranscriptionIsSentAsSTTRawTextBeforeUserTranscription(t *testing.T) {
	o := rawTextObserver()
	o.push(frames.NewInterimTranscriptionFrame("Hel", "user", "t1"))
	o.push(frames.NewTranscriptionFrame("Hello.", "user", "t2"))

	once := []string{TypeUserInput, TypeSTTRawText, TypeUserTranscription}
	if got, want := o.types(), slices.Concat(once, once); !slices.Equal(got, want) {
		t.Fatalf("sent %v, want %v", got, want)
	}
	want := []STTRawTextData{
		{Text: "Hel", UserID: "user", Timestamp: "t1", Final: false},
		{Text: "Hello.", UserID: "user", Timestamp: "t2", Final: true},
	}
	if o.sent[1].Data != want[0] || o.sent[4].Data != want[1] {
		t.Errorf("data = %+v and %+v, want %+v", o.sent[1].Data, o.sent[4].Data, want)
	}
}

func TestLLMTextIsSentAsLLMRawTextBeforeBotLLMText(t *testing.T) {
	o := rawTextObserver()
	o.push(frames.NewLLMTextFrame("Hi"))

	if got, want := o.types(), []string{TypeLLMRawText, TypeBotLLMText}; !slices.Equal(got, want) {
		t.Fatalf("sent %v, want %v", got, want)
	}
	if d, _ := o.sent[0].Data.(TextData); d.Text != "Hi" {
		t.Errorf("data = %+v, want the text streamed", o.sent[0].Data)
	}
}

func TestLLMTextIsOnlySentAsLLMRawTextWithoutBotLLMMessages(t *testing.T) {
	o := rawTextObserver()
	off := false
	o.params.BotLLMEnabled = &off
	o.push(frames.NewLLMTextFrame("Hi there."))

	if got := o.types(); !slices.Equal(got, []string{TypeLLMRawText}) {
		t.Fatalf("sent %v, want only llm-raw-text", got)
	}
}

func TestSpokenTextIsSentAsTTSRawTextBeforeBotTTSText(t *testing.T) {
	o := rawTextObserver()
	o.pushOneOfEach()

	var tts []Message
	for _, m := range o.sent {
		if m.Type == TypeTTSRawText || m.Type == TypeBotTTSText {
			tts = append(tts, m)
		}
	}
	if len(tts) != 2 || tts[0].Type != TypeTTSRawText || tts[1].Type != TypeBotTTSText {
		t.Fatalf("sent %+v, want tts-raw-text then bot-tts-text", tts)
	}
	if d, _ := tts[0].Data.(TextData); d.Text != "Hi" {
		t.Errorf("data = %+v, want the word spoken", tts[0].Data)
	}
}

func TestSpokenTextIsOnlySentAsTTSRawTextWithoutBotOutputOrTTS(t *testing.T) {
	o := rawTextObserver()
	off := false
	o.params.BotOutputEnabled = &off
	o.params.BotTTSEnabled = &off
	segment := statusSegment("Hi there.", frames.TextTypeSentence)
	o.push(segment)
	o.push(word("Hi", segment))

	if got := o.types(); !slices.Equal(got, []string{TypeTTSRawText}) {
		t.Fatalf("sent %v, want only tts-raw-text", got)
	}
}

func TestTTSRawTextLeavesOutSkippedTextTypes(t *testing.T) {
	o := rawTextObserver()
	o.params.SkipTextTypes = []frames.TextType{"status"}
	segment := statusSegment("Looking that up.", "status")
	o.push(segment)
	o.push(word("Looking", segment))

	if got := o.rawText(); len(got) != 0 {
		t.Fatalf("sent %+v, want no raw text of a skipped type", got)
	}
}

func TestRawTextIsKeptFromOlderClients(t *testing.T) {
	for _, version := range [][3]int{{2, 1, 0}, {1, 4, 0}} {
		o := rawTextObserver()
		o.connectClient(version)
		o.pushOneOfEach()

		if got := o.rawText(); len(got) != 0 {
			t.Errorf("%v: sent %+v, want no raw text", version, got)
		}
	}
}
