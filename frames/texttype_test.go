package frames_test

import (
	"testing"

	"github.com/gojargo/jargo/frames"
)

// TestAggregationTypeIsTextType checks the former names of TextType and its
// constants still name the same things.
func TestAggregationTypeIsTextType(t *testing.T) {
	var old frames.AggregationType = frames.AggregationSentence //nolint:staticcheck // the deprecated names under test
	if old != frames.TextTypeSentence {
		t.Errorf("AggregationSentence = %q, want %q", old, frames.TextTypeSentence)
	}
	if frames.AnyAggregation != frames.AnyTextType { //nolint:staticcheck // the deprecated name under test
		t.Error("AnyAggregation no longer stands for AnyTextType")
	}
}
