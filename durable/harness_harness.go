// Ported from packages/durable/src/harness/harness.ts (pi v1.0.0).

package durable

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable/env"
)

const hhScanPageSize = 256

type hhCreateTargetKind string

const (
	hhTargetRoot        hhCreateTargetKind = "root"
	hhTargetIndependent hhCreateTargetKind = "independent"
	hhTargetFork        hhCreateTargetKind = "fork"
)

type hhCreateTarget struct {
	kind      hhCreateTargetKind
	ownership ConversationOwnership
	parentId  ConversationId
	at        EntryId
}

type hhConversationImpl struct {
	id   ConversationId
	host *hhHarnessImpl
}

func (c *hhConversationImpl) Id() ConversationId { return c.id }

func (c *hhConversationImpl) Agent(ctx chord.Context) (*Agent, error) {
	return c.host.resolveAgent(ctx, c.id, nil)
}

func (c *hhConversationImpl) Configure(change AgentChange, ctx chord.Context) error {
	return c.host.Commit(ctx, func(tx Tx) error {
		return Configure(tx, c.id, change)
	})
}

func (c *hhConversationImpl) Submit(submission SubmissionDraft, ctx chord.Context) (Submission, error) {
	return c.host.submissions.Submit(ctx, c.id, submission)
}

func (c *hhConversationImpl) Reset(handoff *string, ctx chord.Context) error {
	var model ai.MessageList
	if handoff != nil {
		text := *handoff
		model = ai.MessageList{
			&ai.UserMessage{
				Role:      "user",
				Content:   ai.UserMessageContent{Text: &text},
				Timestamp: c.host.now(),
			},
		}
	}
	entry := EntryDraft{
		Kind:  ResetEntry.Kind,
		Head:  EntryHeadSelf(),
		Model: model,
	}
	_, err := c.host.submissions.Submit(ctx, c.id, SubmissionDraft{
		Type:  SubmissionTypeWrite,
		Entry: &entry,
	})
	return err
}

func (c *hhConversationImpl) Compact(instructions *string, ctx chord.Context) (TaskId, error) {
	c.host.tasks.Resume()
	var tid TaskId
	if err := c.host.Commit(ctx, func(tx Tx) error {
		var err error
		tid, err = CreateCompaction(tx, c.id, CompactionInput{
			Reason:       CompactionReasonManual,
			Instructions: instructions,
		}, nil)
		return err
	}); err != nil {
		return 0, err
	}
	return tid, nil
}

func (c *hhConversationImpl) Commit(ctx chord.Context, change func(tx Tx) (any, error)) (any, error) {
	var res any
	cid := c.id
	if err := c.host.CommitWith(ctx, func(tx *Transaction) error {
		var err error
		res, err = change(tx)
		return err
	}, &TransactionScope{ConversationId: &cid}); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *hhConversationImpl) Context(ctx chord.Context) (*ContextView, error) {
	return hxReadContext(ctx, c.host.SessionImpl, c.host.storage, c.id)
}

func (c *hhConversationImpl) Entries(
	query ConversationEntryQuery,
	limit int,
	cursor Cursor,
	ctx chord.Context,
) (Page[EntryRecord, Cursor], error) {
	bounded := EntryQuery{
		ConversationId: c.id,
		MinEntryId:     query.MinEntryId,
		MaxEntryId:     query.MaxEntryId,
	}
	var page Page[EntryRecord, Cursor]
	err := c.host.ReadOnLine(func() error {
		var e error
		page, e = c.host.storage.ScanEntries(ctx, bounded, limit, cursor)
		return e
	})
	return page, err
}

func (c *hhConversationImpl) Fork(at EntryId, options ConversationCreateOptions, ctx chord.Context) (Conversation, error) {
	return c.host.create(ctx, hhCreateTarget{
		kind:      hhTargetFork,
		parentId:  c.id,
		at:        at,
		ownership: options.Ownership,
	}, options.Agent, options.Init)
}

func (c *hhConversationImpl) Abort(ctx chord.Context, options ...ConversationAbortOptions) error {
	c.host.tasks.Resume()
	bg := len(options) > 0 && options[0].Background
	return c.host.tasks.AbortConversation(ctx, c.id, bg)
}

func (c *hhConversationImpl) WaitForIdle(ctx chord.Context) error {
	c.host.tasks.Resume()
	cid := c.id
	return c.host.tasks.WaitForIdle(ctx, &cid)
}

func (c *hhConversationImpl) ViewState(ctx chord.Context) (chord.AttachedReplicatedState[ConversationView], error) {
	return c.host.views.State(c.id, ctx)
}

func (c *hhConversationImpl) Watch(ctx chord.Context) (ConversationWatch, error) {
	return c.host.views.Watch(c.id, ctx)
}

type hhHarnessImpl struct {
	*SessionImpl

	storage     Storage
	options     HarnessOptions
	report      func(error)
	now         func() int64
	tasks       *hsTaskScheduler
	submissions *hsubSubmissions
	views       *ConversationViews
	taskGraph   *TaskGraphView

	mu     sync.Mutex
	closed bool
}

func hhNewHarnessImpl(storage Storage, options HarnessOptions, ctx chord.Context) *hhHarnessImpl {
	report := options.OnReport
	if report == nil {
		report = func(error) {}
	}
	now := options.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	h := &hhHarnessImpl{
		storage: storage,
		options: options,
		report:  report,
		now:     now,
	}
	h.SessionImpl = NewSessionWithHooks(storage, SessionHooks{
		ConversationCreated: h.onConversationCreated,
		BeforeClose:         h.onBeforeClose,
	})
	settings := func() Settings {
		return h.currentSettings()
	}
	queueModes := func() QueueModes {
		s := h.currentSettings()
		return QueueModes{
			SteeringMode: s.SteeringMode,
			FollowUpMode: s.FollowUpMode,
		}
	}
	h.tasks = hsNewTaskScheduler(hsTaskSchedulerOptions{
		session:  h.SessionImpl,
		storage:  storage,
		registry: options.Registry,
		models:   options.Models,
		agent: func(id ConversationId, snapshot RegistrySnapshot, callCtx chord.Context) (*Agent, error) {
			return h.resolveAgent(callCtx, id, snapshot)
		},
		settings:       settings,
		env:            h.buildEnv,
		now:            now,
		report:         report,
		settleOutcome:  hlSettleSchedulerOutcome,
		withdrawInputs: hiWithdrawQueuedInputs,
		conversation: func(id ConversationId, binding hsInvocationBinding, callCtx chord.Context) (ConversationHandle, error) {
			var rec *ConversationRecord
			if err := h.ReadOnLine(func() error {
				var err error
				rec, err = storage.Conversation(callCtx, id)
				return err
			}); err != nil {
				return nil, err
			}
			if rec == nil {
				return nil, nil
			}
			return hhBoundConversation(id, binding, h.submissions, h.tasks), nil
		},
		ctx: chord.WithoutAbortSignal(ctx),
	})
	h.submissions = hsubNewSubmissions(h.SessionImpl, storage, now, queueModes, func() {
		h.tasks.Resume()
	})
	h.views = NewConversationViews(h.SessionImpl, storage)
	h.taskGraph = NewTaskGraphView(h.SessionImpl, storage)
	return h
}

func (h *hhHarnessImpl) hvViews() *ConversationViews {
	return h.views
}

func (h *hhHarnessImpl) currentSettings() Settings {
	if h.options.Settings != nil {
		s := h.options.Settings()
		return ResolveSettings(&s)
	}
	return ResolveSettings(h.options.StaticSettings)
}

func (h *hhHarnessImpl) resolveAgent(ctx chord.Context, id ConversationId, snapshot RegistrySnapshot) (*Agent, error) {
	if snapshot == nil {
		snapshot = h.options.Registry.Snapshot()
	}
	state, err := h.Snapshot(ctx, AgentDoc, id)
	if err != nil {
		return nil, err
	}
	return ResolveAgent(state, snapshot, h.currentSettings(), h.report), nil
}

func (h *hhHarnessImpl) buildEnv(id ConversationId, ctx chord.Context) (env.ExecutionEnv, error) {
	if h.options.Env == nil {
		return nil, nil
	}
	state, err := h.Snapshot(ctx, AgentDoc, id)
	if err != nil {
		return nil, err
	}
	var cwd *string
	if state != nil {
		if rawCwd, ok := state.Get("cwd"); ok {
			if s, isStr := rawCwd.(string); isStr {
				cwd = &s
			}
		}
	}
	return h.options.Env(EnvTarget{
		ConversationId: id,
		Cwd:            cwd,
		Read:           h,
	}, ctx)
}

func (h *hhHarnessImpl) Resume() error {
	if err := h.assertOpen(); err != nil {
		return err
	}
	h.tasks.Resume()
	return nil
}

func (h *hhHarnessImpl) Root(ctx chord.Context, options ...RootOptions) (Conversation, error) {
	var agent *AgentChange
	var init ConversationInit
	if len(options) > 0 {
		agent = options[0].Agent
		init = options[0].Init
	}
	return h.create(ctx, hhCreateTarget{kind: hhTargetRoot}, agent, init)
}

func (h *hhHarnessImpl) Conversation(id ConversationId, ctx chord.Context) (Conversation, error) {
	if err := h.assertOpen(); err != nil {
		return nil, err
	}
	var rec *ConversationRecord
	if err := h.ReadOnLine(func() error {
		var err error
		rec, err = h.storage.Conversation(ctx, id)
		return err
	}); err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	return &hhConversationImpl{id: rec.Id, host: h}, nil
}

func (h *hhHarnessImpl) CreateConversation(options ConversationCreateOptions, ctx chord.Context) (Conversation, error) {
	return h.create(ctx, hhCreateTarget{
		kind:      hhTargetIndependent,
		ownership: options.Ownership,
	}, options.Agent, options.Init)
}

func (h *hhHarnessImpl) GetTask(ctx chord.Context, id TaskId) (*TaskRecord, error) {
	var rec *TaskRecord
	if err := h.ReadOnLine(func() error {
		var err error
		rec, err = h.storage.Task(ctx, id)
		return err
	}); err != nil {
		return nil, err
	}
	return rec, nil
}

func (h *hhHarnessImpl) Inspect(ctx chord.Context) (*HarnessInspection, error) {
	var inspection *HarnessInspection
	if err := h.ReadOnLine(func() error {
		scheduling, tasks, err := h.tasks.Inspect(h.options.Registry.Snapshot())
		if err != nil {
			return err
		}
		scan := func(status SubmissionStatus) ([]SubmissionRecord, error) {
			return hutScanAll(func(cursor Cursor) (Page[SubmissionRecord, Cursor], error) {
				return h.storage.ScanSubmissions(ctx, SubmissionQuery{Status: status}, hhScanPageSize, cursor)
			})
		}
		queued, err := scan(SubmissionStatusQueued)
		if err != nil {
			return err
		}
		placed, err := scan(SubmissionStatusPlaced)
		if err != nil {
			return err
		}
		submissions := make([]SubmissionRecord, 0, len(queued)+len(placed))
		submissions = append(submissions, queued...)
		submissions = append(submissions, placed...)
		slices.SortFunc(submissions, func(a, b SubmissionRecord) int {
			if a.Id < b.Id {
				return -1
			}
			if a.Id > b.Id {
				return 1
			}
			return 0
		})
		inspection = &HarnessInspection{
			Scheduling:  scheduling,
			Tasks:       tasks,
			Submissions: submissions,
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return inspection, nil
}

func (h *hhHarnessImpl) Submission(ctx chord.Context, id SubmissionId) (Submission, error) {
	return h.submissions.Get(ctx, id)
}

func (h *hhHarnessImpl) AbortSubmission(ctx chord.Context, id SubmissionId, conversationId ...ConversationId) (SubmissionAbortResult, error) {
	var cid *ConversationId
	if len(conversationId) > 0 {
		c := conversationId[0]
		cid = &c
	}
	return h.submissions.Abort(ctx, id, cid)
}

func (h *hhHarnessImpl) AbortTask(ctx chord.Context, id TaskId) (TaskAbortResult, error) {
	return h.tasks.Abort(ctx, id)
}

func (h *hhHarnessImpl) WaitForTask(ctx chord.Context, id TaskId) (*SettledTask, error) {
	h.tasks.Resume()
	return h.tasks.WaitForTask(ctx, id)
}

func (h *hhHarnessImpl) WaitForIdle(ctx chord.Context) error {
	h.tasks.Resume()
	return h.tasks.WaitForIdle(ctx, nil)
}

func (h *hhHarnessImpl) Usage(ctx chord.Context) (*UsageState, error) {
	var conversations []ConversationRecord
	if err := h.ReadOnLine(func() error {
		var err error
		conversations, err = hutScanAll(func(cursor Cursor) (Page[ConversationRecord, Cursor], error) {
			return h.storage.ScanConversations(ctx, ConversationQuery{}, hhScanPageSize, cursor)
		})
		return err
	}); err != nil {
		return nil, err
	}
	total := NewUsageState()
	for _, conv := range conversations {
		rawState, err := h.Snapshot(ctx, UsageDoc, conv.Id)
		if err != nil {
			return nil, err
		}
		if rawState == nil {
			continue
		}
		st, err := huDecodeUsageState(rawState)
		if err != nil {
			return nil, err
		}
		AddUsageState(total, *st)
	}
	return total, nil
}

func (h *hhHarnessImpl) TaskGraph(ctx chord.Context) (chord.AttachedReplicatedState[TaskGraph], error) {
	return h.taskGraph.State(ctx)
}

func (h *hhHarnessImpl) WatchTaskGraph(ctx chord.Context) (TaskGraphWatch, error) {
	return h.taskGraph.Watch(ctx)
}

func (h *hhHarnessImpl) Close(ctx chord.Context) error {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	return h.SessionImpl.Close(ctx)
}

func (h *hhHarnessImpl) onBeforeClose() {
	h.tasks.Join()
}

func (h *hhHarnessImpl) onConversationCreated(tx *Transaction, rec ConversationRecord) error {
	if _, err := tx.Doc(LiveDoc, rec.Id); err != nil {
		return err
	}
	if _, err := tx.Doc(InboxDoc, rec.Id); err != nil {
		return err
	}
	if _, err := tx.Doc(UsageDoc, rec.Id); err != nil {
		return err
	}
	if err := CreateAgent(tx, rec); err != nil {
		return err
	}
	if h.options.ConversationCreated != nil {
		return h.options.ConversationCreated(tx, rec)
	}
	return nil
}

func (h *hhHarnessImpl) create(
	ctx chord.Context,
	target hhCreateTarget,
	agent *AgentChange,
	initFn ConversationInit,
) (Conversation, error) {
	if err := h.assertOpen(); err != nil {
		return nil, err
	}
	var id ConversationId
	if err := h.CommitWith(ctx, func(tx *Transaction) error {
		if target.kind == hhTargetRoot {
			existing, err := tx.Conversation(RootConversationId)
			if err != nil {
				return err
			}
			if existing != nil {
				id = RootConversationId
				return nil
			}
		}
		var rec *ConversationRecord
		var err error
		switch target.kind {
		case hhTargetRoot:
			rec, err = tx.CreateRootConversation()
		case hhTargetFork:
			rec, err = tx.ForkConversation(target.parentId, target.at, ConversationCreateTxOptions{
				Ownership: target.ownership,
			})
		default:
			rec, err = tx.CreateConversation(ConversationCreateTxOptions{
				Ownership: target.ownership,
			})
		}
		if err != nil {
			return err
		}
		if agent != nil {
			if err := Configure(tx, rec.Id, *agent); err != nil {
				return err
			}
		}
		if initFn != nil {
			if err := initFn(tx, rec.Id); err != nil {
				return err
			}
		}
		id = rec.Id
		return nil
	}, nil); err != nil {
		return nil, err
	}
	return &hhConversationImpl{id: id, host: h}, nil
}

func (h *hhHarnessImpl) assertOpen() error {
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed {
		return fmt.Errorf("Harness is closed")
	}
	return nil
}

func hhBoundConversation(
	id ConversationId,
	binding hsInvocationBinding,
	submissions *hsubSubmissions,
	tasks *hsTaskScheduler,
) ConversationHandle {
	bind := func(ctx chord.Context) chord.Context {
		return chord.WithAbortSignal(binding.signal, ctx)
	}
	return &hhBoundConversationHandle{
		id:          id,
		binding:     binding,
		bind:        bind,
		submissions: submissions,
		tasks:       tasks,
	}
}

type hhBoundConversationHandle struct {
	id          ConversationId
	binding     hsInvocationBinding
	bind        func(ctx chord.Context) chord.Context
	submissions *hsubSubmissions
	tasks       *hsTaskScheduler
}

func (h *hhBoundConversationHandle) Id() ConversationId { return h.id }

func (h *hhBoundConversationHandle) Submit(draft InputSubmissionDraft, ctx chord.Context) (Submission, error) {
	if err := h.binding.check(); err != nil {
		return nil, err
	}
	whenBusy := draft.WhenBusy
	sub, err := h.submissions.Submit(h.bind(ctx), h.id, SubmissionDraft{
		RequestId: draft.RequestId,
		Type:      SubmissionTypeInput,
		Content:   draft.Content,
		WhenBusy:  whenBusy,
	})
	if err != nil {
		return nil, err
	}
	return &hhBoundSubmission{
		inner:   sub,
		binding: h.binding,
		bind:    h.bind,
	}, nil
}

func (h *hhBoundConversationHandle) Abort(ctx chord.Context, options ...ConversationAbortOptions) error {
	if err := h.binding.check(); err != nil {
		return err
	}
	bg := len(options) > 0 && options[0].Background
	return h.tasks.AbortConversation(h.bind(ctx), h.id, bg)
}

func (h *hhBoundConversationHandle) WaitForIdle(ctx chord.Context) error {
	if err := h.binding.check(); err != nil {
		return err
	}
	cid := h.id
	return h.tasks.WaitForIdle(h.bind(ctx), &cid)
}

type hhBoundSubmission struct {
	inner   Submission
	binding hsInvocationBinding
	bind    func(ctx chord.Context) chord.Context
}

func (s *hhBoundSubmission) Id() SubmissionId { return s.inner.Id() }

func (s *hhBoundSubmission) Status(ctx chord.Context) (*SubmissionRecord, error) {
	if err := s.binding.check(); err != nil {
		return nil, err
	}
	return s.inner.Status(s.bind(ctx))
}

func (s *hhBoundSubmission) Wait(ctx chord.Context) (*SettledSubmissionRecord, error) {
	if err := s.binding.check(); err != nil {
		return nil, err
	}
	return s.inner.Wait(s.bind(ctx))
}

func (s *hhBoundSubmission) Abort(ctx chord.Context) (SubmissionAbortResult, error) {
	if err := s.binding.check(); err != nil {
		return "", err
	}
	return s.inner.Abort(s.bind(ctx))
}

// HarnessOpen opens a durable agent Harness over storage.
// The registry may continue changing while the Harness runs.
func HarnessOpen(storage Storage, options HarnessOptions, ctx chord.Context) (Harness, error) {
	if err := hutCheckAborted(ctx); err != nil {
		return nil, err
	}
	if options.Registry == nil {
		return nil, fmt.Errorf("HarnessOptions.Registry is required")
	}
	snapshot := options.Registry.Snapshot()
	var missing []string
	for _, task := range BuiltinTasks {
		if snapshot.Task(task.TaskKind()) == nil {
			missing = append(missing, task.TaskKind())
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("Registry lacks built-in tasks %s; create it with CreateRegistry()", strings.Join(missing, ", "))
	}
	harness := hhNewHarnessImpl(storage, options, ctx)
	if err := harness.tasks.Open(ctx); err != nil {
		if closeErr := harness.Close(chord.WithoutAbortSignal(ctx)); closeErr != nil && options.OnReport != nil {
			options.OnReport(closeErr)
		}
		return nil, err
	}
	return harness, nil
}
