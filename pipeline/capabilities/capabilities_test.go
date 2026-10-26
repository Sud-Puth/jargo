package capabilities_test

import (
	"reflect"
	"testing"

	"github.com/gojargo/jargo/pipeline/capabilities"
)

func TestCombineTrueWins(t *testing.T) {
	a := capabilities.BotCapabilities{AudioIn: new(false), VideoOut: new(true)}
	b := capabilities.BotCapabilities{AudioIn: new(true), VideoOut: new(false)}
	want := capabilities.BotCapabilities{AudioIn: new(true), VideoOut: new(true)}
	if got := a.Combine(b); !reflect.DeepEqual(got, want) {
		t.Errorf("combined %+v, want %+v", got, want)
	}
}

func TestCombineKeepsFalseAndUnnew(t *testing.T) {
	a := capabilities.BotCapabilities{VideoIn: new(false)}
	want := capabilities.BotCapabilities{VideoIn: new(false)}
	if got := a.Combine(capabilities.BotCapabilities{}); !reflect.DeepEqual(got, want) {
		t.Errorf("combined %+v, want %+v", got, want)
	}
}

func TestOverrideAppliesOnlyKnownFields(t *testing.T) {
	base := capabilities.BotCapabilities{AudioIn: new(true), VideoOut: new(false), Metrics: new(true)}
	got := base.Override(capabilities.BotCapabilities{VideoOut: new(true)})
	want := capabilities.BotCapabilities{AudioIn: new(true), VideoOut: new(true), Metrics: new(true)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("overridden %+v, want %+v", got, want)
	}
}
