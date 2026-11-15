// Ported from packages/coding-agent/src/experimental/vacation/runtime.ts (pi v1.0.0).

package vacation

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

var runtContext = chord.BackgroundContext

// ModelSummary describes an available model option rendered in the TUI model selector.
type ModelSummary struct {
	Provider      string `json:"provider"`
	ModelId       string `json:"modelId"`
	Name          string `json:"name"`
	ContextWindow int    `json:"contextWindow"`
}

// NoticeLevel classifies a user-visible durable runtime notice.
type NoticeLevel string

const (
	NoticeLevelInfo    NoticeLevel = "info"
	NoticeLevelWarning NoticeLevel = "warning"
	NoticeLevelError   NoticeLevel = "error"
)

// Notice holds one status or error notice displayed in the TUI dock.
type Notice struct {
	Id      int         `json:"id"`
	Level   NoticeLevel `json:"level"`
	Message string      `json:"message"`
}

// ConversationSummary describes a conversation the user can switch to: main or a subagent.
type ConversationSummary struct {
	Id    durable.ConversationId `json:"id"`
	Label string                 `json:"label"`
	// Title is the first user message; for a subagent, its task.
	Title *string `json:"title,omitzero"`
}

// DurableSessionInfo summarizes the open durable session directory and database location.
type DurableSessionInfo struct {
	Id        string `json:"id"`
	Directory string `json:"directory"`
	Cwd       string `json:"cwd"`
}

// DurableView contains everything the TUI renders as plain values with no Harness objects crossing the boundary.
type DurableView struct {
	Session       DurableSessionInfo       `json:"session"`
	Conversation  durable.ConversationView `json:"conversation"`
	Conversations []ConversationSummary    `json:"conversations"`
	Models        []ModelSummary           `json:"models"`
	Notices       []Notice                 `json:"notices"`
	Tasks         *durable.TaskGraph       `json:"tasks,omitzero"`
}

// DurableViewSource provides synchronized snapshots and change subscriptions for DurableView.
type DurableViewSource interface {
	Current() DurableView
	Subscribe(listener func()) func()
}

// DurableController defines the operations the TUI can trigger on the active durable harness.
type DurableController interface {
	// Submit prompts when idle; otherwise steers or queues a follow-up.
	Submit(ctx context.Context, text string, whenBusy durable.SubmissionWhenBusy) error
	Compact(ctx context.Context, instructions *string) error
	Abort(ctx context.Context) error
	CycleThinking(ctx context.Context) error
	SetModel(ctx context.Context, model durable.ModelRef) error
	ToggleTasks(ctx context.Context) error
	// SwitchConversation selects another conversation to show and talk to.
	SwitchConversation(ctx context.Context, id durable.ConversationId) error
}

// OpenDurableOptions configures OpenDurable.
type OpenDurableOptions struct {
	Cwd             string
	ContinueSession bool
}

// OpenDurableResult bundles the view source, controller, settings manager, and closer for an open session.
type OpenDurableResult struct {
	View       DurableViewSource
	Controller DurableController
	Settings   *core.SettingsManager
	Close      func() error
}

func runtStringSlice(raw any) []string {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// AgentOf returns the resolved pi.agent document of view, or an empty AgentState if absent.
func AgentOf(view durable.ConversationView) durable.AgentState {
	var state durable.AgentState
	raw := view.Docs[durable.AgentDoc.Definition.Kind]
	if raw == nil {
		return state
	}
	if rawModel, ok := raw.Get("model"); ok && rawModel != nil {
		var m durable.ModelRef
		if err := jsonx.Decode(rawModel, &m); err == nil {
			state.Model = &m
		}
	}
	if rawLevel, ok := raw.Get("thinkingLevel"); ok {
		if s, isStr := rawLevel.(string); isStr {
			lvl := ai.ModelThinkingLevel(s)
			state.ThinkingLevel = &lvl
		}
	}
	if rawExt, ok := raw.Get("extensions"); ok && rawExt != nil {
		switch v := rawExt.(type) {
		case []any:
			state.Extensions = &durable.AgentExtensionsState{List: runtStringSlice(v)}
		case *jsonx.Object:
			extState := &durable.AgentExtensionsState{}
			if addRaw, ok := v.Get("add"); ok {
				extState.Add = runtStringSlice(addRaw)
			}
			if remRaw, ok := v.Get("remove"); ok {
				extState.Remove = runtStringSlice(remRaw)
			}
			state.Extensions = extState
		}
	}
	if rawTools, ok := raw.Get("tools"); ok && rawTools != nil {
		switch v := rawTools.(type) {
		case []any:
			state.Tools = &durable.AgentToolsState{List: runtStringSlice(v)}
		case *jsonx.Object:
			toolState := &durable.AgentToolsState{}
			if remRaw, ok := v.Get("remove"); ok {
				toolState.Remove = runtStringSlice(remRaw)
			}
			state.Tools = toolState
		}
	}
	if rawInstr, ok := raw.Get("instructions"); ok {
		if s, isStr := rawInstr.(string); isStr {
			state.Instructions = &s
		}
	}
	if rawCwd, ok := raw.Get("cwd"); ok {
		if s, isStr := rawCwd.(string); isStr {
			state.Cwd = &s
		}
	}
	return state
}

// runtFirstInput resolves a subagent's task from its oldest user entry.
func runtFirstInput(harness durable.Harness, id durable.ConversationId) *string {
	if id == durable.RootConversationId {
		return nil
	}
	conversation, err := harness.Conversation(id, runtContext)
	if err != nil || conversation == nil {
		return nil
	}
	var first *durable.EntryRecord
	var cursor durable.Cursor
	for {
		page, pageErr := conversation.Entries(durable.ConversationEntryQuery{}, 256, cursor, runtContext)
		if pageErr != nil {
			break
		}
		for i := len(page.Items) - 1; i >= 0; i-- {
			if page.Items[i].Kind == durable.UserEntry.Kind {
				candidate := page.Items[i]
				first = &candidate
				break
			}
		}
		if page.Next == nil {
			break
		}
		cursor = page.Next
	}
	return runtTitleOf(first)
}

func runtTitleOf(entry *durable.EntryRecord) *string {
	if entry == nil || len(entry.Model) == 0 {
		return nil
	}
	msg, ok := entry.Model[0].(*ai.UserMessage)
	if !ok || msg == nil {
		return nil
	}
	var raw string
	if msg.Content.Text != nil {
		raw = *msg.Content.Text
	} else {
		var parts []string
		for _, block := range msg.Content.Blocks {
			if tc, ok := block.(*ai.TextContent); ok && tc != nil {
				parts = append(parts, tc.Text)
			}
		}
		raw = strings.Join(parts, " ")
	}
	normalized := strings.Join(strings.Fields(raw), " ")
	if normalized == "" {
		return nil
	}
	return &normalized
}

type runtViewStore struct {
	mu          sync.Mutex
	state       DurableView
	listeners   map[int]func()
	nextListen  int
	notifying   bool
	closed      bool
	wg          sync.WaitGroup
	nextNotice  int
	pendingErrs []error
	reportReady bool
}

func runtNewViewStore(initial DurableView) *runtViewStore {
	return &runtViewStore{
		state:      initial,
		listeners:  make(map[int]func()),
		nextNotice: 1,
	}
}

func (s *runtViewStore) Current() DurableView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *runtViewStore) Subscribe(listener func()) func() {
	if listener == nil {
		return func() {}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return func() {}
	}
	id := s.nextListen
	s.nextListen++
	s.listeners[id] = listener
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.listeners, id)
		s.mu.Unlock()
	}
}

// Update mutates state under mu and coalesces listener notification off-lock, once per burst.
func (s *runtViewStore) update(patch func(*DurableView)) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	patch(&s.state)
	if s.notifying {
		s.mu.Unlock()
		return
	}
	s.notifying = true
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		s.mu.Lock()
		s.notifying = false
		if s.closed {
			s.mu.Unlock()
			return
		}
		callbacks := make([]func(), 0, len(s.listeners))
		for _, fn := range s.listeners {
			callbacks = append(callbacks, fn)
		}
		s.mu.Unlock()

		for _, fn := range callbacks {
			fn()
		}
	}()
}

func (s *runtViewStore) notice(level NoticeLevel, message string) {
	s.update(func(view *DurableView) {
		id := s.nextNotice
		s.nextNotice++
		notices := append(append([]Notice(nil), view.Notices...), Notice{
			Id:      id,
			Level:   level,
			Message: message,
		})
		if len(notices) > 20 {
			notices = notices[len(notices)-20:]
		}
		view.Notices = notices
	})
}

func (s *runtViewStore) fail(err error) {
	if err == nil {
		return
	}
	s.notice(NoticeLevelError, err.Error())
}

func (s *runtViewStore) onReport(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	ready := s.reportReady
	if !ready {
		s.pendingErrs = append(s.pendingErrs, err)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.notice(NoticeLevelWarning, err.Error())
}

func (s *runtViewStore) flushReports() {
	s.mu.Lock()
	s.reportReady = true
	pending := s.pendingErrs
	s.pendingErrs = nil
	s.mu.Unlock()
	for _, err := range pending {
		s.notice(NoticeLevelWarning, err.Error())
	}
}

func (s *runtViewStore) stop() {
	s.mu.Lock()
	s.closed = true
	s.listeners = make(map[int]func())
	s.mu.Unlock()
	s.wg.Wait()
}

type runtCommandQueue struct {
	ch   chan func()
	done chan struct{}
	once sync.Once
}

func runtNewCommandQueue() *runtCommandQueue {
	q := &runtCommandQueue{
		ch:   make(chan func(), 64),
		done: make(chan struct{}),
	}
	go func() {
		defer close(q.done)
		for fn := range q.ch {
			fn()
		}
	}()
	return q
}

func (q *runtCommandQueue) run(op func() error) error {
	errCh := make(chan error, 1)
	func() {
		defer func() {
			if recover() != nil {
				errCh <- fmt.Errorf("durable runtime is closed")
			}
		}()
		q.ch <- func() {
			errCh <- op()
		}
	}()
	return <-errCh
}

func (q *runtCommandQueue) close() {
	q.once.Do(func() {
		close(q.ch)
	})
	<-q.done
}

type runtController struct {
	opened       durable.Harness
	modelRuntime *core.ModelRuntime
	store        *runtViewStore
	queue        *runtCommandQueue

	convMu          sync.Mutex
	current         durable.Conversation
	convState       chord.AttachedReplicatedState[durable.ConversationView]
	convUnsub       func()
	tasksState      chord.AttachedReplicatedState[durable.TaskGraph]
	tasksUnsub      func()
	asyncWG         sync.WaitGroup
	asyncCancelOnce sync.Once
	asyncClosed     bool
	asyncMu         sync.Mutex
}

func (c *runtController) isClosing() bool {
	c.asyncMu.Lock()
	defer c.asyncMu.Unlock()
	return c.asyncClosed
}

func (c *runtController) subscribeConversation(convState chord.AttachedReplicatedState[durable.ConversationView]) func() {
	return convState.Subscribe(func(val durable.ConversationView, _ chord.Context, _ chord.ReplicatedStateDelivery) error {
		c.store.update(func(view *DurableView) {
			view.Conversation = val
		})
		return nil
	})
}

func (c *runtController) closeTasksLocked() {
	if c.tasksUnsub != nil {
		c.tasksUnsub()
		c.tasksUnsub = nil
	}
	if c.tasksState != nil {
		c.tasksState.Dispose()
		c.tasksState = nil
	}
}

func (c *runtController) close() {
	c.asyncCancelOnce.Do(func() {
		c.asyncMu.Lock()
		c.asyncClosed = true
		c.asyncMu.Unlock()
	})
	c.queue.close()
	c.convMu.Lock()
	if c.convUnsub != nil {
		c.convUnsub()
		c.convUnsub = nil
	}
	if c.convState != nil {
		c.convState.Dispose()
		c.convState = nil
	}
	c.closeTasksLocked()
	c.convMu.Unlock()
	c.asyncWG.Wait()
}

func (c *runtController) watchAnswer(submission durable.Submission) {
	if c.isClosing() {
		return
	}
	c.asyncWG.Add(1)
	go func() {
		defer c.asyncWG.Done()
		settled, err := submission.Wait(runtContext)
		if err != nil {
			if !c.isClosing() {
				c.store.fail(err)
			}
			return
		}
		if settled != nil && settled.Status == durable.SubmissionStatusUnanswered &&
			(settled.Reason == nil || *settled.Reason != "aborted") {
			reason := ""
			if settled.Reason != nil {
				reason = *settled.Reason
			}
			detailStr := ""
			if settled.Detail != nil {
				if raw, encErr := jsonx.Stringify(settled.Detail); encErr == nil {
					detailStr = " " + raw
				}
			}
			c.store.notice(NoticeLevelError, fmt.Sprintf("No answer: %s%s", reason, detailStr))
		}
	}()
}

func (c *runtController) currentConv() durable.Conversation {
	c.convMu.Lock()
	defer c.convMu.Unlock()
	return c.current
}

func (c *runtController) agentModel() (*ai.Model, error) {
	ref := AgentOf(c.store.Current().Conversation).Model
	if ref == nil {
		return nil, fmt.Errorf("No model selected")
	}
	model := c.modelRuntime.GetModel(ref.Provider, ref.ModelId)
	if model == nil {
		return nil, fmt.Errorf("Current model is unavailable")
	}
	return model, nil
}

func (c *runtController) Submit(_ context.Context, text string, whenBusy durable.SubmissionWhenBusy) error {
	err := c.queue.run(func() error {
		sub, subErr := c.currentConv().Submit(durable.SubmissionDraft{
			Type:     durable.SubmissionTypeInput,
			Content:  durable.UserInput{Text: &text},
			WhenBusy: whenBusy,
		}, runtContext)
		if subErr != nil {
			return subErr
		}
		c.watchAnswer(sub)
		return nil
	})
	if err != nil {
		c.store.fail(err)
	}
	return err
}

func (c *runtController) Compact(_ context.Context, instructions *string) error {
	err := c.queue.run(func() error {
		id, compactErr := c.currentConv().Compact(instructions, runtContext)
		if compactErr != nil {
			return compactErr
		}
		if !c.isClosing() {
			c.asyncWG.Add(1)
			go func() {
				defer c.asyncWG.Done()
				receipt, waitErr := c.opened.WaitForTask(runtContext, id)
				if waitErr != nil {
					if !c.isClosing() {
						c.store.fail(waitErr)
					}
					return
				}
				if receipt == nil || receipt.State.Outcome == nil {
					return
				}
				outcome := receipt.State.Outcome
				switch outcome.Status {
				case durable.TaskOutcomeCompleted:
					var res durable.CompactionResult
					if raw, ok := outcome.Result.Get(); ok {
						_ = jsonx.Decode(raw, &res)
					}
					var status durable.SubmissionStatus
					if res.SubmissionId != nil {
						if sub, subErr := c.opened.Submission(runtContext, *res.SubmissionId); subErr == nil && sub != nil {
							if rec, stErr := sub.Status(runtContext); stErr == nil && rec != nil {
								status = rec.Status
							}
						}
					}
					switch {
					case res.EntryId != nil || status == durable.SubmissionStatusDone:
						c.store.notice(NoticeLevelInfo, "Compacted.")
					case status == durable.SubmissionStatusQueued:
						c.store.notice(NoticeLevelInfo, "Compaction summary queued; it is placed at the next turn boundary.")
					case status == durable.SubmissionStatusUnanswered:
						c.store.notice(NoticeLevelInfo, "Compaction summary dropped: the context changed under it.")
					default:
						c.store.notice(NoticeLevelInfo, "Nothing to compact: the context fits in the recent window that is kept verbatim.")
					}
				case durable.TaskOutcomeAborted:
					c.store.notice(NoticeLevelInfo, "Compaction aborted.")
				default:
					msg := ""
					if outcome.Error != nil && outcome.Error.Message != "" {
						msg = outcome.Error.Message
					} else if outcome.Reason != nil {
						msg = *outcome.Reason
					}
					c.store.notice(NoticeLevelError, fmt.Sprintf("Compaction %s: %s", outcome.Status, msg))
				}
			}()
		}
		return nil
	})
	if err != nil {
		c.store.fail(err)
	}
	return err
}

// Abort is not queued: it waits until the conversation is idle.
func (c *runtController) Abort(_ context.Context) error {
	err := c.currentConv().Abort(runtContext)
	if err != nil && !c.isClosing() {
		c.store.fail(err)
	}
	return err
}

func (c *runtController) CycleThinking(_ context.Context) error {
	err := c.queue.run(func() error {
		model, err := c.agentModel()
		if err != nil {
			return err
		}
		if !model.Reasoning {
			return fmt.Errorf("Current model does not support thinking")
		}
		levels := ai.GetSupportedThinkingLevels(model)
		if len(levels) == 0 {
			levels = []ai.ModelThinkingLevel{ai.ModelThinkingLevelOff}
		}
		level := AgentOf(c.store.Current().Conversation).ThinkingLevel
		currentLevel := ai.ModelThinkingLevelOff
		if level != nil && *level != "" {
			currentLevel = *level
		}
		idx := -1
		for i, candidate := range levels {
			if candidate == currentLevel {
				idx = i
				break
			}
		}
		next := levels[(idx+1)%len(levels)]
		return c.currentConv().Configure(durable.AgentChange{
			ThinkingLevel: jsonx.Some(next),
		}, runtContext)
	})
	if err != nil {
		c.store.fail(err)
	}
	return err
}

func (c *runtController) SetModel(_ context.Context, ref durable.ModelRef) error {
	err := c.queue.run(func() error {
		model := c.modelRuntime.GetModel(ref.Provider, ref.ModelId)
		if model == nil {
			return fmt.Errorf("Unknown model: %s/%s", ref.Provider, ref.ModelId)
		}
		thinking := ai.ModelThinkingLevelOff
		if existing := AgentOf(c.store.Current().Conversation).ThinkingLevel; existing != nil && *existing != "" {
			thinking = *existing
		}
		clamped := ai.ClampThinkingLevel(model, thinking)
		return c.currentConv().Configure(durable.AgentChange{
			Model:         jsonx.Some(ref),
			ThinkingLevel: jsonx.Some(clamped),
		}, runtContext)
	})
	if err != nil {
		c.store.fail(err)
	}
	return err
}

func (c *runtController) ToggleTasks(_ context.Context) error {
	err := c.queue.run(func() error {
		c.convMu.Lock()
		hasTasks := c.tasksState != nil
		if hasTasks {
			c.closeTasksLocked()
			c.convMu.Unlock()
			c.store.update(func(view *DurableView) {
				view.Tasks = nil
			})
			return nil
		}
		c.convMu.Unlock()

		graph, err := c.opened.TaskGraph(runtContext)
		if err != nil {
			return err
		}
		c.convMu.Lock()
		c.tasksState = graph
		if initial, ok := graph.Value(); ok {
			copyVal := initial
			c.store.update(func(view *DurableView) {
				view.Tasks = &copyVal
			})
		}
		c.tasksUnsub = graph.Subscribe(func(val durable.TaskGraph, _ chord.Context, _ chord.ReplicatedStateDelivery) error {
			copyVal := val
			c.store.update(func(view *DurableView) {
				view.Tasks = &copyVal
			})
			return nil
		})
		c.convMu.Unlock()
		return nil
	})
	if err != nil {
		c.store.fail(err)
	}
	return err
}

func (c *runtController) SwitchConversation(_ context.Context, id durable.ConversationId) error {
	err := c.queue.run(func() error {
		next, err := c.opened.Conversation(id, runtContext)
		if err != nil {
			return err
		}
		if next == nil {
			return fmt.Errorf("Conversation %d does not exist", id)
		}
		nextState, err := next.ViewState(runtContext)
		if err != nil {
			return err
		}
		c.convMu.Lock()
		if c.convUnsub != nil {
			c.convUnsub()
		}
		if c.convState != nil {
			c.convState.Dispose()
		}
		c.current = next
		c.convState = nextState
		c.convUnsub = c.subscribeConversation(nextState)
		c.convMu.Unlock()

		if initial, ok := nextState.Value(); ok {
			c.store.update(func(view *DurableView) {
				view.Conversation = initial
			})
		}
		return nil
	})
	if err != nil {
		c.store.fail(err)
	}
	return err
}

// OpenDurable opens or resumes a standalone vacation session directory and initializes the Harness, view store, and controller.
func OpenDurable(ctx context.Context, options ...*OpenDurableOptions) (*OpenDurableResult, error) {
	var opts OpenDurableOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cwd := opts.Cwd
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		cwd = wd
	}

	location, err := SelectSession(ctx, cwd, opts.ContinueSession)
	if err != nil {
		return nil, err
	}

	var harness durable.Harness
	var modelRuntime *core.ModelRuntime
	cleanupOnErr := func() {
		if harness != nil {
			_ = harness.Close(runtContext)
		}
		if modelRuntime != nil {
			modelRuntime.Close()
		}
		_ = location.Release()
	}

	modelRuntime, err = core.CreateModelRuntime(ctx)
	if err != nil {
		cleanupOnErr()
		return nil, err
	}
	settingsManager := core.SettingsManagerCreate(location.Cwd, utils.GetAgentDir())
	if err := ConfigureHarnessHttp(settingsManager); err != nil {
		cleanupOnErr()
		return nil, err
	}
	settings := CreateHarnessSettings(settingsManager)
	registry, err := CreateVacationRegistry()
	if err != nil {
		cleanupOnErr()
		return nil, err
	}

	sqlStorage, err := sqlite.OpenNodeSqliteStorage(location.Database)
	if err != nil {
		cleanupOnErr()
		return nil, err
	}

	store := runtNewViewStore(DurableView{})
	harness, err = durable.HarnessOpen(sqlStorage, durable.HarnessOptions{
		Models:   modelRuntime,
		Registry: registry,
		Settings: settings,
		OnReport: store.onReport,
	}, runtContext)
	if err != nil {
		cleanupOnErr()
		return nil, err
	}

	var initial *InitialModel
	if location.Created {
		initial, err = FindInitialAgentModel(ctx, settingsManager, modelRuntime, nil)
		if err != nil {
			cleanupOnErr()
			return nil, err
		}
	}

	agentChange := durable.AgentChange{
		// Only the vacation planner; the research subagent selects search itself.
		Extensions: jsonx.Some(durable.AgentExtensionsSet(Vacation)),
	}
	if initial != nil {
		if initial.Model != nil {
			agentChange.Model = jsonx.Some(*initial.Model)
		}
		if initial.ThinkingLevel != nil && *initial.ThinkingLevel != "" {
			agentChange.ThinkingLevel = jsonx.Some(*initial.ThinkingLevel)
		}
	}
	root, err := harness.Root(runtContext, durable.RootOptions{
		Agent: &agentChange,
	})
	if err != nil {
		cleanupOnErr()
		return nil, err
	}

	labelFor := func(id durable.ConversationId) string {
		if id == root.Id() {
			return "main"
		}
		return fmt.Sprintf("subagent %d", id)
	}

	summaries := make([]ConversationSummary, 0)
	var convIds []durable.ConversationId
	var cursor durable.Cursor
	for {
		if err := harness.Commit(runtContext, func(tx durable.Tx) error {
			page, scanErr := tx.ScanConversations(durable.ConversationQuery{}, 256, cursor)
			if scanErr != nil {
				return scanErr
			}
			for _, rec := range page.Items {
				convIds = append(convIds, rec.Id)
			}
			cursor = page.Next
			return nil
		}); err != nil {
			cleanupOnErr()
			return nil, err
		}
		if cursor == nil {
			break
		}
	}
	for _, id := range convIds {
		summaries = append(summaries, ConversationSummary{
			Id:    id,
			Label: labelFor(id),
			Title: runtFirstInput(harness, id),
		})
	}

	convState, err := root.ViewState(runtContext)
	if err != nil {
		cleanupOnErr()
		return nil, err
	}
	initialView, _ := convState.Value()

	availModels := modelRuntime.GetAvailableSnapshot()
	modelSummaries := make([]ModelSummary, 0, len(availModels))
	for _, m := range availModels {
		if m == nil {
			continue
		}
		modelSummaries = append(modelSummaries, ModelSummary{
			Provider:      string(m.Provider),
			ModelId:       m.Id,
			Name:          m.Name,
			ContextWindow: m.ContextWindow,
		})
	}

	store.mu.Lock()
	store.state = DurableView{
		Session: DurableSessionInfo{
			Id:        location.Id,
			Directory: location.Directory,
			Cwd:       location.Cwd,
		},
		Conversation:  initialView,
		Conversations: summaries,
		Models:        modelSummaries,
		Notices:       nil,
	}
	store.mu.Unlock()
	store.flushReports()

	ctrl := &runtController{
		opened:       harness,
		modelRuntime: modelRuntime,
		store:        store,
		queue:        runtNewCommandQueue(),
		current:      root,
		convState:    convState,
	}
	ctrl.convUnsub = ctrl.subscribeConversation(convState)

	// Subagents appear as their conversations are created. A commit listener only records; it calls no Session API.
	unsubscribeCommits := harness.SubscribeCommits(func(publication durable.CommitPublication, _ chord.Context) {
		store.update(func(view *DurableView) {
			conversations := view.Conversations
			changed := false
			for _, change := range publication.Changes {
				switch c := change.(type) {
				case durable.StorageWriteConversation:
					conversations = append(append([]ConversationSummary(nil), conversations...), ConversationSummary{
						Id:    c.Value.Id,
						Label: labelFor(c.Value.Id),
					})
					changed = true
				case *durable.StorageWriteConversation:
					if c != nil {
						conversations = append(append([]ConversationSummary(nil), conversations...), ConversationSummary{
							Id:    c.Value.Id,
							Label: labelFor(c.Value.Id),
						})
						changed = true
					}
				case durable.StorageWriteEntry:
					if c.Value.Kind == durable.UserEntry.Kind {
						entry := c.Value
						updated := make([]ConversationSummary, len(conversations))
						copy(updated, conversations)
						for i := range updated {
							if updated[i].Id == entry.ConversationId && updated[i].Title == nil {
								updated[i].Title = runtTitleOf(&entry)
								changed = true
							}
						}
						conversations = updated
					}
				case *durable.StorageWriteEntry:
					if c != nil && c.Value.Kind == durable.UserEntry.Kind {
						entry := c.Value
						updated := make([]ConversationSummary, len(conversations))
						copy(updated, conversations)
						for i := range updated {
							if updated[i].Id == entry.ConversationId && updated[i].Title == nil {
								updated[i].Title = runtTitleOf(&entry)
								changed = true
							}
						}
						conversations = updated
					}
				}
			}
			if changed {
				view.Conversations = conversations
			}
		})
	})

	saved := AgentOf(store.Current().Conversation).Model
	if saved == nil {
		store.notice(NoticeLevelWarning, "No model configured; select one with /model.")
	} else if modelRuntime.GetModel(saved.Provider, saved.ModelId) == nil {
		store.notice(NoticeLevelWarning, fmt.Sprintf("Saved model is unavailable: %s/%s", saved.Provider, saved.ModelId))
	}
	if initial != nil && initial.FallbackMessage != nil && *initial.FallbackMessage != "" {
		store.notice(NoticeLevelInfo, *initial.FallbackMessage)
	}

	// The task panel starts open; /tasks hides it.
	if err := ctrl.ToggleTasks(ctx); err != nil {
		unsubscribeCommits()
		ctrl.close()
		store.stop()
		cleanupOnErr()
		return nil, err
	}
	// Recovered work from an interrupted turn continues now.
	if err := harness.Resume(); err != nil {
		unsubscribeCommits()
		ctrl.close()
		store.stop()
		cleanupOnErr()
		return nil, err
	}

	var closeOnce sync.Once
	var closeErr error
	return &OpenDurableResult{
		View:       store,
		Controller: ctrl,
		Settings:   settingsManager,
		Close: func() error {
			closeOnce.Do(func() {
				unsubscribeCommits()
				ctrl.close()
				store.stop()
				// Close writes no outcome: a running turn resumes with --continue.
				if err := harness.Close(runtContext); err != nil {
					closeErr = err
				}
				modelRuntime.Close()
				if relErr := location.Release(); relErr != nil && closeErr == nil {
					closeErr = relErr
				}
			})
			return closeErr
		},
	}, nil
}
