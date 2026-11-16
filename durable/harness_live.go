// Ported from packages/durable/src/harness/live.ts (pi v1.0.0).

package durable

import (
	"fmt"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// ToolSlotStatus describes the live status of one tool slot in pi.live.tools.
type ToolSlotStatus string

const (
	ToolSlotPending ToolSlotStatus = "pending"
	ToolSlotRunning ToolSlotStatus = "running"
	ToolSlotDone    ToolSlotStatus = "done"
)

// ToolSlot is the live presentation of one tool call of the current round.
type ToolSlot struct {
	CallId       string           `json:"callId"`
	Name         string           `json:"name"`
	TaskId       *TaskId          `json:"taskId,omitzero"`
	Status       ToolSlotStatus   `json:"status"`
	Output       *string          `json:"output,omitzero"`
	DroppedBytes *int             `json:"droppedBytes,omitzero"`
	DroppedLines *int             `json:"droppedLines,omitzero"`
	Details      JsonValue        `json:"details,omitzero"`
	Diagnostics  []ToolDiagnostic `json:"diagnostics,omitzero"`
	Entry        *EntryId         `json:"entry,omitzero"`
}

// LiveRetryStatus describes durable backoff state for a retrying attempt.
type LiveRetryStatus struct {
	At    int64  `json:"at"`
	Error string `json:"error"`
}

// CompactionStatus is the live presentation of an in-flight compaction task.
type CompactionStatus struct {
	TaskId   TaskId           `json:"taskId"`
	Reason   CompactionReason `json:"reason"`
	Blocking bool             `json:"blocking"`
	Attempt  int              `json:"attempt"`
	Retry    *LiveRetryStatus `json:"retry,omitzero"`
}

// LiveRunState records the active generation task and the placed input submissions it settles.
type LiveRunState struct {
	TaskId TaskId         `json:"taskId"`
	Inputs []SubmissionId `json:"inputs"`
}

// LiveDeferredStatus describes deferred response polling state.
type LiveDeferredStatus struct {
	PollAt int64 `json:"pollAt"`
}

// LiveGenerationState presents the current generation attempt.
type LiveGenerationState struct {
	Attempt  int                  `json:"attempt"`
	Message  *ai.AssistantMessage `json:"message,omitzero"`
	Retry    *LiveRetryStatus     `json:"retry,omitzero"`
	Deferred *LiveDeferredStatus  `json:"deferred,omitzero"`
}

// LiveState is the built-in conversation live state document ("pi.live").
type LiveState struct {
	Run         *LiveRunState        `json:"run,omitzero"`
	Generation  *LiveGenerationState `json:"generation,omitzero"`
	Tools       []ToolSlot           `json:"tools,omitzero"`
	Compactions []CompactionStatus   `json:"compactions,omitzero"`
}

// LiveDoc is the built-in live conversation state document ("pi.live").
var LiveDoc = docuMustDefineDoc(DocDefinition{
	Kind:    "pi.live",
	Version: 1,
	Scope:   DocumentScopeConversation,
	History: DocumentHistoryLatest,
	Fork:    DocumentForkInitial,
	Initial: func() JsonObject {
		return jsonx.NewObject()
	},
	CheckpointWhen: func(value JsonObject, _ []chord.Op, _ CheckpointInfo) bool {
		if value == nil {
			return true
		}
		if gen, ok := value.Get("generation"); ok && gen != nil {
			return false
		}
		if toolsRaw, ok := value.Get("tools"); ok && toolsRaw != nil {
			if arr, isArr := toolsRaw.([]any); isArr {
				for _, item := range arr {
					if obj, isObj := item.(*jsonx.Object); isObj && obj != nil {
						if st, ok := obj.Get("status"); ok && st == string(ToolSlotRunning) {
							return false
						}
					}
				}
			}
		}
		return true
	},
})

const (
	hlToolTaskKind       = "pi.tool"
	hlCompactionTaskKind = "pi.compaction"
)

var hlRunTaskKinds = map[string]struct{}{
	"pi.generation": {},
}

// EndRun ends the run owned by taskId in live: settles each input, deletes run, and clears generation/tools.
func EndRun(tx Tx, live *jsonx.Object, taskId TaskId, settlement SubmissionSettlement) error {
	if live == nil {
		return nil
	}
	if runObj := hlGetObj(live, "run"); runObj != nil {
		if tid, ok := hlGetInt64(runObj, "taskId"); ok && TaskId(tid) == taskId {
			if rawInputs, ok := runObj.Get("inputs"); ok {
				if arr, isArr := rawInputs.([]any); isArr {
					for _, item := range arr {
						if sid, ok := hlToInt64(item); ok {
							if err := tx.SettleSubmission(SubmissionId(sid), settlement); err != nil {
								return err
							}
						}
					}
				}
			}
			live.Delete("run")
		}
	}
	live.Delete("generation")
	live.Delete("tools")
	return nil
}

// AddCompactionStatus appends a CompactionStatus to live.compactions in task ID order.
func AddCompactionStatus(live *jsonx.Object, status CompactionStatus) error {
	if live == nil {
		return nil
	}
	encoded, err := jsonx.Encode(status)
	if err != nil {
		return err
	}
	var list []any
	if raw, ok := live.Get("compactions"); ok {
		if arr, isArr := raw.([]any); isArr {
			list = append([]any(nil), arr...)
		}
	}
	list = append(list, encoded)
	live.Set("compactions", list)
	return nil
}

// GetCompactionStatus returns the live.compactions element (*jsonx.Object) for taskId, if present.
func GetCompactionStatus(live *jsonx.Object, taskId TaskId) (*jsonx.Object, bool) {
	if live == nil {
		return nil, false
	}
	raw, ok := live.Get("compactions")
	if !ok {
		return nil, false
	}
	arr, isArr := raw.([]any)
	if !isArr {
		return nil, false
	}
	for _, item := range arr {
		if obj, isObj := item.(*jsonx.Object); isObj && obj != nil {
			if tid, has := hlGetInt64(obj, "taskId"); has && TaskId(tid) == taskId {
				return obj, true
			}
		}
	}
	return nil, false
}

// RemoveCompactionStatus removes taskId from live.compactions, deleting the compactions field when empty.
func RemoveCompactionStatus(live *jsonx.Object, taskId TaskId) {
	if live == nil {
		return
	}
	raw, ok := live.Get("compactions")
	if !ok {
		return
	}
	arr, isArr := raw.([]any)
	if !isArr {
		return
	}
	idx := -1
	for i, item := range arr {
		if obj, isObj := item.(*jsonx.Object); isObj && obj != nil {
			if tid, has := hlGetInt64(obj, "taskId"); has && TaskId(tid) == taskId {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return
	}
	if len(arr) == 1 {
		live.Delete("compactions")
		return
	}
	next := make([]any, 0, len(arr)-1)
	next = append(next, arr[:idx]...)
	next = append(next, arr[idx+1:]...)
	live.Set("compactions", next)
}

// GetToolSlot returns the live.tools slot (*jsonx.Object) matching taskId, if listed.
func GetToolSlot(live *jsonx.Object, taskId TaskId) (*jsonx.Object, bool) {
	if live == nil {
		return nil, false
	}
	raw, ok := live.Get("tools")
	if !ok {
		return nil, false
	}
	arr, isArr := raw.([]any)
	if !isArr {
		return nil, false
	}
	for _, item := range arr {
		if obj, isObj := item.(*jsonx.Object); isObj && obj != nil {
			if tid, has := hlGetInt64(obj, "taskId"); has && TaskId(tid) == taskId {
				return obj, true
			}
		}
	}
	return nil, false
}

// FinishSlot marks a tool slot done, recording entry when non-nil and clearing progress fields.
func FinishSlot(slot *jsonx.Object, entry *EntryId) {
	if slot == nil {
		return
	}
	slot.Set("status", string(ToolSlotDone))
	if entry != nil {
		slot.Set("entry", float64(*entry))
	}
	ClearSlotProgress(slot)
}

// ClearSlotProgress removes transient running output, truncation counts, details, and diagnostics.
func ClearSlotProgress(slot *jsonx.Object) {
	if slot == nil {
		return
	}
	slot.Delete("output")
	slot.Delete("droppedBytes")
	slot.Delete("droppedLines")
	slot.Delete("details")
	slot.Delete("diagnostics")
}

// hlSettleSchedulerOutcome performs Harness cleanup when the scheduler writes a terminal faulted or orphaned outcome.
func hlSettleSchedulerOutcome(tx *Transaction, record TaskRecord, outcome TaskOutcome) error {
	switch record.Kind {
	case hlToolTaskKind:
		draft, err := tx.Doc(LiveDoc, record.ConversationId)
		if err != nil {
			return err
		}
		if slot, ok := GetToolSlot(draft.Value, record.Id); ok {
			FinishSlot(slot, nil)
		}
		return nil
	case hlCompactionTaskKind:
		draft, err := tx.Doc(LiveDoc, record.ConversationId)
		if err != nil {
			return err
		}
		RemoveCompactionStatus(draft.Value, record.Id)
		return nil
	default:
		if _, isRun := hlRunTaskKinds[record.Kind]; !isRun {
			return nil
		}
	}

	draft, err := tx.Doc(LiveDoc, record.ConversationId)
	if err != nil {
		return err
	}
	runObj := hlGetObj(draft.Value, "run")
	if runObj == nil {
		return nil
	}
	tid, ok := hlGetInt64(runObj, "taskId")
	if !ok || TaskId(tid) != record.Id {
		return nil
	}
	if err := hgConvertPartial(tx, draft.Value, record.ConversationId); err != nil {
		return err
	}

	reason := "faulted"
	var detail JsonValue
	if outcome.Status == TaskOutcomeFaulted && outcome.Error != nil {
		detail = outcome.Error.Message
	} else if outcome.Reason != nil {
		reason = *outcome.Reason
	} else {
		reason = fmt.Sprint(outcome.Status)
	}
	return EndRun(tx, draft.Value, record.Id, SubmissionSettlement{
		Status: SubmissionStatusUnanswered,
		Reason: &reason,
		Detail: detail,
	})
}

func hlGetObj(obj *jsonx.Object, key string) *jsonx.Object {
	if obj == nil {
		return nil
	}
	v, ok := obj.Get(key)
	if !ok || v == nil {
		return nil
	}
	child, _ := v.(*jsonx.Object)
	return child
}

func hlGetInt64(obj *jsonx.Object, key string) (int64, bool) {
	if obj == nil {
		return 0, false
	}
	v, ok := obj.Get(key)
	if !ok {
		return 0, false
	}
	return hlToInt64(v)
}

func hlToInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}
