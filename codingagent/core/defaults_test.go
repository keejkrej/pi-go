package core

import (
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

func TestDefaults(t *testing.T) {
	if DefaultThinkingLevel != ai.ModelThinkingLevelMedium {
		t.Fatalf("DefaultThinkingLevel = %q, want %q", DefaultThinkingLevel, ai.ModelThinkingLevelMedium)
	}

	wantOptions := []ai.ModelThinkingLevel{
		ai.ModelThinkingLevelOff,
		ai.ModelThinkingLevelMinimal,
		ai.ModelThinkingLevelLow,
		ai.ModelThinkingLevelMedium,
		ai.ModelThinkingLevelHigh,
		ai.ModelThinkingLevelXhigh,
		ai.ModelThinkingLevelMax,
	}
	if !slices.Equal(ThinkingLevelOptions, wantOptions) {
		t.Fatalf("ThinkingLevelOptions = %v, want %v", ThinkingLevelOptions, wantOptions)
	}
}

func TestAreExperimentalFeaturesEnabled(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		t.Setenv("PI_EXPERIMENTAL", "")
		if AreExperimentalFeaturesEnabled() {
			t.Fatal("expected false when empty")
		}
	})

	t.Run("set to 1", func(t *testing.T) {
		t.Setenv("PI_EXPERIMENTAL", "1")
		if !AreExperimentalFeaturesEnabled() {
			t.Fatal("expected true when PI_EXPERIMENTAL=1")
		}
	})

	t.Run("set to 0", func(t *testing.T) {
		t.Setenv("PI_EXPERIMENTAL", "0")
		if AreExperimentalFeaturesEnabled() {
			t.Fatal("expected false when PI_EXPERIMENTAL=0")
		}
	})

	t.Run("set to true", func(t *testing.T) {
		t.Setenv("PI_EXPERIMENTAL", "true")
		if AreExperimentalFeaturesEnabled() {
			t.Fatal("expected false when PI_EXPERIMENTAL=true")
		}
	})
}
