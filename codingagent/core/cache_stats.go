// Ported from packages/coding-agent/src/core/cache-stats.ts (pi v1.5.2).

package core

import (
	"fmt"
	"math"
	"time"

	"github.com/keejkrej/pi-go/ai"
)

// CacheTtlMs is the prompt-cache idle threshold (5 minutes).
const CacheTtlMs int64 = 5 * 60 * 1000

const csNoiseFloorTokens = 1024

// CacheMiss describes a counted prompt-cache miss on one assistant message.
type CacheMiss struct {
	MissedTokens int     `json:"missedTokens"`
	MissedCost   float64 `json:"missedCost"`
	IdleMs       int64   `json:"idleMs"`
	ModelChanged bool    `json:"modelChanged"`
}

// CacheWasteTotals aggregates prompt-cache waste across a session.
type CacheWasteTotals struct {
	MissedTokens int     `json:"missedTokens"`
	MissedCost   float64 `json:"missedCost"`
	MissCount    int     `json:"missCount"`
}

// ModelPriceSource provides minimal pricing lookup for cache miss cost estimation.
type ModelPriceSource interface {
	GetModel(provider, modelId string) *ai.Model
}

type csPreviousRequest struct {
	promptTokens  int
	modelKey      string
	timestamp     int64
	reportedCache bool
}

func csDetectMiss(prev *csPreviousRequest, message *ai.AssistantMessage, models ModelPriceSource) *CacheMiss {
	if prev == nil || message == nil {
		return nil
	}
	usage := message.Usage
	promptTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	if promptTokens <= 0 || (usage.CacheRead+usage.CacheWrite == 0 && !prev.reportedCache) {
		return nil
	}

	minPrompt := prev.promptTokens
	if promptTokens < minPrompt {
		minPrompt = promptTokens
	}
	missedTokens := minPrompt - usage.CacheRead
	if missedTokens <= csNoiseFloorTokens {
		return nil
	}

	paidTokens := usage.Input + usage.CacheWrite
	var paidPerToken float64
	if paidTokens > 0 {
		paidPerToken = (usage.Cost.Input + usage.Cost.CacheWrite) / float64(paidTokens)
	}
	var readPerToken float64
	if usage.CacheRead > 0 {
		readPerToken = usage.Cost.CacheRead / float64(usage.CacheRead)
	} else if models != nil {
		if model := models.GetModel(string(message.Provider), message.Model); model != nil {
			readPerToken = model.Cost.CacheRead / 1_000_000
		}
	}

	idleMs := message.Timestamp - prev.timestamp
	if idleMs < 0 {
		idleMs = 0
	}
	modelKey := fmt.Sprintf("%s/%s", message.Provider, message.Model)
	return &CacheMiss{
		MissedTokens: missedTokens,
		MissedCost:   float64(missedTokens) * math.Max(0, paidPerToken-readPerToken),
		IdleMs:       idleMs,
		ModelChanged: modelKey != prev.modelKey,
	}
}

func csAsPreviousRequest(message *ai.AssistantMessage, reportedCache bool) *csPreviousRequest {
	if message == nil {
		return nil
	}
	usage := message.Usage
	promptTokens := usage.Input + usage.CacheRead + usage.CacheWrite
	if promptTokens <= 0 {
		return nil
	}
	return &csPreviousRequest{
		promptTokens:  promptTokens,
		modelKey:      fmt.Sprintf("%s/%s", message.Provider, message.Model),
		timestamp:     message.Timestamp,
		reportedCache: reportedCache || usage.CacheRead+usage.CacheWrite > 0,
	}
}

type csScanResult struct {
	prev   *csPreviousRequest
	totals CacheWasteTotals
	misses map[*ai.AssistantMessage]CacheMiss
}

func csScan(entries []SessionEntry, models ModelPriceSource) csScanResult {
	var prev *csPreviousRequest
	totals := CacheWasteTotals{}
	misses := make(map[*ai.AssistantMessage]CacheMiss)

	for _, entry := range entries {
		switch e := entry.(type) {
		case *CompactionEntry, *BranchSummaryEntry:
			prev = nil
		case *UsageEntry:
			if e != nil && e.Kind == "cache_warm" {
				promptTokens := e.Usage.Input + e.Usage.CacheRead + e.Usage.CacheWrite
				if promptTokens > 0 {
					var ts int64
					if parsed, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
						ts = parsed.UnixMilli()
					}
					prev = &csPreviousRequest{
						promptTokens:  promptTokens,
						modelKey:      fmt.Sprintf("%s/%s", e.Provider, e.Model),
						timestamp:     ts,
						reportedCache: true,
					}
				}
			}
		case *SessionMessageEntry:
			if e == nil {
				continue
			}
			assistant, ok := e.Message.(*ai.AssistantMessage)
			if !ok || assistant == nil {
				continue
			}
			if miss := csDetectMiss(prev, assistant, models); miss != nil {
				totals.MissedTokens += miss.MissedTokens
				totals.MissedCost += miss.MissedCost
				totals.MissCount++
				misses[assistant] = *miss
			}
			var prevReported bool
			if prev != nil {
				prevReported = prev.reportedCache
			}
			if next := csAsPreviousRequest(assistant, prevReported); next != nil {
				prev = next
			}
		}
	}
	return csScanResult{prev: prev, totals: totals, misses: misses}
}

// ComputeCacheWaste computes cumulative prompt cache waste across entries.
func ComputeCacheWaste(entries []SessionEntry, models ModelPriceSource) CacheWasteTotals {
	return csScan(entries, models).totals
}

// CollectCacheMisses collects all counted cache misses across entries keyed by AssistantMessage pointer.
func CollectCacheMisses(entries []SessionEntry, models ModelPriceSource) map[*ai.AssistantMessage]CacheMiss {
	return csScan(entries, models).misses
}

// DetectCacheMiss detects a cache miss on a just-completed assistant message relative to entries.
func DetectCacheMiss(entries []SessionEntry, message *ai.AssistantMessage, models ModelPriceSource) *CacheMiss {
	return csDetectMiss(csScan(entries, models).prev, message, models)
}
