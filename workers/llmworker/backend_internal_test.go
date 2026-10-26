package llmworker

import "testing"

// Ported from upstream's backend worker suite: the parts that read a private
// helper.

func TestAMarkOnALaterLineIsNotSpokenAsText(t *testing.T) {
	note, spoken := splitSpoken("Found it.\n\n>> The test is fixed.\n>> All 42 pass.")
	if note != "Found it." || spoken != "The test is fixed.\nAll 42 pass." {
		t.Errorf("split into note %q and spoken %q", note, spoken)
	}
}

func TestAMessageWithoutTheMarkIsAllNote(t *testing.T) {
	note, spoken := splitSpoken("Still looking.")
	if note != "Still looking." || spoken != "" {
		t.Errorf("split into note %q and spoken %q", note, spoken)
	}
}
