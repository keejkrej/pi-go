// Ported from packages/durable/src/harness/context.ts (pi v1.0.0).

package durable

import (
	"fmt"
	"slices"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	hxScanPageSize      = 256
	hxMissingResultText = "Tool result unavailable: history ends before this call completed."
)

// hxContextBounds holds the newest visible head marker and tail entry ID fixing a committed context range.
type hxContextBounds struct {
	Head *EntryRecord
	Tail EntryId
}

// hxCaptureContextBounds captures committed bounds on the Session line using two O(1) reads.
func hxCaptureContextBounds(
	ctx chord.Context,
	storage Storage,
	conversationId ConversationId,
	at ...EntryId,
) (*hxContextBounds, error) {
	var tail EntryId
	if len(at) == 0 {
		page, err := storage.ScanEntries(ctx, EntryQuery{ConversationId: conversationId}, 1, nil)
		if err != nil {
			return nil, err
		}
		if len(page.Items) == 0 {
			return nil, nil
		}
		tail = page.Items[0].Id
	} else {
		target := at[0]
		found, err := storage.EntryInConversation(ctx, conversationId, target)
		if err != nil {
			return nil, err
		}
		if found == nil {
			return nil, fmt.Errorf("Entry %d is not visible from conversation %d", target, conversationId)
		}
		tail = target
	}
	head, err := storage.FindLatestHeadMarker(ctx, conversationId, &tail)
	if err != nil {
		return nil, err
	}
	return &hxContextBounds{Head: head, Tail: tail}, nil
}

// hxReadContext captures context bounds on the Session line and derives the active context off it.
func hxReadContext(
	ctx chord.Context,
	session *SessionImpl,
	storage Storage,
	conversationId ConversationId,
	at ...EntryId,
) (*ContextView, error) {
	var bounds *hxContextBounds
	if err := session.ReadOnLine(func() error {
		b, err := hxCaptureContextBounds(ctx, storage, conversationId, at...)
		if err != nil {
			return err
		}
		bounds = b
		return nil
	}); err != nil {
		return nil, err
	}
	return hxDeriveContext(ctx, storage, conversationId, bounds)
}

// hxDeriveContext derives the active transcript and model context within captured bounds.
func hxDeriveContext(
	ctx chord.Context,
	storage Storage,
	conversationId ConversationId,
	bounds *hxContextBounds,
) (*ContextView, error) {
	if bounds == nil {
		return &ContextView{
			Entries:       []EntryRecord{},
			Contributions: [][]ai.Message{},
			Messages:      []ai.Message{},
		}, nil
	}
	head := bounds.Head
	rng, err := hxScanRange(ctx, storage, conversationId, bounds)
	if err != nil {
		return nil, err
	}
	edits := make(map[EntryId]ContextEdit)
	for _, entry := range rng {
		for _, edit := range entry.Edits {
			edits[edit.Target] = edit
		}
	}

	entries := hxSelectActive(head, rng)
	contributions := make([][]ai.Message, len(entries))
	var flat []ai.Message
	for i, entry := range entries {
		var contributed []ai.Message
		if edit, hasEdit := edits[entry.Id]; hasEdit {
			if edit.Action == ContextEditActionOmit {
				contributions[i] = []ai.Message{}
				continue
			}
			contributed = edit.Messages
		} else {
			contributed = entry.Model
		}
		kept := make([]ai.Message, 0, len(contributed))
		for _, msg := range contributed {
			if am, ok := msg.(*ai.AssistantMessage); ok && am != nil {
				switch am.StopReason {
				case ai.StopReasonAborted, ai.StopReasonError, ai.StopReasonDeferred:
					continue
				}
			}
			kept = append(kept, msg)
		}
		contributions[i] = kept
		flat = append(flat, kept...)
	}
	return &ContextView{
		Head:          head,
		Entries:       entries,
		Contributions: contributions,
		Messages:      OrderToolResults(flat),
	}, nil
}

// hxActiveEntries returns the raw active entries within captured bounds without deriving model context.
func hxActiveEntries(
	ctx chord.Context,
	storage Storage,
	conversationId ConversationId,
	bounds *hxContextBounds,
) ([]EntryRecord, error) {
	if bounds == nil {
		return []EntryRecord{}, nil
	}
	rng, err := hxScanRange(ctx, storage, conversationId, bounds)
	if err != nil {
		return nil, err
	}
	return hxSelectActive(bounds.Head, rng), nil
}

func hxScanRange(
	ctx chord.Context,
	storage Storage,
	conversationId ConversationId,
	bounds *hxContextBounds,
) ([]EntryRecord, error) {
	tail := bounds.Tail
	query := EntryQuery{
		ConversationId: conversationId,
		MaxEntryId:     &tail,
	}
	if bounds.Head != nil && bounds.Head.Head != nil {
		minId := *bounds.Head.Head
		query.MinEntryId = &minId
	}
	var items []EntryRecord
	var cursor Cursor
	for {
		page, err := storage.ScanEntries(ctx, query, hxScanPageSize, cursor)
		if err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		if page.Next == nil {
			break
		}
		cursor = page.Next
	}
	slices.Reverse(items)
	return items, nil
}

func hxSelectActive(head *EntryRecord, rng []EntryRecord) []EntryRecord {
	if head == nil {
		return rng
	}
	out := make([]EntryRecord, 0, len(rng)+1)
	out = append(out, *head)
	for _, entry := range rng {
		if entry.Head == nil {
			out = append(out, entry)
		}
	}
	return out
}

// OrderToolResults places each assistant message's tool results directly after it in call order,
// synthesizing error results for missing calls and dropping unmatched results.
func OrderToolResults(messages []ai.Message) []ai.Message {
	ordered := make([]ai.Message, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		if msg == nil {
			continue
		}
		if _, isTR := msg.(*ai.ToolResultMessage); isTR {
			continue
		}
		ordered = append(ordered, msg)
		am, isAsst := msg.(*ai.AssistantMessage)
		if !isAsst {
			continue
		}
		var calls []*ai.ToolCall
		for _, block := range am.Content {
			if tc, ok := block.(*ai.ToolCall); ok && tc != nil {
				calls = append(calls, tc)
			}
		}
		if len(calls) == 0 {
			continue
		}
		results := make(map[string]int)
		for next := i + 1; next < len(messages); next++ {
			cand := messages[next]
			if _, ok := cand.(*ai.AssistantMessage); ok {
				break
			}
			if tr, ok := cand.(*ai.ToolResultMessage); ok && tr != nil {
				if _, seen := results[tr.ToolCallId]; !seen {
					results[tr.ToolCallId] = next
				}
			}
		}
		for _, call := range calls {
			if idx, ok := results[call.Id]; ok {
				ordered = append(ordered, messages[idx])
			} else {
				ordered = append(ordered, hxMissingResult(call, am.Timestamp))
			}
		}
	}
	return ordered
}

func hxMissingResult(call *ai.ToolCall, timestamp int64) *ai.ToolResultMessage {
	return &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: call.Id,
		ToolName:   call.Name,
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: hxMissingResultText}},
		Details:    jsonx.Some[any](jsonx.ObjectOf("reason", "missing_result")),
		IsError:    true,
		Timestamp:  timestamp,
	}
}
