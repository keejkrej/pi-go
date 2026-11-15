// Ported from packages/coding-agent/src/core/usage-totals.ts (pi v1.5.2).

package core

import (
	"cmp"
	"slices"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/omap"
)

// UsageTotals aggregates input, output, cache token counts, and total dollar cost.
type UsageTotals struct {
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	CacheRead  int     `json:"cacheRead"`
	CacheWrite int     `json:"cacheWrite"`
	Cost       float64 `json:"cost"`
}

// UsageCostBreakdownEntry summarizes accumulated cost and tokens for a model bucket or summary group.
type UsageCostBreakdownEntry struct {
	Key    string  `json:"key"`
	Cost   float64 `json:"cost"`
	Tokens int     `json:"tokens"`
}

// CreateUsageTotals initializes a zero-valued UsageTotals struct.
func CreateUsageTotals() UsageTotals {
	return UsageTotals{}
}

// AddUsageToTotals accumulates usage token counts and total cost into totals.
func AddUsageToTotals(totals *UsageTotals, usage ai.Usage) {
	if totals == nil {
		return
	}
	totals.Input += usage.Input
	totals.Output += usage.Output
	totals.CacheRead += usage.CacheRead
	totals.CacheWrite += usage.CacheWrite
	totals.Cost += usage.Cost.Total
}

// CombineUsage returns the sum of first and second, keeping optional token splits
// when either side reports them and setting TotalFromProvider only when both do.
func CombineUsage(first, second ai.Usage) ai.Usage {
	var cacheWrite1h *int
	if first.CacheWrite1h != nil || second.CacheWrite1h != nil {
		var v int
		if first.CacheWrite1h != nil {
			v += *first.CacheWrite1h
		}
		if second.CacheWrite1h != nil {
			v += *second.CacheWrite1h
		}
		cacheWrite1h = &v
	}

	var reasoning *int
	if first.Reasoning != nil || second.Reasoning != nil {
		var v int
		if first.Reasoning != nil {
			v += *first.Reasoning
		}
		if second.Reasoning != nil {
			v += *second.Reasoning
		}
		reasoning = &v
	}

	totalFromProvider := (first.Cost.TotalFromProvider != nil && *first.Cost.TotalFromProvider) &&
		(second.Cost.TotalFromProvider != nil && *second.Cost.TotalFromProvider)

	return ai.Usage{
		Input:        first.Input + second.Input,
		Output:       first.Output + second.Output,
		CacheRead:    first.CacheRead + second.CacheRead,
		CacheWrite:   first.CacheWrite + second.CacheWrite,
		CacheWrite1h: cacheWrite1h,
		Reasoning:    reasoning,
		TotalTokens:  first.TotalTokens + second.TotalTokens,
		Cost: ai.UsageCost{
			Input:             first.Cost.Input + second.Cost.Input,
			Output:            first.Cost.Output + second.Cost.Output,
			CacheRead:         first.Cost.CacheRead + second.Cost.CacheRead,
			CacheWrite:        first.Cost.CacheWrite + second.Cost.CacheWrite,
			Total:             first.Cost.Total + second.Cost.Total,
			TotalFromProvider: &totalFromProvider,
		},
	}
}

// GetUsageCostBreakdown groups model-attributed usage by provider/model and other usage into "Tools/summaries",
// excluding zero-cost zero-token buckets and sorting descending by cost.
func GetUsageCostBreakdown(entries []SessionEntry) []UsageCostBreakdownEntry {
	totalsByKey := omap.NewMap[string, *UsageTotals]()

	for _, entry := range entries {
		var key string
		var usage *ai.Usage

		switch e := entry.(type) {
		case *SessionMessageEntry:
			switch msg := e.Message.(type) {
			case *ai.AssistantMessage:
				modelName := msg.Model
				if msg.ResponseModel != nil && *msg.ResponseModel != "" {
					modelName = *msg.ResponseModel
				}
				key = string(msg.Provider) + "/" + modelName
				u := msg.Usage
				usage = &u
			case *ai.ToolResultMessage:
				if msg.Usage != nil {
					key = "Tools/summaries"
					usage = msg.Usage
				}
			}
		case *UsageEntry:
			key = e.Provider + "/" + e.Model
			u := e.Usage
			usage = &u
		case *BranchSummaryEntry:
			if e.Usage != nil {
				key = "Tools/summaries"
				usage = e.Usage
			}
		case *CompactionEntry:
			if e.Usage != nil {
				key = "Tools/summaries"
				usage = e.Usage
			}
		}

		if key == "" || usage == nil {
			continue
		}
		totals, ok := totalsByKey.Get(key)
		if !ok {
			t := CreateUsageTotals()
			totals = &t
			totalsByKey.Set(key, totals)
		}
		AddUsageToTotals(totals, *usage)
	}

	out := make([]UsageCostBreakdownEntry, 0, totalsByKey.Len())
	for key, totals := range totalsByKey.All() {
		tokens := totals.Input + totals.Output + totals.CacheRead + totals.CacheWrite
		if totals.Cost > 0 || tokens > 0 {
			out = append(out, UsageCostBreakdownEntry{
				Key:    key,
				Cost:   totals.Cost,
				Tokens: tokens,
			})
		}
	}
	slices.SortFunc(out, func(a, b UsageCostBreakdownEntry) int {
		return cmp.Compare(b.Cost, a.Cost)
	})
	return out
}
