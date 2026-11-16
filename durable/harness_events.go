// Ported from packages/durable/src/harness/events.ts (pi v1.0.0).

package durable

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// QueuedItem summarizes a queued inbox item in snapshots and inbox updates.
type QueuedItem struct {
	Id   SubmissionId `json:"id"`
	Mode InboxMode    `json:"mode"`
}

// SnapshotRun identifies the inputs of the currently active run in SnapshotEvent.
type SnapshotRun struct {
	Inputs []SubmissionId `json:"inputs"`
}

// MessageChange describes one incremental change to the in-flight assistant message.
type MessageChange struct {
	Type         string               `json:"type"`
	ContentIndex *int                 `json:"contentIndex,omitzero"`
	Block        ai.Content           `json:"block,omitzero"`
	Delta        *string              `json:"delta,omitzero"`
	Path         chord.Path           `json:"path,omitzero"`
	Message      *ai.AssistantMessage `json:"message,omitzero"`
}

// ToolOutputUpdate describes an incremental output change in a running tool slot.
type ToolOutputUpdate struct {
	TrimStart *int    `json:"trimStart,omitzero"`
	Append    *string `json:"append,omitzero"`
	Set       *string `json:"set,omitzero"`
}

// AgentEvent is one experimental durable conversation event emitted by WatchEvents.
type AgentEvent interface {
	isDurableAgentEvent()
	AgentEventType() string
}

// SnapshotEvent is the initial conversation state snapshot captured at WatchEvents attachment.
type SnapshotEvent struct {
	Type        string               `json:"type"`
	Entries     []EntryRecord        `json:"entries"`
	Run         *SnapshotRun         `json:"run,omitzero"`
	Generation  *LiveGenerationState `json:"generation,omitzero"`
	Tools       []ToolSlot           `json:"tools"`
	Compactions []CompactionStatus   `json:"compactions"`
	Inbox       []QueuedItem         `json:"inbox"`
	Agent       AgentState           `json:"agent"`
	Usage       UsageState           `json:"usage"`
}

func (*SnapshotEvent) isDurableAgentEvent()   {}
func (*SnapshotEvent) AgentEventType() string { return "snapshot" }

type RunStartEvent struct {
	Type   string         `json:"type"`
	Inputs []SubmissionId `json:"inputs"`
}

func (*RunStartEvent) isDurableAgentEvent()   {}
func (*RunStartEvent) AgentEventType() string { return "run_start" }

type RunEndEvent struct {
	Type   string         `json:"type"`
	Inputs []SubmissionId `json:"inputs"`
}

func (*RunEndEvent) isDurableAgentEvent()   {}
func (*RunEndEvent) AgentEventType() string { return "run_end" }

type TurnStartEvent struct {
	Type string `json:"type"`
}

func (*TurnStartEvent) isDurableAgentEvent()   {}
func (*TurnStartEvent) AgentEventType() string { return "turn_start" }

type TurnEndEvent struct {
	Type string `json:"type"`
}

func (*TurnEndEvent) isDurableAgentEvent()   {}
func (*TurnEndEvent) AgentEventType() string { return "turn_end" }

type MessageStartEvent struct {
	Type    string     `json:"type"`
	Message ai.Message `json:"message"`
}

func (*MessageStartEvent) isDurableAgentEvent()   {}
func (*MessageStartEvent) AgentEventType() string { return "message_start" }

type MessageUpdateEvent struct {
	Type    string          `json:"type"`
	Usage   ai.Usage        `json:"usage"`
	Changes []MessageChange `json:"changes"`
}

func (*MessageUpdateEvent) isDurableAgentEvent()   {}
func (*MessageUpdateEvent) AgentEventType() string { return "message_update" }

type MessageEndEvent struct {
	Type  string      `json:"type"`
	Entry EntryRecord `json:"entry"`
}

func (*MessageEndEvent) isDurableAgentEvent()   {}
func (*MessageEndEvent) AgentEventType() string { return "message_end" }

type ToolExecutionStartEvent struct {
	Type       string     `json:"type"`
	ToolCallId string     `json:"toolCallId"`
	ToolName   string     `json:"toolName"`
	Args       JsonObject `json:"args"`
}

func (*ToolExecutionStartEvent) isDurableAgentEvent()   {}
func (*ToolExecutionStartEvent) AgentEventType() string { return "tool_execution_start" }

type ToolExecutionUpdateEvent struct {
	Type        string                      `json:"type"`
	ToolCallId  string                      `json:"toolCallId"`
	ToolName    string                      `json:"toolName"`
	Output      *ToolOutputUpdate           `json:"output,omitzero"`
	Details     jsonx.Opt[JsonValue]        `json:"details,omitzero"`
	Diagnostics jsonx.Opt[[]ToolDiagnostic] `json:"diagnostics,omitzero"`
}

func (*ToolExecutionUpdateEvent) isDurableAgentEvent()   {}
func (*ToolExecutionUpdateEvent) AgentEventType() string { return "tool_execution_update" }

type ToolExecutionEndEvent struct {
	Type       string       `json:"type"`
	ToolCallId string       `json:"toolCallId"`
	ToolName   string       `json:"toolName"`
	Entry      *EntryRecord `json:"entry,omitzero"`
}

func (*ToolExecutionEndEvent) isDurableAgentEvent()   {}
func (*ToolExecutionEndEvent) AgentEventType() string { return "tool_execution_end" }

type InboxUpdateEvent struct {
	Type  string       `json:"type"`
	Items []QueuedItem `json:"items"`
}

func (*InboxUpdateEvent) isDurableAgentEvent()   {}
func (*InboxUpdateEvent) AgentEventType() string { return "inbox_update" }

type SubmissionEvent struct {
	Type   string           `json:"type"`
	Record SubmissionRecord `json:"record"`
}

func (*SubmissionEvent) isDurableAgentEvent()   {}
func (*SubmissionEvent) AgentEventType() string { return "submission" }

type AutoRetryStartEvent struct {
	Type         string `json:"type"`
	Attempt      int    `json:"attempt"`
	At           int64  `json:"at"`
	ErrorMessage string `json:"errorMessage"`
}

func (*AutoRetryStartEvent) isDurableAgentEvent()   {}
func (*AutoRetryStartEvent) AgentEventType() string { return "auto_retry_start" }

type AutoRetryEndEvent struct {
	Type    string `json:"type"`
	Attempt int    `json:"attempt"`
}

func (*AutoRetryEndEvent) isDurableAgentEvent()   {}
func (*AutoRetryEndEvent) AgentEventType() string { return "auto_retry_end" }

type DeferredPollEvent struct {
	Type   string `json:"type"`
	PollAt int64  `json:"pollAt"`
}

func (*DeferredPollEvent) isDurableAgentEvent()   {}
func (*DeferredPollEvent) AgentEventType() string { return "deferred_poll" }

type EntryAppendedEvent struct {
	Type  string      `json:"type"`
	Entry EntryRecord `json:"entry"`
}

func (*EntryAppendedEvent) isDurableAgentEvent()   {}
func (*EntryAppendedEvent) AgentEventType() string { return "entry_appended" }

type AgentChangedEvent struct {
	Type  string     `json:"type"`
	Agent AgentState `json:"agent"`
}

func (*AgentChangedEvent) isDurableAgentEvent()   {}
func (*AgentChangedEvent) AgentEventType() string { return "agent_changed" }

type UsageChangedEvent struct {
	Type  string     `json:"type"`
	Usage UsageState `json:"usage"`
}

func (*UsageChangedEvent) isDurableAgentEvent()   {}
func (*UsageChangedEvent) AgentEventType() string { return "usage_changed" }

type TaskFailedEvent struct {
	Type    string `json:"type"`
	TaskId  TaskId `json:"taskId"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func (*TaskFailedEvent) isDurableAgentEvent()   {}
func (*TaskFailedEvent) AgentEventType() string { return "task_failed" }

type CompactionStartEvent struct {
	Type     string           `json:"type"`
	TaskId   TaskId           `json:"taskId"`
	Reason   CompactionReason `json:"reason"`
	Blocking bool             `json:"blocking"`
}

func (*CompactionStartEvent) isDurableAgentEvent()   {}
func (*CompactionStartEvent) AgentEventType() string { return "compaction_start" }

type CompactionEndEvent struct {
	Type   string           `json:"type"`
	TaskId TaskId           `json:"taskId"`
	Reason CompactionReason `json:"reason"`
}

func (*CompactionEndEvent) isDurableAgentEvent()   {}
func (*CompactionEndEvent) AgentEventType() string { return "compaction_end" }

// AgentEventStream is a serialized stream of one conversation's event batches, one per commit.
type AgentEventStream interface {
	Snapshot() SnapshotEvent
	Start(listener func(events []AgentEvent, ctx chord.Context) error) error
	Stop() WatchEnd
	Closed() WatchEnd
}

type heAgentEventStreamImpl struct {
	snapshot SnapshotEvent
	watch    *soCommittedWatch[[]AgentEvent]
}

func (s *heAgentEventStreamImpl) Snapshot() SnapshotEvent {
	return s.snapshot
}

func (s *heAgentEventStreamImpl) Start(listener func(events []AgentEvent, ctx chord.Context) error) error {
	return s.watch.Start(func(events []AgentEvent, _ []chord.Op, ctx chord.Context) error {
		return listener(events, ctx)
	})
}

func (s *heAgentEventStreamImpl) Stop() WatchEnd {
	return s.watch.Stop()
}

func (s *heAgentEventStreamImpl) Closed() WatchEnd {
	return s.watch.Closed()
}

type heEventsObserver struct {
	conversationId ConversationId
	held           map[TaskId]struct{}
	watch          *soCommittedWatch[[]AgentEvent]

	mu      sync.Mutex
	current ConversationView
}

func (o *heEventsObserver) Advance(_ ConversationView, _ []chord.Op, _ chord.Context) {}

func (o *heEventsObserver) Publication(
	before ConversationView,
	after ConversationView,
	ops []chord.Op,
	publication CommitPublication,
	commitCtx chord.Context,
) {
	o.mu.Lock()
	o.current = after
	o.mu.Unlock()
	events := heTranslate(o.conversationId, before, after, ops, publication, o.held)
	if len(events) > 0 {
		o.watch.Advance(events, nil, commitCtx)
	}
}

func (o *heEventsObserver) CloseSession() {
	o.watch.CloseSession()
}

// WatchEvents attaches to one conversation's agent events. Snapshot and commit registration are
// captured atomically on the Session line; overflow replaces undelivered batches with a snapshot.
func WatchEvents(
	harness Harness,
	conversationId ConversationId,
	ctx chord.Context,
) (AgentEventStream, error) {
	views, err := GetConversationViews(harness)
	if err != nil {
		return nil, err
	}
	var snapshot SnapshotEvent
	obs, _, err := hvAttach(views, conversationId, func(initial ConversationView, release func(), storage Storage) (*heEventsObserver, error) {
		kind := GenerationTask.TaskKind()
		completing, err := hutScanAll(func(cursor Cursor) (Page[TaskRecord, Cursor], error) {
			return storage.ScanTasks(ctx, TaskQuery{
				ConversationId: &conversationId,
				Kind:           &kind,
				Status:         TaskStateCompleting,
			}, 100, cursor)
		})
		if err != nil {
			return nil, err
		}
		held := make(map[TaskId]struct{}, len(completing))
		for _, rec := range completing {
			held[rec.Id] = struct{}{}
		}
		snap, err := heSnapshotOf(initial)
		if err != nil {
			return nil, err
		}
		snapshot = snap
		o := &heEventsObserver{
			conversationId: conversationId,
			held:           held,
			current:        initial,
		}
		o.watch = soNewCommittedWatch[[]AgentEvent](nil, release, func() []AgentEvent {
			o.mu.Lock()
			cur := o.current
			o.mu.Unlock()
			s, err := heSnapshotOf(cur)
			if err != nil {
				return []AgentEvent{&snapshot}
			}
			return []AgentEvent{&s}
		})
		return o, nil
	}, ctx)
	if err != nil {
		return nil, err
	}
	sig := hutAbortSignal(ctx)
	if sig != nil && sig.Err() != nil {
		obs.watch.Cancel()
		return nil, hutSignalErr(sig)
	}
	if sig != nil {
		_ = obs.watch.ObserveCancellation(sig)
	}
	return &heAgentEventStreamImpl{
		snapshot: snapshot,
		watch:    obs.watch,
	}, nil
}

type heParts struct {
	liveRaw   *jsonx.Object
	inboxRaw  *jsonx.Object
	agentRaw  *jsonx.Object
	usageRaw  *jsonx.Object
	live      LiveState
	inbox     InboxState
	agent     *AgentState
	usage     *UsageState
	slotRaw   map[string]*jsonx.Object
	slotOrder []string
}

func heExtractParts(view ConversationView) (heParts, error) {
	var p heParts
	p.liveRaw = view.Docs["pi.live"]
	p.inboxRaw = view.Docs["pi.inbox"]
	p.agentRaw = view.Docs["pi.agent"]
	p.usageRaw = view.Docs["pi.usage"]
	if p.liveRaw != nil {
		if err := jsonx.Decode(p.liveRaw, &p.live); err != nil {
			return p, fmt.Errorf("decode pi.live: %w", err)
		}
		if rawTools, ok := p.liveRaw.Get("tools"); ok {
			if arr, isArr := rawTools.([]any); isArr {
				p.slotRaw = make(map[string]*jsonx.Object, len(arr))
				for _, item := range arr {
					if obj, isObj := item.(*jsonx.Object); isObj && obj != nil {
						if cid, ok := obj.Get("callId"); ok {
							if s, isStr := cid.(string); isStr {
								p.slotRaw[s] = obj
								p.slotOrder = append(p.slotOrder, s)
							}
						}
					}
				}
			}
		}
	}
	if p.inboxRaw != nil {
		if err := jsonx.Decode(p.inboxRaw, &p.inbox); err != nil {
			return p, fmt.Errorf("decode pi.inbox: %w", err)
		}
	}
	if p.agentRaw != nil {
		st := haDecodeAgentState(p.agentRaw)
		p.agent = &st
	}
	if p.usageRaw != nil {
		st, err := huDecodeUsageState(p.usageRaw)
		if err != nil {
			return p, err
		}
		p.usage = st
	}
	return p, nil
}

func heSnapshotOf(view ConversationView) (SnapshotEvent, error) {
	p, err := heExtractParts(view)
	if err != nil {
		return SnapshotEvent{}, err
	}
	entries := view.Entries
	if entries == nil {
		entries = []EntryRecord{}
	}
	var run *SnapshotRun
	if p.live.Run != nil {
		inputs := append([]SubmissionId(nil), p.live.Run.Inputs...)
		if inputs == nil {
			inputs = []SubmissionId{}
		}
		run = &SnapshotRun{Inputs: inputs}
	}
	tools := p.live.Tools
	if tools == nil {
		tools = []ToolSlot{}
	}
	compactions := p.live.Compactions
	if compactions == nil {
		compactions = []CompactionStatus{}
	}
	var agent AgentState
	if p.agent != nil {
		agent = *p.agent
	}
	usage := *NewUsageState()
	if p.usage != nil {
		usage = *p.usage
	}
	return SnapshotEvent{
		Type:        "snapshot",
		Entries:     entries,
		Run:         run,
		Generation:  p.live.Generation,
		Tools:       tools,
		Compactions: compactions,
		Inbox:       heQueued(p.inbox),
		Agent:       agent,
		Usage:       usage,
	}, nil
}

func heQueued(inbox InboxState) []QueuedItem {
	out := make([]QueuedItem, 0, len(inbox.Items))
	for _, item := range inbox.Items {
		out = append(out, QueuedItem{Id: item.Id, Mode: item.Mode})
	}
	return out
}

func heResultOf(entries []EntryRecord, callId string) *EntryRecord {
	for i := range entries {
		if len(entries[i].Model) == 0 {
			continue
		}
		if tr, ok := entries[i].Model[0].(*ai.ToolResultMessage); ok && tr.ToolCallId == callId {
			return &entries[i]
		}
	}
	return nil
}

func heTranslate(
	conversationId ConversationId,
	before ConversationView,
	after ConversationView,
	viewOps []chord.Op,
	publication CommitPublication,
	held map[TaskId]struct{},
) []AgentEvent {
	var entries []EntryRecord
	tasks := make(map[TaskId]TaskRecord)
	var taskOrder []TaskRecord
	var submissions []SubmissionRecord
	for _, ch := range publication.Changes {
		switch c := ch.(type) {
		case StorageWriteEntry:
			if c.Value.ConversationId == conversationId {
				entries = append(entries, c.Value)
			}
		case *StorageWriteEntry:
			if c != nil && c.Value.ConversationId == conversationId {
				entries = append(entries, c.Value)
			}
		case StorageWriteTask:
			if c.Value.ConversationId == conversationId {
				if _, exists := tasks[c.Value.Id]; !exists {
					taskOrder = append(taskOrder, c.Value)
				} else {
					for i := range taskOrder {
						if taskOrder[i].Id == c.Value.Id {
							taskOrder[i] = c.Value
							break
						}
					}
				}
				tasks[c.Value.Id] = c.Value
			}
		case *StorageWriteTask:
			if c != nil && c.Value.ConversationId == conversationId {
				if _, exists := tasks[c.Value.Id]; !exists {
					taskOrder = append(taskOrder, c.Value)
				} else {
					for i := range taskOrder {
						if taskOrder[i].Id == c.Value.Id {
							taskOrder[i] = c.Value
							break
						}
					}
				}
				tasks[c.Value.Id] = c.Value
			}
		case StorageWriteSubmission:
			if c.Value.ConversationId == conversationId {
				submissions = append(submissions, c.Value)
			}
		case *StorageWriteSubmission:
			if c != nil && c.Value.ConversationId == conversationId {
				submissions = append(submissions, c.Value)
			}
		}
	}
	if len(viewOps) == 0 && len(entries) == 0 && len(tasks) == 0 && len(submissions) == 0 {
		return nil
	}
	slices.SortFunc(submissions, func(a, b SubmissionRecord) int {
		if a.Id < b.Id {
			return -1
		}
		if a.Id > b.Id {
			return 1
		}
		return 0
	})
	was, err1 := heExtractParts(before)
	now, err2 := heExtractParts(after)
	if err1 != nil || err2 != nil {
		return nil
	}
	var events []AgentEvent

	slotsBefore := make(map[string]ToolSlot, len(was.live.Tools))
	for _, slot := range was.live.Tools {
		slotsBefore[slot.CallId] = slot
	}
	slots := now.live.Tools
	for _, slot := range slots {
		prev, hasPrev := slotsBefore[slot.CallId]
		if slot.Status != ToolSlotRunning || (hasPrev && prev.Status == ToolSlotRunning) {
			continue
		}
		args := jsonx.NewObject()
		if slot.TaskId != nil {
			if taskRec, ok := tasks[*slot.TaskId]; ok {
				if cpObj, ok := taskRec.State.Checkpoint.(*jsonx.Object); ok && cpObj != nil {
					if rawArgs, hasArgs := cpObj.Get("arguments"); hasArgs {
						if aObj, ok := rawArgs.(*jsonx.Object); ok && aObj != nil {
							args = aObj
						}
					}
				}
			}
		}
		events = append(events, &ToolExecutionStartEvent{
			Type:       "tool_execution_start",
			ToolCallId: slot.CallId,
			ToolName:   slot.Name,
			Args:       args,
		})
	}

	var rawPartialBefore, rawPartialAfter any
	if was.liveRaw != nil {
		if genObj := hlGetObj(was.liveRaw, "generation"); genObj != nil {
			rawPartialBefore, _ = genObj.Get("message")
		}
	}
	if now.liveRaw != nil {
		if genObj := hlGetObj(now.liveRaw, "generation"); genObj != nil {
			rawPartialAfter, _ = genObj.Get("message")
		}
	}
	var partialBefore, partial *ai.AssistantMessage
	if was.live.Generation != nil {
		partialBefore = was.live.Generation.Message
	}
	if now.live.Generation != nil {
		partial = now.live.Generation.Message
	}
	if partial != nil && partialBefore == nil {
		events = append(events, &MessageStartEvent{
			Type:    "message_start",
			Message: partial,
		})
	} else if partial != nil && !heSameJsonRef(rawPartialAfter, rawPartialBefore) {
		events = append(events, &MessageUpdateEvent{
			Type:    "message_update",
			Usage:   partial.Usage,
			Changes: heMessageChanges(viewOps, partial),
		})
	}

	for idx, slot := range slots {
		prev, hasPrev := slotsBefore[slot.CallId]
		if slot.Status != ToolSlotRunning || !hasPrev || prev.Status != ToolSlotRunning {
			continue
		}
		var rawSlot, rawPrev *jsonx.Object
		if now.slotRaw != nil {
			rawSlot = now.slotRaw[slot.CallId]
		}
		if was.slotRaw != nil {
			rawPrev = was.slotRaw[slot.CallId]
		}
		if upd := heToolUpdate(viewOps, idx, rawSlot, rawPrev, slot, prev); upd != nil {
			events = append(events, upd)
		}
	}

	gen := now.live.Generation
	genBefore := was.live.Generation
	if gen != nil && gen.Retry != nil && (genBefore == nil || genBefore.Retry == nil) {
		events = append(events, &AutoRetryStartEvent{
			Type:         "auto_retry_start",
			Attempt:      gen.Attempt,
			At:           gen.Retry.At,
			ErrorMessage: gen.Retry.Error,
		})
	}
	if genBefore != nil && genBefore.Retry != nil && (gen == nil || gen.Retry == nil) {
		events = append(events, &AutoRetryEndEvent{
			Type:    "auto_retry_end",
			Attempt: genBefore.Attempt,
		})
	}
	if gen != nil && gen.Deferred != nil {
		if genBefore == nil || genBefore.Deferred == nil || gen.Deferred.PollAt != genBefore.Deferred.PollAt {
			events = append(events, &DeferredPollEvent{
				Type:   "deferred_poll",
				PollAt: gen.Deferred.PollAt,
			})
		}
	}

	slotsAfterByCall := make(map[string]ToolSlot, len(slots))
	for _, slot := range slots {
		slotsAfterByCall[slot.CallId] = slot
	}
	var toolEnds []*ToolExecutionEndEvent
	endTool := func(callId, name string, entryId *EntryId) {
		var entryPtr *EntryRecord
		if entryId != nil {
			for i := range entries {
				if entries[i].Id == *entryId {
					copyEntry := entries[i]
					entryPtr = &copyEntry
					break
				}
			}
		}
		toolEnds = append(toolEnds, &ToolExecutionEndEvent{
			Type:       "tool_execution_end",
			ToolCallId: callId,
			ToolName:   name,
			Entry:      entryPtr,
		})
	}
	for _, prev := range was.live.Tools {
		if prev.Status == ToolSlotDone {
			continue
		}
		if slot, exists := slotsAfterByCall[prev.CallId]; exists {
			if slot.Status == ToolSlotDone {
				endTool(prev.CallId, prev.Name, slot.Entry)
			}
		} else {
			res := heResultOf(entries, prev.CallId)
			var eid *EntryId
			if res != nil {
				eid = &res.Id
			}
			endTool(prev.CallId, prev.Name, eid)
		}
	}
	for _, slot := range slots {
		if _, existed := slotsBefore[slot.CallId]; !existed && slot.Status == ToolSlotDone {
			endTool(slot.CallId, slot.Name, slot.Entry)
		}
	}

	assistantAppended := false
	for _, entry := range entries {
		for _, end := range toolEnds {
			if end.Entry != nil && end.Entry.Id == entry.Id {
				events = append(events, end)
			}
		}
		if len(entry.Model) == 0 {
			events = append(events, &EntryAppendedEvent{
				Type:  "entry_appended",
				Entry: entry,
			})
			continue
		}
		message := entry.Model[0]
		streamed := message.GetRole() == "assistant" && partialBefore != nil && !assistantAppended
		if message.GetRole() == "assistant" {
			assistantAppended = true
		}
		if !streamed {
			events = append(events, &MessageStartEvent{
				Type:    "message_start",
				Message: message,
			})
		}
		events = append(events, &MessageEndEvent{
			Type:  "message_end",
			Entry: entry,
		})
	}
	for _, end := range toolEnds {
		if end.Entry == nil {
			events = append(events, end)
		}
	}

	compactionsBefore := was.live.Compactions
	compactions := now.live.Compactions
	for _, c := range compactionsBefore {
		found := false
		for _, cur := range compactions {
			if cur.TaskId == c.TaskId {
				found = true
				break
			}
		}
		if !found {
			events = append(events, &CompactionEndEvent{
				Type:   "compaction_end",
				TaskId: c.TaskId,
				Reason: c.Reason,
			})
		}
	}

	turnEnded := false
	for _, task := range taskOrder {
		status := task.State.Status
		if task.Kind == GenerationTask.TaskKind() && status == TaskStateCompleting {
			if _, ok := held[task.Id]; !ok {
				held[task.Id] = struct{}{}
				turnEnded = true
			}
		}
		if status != TaskStateTerminal {
			continue
		}
		if task.Kind == GenerationTask.TaskKind() {
			if _, ok := held[task.Id]; ok {
				delete(held, task.Id)
			} else {
				turnEnded = true
			}
		}
		if task.State.Outcome != nil && (task.State.Outcome.Status == TaskOutcomeFaulted || task.State.Outcome.Status == TaskOutcomeOrphaned) {
			msg := ""
			if task.State.Outcome.Status == TaskOutcomeFaulted && task.State.Outcome.Error != nil {
				msg = task.State.Outcome.Error.Message
			} else if task.State.Outcome.Reason != nil {
				msg = *task.State.Outcome.Reason
			}
			events = append(events, &TaskFailedEvent{
				Type:    "task_failed",
				TaskId:  task.Id,
				Kind:    task.Kind,
				Message: msg,
			})
		}
	}
	if turnEnded {
		events = append(events, &TurnEndEvent{Type: "turn_end"})
	}

	run := now.live.Run
	runBefore := was.live.Run
	runChanged := heFirstInput(run) != heFirstInput(runBefore) || (heHasFirstInput(run) != heHasFirstInput(runBefore))
	if runBefore != nil && runChanged {
		events = append(events, &RunEndEvent{
			Type:   "run_end",
			Inputs: append([]SubmissionId(nil), runBefore.Inputs...),
		})
	}

	for _, rec := range submissions {
		events = append(events, &SubmissionEvent{
			Type:   "submission",
			Record: rec,
		})
	}
	if !heSameJsonRef(now.inboxRaw, was.inboxRaw) {
		events = append(events, &InboxUpdateEvent{
			Type:  "inbox_update",
			Items: heQueued(now.inbox),
		})
	}
	if !heSameJsonRef(now.agentRaw, was.agentRaw) {
		var agent AgentState
		if now.agent != nil {
			agent = *now.agent
		}
		events = append(events, &AgentChangedEvent{
			Type:  "agent_changed",
			Agent: agent,
		})
	}
	if !heSameJsonRef(now.usageRaw, was.usageRaw) {
		usage := *NewUsageState()
		if now.usage != nil {
			usage = *now.usage
		}
		events = append(events, &UsageChangedEvent{
			Type:  "usage_changed",
			Usage: usage,
		})
	}
	for _, c := range compactions {
		found := false
		for _, prev := range compactionsBefore {
			if prev.TaskId == c.TaskId {
				found = true
				break
			}
		}
		if !found {
			events = append(events, &CompactionStartEvent{
				Type:     "compaction_start",
				TaskId:   c.TaskId,
				Reason:   c.Reason,
				Blocking: c.Blocking,
			})
		}
	}
	if run != nil && runChanged {
		events = append(events, &RunStartEvent{
			Type:   "run_start",
			Inputs: append([]SubmissionId(nil), run.Inputs...),
		})
	}
	if run != nil && (runBefore == nil || run.TaskId != runBefore.TaskId) {
		if taskRec, ok := tasks[run.TaskId]; ok && taskRec.Kind == GenerationTask.TaskKind() {
			events = append(events, &TurnStartEvent{Type: "turn_start"})
		}
	}
	return events
}

var hePartialPath = chord.Path{
	chord.SegString("docs"),
	chord.SegString("pi.live"),
	chord.SegString("generation"),
	chord.SegString("message"),
}

func heMessageChanges(viewOps []chord.Op, message *ai.AssistantMessage) []MessageChange {
	changes := []MessageChange{}
	whole := make(map[int]struct{})
	for _, op := range viewOps {
		path := heOpPath(op)
		if !heStartsWith(path, hePartialPath) {
			if heStartsWith(hePartialPath, path) {
				return []MessageChange{{Type: "message", Message: message}}
			}
			continue
		}
		rest := path[len(hePartialPath):]
		if len(rest) > 0 && rest[0] == chord.SegString("usage") {
			continue
		}
		if len(rest) == 0 || rest[0] != chord.SegString("content") {
			return []MessageChange{{Type: "message", Message: message}}
		}
		if len(rest) == 1 {
			sp, ok := op.(chord.OpSplice)
			if !ok || sp.Remove != 0 {
				return []MessageChange{{Type: "message", Message: message}}
			}
			for offset, rawItem := range sp.Items {
				blk, err := ai.DecodeContent(rawItem)
				if err != nil {
					return []MessageChange{{Type: "message", Message: message}}
				}
				typ := "toolcall_start"
				switch blk.(type) {
				case *ai.TextContent:
					typ = "text_start"
				case *ai.ThinkingContent:
					typ = "thinking_start"
				}
				idx := sp.Index + offset
				changes = append(changes, MessageChange{
					Type:         typ,
					ContentIndex: &idx,
					Block:        blk,
				})
			}
			continue
		}
		segNum, ok := rest[1].(chord.SegNumber)
		if !ok {
			return []MessageChange{{Type: "message", Message: message}}
		}
		contentIndex := int(segNum)
		if _, exists := whole[contentIndex]; exists {
			continue
		}
		var field string
		if len(rest) >= 3 {
			if s, ok := rest[2].(chord.SegString); ok {
				field = string(s)
			}
		}
		if app, ok := op.(chord.OpAppend); ok && len(rest) == 3 && (field == "text" || field == "thinking") {
			typ := "thinking_delta"
			if field == "text" {
				typ = "text_delta"
			}
			delta := app.Text
			changes = append(changes, MessageChange{
				Type:         typ,
				ContentIndex: &contentIndex,
				Delta:        &delta,
			})
		} else if app, ok := op.(chord.OpAppend); ok && field == "arguments" {
			delta := app.Text
			subPath := append(chord.Path(nil), rest[3:]...)
			changes = append(changes, MessageChange{
				Type:         "toolcall_delta",
				ContentIndex: &contentIndex,
				Path:         subPath,
				Delta:        &delta,
			})
		} else {
			whole[contentIndex] = struct{}{}
			var blk ai.Content
			if contentIndex >= 0 && contentIndex < len(message.Content) {
				blk = message.Content[contentIndex]
			}
			changes = append(changes, MessageChange{
				Type:         "block",
				ContentIndex: &contentIndex,
				Block:        blk,
			})
		}
	}
	return changes
}

func heToolUpdate(
	viewOps []chord.Op,
	index int,
	rawSlot *jsonx.Object,
	rawPrev *jsonx.Object,
	slot ToolSlot,
	previous ToolSlot,
) *ToolExecutionUpdateEvent {
	outputPath := chord.Path{
		chord.SegString("docs"),
		chord.SegString("pi.live"),
		chord.SegString("tools"),
		chord.SegNumber(index),
		chord.SegString("output"),
	}
	trimStart := 0
	var appendBuf strings.Builder
	set := false
	for _, op := range viewOps {
		if !heStartsWith(heOpPath(op), outputPath) {
			continue
		}
		switch o := op.(type) {
		case chord.OpTruncate:
			trimStart += o.Count
		case chord.OpAppend:
			appendBuf.WriteString(o.Text)
		default:
			set = true
		}
	}
	slotOutput := ""
	if slot.Output != nil {
		slotOutput = *slot.Output
	}
	prevOutput := ""
	if previous.Output != nil {
		prevOutput = *previous.Output
	}
	var output *ToolOutputUpdate
	if set || (slotOutput != prevOutput && trimStart == 0 && appendBuf.Len() == 0) {
		s := slotOutput
		output = &ToolOutputUpdate{Set: &s}
	} else if trimStart > 0 || appendBuf.Len() > 0 {
		output = &ToolOutputUpdate{}
		if trimStart > 0 {
			ts := trimStart
			output.TrimStart = &ts
		}
		if appendBuf.Len() > 0 {
			app := appendBuf.String()
			output.Append = &app
		}
	}

	var rawDetailsNow, rawDetailsPrev any
	if rawSlot != nil {
		rawDetailsNow, _ = rawSlot.Get("details")
	}
	if rawPrev != nil {
		rawDetailsPrev, _ = rawPrev.Get("details")
	}
	var detailsOpt jsonx.Opt[JsonValue]
	if !heSameJsonRef(rawDetailsNow, rawDetailsPrev) {
		if slot.Details == nil {
			detailsOpt = jsonx.Null[JsonValue]()
		} else {
			detailsOpt = jsonx.Some(slot.Details)
		}
	}

	var rawDiagNow, rawDiagPrev any
	if rawSlot != nil {
		rawDiagNow, _ = rawSlot.Get("diagnostics")
	}
	if rawPrev != nil {
		rawDiagPrev, _ = rawPrev.Get("diagnostics")
	}
	var diagsOpt jsonx.Opt[[]ToolDiagnostic]
	if !heSameJsonRef(rawDiagNow, rawDiagPrev) {
		if slot.Diagnostics == nil {
			diagsOpt = jsonx.Some([]ToolDiagnostic{})
		} else {
			diagsOpt = jsonx.Some(slot.Diagnostics)
		}
	}

	if output == nil && detailsOpt.IsZero() && diagsOpt.IsZero() {
		return nil
	}
	return &ToolExecutionUpdateEvent{
		Type:        "tool_execution_update",
		ToolCallId:  slot.CallId,
		ToolName:    slot.Name,
		Output:      output,
		Details:     detailsOpt,
		Diagnostics: diagsOpt,
	}
}

func heOpPath(op chord.Op) chord.Path {
	switch o := op.(type) {
	case chord.OpReplace:
		return nil
	case chord.OpSet:
		return o.Path
	case chord.OpDelete:
		return o.Path
	case chord.OpAppend:
		return o.Path
	case chord.OpTruncate:
		return o.Path
	case chord.OpSplice:
		return o.Path
	case chord.OpMove:
		return o.Path
	default:
		return nil
	}
}

func heStartsWith(path chord.Path, prefix chord.Path) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

func heSameJsonRef(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	switch x := a.(type) {
	case *jsonx.Object:
		y, ok := b.(*jsonx.Object)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		if len(x) == 0 {
			return true
		}
		return reflect.ValueOf(x).Pointer() == reflect.ValueOf(y).Pointer()
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case int64:
		y, ok := b.(int64)
		return ok && x == y
	case int:
		y, ok := b.(int)
		return ok && x == y
	default:
		return reflect.DeepEqual(a, b)
	}
}

func heHasFirstInput(run *LiveRunState) bool {
	return run != nil && len(run.Inputs) > 0
}

func heFirstInput(run *LiveRunState) SubmissionId {
	if run == nil || len(run.Inputs) == 0 {
		return 0
	}
	return run.Inputs[0]
}
