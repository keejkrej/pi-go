// Ported from packages/durable/src/harness/inbox.ts (pi v1.0.0).

package durable

import (
	"slices"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// InboxMode describes one queued item's mode in pi.inbox.items.
type InboxMode string

const (
	InboxModeSteer    InboxMode = "steer"
	InboxModeFollowUp InboxMode = "followUp"
	InboxModeWrite    InboxMode = "write"
)

// InboxItem represents a queued submission waiting for a placement boundary.
type InboxItem struct {
	Id      SubmissionId `json:"id"`
	Mode    InboxMode    `json:"mode"`
	Content JsonValue    `json:"content,omitzero"`
	Entry   JsonObject   `json:"entry,omitzero"`
}

// InboxState is the built-in queue document shape ("pi.inbox").
type InboxState struct {
	Items []InboxItem `json:"items"`
}

// InboxDoc is the built-in conversation queue of submissions waiting for a boundary ("pi.inbox").
var InboxDoc = docuMustDefineDoc(DocDefinition{
	Kind:    "pi.inbox",
	Version: 1,
	Scope:   DocumentScopeConversation,
	History: DocumentHistoryLatest,
	Fork:    DocumentForkInitial,
	Initial: func() JsonObject {
		return jsonx.ObjectOf("items", []any{})
	},
	CheckpointWhen: func(value JsonObject, _ []chord.Op, _ CheckpointInfo) bool {
		if value == nil {
			return true
		}
		raw, ok := value.Get("items")
		if !ok || raw == nil {
			return true
		}
		arr, isArr := raw.([]any)
		return !isArr || len(arr) == 0
	},
})

// QueueModes selects steering and follow-up drainage counts at a boundary.
type QueueModes struct {
	SteeringMode QueueMode
	FollowUpMode QueueMode
}

// BoundaryAt names which boundary is placing queued inbox items.
type BoundaryAt string

const (
	BoundaryPostTools BoundaryAt = "postTools"
	BoundaryFinal     BoundaryAt = "final"
)

// Boundary holds inbox state and active head read before the commit's first table write.
type Boundary struct {
	ConversationId ConversationId
	Inbox          *jsonx.Object
	SteeringMode   QueueMode
	FollowUpMode   QueueMode
	Head           *EntryId
}

// BoundaryResult reports placed user submissions and whether a reset write was placed.
type BoundaryResult struct {
	Users []SubmissionId
	Reset bool
}

// PrepareBoundary reads the latest head marker and acquires the inbox document draft.
// Callers must run this before the commit's first table write.
func PrepareBoundary(tx Tx, conversationId ConversationId, modes QueueModes) (*Boundary, error) {
	marker, err := tx.LatestHeadMarker(conversationId)
	if err != nil {
		return nil, err
	}
	var head *EntryId
	if marker != nil && marker.Head != nil {
		h := *marker.Head
		head = &h
	}
	inbox, err := tx.Doc(InboxDoc, conversationId)
	if err != nil {
		return nil, err
	}
	return &Boundary{
		ConversationId: conversationId,
		Inbox:          inbox.Value,
		SteeringMode:   modes.SteeringMode,
		FollowUpMode:   modes.FollowUpMode,
		Head:           head,
	}, nil
}

// ApplyBoundary places queued writes (then selected steer/follow-up inputs) in ID order,
// settling stale writes and removing handled items from the inbox.
func ApplyBoundary(tx Tx, boundary *Boundary, at BoundaryAt, now int64) (BoundaryResult, error) {
	items := hiItemsSlice(boundary.Inbox)
	reset := false
	for _, raw := range items {
		obj, ok := raw.(*jsonx.Object)
		if !ok || obj == nil {
			continue
		}
		if mode, _ := obj.Get("mode"); mode == string(InboxModeWrite) {
			if entryObj, ok := obj.Get("entry"); ok {
				if eo, isObj := entryObj.(*jsonx.Object); isObj && eo != nil {
					if hv, hasHead := eo.Get("head"); hasHead && hv == "self" {
						reset = true
						break
					}
				}
			}
		}
	}

	isFinal := at == BoundaryFinal || reset
	var writes []int
	var steers []int
	var followUps []int
	for idx, raw := range items {
		obj, ok := raw.(*jsonx.Object)
		if !ok || obj == nil {
			continue
		}
		mode, _ := obj.Get("mode")
		switch mode {
		case string(InboxModeWrite):
			writes = append(writes, idx)
		case string(InboxModeSteer):
			steers = append(steers, idx)
		case string(InboxModeFollowUp):
			followUps = append(followUps, idx)
		}
	}

	pick := func(indices []int, qm QueueMode) []int {
		if qm == QueueModeAll || len(indices) <= 1 {
			return indices
		}
		return indices[:1]
	}

	var users []int
	users = append(users, pick(steers, boundary.SteeringMode)...)
	if isFinal {
		users = append(users, pick(followUps, boundary.FollowUpMode)...)
	}
	slices.Sort(users)

	for _, idx := range writes {
		obj := items[idx].(*jsonx.Object)
		sid, _ := hiItemId(obj)
		rawEntry, _ := obj.Get("entry")
		var draft EntryDraft
		if err := jsonx.Decode(rawEntry, &draft); err != nil {
			return BoundaryResult{}, err
		}
		if IsStaleEntry(boundary, draft) {
			reason := "stale"
			if err := tx.SettleSubmission(sid, SubmissionSettlement{
				Status: SubmissionStatusUnanswered,
				Reason: &reason,
			}); err != nil {
				return BoundaryResult{}, err
			}
			continue
		}
		entry, err := tx.AppendEntry(boundary.ConversationId, draft)
		if err != nil {
			return BoundaryResult{}, err
		}
		if draft.Head.IsSelf() {
			h := entry.Id
			boundary.Head = &h
		} else if hid, ok := draft.Head.Id(); ok {
			h := hid
			boundary.Head = &h
		}
		if err := tx.PlaceSubmission(sid, entry.Id); err != nil {
			return BoundaryResult{}, err
		}
	}

	placed := make([]SubmissionId, 0, len(users))
	for _, idx := range users {
		obj := items[idx].(*jsonx.Object)
		sid, _ := hiItemId(obj)
		rawContent, _ := obj.Get("content")
		var content UserInput
		if err := jsonx.Decode(rawContent, &content); err != nil {
			return BoundaryResult{}, err
		}
		userMsg := &ai.UserMessage{
			Role:      "user",
			Content:   content,
			Timestamp: now,
		}
		entry, err := tx.AppendTypedEntry(UserEntry, boundary.ConversationId, EntryDraft{
			Model: []ai.Message{userMsg},
		})
		if err != nil {
			return BoundaryResult{}, err
		}
		if err := tx.PlaceSubmission(sid, entry.Id); err != nil {
			return BoundaryResult{}, err
		}
		placed = append(placed, sid)
	}

	removedSet := make(map[int]struct{}, len(writes)+len(users))
	for _, idx := range writes {
		removedSet[idx] = struct{}{}
	}
	for _, idx := range users {
		removedSet[idx] = struct{}{}
	}
	if len(removedSet) > 0 {
		remaining := make([]any, 0, len(items)-len(removedSet))
		for idx, item := range items {
			if _, drop := removedSet[idx]; !drop {
				remaining = append(remaining, item)
			}
		}
		boundary.Inbox.Set("items", remaining)
	}

	return BoundaryResult{Users: placed, Reset: reset}, nil
}

// IsStaleEntry reports whether a draft's head marker targets an entry older than the active range.
func IsStaleEntry(boundary *Boundary, entry EntryDraft) bool {
	if boundary == nil || boundary.Head == nil {
		return false
	}
	if hid, ok := entry.Head.Id(); ok {
		return hid < *boundary.Head
	}
	return false
}

// RemoveInboxItem removes one withdrawn submission's item from conversationId's pi.inbox.
func RemoveInboxItem(tx Tx, conversationId ConversationId, id SubmissionId) error {
	draft, err := tx.Doc(InboxDoc, conversationId)
	if err != nil {
		return err
	}
	items := hiItemsSlice(draft.Value)
	for i, raw := range items {
		if obj, ok := raw.(*jsonx.Object); ok && obj != nil {
			if itemId, ok := hiItemId(obj); ok && itemId == id {
				next := make([]any, 0, len(items)-1)
				next = append(next, items[:i]...)
				next = append(next, items[i+1:]...)
				draft.Value.Set("items", next)
				return nil
			}
		}
	}
	return nil
}

// hiWithdrawQueuedInputs settles queued inputs as unanswered/aborted and leaves queued writes intact.
func hiWithdrawQueuedInputs(tx *Transaction, conversationId ConversationId) error {
	draft, err := tx.Doc(InboxDoc, conversationId)
	if err != nil {
		return err
	}
	items := hiItemsSlice(draft.Value)
	if len(items) == 0 {
		return nil
	}
	remaining := make([]any, 0, len(items))
	changed := false
	for _, raw := range items {
		obj, ok := raw.(*jsonx.Object)
		if !ok || obj == nil {
			remaining = append(remaining, raw)
			continue
		}
		if mode, _ := obj.Get("mode"); mode == string(InboxModeWrite) {
			remaining = append(remaining, raw)
			continue
		}
		sid, ok := hiItemId(obj)
		if ok {
			reason := "aborted"
			if err := tx.SettleSubmission(sid, SubmissionSettlement{
				Status: SubmissionStatusUnanswered,
				Reason: &reason,
			}); err != nil {
				return err
			}
		}
		changed = true
	}
	if changed {
		draft.Value.Set("items", remaining)
	}
	return nil
}

func hiItemsSlice(inbox *jsonx.Object) []any {
	if inbox == nil {
		return nil
	}
	raw, ok := inbox.Get("items")
	if !ok || raw == nil {
		return nil
	}
	arr, _ := raw.([]any)
	return arr
}

func hiItemId(obj *jsonx.Object) (SubmissionId, bool) {
	n, ok := hlGetInt64(obj, "id")
	if !ok {
		return 0, false
	}
	return SubmissionId(n), true
}
