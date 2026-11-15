// Ported from packages/durable/src/harness/usage.ts (pi v1.0.0).

package durable

import (
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// UsageState is the ledger of one conversation's own spend: its assistant/compaction model spend and tool spend.
type UsageState struct {
	Models map[string]ai.Usage `json:"models"`
	Tools  map[string]ai.Usage `json:"tools"`
}

// NewUsageState returns an initialized empty UsageState with non-nil maps.
func NewUsageState() *UsageState {
	return &UsageState{
		Models: make(map[string]ai.Usage),
		Tools:  make(map[string]ai.Usage),
	}
}

// UsageDoc is the built-in per-conversation ledger document ("pi.usage").
var UsageDoc = docuMustDefineDoc(DocDefinition{
	Kind:    "pi.usage",
	Version: 1,
	Scope:   DocumentScopeConversation,
	History: DocumentHistoryLatest,
	Fork:    DocumentForkInitial,
	Initial: func() JsonObject {
		return jsonx.ObjectOf("models", jsonx.NewObject(), "tools", jsonx.NewObject())
	},
	CheckpointWhen: func(_ JsonObject, _ []chord.Op, _ CheckpointInfo) bool {
		return true
	},
})

// huRecordUsage adds usage to one bucket ("models" or "tools") of conversationId's pi.usage document.
func huRecordUsage(tx Tx, conversationId ConversationId, bucket string, key string, usage ai.Usage) error {
	draft, err := tx.Doc(UsageDoc, conversationId)
	if err != nil {
		return err
	}
	var bucketObj *jsonx.Object
	if rawBucket, ok := draft.Value.Get(bucket); ok {
		if existing, isObj := rawBucket.(*jsonx.Object); isObj && existing != nil {
			bucketObj = existing
		}
	}
	if bucketObj == nil {
		bucketObj = jsonx.NewObject()
		draft.Value.Set(bucket, bucketObj)
	}
	rawTotal, exists := bucketObj.Get(key)
	var next ai.Usage
	if !exists || rawTotal == nil {
		next = huCopyUsage(usage)
	} else {
		if err := jsonx.Decode(rawTotal, &next); err != nil {
			return fmt.Errorf("decode %s usage for %q: %w", bucket, key, err)
		}
		AddUsage(&next, usage)
	}
	encoded, err := jsonx.Encode(next)
	if err != nil {
		return err
	}
	bucketObj.Set(key, encoded)
	return nil
}

// AddUsage accumulates every counter of usage into total. Optional counters appear once either side reports them.
func AddUsage(total *ai.Usage, usage ai.Usage) {
	if total == nil {
		return
	}
	total.Input += usage.Input
	total.Output += usage.Output
	total.CacheRead += usage.CacheRead
	total.CacheWrite += usage.CacheWrite
	total.TotalTokens += usage.TotalTokens
	if usage.CacheWrite1h != nil {
		cur := 0
		if total.CacheWrite1h != nil {
			cur = *total.CacheWrite1h
		}
		v := cur + *usage.CacheWrite1h
		total.CacheWrite1h = &v
	}
	if usage.Reasoning != nil {
		cur := 0
		if total.Reasoning != nil {
			cur = *total.Reasoning
		}
		v := cur + *usage.Reasoning
		total.Reasoning = &v
	}
	total.Cost.Input += usage.Cost.Input
	total.Cost.Output += usage.Cost.Output
	total.Cost.CacheRead += usage.Cost.CacheRead
	total.Cost.CacheWrite += usage.Cost.CacheWrite
	total.Cost.Total += usage.Cost.Total
	if usage.Cost.TotalFromProvider != nil {
		b := *usage.Cost.TotalFromProvider
		if total.Cost.TotalFromProvider == nil || b {
			total.Cost.TotalFromProvider = &b
		}
	}
}

// AddUsageState adds every bucket of state into sum.
func AddUsageState(sum *UsageState, state UsageState) {
	if sum == nil {
		return
	}
	if sum.Models == nil {
		sum.Models = make(map[string]ai.Usage)
	}
	if sum.Tools == nil {
		sum.Tools = make(map[string]ai.Usage)
	}
	for k, u := range state.Models {
		if total, exists := sum.Models[k]; exists {
			AddUsage(&total, u)
			sum.Models[k] = total
		} else {
			sum.Models[k] = huCopyUsage(u)
		}
	}
	for k, u := range state.Tools {
		if total, exists := sum.Tools[k]; exists {
			AddUsage(&total, u)
			sum.Tools[k] = total
		} else {
			sum.Tools[k] = huCopyUsage(u)
		}
	}
}

func huCopyUsage(u ai.Usage) ai.Usage {
	out := u
	if u.CacheWrite1h != nil {
		v := *u.CacheWrite1h
		out.CacheWrite1h = &v
	}
	if u.Reasoning != nil {
		v := *u.Reasoning
		out.Reasoning = &v
	}
	if u.Cost.TotalFromProvider != nil {
		v := *u.Cost.TotalFromProvider
		out.Cost.TotalFromProvider = &v
	}
	return out
}

func huDecodeUsageState(obj JsonObject) (*UsageState, error) {
	state := NewUsageState()
	if obj == nil {
		return state, nil
	}
	if err := jsonx.Decode(obj, state); err != nil {
		return nil, err
	}
	if state.Models == nil {
		state.Models = make(map[string]ai.Usage)
	}
	if state.Tools == nil {
		state.Tools = make(map[string]ai.Usage)
	}
	return state, nil
}
