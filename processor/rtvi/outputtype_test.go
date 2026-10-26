package rtvi

import (
	"encoding/json"
	"testing"

	"github.com/gojargo/jargo/frames"
)

// TestBotOutputSendsTheOutputTypeUnderEveryName checks a bot-output message
// carries output_type, and the same value under the deprecated text_type and
// aggregated_by, for the clients still reading those.
func TestBotOutputSendsTheOutputTypeUnderEveryName(t *testing.T) {
	msg := BotOutput(BotOutputData{Text: "Hello.", OutputType: frames.TextTypeSentence})

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"output_type", "text_type", "aggregated_by"} {
		if decoded.Data[name] != "sentence" {
			t.Errorf("%s = %v, want sentence", name, decoded.Data[name])
		}
	}
}

// TestBotOutputReadsTheOutputTypeFromAnyName checks a payload naming the type
// only by one of the deprecated names, as an older bot sends it, reads the same.
func TestBotOutputReadsTheOutputTypeFromAnyName(t *testing.T) {
	for _, name := range []string{"text_type", "aggregated_by"} {
		var d BotOutputData
		if err := json.Unmarshal([]byte(`{"text":"Hi.","`+name+`":"code"}`), &d); err != nil {
			t.Fatal(err)
		}
		//nolint:staticcheck // the deprecated names under test
		if d.OutputType != "code" || d.TextType != "code" || d.AggregatedBy != "code" {
			t.Errorf("from %s: %+v, want code under every name", name, d)
		}
	}
}
