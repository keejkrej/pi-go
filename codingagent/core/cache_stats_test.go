package core

import (
	"math"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

type tcsModelPriceMap map[string]*ai.Model

func (m tcsModelPriceMap) GetModel(provider, modelId string) *ai.Model {
	return m[provider+"/"+modelId]
}

func tcsNewAssistantEntry(id string, tsMs int64, provider ai.ProviderId, modelId string, usage ai.Usage, stopReason ...ai.StopReason) *SessionMessageEntry {
	sr := ai.StopReasonStop
	if len(stopReason) > 0 {
		sr = stopReason[0]
	}
	return &SessionMessageEntry{
		SessionEntryBase: SessionEntryBase{Type: "message", Id: id, Timestamp: "2026-01-01T00:00:00Z"},
		Message: &ai.AssistantMessage{
			Role:       "assistant",
			Provider:   provider,
			Model:      modelId,
			Api:        "anthropic-messages",
			StopReason: sr,
			Timestamp:  tsMs,
			Usage:      usage,
		},
	}
}

func TestComputeCacheWasteAndMisses(t *testing.T) {
	models := tcsModelPriceMap{
		"anthropic/claude-sonnet": {
			BaseModel: ai.BaseModel{
				Id:       "claude-sonnet",
				Provider: "anthropic",
				Cost:     ai.ModelCost{Input: 3.0, CacheRead: 0.3},
			},
		},
	}

	baseTs := int64(1_700_000_000_000)
	entries := []SessionEntry{
		tcsNewAssistantEntry("m1", baseTs, "anthropic", "claude-sonnet", ai.Usage{
			Input:      5000,
			CacheWrite: 10000,
			CacheRead:  0,
		}),
		// 10 minutes later (> CacheTtlMs) with zero cacheRead -> counted miss.
		tcsNewAssistantEntry("m2", baseTs+CacheTtlMs+60_000, "anthropic", "claude-sonnet", ai.Usage{
			Input:      15000,
			CacheRead:  0,
			CacheWrite: 0,
			Cost:       ai.UsageCost{Input: 0.045},
		}),
	}

	totals := ComputeCacheWaste(entries, models)
	if totals.MissCount != 1 {
		t.Fatalf("expected 1 miss, got %+v", totals)
	}
	if totals.MissedTokens != 15000 {
		t.Fatalf("expected 15000 missed tokens, got %d", totals.MissedTokens)
	}
	// Delta cost per 1M = 3.0 - 0.3 = 2.7 -> 15000 / 1e6 * 2.7 = 0.0405
	if math.Abs(totals.MissedCost-0.0405) > 1e-9 {
		t.Fatalf("expected MissedCost ~0.0405, got %v", totals.MissedCost)
	}

	misses := CollectCacheMisses(entries, models)
	secondMsg := entries[1].(*SessionMessageEntry).Message.(*ai.AssistantMessage)
	miss := misses[secondMsg]
	if miss.IdleMs != CacheTtlMs+60_000 || miss.ModelChanged {
		t.Fatalf("unexpected CacheMiss metadata: %+v", miss)
	}

	single := DetectCacheMiss(entries, secondMsg, models)
	if single == nil || single.MissedTokens != 15000 {
		t.Fatalf("expected DetectCacheMiss to match, got %+v", single)
	}
}

func TestCacheStatsResetsOnCompactionAndTracksWarmUsage(t *testing.T) {
	baseTs := int64(1_700_000_000_000)
	entries := []SessionEntry{
		tcsNewAssistantEntry("m1", baseTs, "anthropic", "claude-sonnet", ai.Usage{
			Input:      5000,
			CacheWrite: 5000,
		}),
		&CompactionEntry{
			SessionEntryBase: SessionEntryBase{Type: "compaction", Id: "c1", Timestamp: "2026-01-01T00:01:00Z"},
		},
		tcsNewAssistantEntry("m2", baseTs+CacheTtlMs+60_000, "anthropic", "claude-sonnet", ai.Usage{
			Input: 10000,
		}),
	}
	// Compaction reset prev, and m2 did not report cache either -> 0 misses.
	if totals := ComputeCacheWaste(entries, nil); totals.MissCount != 0 {
		t.Fatalf("expected 0 misses after compaction reset, got %+v", totals)
	}

	// cache_warm UsageEntry sets previous request baseline, then model change is flagged on miss.
	warmEntries := []SessionEntry{
		&UsageEntry{
			SessionEntryBase: SessionEntryBase{Type: "usage", Id: "u1", Timestamp: "2026-01-01T00:00:00Z"},
			Kind:             "cache_warm",
			Provider:         "anthropic",
			Model:            "claude-sonnet",
			Usage:            ai.Usage{Input: 4000, CacheRead: 4000},
		},
		tcsNewAssistantEntry("m3", baseTs+10_000, "anthropic", "claude-opus", ai.Usage{
			Input:      8000,
			CacheRead:  0,
			CacheWrite: 0,
		}),
	}
	misses := CollectCacheMisses(warmEntries, nil)
	m3 := warmEntries[1].(*SessionMessageEntry).Message.(*ai.AssistantMessage)
	miss, ok := misses[m3]
	if !ok || !miss.ModelChanged || miss.MissedTokens != 8000 {
		t.Fatalf("expected cache_warm -> model change miss, got ok=%v miss=%+v", ok, miss)
	}
}
