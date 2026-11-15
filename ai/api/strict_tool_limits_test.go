// Ported from packages/ai/test/strict-tool-limits.test.ts (pi v1.5.2).

package api

import (
	"fmt"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

func tstlStrPtr(s string) *string { return &s }

func TestStrictToolLimitSessions(t *testing.T) {
	t.Cleanup(func() { _ = ai.CleanupSessionResources(nil) })

	t.Run("remembers marked sessions until their cleanup", func(t *testing.T) {
		defer func() { _ = ai.CleanupSessionResources(nil) }()
		a := tstlStrPtr("a")
		b := tstlStrPtr("b")
		c := tstlStrPtr("c")

		MarkStrictToolLimitsExceeded(a)
		MarkStrictToolLimitsExceeded(b)
		MarkStrictToolLimitsExceeded(nil)

		if !HasExceededStrictToolLimits(a) {
			t.Fatalf("expected session a to be marked")
		}
		if HasExceededStrictToolLimits(c) {
			t.Fatalf("expected session c not to be marked")
		}
		if HasExceededStrictToolLimits(nil) {
			t.Fatalf("expected nil session not to be marked")
		}

		if err := ai.CleanupSessionResources(a); err != nil {
			t.Fatalf("cleanup session a failed: %v", err)
		}
		if HasExceededStrictToolLimits(a) {
			t.Fatalf("expected session a to be cleared after cleanup")
		}
		if !HasExceededStrictToolLimits(b) {
			t.Fatalf("expected session b to remain marked")
		}

		if err := ai.CleanupSessionResources(nil); err != nil {
			t.Fatalf("cleanup all sessions failed: %v", err)
		}
		if HasExceededStrictToolLimits(b) {
			t.Fatalf("expected session b to be cleared after full cleanup")
		}
	})

	t.Run("drops the oldest session beyond 10,000", func(t *testing.T) {
		defer func() { _ = ai.CleanupSessionResources(nil) }()
		for i := 0; i <= 10_000; i++ {
			id := fmt.Sprintf("session-%d", i)
			MarkStrictToolLimitsExceeded(&id)
		}
		if HasExceededStrictToolLimits(tstlStrPtr("session-0")) {
			t.Fatalf("expected session-0 to be evicted beyond 10,000")
		}
		if !HasExceededStrictToolLimits(tstlStrPtr("session-1")) {
			t.Fatalf("expected session-1 to be retained")
		}
		if !HasExceededStrictToolLimits(tstlStrPtr("session-10000")) {
			t.Fatalf("expected session-10000 to be retained")
		}
	})

	t.Run("recognizes strict tool limit error messages", func(t *testing.T) {
		cases := []struct {
			msg  string
			want bool
		}{
			{"The compiled grammar is too large to process.", true},
			{"Too many strict tools (25 max 20)", true},
			{"Schemas contains too many parameters with union types (18)", true},
			{"Invalid JSON schema", false},
		}
		for _, tc := range cases {
			if got := IsStrictToolLimitErrorMessage(tc.msg); got != tc.want {
				t.Fatalf("IsStrictToolLimitErrorMessage(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		}
	})
}
