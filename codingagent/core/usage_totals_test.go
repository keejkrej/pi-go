package core

import (
	"math"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

func tutBoolPtr(v bool) *bool { return &v }
func tutIntPtr(v int) *int    { return &v }
func tutStrPtr(v string) *string {
	return &v
}

func tutApproxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-12
}

func TestUsageTotalsAccumulation(t *testing.T) {
	totals := CreateUsageTotals()
	AddUsageToTotals(&totals, ai.Usage{
		Input:       100,
		Output:      50,
		CacheRead:   200,
		CacheWrite:  25,
		TotalTokens: 375,
		Cost: ai.UsageCost{
			Input:      0.1,
			Output:     0.2,
			CacheRead:  0.05,
			CacheWrite: 0.03,
			Total:      0.38,
		},
	})
	AddUsageToTotals(&totals, ai.Usage{
		Input:       40,
		Output:      10,
		CacheRead:   20,
		CacheWrite:  5,
		TotalTokens: 75,
		Cost: ai.UsageCost{
			Input:      0.04,
			Output:     0.05,
			CacheRead:  0.01,
			CacheWrite: 0.01,
			Total:      0.11,
		},
	})

	if totals.Input != 140 || totals.Output != 60 || totals.CacheRead != 220 || totals.CacheWrite != 30 {
		t.Fatalf("unexpected token totals: %+v", totals)
	}
	if !tutApproxEqual(totals.Cost, 0.49) {
		t.Fatalf("totals.Cost = %v, want 0.49", totals.Cost)
	}
}

func TestCombineUsage(t *testing.T) {
	combined := CombineUsage(
		ai.Usage{
			Input:        100,
			Output:       50,
			CacheRead:    200,
			CacheWrite:   25,
			CacheWrite1h: tutIntPtr(10),
			TotalTokens:  375,
			Cost: ai.UsageCost{
				Input:             0.1,
				Output:            0.2,
				CacheRead:         0.05,
				CacheWrite:        0.03,
				Total:             0.38,
				TotalFromProvider: tutBoolPtr(true),
			},
		},
		ai.Usage{
			Input:       40,
			Output:      10,
			CacheRead:   20,
			CacheWrite:  5,
			Reasoning:   tutIntPtr(7),
			TotalTokens: 75,
			Cost: ai.UsageCost{
				Input:             0.04,
				Output:            0.05,
				CacheRead:         0.01,
				CacheWrite:        0.01,
				Total:             0.11,
				TotalFromProvider: tutBoolPtr(true),
			},
		},
	)

	if combined.Input != 140 || combined.Output != 60 || combined.CacheRead != 220 ||
		combined.CacheWrite != 30 || combined.TotalTokens != 450 {
		t.Fatalf("unexpected combined counts: %+v", combined)
	}
	if combined.CacheWrite1h == nil || *combined.CacheWrite1h != 10 {
		t.Fatalf("CacheWrite1h = %v", combined.CacheWrite1h)
	}
	if combined.Reasoning == nil || *combined.Reasoning != 7 {
		t.Fatalf("Reasoning = %v", combined.Reasoning)
	}
	if combined.Cost.TotalFromProvider == nil || !*combined.Cost.TotalFromProvider {
		t.Fatalf("expected TotalFromProvider=true when both are true: %v", combined.Cost.TotalFromProvider)
	}
	if !tutApproxEqual(combined.Cost.Input, 0.14) ||
		!tutApproxEqual(combined.Cost.Output, 0.25) ||
		!tutApproxEqual(combined.Cost.CacheRead, 0.06) ||
		!tutApproxEqual(combined.Cost.CacheWrite, 0.04) ||
		!tutApproxEqual(combined.Cost.Total, 0.49) {
		t.Fatalf("unexpected combined cost: %+v", combined.Cost)
	}

	mixedProviderFlag := CombineUsage(
		ai.Usage{Cost: ai.UsageCost{TotalFromProvider: tutBoolPtr(true)}},
		ai.Usage{Cost: ai.UsageCost{TotalFromProvider: tutBoolPtr(false)}},
	)
	if mixedProviderFlag.Cost.TotalFromProvider == nil || *mixedProviderFlag.Cost.TotalFromProvider {
		t.Fatalf("expected TotalFromProvider=false when one side is false: %v", mixedProviderFlag.Cost.TotalFromProvider)
	}
}

func TestGetUsageCostBreakdown(t *testing.T) {
	entries := []SessionEntry{
		&SessionMessageEntry{
			SessionEntryBase: SessionEntryBase{Type: "message", Id: "m1"},
			Message: &ai.AssistantMessage{
				Provider:      "anthropic",
				Model:         "claude-alias",
				ResponseModel: tutStrPtr("claude-sonnet-4-5"),
				Usage: ai.Usage{
					Input: 100,
					Cost:  ai.UsageCost{Total: 0.50},
				},
			},
		},
		&SessionMessageEntry{
			SessionEntryBase: SessionEntryBase{Type: "message", Id: "m2"},
			Message: &ai.ToolResultMessage{
				Usage: &ai.Usage{
					Input: 20,
					Cost:  ai.UsageCost{Total: 0.05},
				},
			},
		},
		&UsageEntry{
			SessionEntryBase: SessionEntryBase{Type: "usage", Id: "u1"},
			Kind:             "warm",
			Provider:         "openai",
			Model:            "gpt-5",
			Usage: ai.Usage{
				Output: 50,
				Cost:   ai.UsageCost{Total: 0.90},
			},
		},
		&BranchSummaryEntry{
			SessionEntryBase: SessionEntryBase{Type: "branch_summary", Id: "b1"},
			Usage: &ai.Usage{
				CacheRead: 10,
				Cost:      ai.UsageCost{Total: 0.10},
			},
		},
		&CompactionEntry{
			SessionEntryBase: SessionEntryBase{Type: "compaction", Id: "c1"},
			Usage: &ai.Usage{
				CacheWrite: 5,
				Cost:       ai.UsageCost{Total: 0.05},
			},
		},
		// Zero-cost, zero-token entries must be omitted
		&UsageEntry{
			SessionEntryBase: SessionEntryBase{Type: "usage", Id: "u0"},
			Kind:             "empty",
			Provider:         "zero",
			Model:            "none",
			Usage:            ai.Usage{},
		},
	}

	breakdown := GetUsageCostBreakdown(entries)
	if len(breakdown) != 3 {
		t.Fatalf("len(breakdown) = %d, want 3 (%+v)", len(breakdown), breakdown)
	}
	if breakdown[0].Key != "openai/gpt-5" || !tutApproxEqual(breakdown[0].Cost, 0.90) || breakdown[0].Tokens != 50 {
		t.Fatalf("breakdown[0] = %+v", breakdown[0])
	}
	if breakdown[1].Key != "anthropic/claude-sonnet-4-5" || !tutApproxEqual(breakdown[1].Cost, 0.50) || breakdown[1].Tokens != 100 {
		t.Fatalf("breakdown[1] = %+v", breakdown[1])
	}
	if breakdown[2].Key != "Tools/summaries" || !tutApproxEqual(breakdown[2].Cost, 0.20) || breakdown[2].Tokens != 35 {
		t.Fatalf("breakdown[2] = %+v", breakdown[2])
	}
}
