// Ported from packages/telemetry/test/render.test.ts (pi v1.5.2).

package telemetry_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/telemetry"
)

func trendSpan(id string, configure func(*telemetry.StoredSpan)) telemetry.StoredSpan {
	s := telemetry.StoredSpan{
		Id:         id,
		TraceId:    "trace",
		Name:       id,
		StartedMs:  0,
		EndedMs:    10,
		Attributes: thlpAttrs(),
		Events:     []telemetry.StoredSpanEvent{},
		Links:      []telemetry.StoredSpanLink{},
		Status:     &telemetry.SpanStatusOk{Status: telemetry.SpanStatusKindOk},
	}
	if configure != nil {
		configure(&s)
	}
	return s
}

func TestRender_UnorderedTraceWithAttributesDurationsErrorsEventsAndLinks(t *testing.T) {
	root := trendSpan("root", func(s *telemetry.StoredSpan) {
		s.StartedMs = 1000
		s.EndedMs = 1020
		s.Attributes = thlpAttrs(
			"request", "a b",
			"attempt", 2,
			"cached", false,
			"tags", []string{"a", "b"},
			"sizes", []int{1, 2},
			"flags", []bool{true, false},
		)
		s.Events = []telemetry.StoredSpanEvent{
			{Name: "checkpoint", TimeMs: 2, Attributes: thlpAttrs("phase", "tools")},
			{Name: "tick", TimeMs: 3.5, Attributes: thlpAttrs()},
		}
		s.Links = []telemetry.StoredSpanLink{
			{TraceId: "remote-trace", SpanId: "remote", Attributes: thlpAttrs("reason", "steer")},
			{TraceId: "other-trace", SpanId: "other"},
		}
	})
	first := trendSpan("first", func(s *telemetry.StoredSpan) {
		s.ParentId = thlpStrPtr("root")
		s.StartedMs = 1005
		s.EndedMs = 1009.5
		s.Status = &telemetry.SpanStatusError{
			Status: telemetry.SpanStatusKindError,
			Error:  &telemetry.SpanError{Name: "Error", Message: "failed call"},
		}
	})
	nested := trendSpan("nested", func(s *telemetry.StoredSpan) {
		s.ParentId = thlpStrPtr("first")
		s.StartedMs = 1006
		s.EndedMs = 1007
	})
	second := trendSpan("second", func(s *telemetry.StoredSpan) {
		s.ParentId = thlpStrPtr("root")
		s.StartedMs = 1010
		s.EndedMs = 1012
		s.Status = &telemetry.SpanStatusError{Status: telemetry.SpanStatusKindError}
	})
	spans := []telemetry.StoredSpan{second, nested, first, root}
	before := []telemetry.StoredSpan{second, nested, first, root}

	want := strings.Join([]string{
		`root request="a b" attempt=2 cached=false tags=["a","b"] sizes=[1,2] flags=[true,false] (20ms)`,
		`  · checkpoint phase="tools" (+2ms)`,
		`  · tick (+3.5ms)`,
		`  → link trace remote-trace span remote reason="steer"`,
		`  → link trace other-trace span other`,
		`  first (4.5ms) error Error: failed call`,
		`    nested (1ms)`,
		`  second (2ms) error`,
	}, "\n")

	if got := telemetry.RenderTrace(spans); got != want {
		t.Fatalf("render mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}
	if diff := cmp.Diff(before, spans, thlpCmpOpts()...); diff != "" {
		t.Fatalf("RenderTrace mutated input spans (-want +got):\n%s", diff)
	}
}

func TestRender_TreatsMissingParentsAsRootsAndOrdersByStartTime(t *testing.T) {
	got := telemetry.RenderTrace([]telemetry.StoredSpan{
		trendSpan("later", func(s *telemetry.StoredSpan) {
			s.ParentId = thlpStrPtr("external")
			s.StartedMs = 20
			s.EndedMs = 30
		}),
		trendSpan("child", func(s *telemetry.StoredSpan) {
			s.ParentId = thlpStrPtr("later")
			s.StartedMs = 25
			s.EndedMs = 30
		}),
		trendSpan("earlier", func(s *telemetry.StoredSpan) {
			s.StartedMs = 10
			s.EndedMs = 15
			s.Links = []telemetry.StoredSpanLink{{TraceId: "trace", SpanId: "later"}}
		}),
	})
	want := strings.Join([]string{
		"earlier (5ms)",
		"  → link trace trace span later",
		"later (10ms)",
		"  child (5ms)",
	}, "\n")
	if got != want {
		t.Fatalf("render mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}
}

func TestRender_PrintsCrossTraceLinksWithoutFollowingCycles(t *testing.T) {
	got := telemetry.RenderTrace([]telemetry.StoredSpan{
		trendSpan("server:1", func(s *telemetry.StoredSpan) {
			s.Name = "server"
			s.TraceId = "server-trace"
			s.Links = []telemetry.StoredSpanLink{{TraceId: "worker-trace", SpanId: "worker:1"}}
		}),
		trendSpan("worker:1", func(s *telemetry.StoredSpan) {
			s.Name = "worker"
			s.TraceId = "worker-trace"
			s.Links = []telemetry.StoredSpanLink{{TraceId: "server-trace", SpanId: "server:1"}}
		}),
	})
	want := strings.Join([]string{
		"server (10ms)",
		"  → link trace worker-trace span worker:1",
		"worker (10ms)",
		"  → link trace server-trace span server:1",
	}, "\n")
	if got != want {
		t.Fatalf("render mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}
}

func TestRender_DeduplicatesSpansMergedFromStoresByGlobalID(t *testing.T) {
	root := trendSpan("server:1", func(s *telemetry.StoredSpan) { s.Name = "root" })
	child := trendSpan("worker:1", func(s *telemetry.StoredSpan) {
		s.Name = "child"
		s.ParentId = thlpStrPtr("server:1")
		s.Events = []telemetry.StoredSpanEvent{{Name: "done", TimeMs: 3, Attributes: thlpAttrs()}}
	})
	grandchild := trendSpan("worker:2", func(s *telemetry.StoredSpan) {
		s.Name = "grandchild"
		s.ParentId = thlpStrPtr("worker:1")
	})
	got := telemetry.RenderTrace([]telemetry.StoredSpan{child, root, grandchild, root, child})
	want := "root (10ms)\n  child (10ms)\n    · done (+3ms)\n    grandchild (10ms)"
	if got != want {
		t.Fatalf("render mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}
}

func TestRender_PreservesInputOrderForEqualStartTimesWithoutMutatingInput(t *testing.T) {
	spans := []telemetry.StoredSpan{
		trendSpan("root", nil),
		trendSpan("z", func(s *telemetry.StoredSpan) { s.ParentId = thlpStrPtr("root") }),
		trendSpan("a", func(s *telemetry.StoredSpan) { s.ParentId = thlpStrPtr("root") }),
		trendSpan("other", nil),
	}
	want := "root (10ms)\n  z (10ms)\n  a (10ms)\nother (10ms)"
	if got := telemetry.RenderTrace(spans); got != want {
		t.Fatalf("render mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}
}

func TestRender_EscapesStringAttributeValuesWithoutHidingContents(t *testing.T) {
	got := telemetry.RenderTrace([]telemetry.StoredSpan{
		trendSpan("root", func(s *telemetry.StoredSpan) {
			s.Attributes = thlpAttrs("text", "line one\n\"line two\"")
		}),
	})
	want := `root text="line one\n\"line two\"" (10ms)`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRender_RendersDeepTreesWithoutRecursiveCalls(t *testing.T) {
	const count = 2000
	spans := make([]telemetry.StoredSpan, count)
	for index := 0; index < count; index++ {
		idx := index
		spans[index] = trendSpan(fmt.Sprintf("%d", idx), func(s *telemetry.StoredSpan) {
			if idx > 0 {
				s.ParentId = thlpStrPtr(fmt.Sprintf("%d", idx-1))
			}
		})
	}
	rendered := telemetry.RenderTrace(spans)
	lines := strings.Split(rendered, "\n")
	if len(lines) != len(spans) {
		t.Fatalf("expected %d lines, got %d", len(spans), len(lines))
	}
	wantSuffix := strings.Repeat("  ", count-1) + "1999 (10ms)"
	if !strings.HasSuffix(rendered, wantSuffix) {
		t.Fatalf("expected rendered suffix %q, got tail %q", wantSuffix, lines[len(lines)-1])
	}
}

func TestRender_MicrosecondsPrecision(t *testing.T) {
	root := trendSpan("root", func(s *telemetry.StoredSpan) {
		s.StartedMs = 1_793_789_452_816.123
		s.EndedMs = 1_793_789_452_816.431
		s.Events = []telemetry.StoredSpanEvent{{Name: "half", TimeMs: 0.15400000002, Attributes: thlpAttrs()}}
	})
	want := "root (0.308ms)\n  · half (+0.154ms)"
	if got := telemetry.RenderTrace([]telemetry.StoredSpan{root}); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRender_EmptyInputReturnsEmptyString(t *testing.T) {
	if got := telemetry.RenderTrace(nil); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
	if got := telemetry.RenderTrace([]telemetry.StoredSpan{}); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestRender_EscapesControlCharactersInIdsNamesAttributeKeysAndErrorMessages(t *testing.T) {
	hostile := "\u001b]0;PWNED\u0007\u001b[2J"
	rendered := telemetry.RenderTrace([]telemetry.StoredSpan{
		trendSpan("root", func(s *telemetry.StoredSpan) {
			s.Name = "name" + hostile
			s.Attributes = thlpAttrs("key"+hostile, hostile)
			s.Events = []telemetry.StoredSpanEvent{{Name: "event" + hostile, TimeMs: 1, Attributes: thlpAttrs()}}
			s.Links = []telemetry.StoredSpanLink{{TraceId: hostile, SpanId: "span\u009b" + hostile}}
			s.Status = &telemetry.SpanStatusError{
				Status: telemetry.SpanStatusKindError,
				Error: &telemetry.SpanError{
					Name:    "error" + hostile,
					Message: "message\u007f" + hostile,
				},
			}
		}),
	})
	for _, line := range strings.Split(rendered, "\n") {
		for i := 0; i < len(line); {
			r, size := utf8.DecodeRuneInString(line[i:])
			if (r >= 0x00 && r <= 0x1f) || (r >= 0x7f && r <= 0x9f) {
				t.Fatalf("unexpected raw control rune %U in line %q", r, line)
			}
			i += size
		}
	}
	wants := []string{
		`name\u001b]0;PWNED\u0007\u001b[2J`,
		`→ link trace \u001b]0;PWNED`,
		`span\u009b`,
		`message\u007f`,
		`key\u001b]0;PWNED\u0007\u001b[2J="\u001b]0;PWNED\u0007\u001b[2J"`,
	}
	for _, w := range wants {
		if !strings.Contains(rendered, w) {
			t.Fatalf("expected rendered trace to contain %q, got:\n%s", w, rendered)
		}
	}
}
