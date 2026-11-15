// Ported from packages/durable/test/harness-compaction.test.ts (pi v1.0.0).

package durable_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/internal/testkit"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/internal/typebox"
)

func thcpPtr[T any](v T) *T { return &v }

func thcpSqlitePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "session.sqlite")
}

func thcpText(label string, tokens int) string {
	n := tokens*4 - len(label) - 1
	if n < 0 {
		n = 0
	}
	return label + " " + strings.Repeat("x", n)
}

type thcpGate struct {
	once sync.Once
	ch   chan struct{}
}

func thcpNewGate() *thcpGate {
	return &thcpGate{ch: make(chan struct{})}
}

func (g *thcpGate) Resolve() {
	g.once.Do(func() { close(g.ch) })
}

func (g *thcpGate) Ch() <-chan struct{} {
	return g.ch
}

func (g *thcpGate) Wait(t *testing.T) {
	t.Helper()
	select {
	case <-g.ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("gate timed out after 5s")
	}
}

type thcpRequest struct {
	Messages []ai.Message
	Options  *ai.SimpleStreamOptions
	Model    string
}

type thcpStep func(ctx context.Context, req thcpRequest) (*ai.AssistantMessage, error)

type thcpScript struct {
	mu              sync.Mutex
	agent           []thcpStep
	summaries       []thcpStep
	agentRequests   []thcpRequest
	summaryRequests []thcpRequest
}

func thcpIsSummaryRequest(messages []ai.Message) bool {
	if len(messages) == 0 {
		return false
	}
	sys, ok := messages[0].(*ai.SystemMessage)
	if !ok || sys.Content.Text == nil {
		return false
	}
	return strings.HasPrefix(*sys.Content.Text, "You are a context summarization assistant")
}

func thcpAttachScript(setup *testkit.ChatSetup) *thcpScript {
	s := &thcpScript{}
	steps := make([]providers.FauxResponseStep, 200)
	factory := providers.FauxResponseFactory(func(ctx context.Context, transcript *ai.TranscriptContext, options *ai.SimpleStreamOptions, _ *providers.FauxProviderState, model *ai.Model) (*ai.AssistantMessage, error) {
		msgs := slices.Clone(transcript.Messages)
		var optsCopy *ai.SimpleStreamOptions
		if options != nil {
			cp := *options
			optsCopy = &cp
		}
		req := thcpRequest{Messages: msgs, Options: optsCopy, Model: model.Id}
		isSum := thcpIsSummaryRequest(msgs)

		s.mu.Lock()
		var step thcpStep
		if isSum {
			s.summaryRequests = append(s.summaryRequests, req)
			if len(s.summaries) > 0 {
				step = s.summaries[0]
				s.summaries = s.summaries[1:]
			}
		} else {
			s.agentRequests = append(s.agentRequests, req)
			if len(s.agent) > 0 {
				step = s.agent[0]
				s.agent = s.agent[1:]
			}
		}
		s.mu.Unlock()

		if step == nil {
			if isSum {
				return nil, errors.New("No scripted summary response")
			}
			return nil, errors.New("No scripted agent response")
		}
		return step(ctx, req)
	})
	for i := range steps {
		steps[i] = factory
	}
	setup.Faux.SetResponses(steps)
	return s
}

func (s *thcpScript) PushAgent(steps ...thcpStep) {
	s.mu.Lock()
	s.agent = append(s.agent, steps...)
	s.mu.Unlock()
}

func (s *thcpScript) PushSummary(steps ...thcpStep) {
	s.mu.Lock()
	s.summaries = append(s.summaries, steps...)
	s.mu.Unlock()
}

func (s *thcpScript) AgentRequests() []thcpRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.agentRequests)
}

func (s *thcpScript) SummaryRequests() []thcpRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.summaryRequests)
}

type thcpModelsWrapper struct {
	ai.MutableModels
	mu                sync.Mutex
	failGetModelPanic error
	failCompleteErr   error
}

func (m *thcpModelsWrapper) GetModel(provider string, id string) *ai.Model {
	m.mu.Lock()
	err := m.failGetModelPanic
	m.mu.Unlock()
	if err != nil {
		panic(err)
	}
	return m.MutableModels.GetModel(provider, id)
}

func (m *thcpModelsWrapper) CompleteSimple(ctx context.Context, model *ai.Model, c *ai.Context, options *ai.ModelsSimpleStreamOptions) (*ai.AssistantMessage, error) {
	m.mu.Lock()
	err := m.failCompleteErr
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return m.MutableModels.CompleteSimple(ctx, model, c, options)
}

func (m *thcpModelsWrapper) SetFailGetModel(err error) {
	m.mu.Lock()
	m.failGetModelPanic = err
	m.mu.Unlock()
}

func (m *thcpModelsWrapper) SetFailCompleteSimple(err error) {
	m.mu.Lock()
	m.failCompleteErr = err
	m.mu.Unlock()
}

func thcpMsgStep(msg *ai.AssistantMessage) thcpStep {
	return func(_ context.Context, _ thcpRequest) (*ai.AssistantMessage, error) {
		return msg, nil
	}
}

func thcpGated(gate *thcpGate, msg *ai.AssistantMessage, reached ...*thcpGate) thcpStep {
	return func(ctx context.Context, _ thcpRequest) (*ai.AssistantMessage, error) {
		for _, r := range reached {
			if r != nil {
				r.Resolve()
			}
		}
		select {
		case <-gate.Ch():
			return msg, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func thcpAnswerMsg(content string) *ai.AssistantMessage {
	return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText(content)}, nil)
}

func thcpAnswer(content string) thcpStep {
	return thcpMsgStep(thcpAnswerMsg(content))
}

func thcpFailureMsg(errorMessage string) *ai.AssistantMessage {
	errStop := ai.StopReasonError
	return providers.FauxAssistantMessage(providers.FauxAssistantBlocks{}, &providers.FauxAssistantMessageOptions{
		StopReason:   &errStop,
		ErrorMessage: &errorMessage,
	})
}

func thcpFailure(errorMessage string) thcpStep {
	return thcpMsgStep(thcpFailureMsg(errorMessage))
}

func thcpSummary(content ...string) thcpStep {
	txt := "SUMMARY"
	if len(content) > 0 {
		txt = content[0]
	}
	return thcpAnswer(txt)
}

const thcpOverflow = "prompt is too long: 250000 tokens > 200000 maximum"

func thcpManualPolicy() durable.CompactionPolicy {
	return durable.CompactionPolicy{Enabled: false, ReserveTokens: 1000, KeepRecentTokens: 150, BackgroundTokens: 0}
}

func thcpBackgroundPolicy() durable.CompactionPolicy {
	return durable.CompactionPolicy{Enabled: true, ReserveTokens: 500, KeepRecentTokens: 150, BackgroundTokens: 1000}
}

func thcpBlockingPolicy() durable.CompactionPolicy {
	return durable.CompactionPolicy{Enabled: true, ReserveTokens: 300, KeepRecentTokens: 150, BackgroundTokens: 0}
}

func thcpPatchFromPolicy(p durable.CompactionPolicy) *durable.CompactionPolicyPatch {
	en := p.Enabled
	res := p.ReserveTokens
	keep := p.KeepRecentTokens
	bg := p.BackgroundTokens
	return &durable.CompactionPolicyPatch{
		Enabled:          &en,
		ReserveTokens:    &res,
		KeepRecentTokens: &keep,
		BackgroundTokens: &bg,
	}
}

type thcpChat struct {
	Harness durable.Harness
	Root    durable.Conversation
	Setup   *testkit.ChatSetup
	Faux    *thcpScript
	Models  *thcpModelsWrapper
}

type thcpOpenOptions struct {
	Policy        *durable.CompactionPolicy
	ContextWindow int
	Storage       durable.Storage
	Setup         *testkit.ChatSetup
	Faux          *thcpScript
}

func thcpOpen(t *testing.T, opts ...thcpOpenOptions) *thcpChat {
	t.Helper()
	var opt thcpOpenOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	setup := opt.Setup
	if setup == nil {
		cw := opt.ContextWindow
		if cw == 0 {
			cw = 100_000
		}
		var err error
		setup, err = testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
			Models: []providers.FauxModelDefinition{
				{Id: "faux-1", ContextWindow: &cw, MaxTokens: thcpPtr(900)},
			},
		})
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
	}
	faux := opt.Faux
	if faux == nil {
		faux = thcpAttachScript(setup)
	}
	if _, err := testkit.AddSection(setup.Registry, "preamble", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
		return "You are helpful.", true, nil
	}, testkit.AddSectionOptions{Tag: thcpPtr(false)}); err != nil {
		t.Fatalf("AddSection: %v", err)
	}
	policy := thcpManualPolicy()
	if opt.Policy != nil {
		policy = *opt.Policy
	}
	setup.UpdateSettings(func(s *durable.HarnessSettings) {
		s.Compaction = thcpPatchFromPolicy(policy)
		s.Retry = &durable.ConversationRetryPolicyPatch{
			Enabled:     thcpPtr(true),
			MaxRetries:  thcpPtr(2),
			BaseDelayMs: thcpPtr(int64(1)),
		}
	})
	st := opt.Storage
	if st == nil {
		st = storage.NewMemoryStorage()
	}
	models := &thcpModelsWrapper{MutableModels: setup.Models}
	prevModels := setup.Models
	setup.Models = models
	h, root, err := testkit.OpenChat(st, setup)
	setup.Models = prevModels
	if err != nil {
		t.Fatalf("OpenChat: %v", err)
	}
	if err := h.Resume(); err != nil {
		_ = h.Close(nil)
		t.Fatalf("Resume: %v", err)
	}
	return &thcpChat{Harness: h, Root: root, Setup: setup, Faux: faux, Models: models}
}

func thcpTurn(t *testing.T, chat *thcpChat, userText, reply string) {
	t.Helper()
	chat.Faux.PushAgent(thcpAnswer(reply))
	settled, err := thgrSubmitInput(t, chat.Root, userText).Wait(nil)
	if err != nil || settled.Status != durable.SubmissionStatusDone {
		t.Fatalf("Turn(%q) settled=%+v err=%v", userText, settled, err)
	}
}

func thcpHistory(t *testing.T, chat *thcpChat) {
	t.Helper()
	thcpTurn(t, chat, thcpText("u1", 100), thcpText("a1", 100))
	thcpTurn(t, chat, thcpText("u2", 100), thcpText("a2", 100))
	thcpTurn(t, chat, thcpText("u3", 100), thcpText("a3", 100))
}

func thcpResult(t *testing.T, chat *thcpChat, id durable.TaskId) *durable.TaskOutcome {
	t.Helper()
	rec, err := chat.Harness.WaitForTask(nil, id)
	if err != nil {
		t.Fatalf("WaitForTask(%d): %v", id, err)
	}
	return rec.State.Outcome
}

func thcpCompactionResult(t *testing.T, outcome *durable.TaskOutcome) durable.CompactionResult {
	t.Helper()
	if outcome == nil || outcome.Status != durable.TaskOutcomeCompleted {
		t.Fatalf("expected completed compaction outcome, got %+v", outcome)
	}
	var res durable.CompactionResult
	raw, ok := outcome.Result.Get()
	if ok && raw != nil {
		if err := jsonx.Decode(raw, &res); err != nil {
			t.Fatalf("Decode CompactionResult: %v", err)
		}
	}
	return res
}

func thcpKinds(t *testing.T, conv durable.Conversation) []string {
	t.Helper()
	entries, err := testkit.AllEntries(conv)
	if err != nil {
		t.Fatalf("AllEntries: %v", err)
	}
	return thgrEntryKinds(entries)
}

func thcpLive(t *testing.T, chat *thcpChat) durable.LiveState {
	t.Helper()
	return thgrLive(t, chat.Harness, chat.Root.Id())
}

func thcpUserText(m ai.Message) string {
	txt, _ := testkit.TextOf(m)
	return txt
}

func thcpCompactionTasks(t *testing.T, chat *thcpChat) []durable.TaskRecord {
	t.Helper()
	insp, err := chat.Harness.Inspect(nil)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	var out []durable.TaskRecord
	for _, item := range insp.Tasks {
		if item.Record.Kind == "pi.compaction" {
			out = append(out, item.Record)
		}
	}
	return out
}

func thcpUsage(t *testing.T, chat *thcpChat) durable.UsageState {
	t.Helper()
	snap, err := chat.Harness.Snapshot(nil, durable.UsageDoc, chat.Root.Id())
	if err != nil {
		t.Fatalf("Snapshot(UsageDoc): %v", err)
	}
	var out durable.UsageState
	if snap != nil {
		if err := jsonx.Decode(snap, &out); err != nil {
			t.Fatalf("Decode UsageState: %v", err)
		}
	}
	return out
}

func thcpUsageInput(t *testing.T, chat *thcpChat) int64 {
	t.Helper()
	u := thcpUsage(t, chat)
	return int64(u.Models["faux/faux-1"].Input)
}

func thcpEmptyTool(name string, execute durable.ToolExecuteFn) *durable.ToolRegistration {
	return durable.DefineTool(&durable.ToolRegistration{
		Name:        name,
		Description: name,
		Parameters:  typebox.Object(omap.NewMap[string, *typebox.Schema]()),
		Execute:     execute,
	})
}

func thcpSubmission(t *testing.T, h durable.Harness, id durable.SubmissionId) durable.Submission {
	t.Helper()
	sub, err := h.Submission(nil, id)
	if err != nil || sub == nil {
		t.Fatalf("Submission(%d): %v err=%v", id, sub, err)
	}
	return sub
}

func thcpEventTypes(events []durable.AgentEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.AgentEventType()
	}
	return out
}

// ─── Range selection & serialization unit helpers ─────────────────────────

func thcpMakeEntry(nextId *durable.EntryId, kind string, model []ai.Message, head ...durable.EntryId) durable.EntryRecord {
	*nextId++
	rec := durable.EntryRecord{
		Id:             *nextId,
		ConversationId: 1,
		Kind:           kind,
		Model:          model,
	}
	if len(head) > 0 {
		h := head[0]
		rec.Head = &h
	}
	return rec
}

func thcpUnitUser(content string) ai.Message {
	c := content
	return &ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &c}}
}

func thcpUnitAssistant(content string, calls []string, stopReason ...ai.StopReason) ai.Message {
	sr := ai.StopReasonStop
	if len(stopReason) > 0 {
		sr = stopReason[0]
	}
	blocks := providers.FauxAssistantBlocks{providers.FauxText(content)}
	for _, cid := range calls {
		id := cid
		blocks = append(blocks, providers.FauxToolCall("read", jsonx.NewObject(), &providers.FauxToolCallOptions{Id: &id}))
	}
	return providers.FauxAssistantMessage(blocks, &providers.FauxAssistantMessageOptions{StopReason: &sr})
}

func thcpUnitToolResult(callId, content string) ai.Message {
	return &ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallId: callId,
		ToolName:   "read",
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: content}},
	}
}

func thcpUnitView(entries []durable.EntryRecord, head *durable.EntryRecord) *durable.ContextView {
	all := make([]durable.EntryRecord, 0, len(entries)+1)
	if head != nil {
		all = append(all, *head)
	}
	all = append(all, entries...)
	contribs := make([][]ai.Message, len(all))
	var flat []ai.Message
	for i, rec := range all {
		var kept []ai.Message
		for _, m := range rec.Model {
			if am, ok := m.(*ai.AssistantMessage); ok {
				if am.StopReason == ai.StopReasonError || am.StopReason == ai.StopReasonAborted || am.StopReason == ai.StopReasonDeferred {
					continue
				}
			}
			kept = append(kept, m)
		}
		contribs[i] = kept
		flat = append(flat, kept...)
	}
	return &durable.ContextView{
		Head:          head,
		Entries:       all,
		Contributions: contribs,
		Messages:      durable.OrderToolResults(flat),
	}
}

func TestHarnessCompactionRangeSelection(t *testing.T) {
	t.Run("keeps about keepRecentTokens and cuts at the first candidate at or after the budget", func(t *testing.T) {
		var nid durable.EntryId
		entries := []durable.EntryRecord{
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser(thcpText("1", 10))}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant("2", []string{"c1"})}),
			thcpMakeEntry(&nid, "pi.tool-result", []ai.Message{thcpUnitToolResult("c1", thcpText("3", 3000))}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant(thcpText("4", 10), nil)}),
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser(thcpText("5", 10))}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant(thcpText("6", 10), nil)}),
		}
		cut, ok := durable.SelectCut(thcpUnitView(entries, nil), 2000)
		if !ok || cut != 3 {
			t.Fatalf("SelectCut = %d, %v, want 3, true", cut, ok)
		}
	})

	t.Run("cuts at a user entry", func(t *testing.T) {
		var nid durable.EntryId
		entries := []durable.EntryRecord{
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser(thcpText("u1", 100))}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant(thcpText("a1", 100), nil)}),
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser(thcpText("u2", 100))}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant(thcpText("a2", 100), nil)}),
		}
		cut, ok := durable.SelectCut(thcpUnitView(entries, nil), 150)
		if !ok || cut != 2 {
			t.Fatalf("SelectCut = %d, %v, want 2, true", cut, ok)
		}
	})

	t.Run("cuts at an assistant in the middle of one long run and never at a tool result", func(t *testing.T) {
		var nid durable.EntryId
		entries := []durable.EntryRecord{thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser("do it")})}
		for i := 0; i < 5; i++ {
			cid := string(rune('a' + i))
			entries = append(entries,
				thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant("step", []string{"c" + cid})}),
				thcpMakeEntry(&nid, "pi.tool-result", []ai.Message{thcpUnitToolResult("c"+cid, thcpText("r"+cid, 100))}),
			)
		}
		cut, ok := durable.SelectCut(thcpUnitView(entries, nil), 150)
		if !ok || entries[cut].Kind != "pi.assistant" || cut != 9 {
			t.Fatalf("SelectCut = %d (%s), %v, want 9 (pi.assistant)", cut, entries[cut].Kind, ok)
		}
	})

	t.Run("keeps a huge last tool result together with its assistant", func(t *testing.T) {
		var nid durable.EntryId
		entries := []durable.EntryRecord{
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser("u")}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant("a", []string{"c"})}),
			thcpMakeEntry(&nid, "pi.tool-result", []ai.Message{thcpUnitToolResult("c", thcpText("big", 5000))}),
		}
		cut, ok := durable.SelectCut(thcpUnitView(entries, nil), 100)
		if !ok || cut != 1 {
			t.Fatalf("SelectCut = %d, %v, want 1, true", cut, ok)
		}
	})

	t.Run("never cuts at a system entry or an excluded error or aborted answer", func(t *testing.T) {
		var nid durable.EntryId
		secs := omap.NewMap[string, *string]()
		sVal := thcpText("s", 100)
		secs.Set("s", &sVal)
		entries := []durable.EntryRecord{
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser(thcpText("u1", 100))}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant(thcpText("a1", 100), nil)}),
			thcpMakeEntry(&nid, "pi.system", []ai.Message{&ai.SystemMessage{Role: "system", Sections: secs}}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant(thcpText("err", 100), nil, ai.StopReasonError)}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant(thcpText("stopped", 100), nil, ai.StopReasonAborted)}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant(thcpText("a2", 100), nil)}),
		}
		cut, ok := durable.SelectCut(thcpUnitView(entries, nil), 150)
		if !ok || cut != 5 {
			t.Fatalf("SelectCut = %d, %v, want 5, true", cut, ok)
		}
	})

	t.Run("follows edited contributions: an omitted entry adds nothing and is no candidate", func(t *testing.T) {
		var nid durable.EntryId
		entries := []durable.EntryRecord{
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser(thcpText("u1", 100))}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant(thcpText("a1", 100), nil)}),
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser(thcpText("u2", 100))}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant(thcpText("a2", 100), nil)}),
		}
		plain := thcpUnitView(entries, nil)
		if cut, ok := durable.SelectCut(plain, 150); !ok || cut != 2 {
			t.Fatalf("plain SelectCut = %d, %v", cut, ok)
		}
		plain.Contributions[2] = nil
		var flat []ai.Message
		for _, c := range plain.Contributions {
			flat = append(flat, c...)
		}
		plain.Messages = durable.OrderToolResults(flat)
		if cut, ok := durable.SelectCut(plain, 150); !ok || cut != 1 {
			t.Fatalf("omitted SelectCut = %d, %v, want 1, true", cut, ok)
		}
	})

	t.Run("does not cut at a user entry that a result of the preceding call still follows", func(t *testing.T) {
		var nid durable.EntryId
		entries := []durable.EntryRecord{
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser(thcpText("u1", 100))}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant("a", []string{"c"})}),
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser(thcpText("steer", 100))}),
			thcpMakeEntry(&nid, "pi.tool-result", []ai.Message{thcpUnitToolResult("c", thcpText("r", 100))}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant(thcpText("a2", 100), nil)}),
		}
		cut, ok := durable.SelectCut(thcpUnitView(entries, nil), 250)
		if !ok || cut != 4 {
			t.Fatalf("SelectCut = %d, %v, want 4, true", cut, ok)
		}
	})

	t.Run("finds nothing when the budget is never reached or only the marker precedes the cut", func(t *testing.T) {
		var nid durable.EntryId
		small := []durable.EntryRecord{
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser("hi")}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant("hello", nil)}),
		}
		if _, ok := durable.SelectCut(thcpUnitView(small, nil), 150); ok {
			t.Fatalf("expected no cut for small view")
		}
		marker := thcpMakeEntry(&nid, "pi.compaction", []ai.Message{thcpUnitUser("summary")}, 0)
		if _, ok := durable.SelectCut(thcpUnitView([]durable.EntryRecord{
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser(thcpText("u", 200))}),
		}, &marker), 150); ok {
			t.Fatalf("expected no cut when only marker precedes cut")
		}
	})

	t.Run("summarizes an earlier summary marker first", func(t *testing.T) {
		var nid durable.EntryId
		marker := thcpMakeEntry(&nid, "pi.compaction", []ai.Message{thcpUnitUser("EARLIER")}, 0)
		kept := []durable.EntryRecord{
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser(thcpText("u1", 100))}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant(thcpText("a1", 100), nil)}),
			thcpMakeEntry(&nid, "pi.user", []ai.Message{thcpUnitUser(thcpText("u2", 100))}),
			thcpMakeEntry(&nid, "pi.assistant", []ai.Message{thcpUnitAssistant(thcpText("a2", 100), nil)}),
		}
		sel := thcpUnitView(kept, &marker)
		cut, ok := durable.SelectCut(sel, 150)
		if !ok || cut != 3 {
			t.Fatalf("SelectCut = %d, %v, want 3, true", cut, ok)
		}
		if ser := durable.SerializeConversation(durable.SummarizedMessages(sel, cut)); !strings.HasPrefix(ser, "[User]: EARLIER") {
			t.Fatalf("serialized = %q", ser)
		}
	})
}

func TestHarnessCompactionSerialization(t *testing.T) {
	t.Run("writes a transcript, truncates tool results, and omits system messages", func(t *testing.T) {
		cid := "c"
		call := providers.FauxToolCall("read", jsonx.ObjectOf("path", "a.ts"), &providers.FauxToolCallOptions{Id: &cid})
		secs := omap.NewMap[string, *string]()
		hidden := "hidden"
		secs.Set("s", &hidden)
		messages := []ai.Message{
			&ai.SystemMessage{Role: "system", Sections: secs},
			thcpUnitUser("hello"),
			providers.FauxAssistantMessage(providers.FauxAssistantBlocks{
				providers.FauxThinking("hmm"),
				providers.FauxText("sure"),
				call,
			}, nil),
			thcpUnitToolResult("c", strings.Repeat("y", 2500)),
		}
		ser := durable.SerializeConversation(messages)
		if strings.Contains(ser, "hidden") {
			t.Fatalf("serialized leaked system content: %s", ser)
		}
		for _, want := range []string{
			"[User]: hello",
			"[Assistant thinking]: hmm",
			"[Assistant]: sure",
			`[Assistant tool calls]: read(path="a.ts")`,
			"[Tool result]: " + strings.Repeat("y", 2000) + "\n\n[... 500 more characters truncated]",
		} {
			if !strings.Contains(ser, want) {
				t.Fatalf("serialized missing %q:\n%s", want, ser)
			}
		}
	})
}

func TestHarnessCompactionManual(t *testing.T) {
	t.Run("places the summary at once when idle and keeps raw history", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		before, err := testkit.AllEntries(chat.Root)
		if err != nil {
			t.Fatalf("AllEntries before: %v", err)
		}
		usageBefore := thcpUsageInput(t, chat)
		chat.Faux.PushSummary(thcpSummary())

		id, err := chat.Root.Compact(thcpPtr("focus on files"), nil)
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		res := thcpCompactionResult(t, thcpResult(t, chat, id))
		if res.SubmissionId == nil {
			t.Fatalf("missing submissionId")
		}
		placed, err := thcpSubmission(t, chat.Harness, *res.SubmissionId).Wait(nil)
		if err != nil || placed.Status != durable.SubmissionStatusDone {
			t.Fatalf("placed = %+v err=%v", placed, err)
		}

		after, err := testkit.AllEntries(chat.Root)
		if err != nil {
			t.Fatalf("AllEntries after: %v", err)
		}
		if !reflect.DeepEqual(after[:len(before)], before) {
			t.Fatalf("raw history changed")
		}
		marker := after[len(after)-1]
		var u3 durable.EntryRecord
		for _, r := range before {
			if len(r.Model) > 0 && strings.HasPrefix(thcpUserText(r.Model[0]), "u3") {
				u3 = r
				break
			}
		}
		if marker.Kind != "pi.compaction" || marker.Head == nil || *marker.Head != u3.Id || placed.Entry == nil || *placed.Entry != marker.Id {
			t.Fatalf("marker = %+v placed=%+v u3=%d", marker, placed, u3.Id)
		}
		var data map[string]any
		if _ = jsonx.Decode(marker.Data, &data); data["reason"] != "manual" {
			t.Fatalf("marker.Data = %v", marker.Data)
		}
		wantSummary := "The conversation history before this point was compacted into the following summary:\n\n<summary>\nSUMMARY\n</summary>"
		if got := thcpUserText(marker.Model[0]); got != wantSummary {
			t.Fatalf("summary text = %q", got)
		}

		view, err := chat.Root.Context(nil)
		if err != nil {
			t.Fatalf("Context: %v", err)
		}
		var viewTexts []string
		for _, m := range view.Messages {
			viewTexts = append(viewTexts, thcpUserText(m))
		}
		if !slices.Equal(viewTexts, []string{wantSummary, thcpText("u3", 100), thcpText("a3", 100)}) {
			t.Fatalf("viewTexts = %v", viewTexts)
		}

		reqs := chat.Faux.SummaryRequests()
		if len(reqs) != 1 || len(reqs[0].Messages) != 2 {
			t.Fatalf("summaryRequests = %+v", reqs)
		}
		prompt := thcpUserText(reqs[0].Messages[1])
		if !strings.HasPrefix(prompt, "<conversation>\n[User]: u1 ") ||
			!strings.Contains(prompt, "[Assistant]: a2 ") ||
			strings.Contains(prompt, "u3 ") ||
			!strings.Contains(prompt, "## Goal") ||
			!strings.HasSuffix(prompt, "\n\nAdditional focus: focus on files") {
			t.Fatalf("prompt = %q", prompt)
		}
		opt := reqs[0].Options
		if opt == nil || opt.CacheRetention == nil || *opt.CacheRetention != ai.CacheRetentionNone ||
			opt.MaxTokens == nil || *opt.MaxTokens != 800 || !opt.Deferred.IsZero() {
			t.Fatalf("summary options = %+v", opt)
		}

		if got := thcpUsageInput(t, chat); got <= usageBefore {
			t.Fatalf("usage input = %d, want > %d", got, usageBefore)
		}
		thcpTurn(t, chat, "next", "done")
		agentReqs := chat.Faux.AgentRequests()
		lastMsgs := agentReqs[len(agentReqs)-1].Messages
		var roles []string
		for _, m := range lastMsgs {
			roles = append(roles, m.GetRole())
		}
		if !slices.Equal(roles, []string{"user", "user", "assistant", "user", "system"}) {
			t.Fatalf("roles = %v", roles)
		}
		sys := lastMsgs[4].(*ai.SystemMessage)
		if pv, ok := sys.Sections.Get("preamble"); !ok || pv == nil || *pv != "You are helpful." {
			t.Fatalf("preamble = %v", pv)
		}
	})

	t.Run("keeps working while busy and places the summary at the next final boundary", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushAgent(thcpGated(gate, thcpAnswerMsg("late answer"), reached))
		input := thgrSubmitInput(t, chat.Root, "busy")
		reached.Wait(t)

		chat.Faux.PushSummary(thcpSummary())
		id, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		res := thcpCompactionResult(t, thcpResult(t, chat, id))
		sub := thcpSubmission(t, chat.Harness, *res.SubmissionId)
		if st, err := sub.Status(nil); err != nil || st.Status != durable.SubmissionStatusQueued {
			t.Fatalf("status = %+v err=%v", st, err)
		}
		gate.Resolve()
		if settled, err := input.Wait(nil); err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("input settled = %+v err=%v", settled, err)
		}
		if placed, err := sub.Wait(nil); err != nil || placed.Status != durable.SubmissionStatusDone {
			t.Fatalf("placed = %+v err=%v", placed, err)
		}
		ks := thcpKinds(t, chat.Root)
		if !slices.Equal(ks[len(ks)-3:], []string{"pi.user", "pi.assistant", "pi.compaction"}) {
			t.Fatalf("tail kinds = %v", ks)
		}
	})

	t.Run("places a queued summary at postTools and the run continues in the compacted context", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		if _, err := testkit.AddTool(chat.Setup.Registry, thcpEmptyTool("wait", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: "waited"}}, HasContent: true}, nil
		})); err != nil {
			t.Fatalf("AddTool: %v", err)
		}
		tools, err := testkit.ToolsNamed(chat.Setup, "wait")
		if err != nil {
			t.Fatalf("ToolsNamed: %v", err)
		}
		if err := chat.Root.Configure(durable.AgentChange{Tools: jsonx.Some(durable.AgentToolsSet(tools...))}, nil); err != nil {
			t.Fatalf("Configure: %v", err)
		}
		thcpHistory(t, chat)
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		toolUse := ai.StopReasonToolUse
		chat.Faux.PushAgent(thcpGated(gate, providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxToolCall("wait", jsonx.NewObject(), nil)},
			&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
		), reached))
		chat.Faux.PushAgent(thcpAnswer("after tools"))
		input := thgrSubmitInput(t, chat.Root, "use a tool")
		reached.Wait(t)
		chat.Faux.PushSummary(thcpSummary())
		compId, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		_ = thcpResult(t, chat, compId)
		gate.Resolve()
		if settled, err := input.Wait(nil); err != nil || settled.Status != durable.SubmissionStatusDone {
			t.Fatalf("settled = %+v err=%v", settled, err)
		}
		reqs := chat.Faux.AgentRequests()
		cont := reqs[len(reqs)-1].Messages
		if !strings.Contains(thcpUserText(cont[0]), "<summary>\nSUMMARY\n</summary>") {
			t.Fatalf("continuation[0] = %q", thcpUserText(cont[0]))
		}
		ks := thcpKinds(t, chat.Root)
		if !slices.Equal(ks[len(ks)-4:], []string{"pi.tool-result", "pi.compaction", "pi.system", "pi.assistant"}) {
			t.Fatalf("tail kinds = %v", ks)
		}
	})

	t.Run("runs follow-ups left by a failed run after placing the summary", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushAgent(thcpGated(gate, thcpFailureMsg("bad request"), reached))
		failed := thgrSubmitInput(t, chat.Root, "fails")
		reached.Wait(t)
		followUp := thgrSubmitInput(t, chat.Root, "follow-up")
		gate.Resolve()
		if st, err := failed.Wait(nil); err != nil || st.Status != durable.SubmissionStatusUnanswered {
			t.Fatalf("failed = %+v err=%v", st, err)
		}
		if st, err := followUp.Status(nil); err != nil || st.Status != durable.SubmissionStatusQueued {
			t.Fatalf("followUp status = %+v err=%v", st, err)
		}
		chat.Faux.PushSummary(thcpSummary())
		chat.Faux.PushAgent(thcpAnswer("followed"))
		compId, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		_ = thcpResult(t, chat, compId)
		if st, err := followUp.Wait(nil); err != nil || st.Status != durable.SubmissionStatusDone {
			t.Fatalf("followUp wait = %+v err=%v", st, err)
		}
		reqs := chat.Faux.AgentRequests()
		last := reqs[len(reqs)-1].Messages
		if !strings.Contains(thcpUserText(last[0]), "SUMMARY") {
			t.Fatalf("last[0] = %q", thcpUserText(last[0]))
		}
		sawFollowUp := false
		for _, m := range last {
			if thcpUserText(m) == "follow-up" {
				sawFollowUp = true
			}
		}
		if !sawFollowUp {
			t.Fatalf("follow-up missing in %+v", last)
		}
	})

	t.Run("settles stale when a reset lands while it summarizes", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		id, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		reached.Wait(t)
		if err := chat.Root.Reset(nil, nil); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		gate.Resolve()
		res := thcpCompactionResult(t, thcpResult(t, chat, id))
		st, err := thcpSubmission(t, chat.Harness, *res.SubmissionId).Status(nil)
		if err != nil || st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "stale" {
			t.Fatalf("st = %+v err=%v", st, err)
		}
		ks := thcpKinds(t, chat.Root)
		if ks[len(ks)-1] != "pi.reset" {
			t.Fatalf("last kind = %q", ks[len(ks)-1])
		}
	})

	t.Run("does not make the conversation busy: a submission during summarization starts its run at once", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		sumGate := thcpNewGate()
		defer sumGate.Resolve()
		sumReached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(sumGate, thcpAnswerMsg("SUMMARY"), sumReached))
		id, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		sumReached.Wait(t)
		ansGate := thcpNewGate()
		defer ansGate.Resolve()
		ansReached := thcpNewGate()
		chat.Faux.PushAgent(thcpGated(ansGate, thcpAnswerMsg("a4"), ansReached))
		input := thgrSubmitInput(t, chat.Root, "u4")
		if st, err := input.Status(nil); err != nil || st.Status != durable.SubmissionStatusPlaced {
			t.Fatalf("input status = %+v err=%v", st, err)
		}
		ansReached.Wait(t)
		reqs := chat.Faux.AgentRequests()
		if thcpUserText(reqs[len(reqs)-1].Messages[0]) != thcpText("u1", 100) {
			t.Fatalf("expected uncompacted first msg, got %q", thcpUserText(reqs[len(reqs)-1].Messages[0]))
		}
		sumGate.Resolve()
		res := thcpCompactionResult(t, thcpResult(t, chat, id))
		sub := thcpSubmission(t, chat.Harness, *res.SubmissionId)
		if st, err := sub.Status(nil); err != nil || st.Status != durable.SubmissionStatusQueued {
			t.Fatalf("sub status = %+v err=%v", st, err)
		}
		ansGate.Resolve()
		if _, err := input.Wait(nil); err != nil {
			t.Fatalf("input Wait: %v", err)
		}
		if _, err := sub.Wait(nil); err != nil {
			t.Fatalf("sub Wait: %v", err)
		}
		ks := thcpKinds(t, chat.Root)
		if !slices.Equal(ks[len(ks)-3:], []string{"pi.user", "pi.assistant", "pi.compaction"}) {
			t.Fatalf("tail kinds = %v", ks)
		}
	})

	t.Run("counts the spend of a summary that ends stale, and writes no entry for it", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		before := thcpUsageInput(t, chat)
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		id, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		reached.Wait(t)
		if err := chat.Root.Reset(nil, nil); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		gate.Resolve()
		res := thcpCompactionResult(t, thcpResult(t, chat, id))
		st, err := thcpSubmission(t, chat.Harness, *res.SubmissionId).Status(nil)
		if err != nil || st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "stale" {
			t.Fatalf("st = %+v err=%v", st, err)
		}
		if after := thcpUsageInput(t, chat); after <= before {
			t.Fatalf("usage = %d, want > %d", after, before)
		}
		if slices.Contains(thcpKinds(t, chat.Root), "pi.compaction") {
			t.Fatalf("unexpected pi.compaction")
		}
	})

	t.Run("lets the compaction that cuts furthest win, whatever finishes first", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		first := thcpNewGate()
		defer first.Resolve()
		firstReached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(first, thcpAnswerMsg("FIRST"), firstReached))
		early, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact early: %v", err)
		}
		firstReached.Wait(t)
		thcpTurn(t, chat, thcpText("u4", 100), thcpText("a4", 100))
		chat.Faux.PushSummary(thcpSummary("SECOND"))
		laterId, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact later: %v", err)
		}
		later := thcpResult(t, chat, laterId)
		if later.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("later = %+v", later)
		}
		first.Resolve()
		earlyRes := thcpCompactionResult(t, thcpResult(t, chat, early))
		st, err := thcpSubmission(t, chat.Harness, *earlyRes.SubmissionId).Status(nil)
		if err != nil || st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "stale" {
			t.Fatalf("early st = %+v err=%v", st, err)
		}
		ctxView, err := chat.Root.Context(nil)
		if err != nil || !strings.Contains(thcpUserText(ctxView.Messages[0]), "SECOND") {
			t.Fatalf("ctxView = %+v err=%v", ctxView, err)
		}
	})

	t.Run("places an older-selected summary that cuts later than the newer one", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("A"), reached))
		a, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact a: %v", err)
		}
		reached.Wait(t)
		p := thcpManualPolicy()
		p.KeepRecentTokens = 350
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
		chat.Faux.PushSummary(thcpSummary("B"))
		bId, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact b: %v", err)
		}
		_ = thcpResult(t, chat, bId)
		ctxB, _ := chat.Root.Context(nil)
		if !strings.Contains(thcpUserText(ctxB.Messages[0]), "B") {
			t.Fatalf("expected B, got %q", thcpUserText(ctxB.Messages[0]))
		}
		gate.Resolve()
		aRes := thcpCompactionResult(t, thcpResult(t, chat, a))
		st, _ := thcpSubmission(t, chat.Harness, *aRes.SubmissionId).Status(nil)
		if st.Status != durable.SubmissionStatusDone {
			t.Fatalf("a status = %+v", st)
		}
		ctxA, _ := chat.Root.Context(nil)
		if !strings.Contains(thcpUserText(ctxA.Messages[0]), "<summary>\nA\n</summary>") {
			t.Fatalf("ctxA[0] = %q", thcpUserText(ctxA.Messages[0]))
		}
		var kept []string
		for _, m := range ctxA.Messages[1:] {
			kept = append(kept, thcpUserText(m))
		}
		if !slices.Equal(kept, []string{thcpText("u3", 100), thcpText("a3", 100)}) {
			t.Fatalf("kept = %v", kept)
		}
	})

	for _, tc := range []struct {
		order string
		keeps [2]int
		stale bool
	}{
		{"before", [2]int{150, 350}, true},
		{"at", [2]int{150, 150}, false},
		{"after", [2]int{350, 150}, false},
	} {
		t.Run("places two queued summaries in one boundary when the second cuts "+tc.order+" the first", func(t *testing.T) {
			chat := thcpOpen(t)
			defer func() { _ = chat.Harness.Close(nil) }()
			thcpHistory(t, chat)
			gate := thcpNewGate()
			defer gate.Resolve()
			reached := thcpNewGate()
			chat.Faux.PushAgent(thcpGated(gate, thcpAnswerMsg("done"), reached))
			input := thgrSubmitInput(t, chat.Root, "busy")
			reached.Wait(t)
			var subs []durable.SubmissionId
			for i, keep := range tc.keeps {
				p := thcpManualPolicy()
				p.KeepRecentTokens = keep
				chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
				chat.Faux.PushSummary(thcpSummary("S" + string(rune('0'+i))))
				cid, err := chat.Root.Compact(nil, nil)
				if err != nil {
					t.Fatalf("Compact: %v", err)
				}
				res := thcpCompactionResult(t, thcpResult(t, chat, cid))
				subs = append(subs, *res.SubmissionId)
			}
			gate.Resolve()
			_, _ = input.Wait(nil)
			var statuses []durable.SubmissionStatus
			for _, id := range subs {
				st, err := thcpSubmission(t, chat.Harness, id).Wait(nil)
				if err != nil {
					t.Fatalf("sub.Wait: %v", err)
				}
				statuses = append(statuses, st.Status)
			}
			wantSecond := durable.SubmissionStatusDone
			if tc.stale {
				wantSecond = durable.SubmissionStatusUnanswered
			}
			if !slices.Equal(statuses, []durable.SubmissionStatus{durable.SubmissionStatusDone, wantSecond}) {
				t.Fatalf("statuses = %v", statuses)
			}
		})
	}

	t.Run("is aborted by Conversation.abort(); an already queued summary survives it", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		g1 := thcpNewGate()
		defer g1.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(g1, thcpAnswerMsg("SUMMARY"), reached))
		id, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		reached.Wait(t)
		if live := thcpLive(t, chat); len(live.Compactions) != 1 {
			t.Fatalf("live.Compactions = %+v", live.Compactions)
		}
		if err := chat.Root.Abort(nil); err != nil {
			t.Fatalf("Abort: %v", err)
		}
		if out := thcpResult(t, chat, id); out.Status != durable.TaskOutcomeAborted {
			t.Fatalf("outcome = %+v", out)
		}
		if live := thcpLive(t, chat); len(live.Compactions) != 0 {
			t.Fatalf("expected empty compactions, got %+v", live.Compactions)
		}
		if slices.Contains(thcpKinds(t, chat.Root), "pi.compaction") {
			t.Fatalf("unexpected pi.compaction")
		}

		g2 := thcpNewGate()
		defer g2.Resolve()
		busy := thcpNewGate()
		chat.Faux.PushAgent(thcpGated(g2, thcpAnswerMsg("never"), busy))
		_ = thgrSubmitInput(t, chat.Root, "busy")
		busy.Wait(t)
		chat.Faux.PushSummary(thcpSummary())
		qId, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact 2: %v", err)
		}
		qRes := thcpCompactionResult(t, thcpResult(t, chat, qId))
		if err := chat.Root.Abort(nil); err != nil {
			t.Fatalf("Abort 2: %v", err)
		}
		sub := thcpSubmission(t, chat.Harness, *qRes.SubmissionId)
		if st, _ := sub.Status(nil); st.Status != durable.SubmissionStatusQueued {
			t.Fatalf("sub status = %+v", st)
		}
		if ares, err := sub.Abort(nil); err != nil || ares != durable.SubmissionAbortAborted {
			t.Fatalf("sub.Abort = %v err=%v", ares, err)
		}
		thcpTurn(t, chat, "next", "ok")
		if st, _ := sub.Status(nil); st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "aborted" {
			t.Fatalf("sub final status = %+v", st)
		}
		if slices.Contains(thcpKinds(t, chat.Root), "pi.compaction") {
			t.Fatalf("unexpected pi.compaction")
		}
	})

	t.Run("is ordinary work: idle waits include it", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		id, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		reached.Wait(t)
		var idle atomic.Bool
		done := make(chan error, 1)
		go func() {
			err := chat.Root.WaitForIdle(nil)
			idle.Store(true)
			done <- err
		}()
		time.Sleep(20 * time.Millisecond)
		if idle.Load() {
			t.Fatalf("WaitForIdle resolved before compaction finished")
		}
		gate.Resolve()
		if err := <-done; err != nil {
			t.Fatalf("WaitForIdle: %v", err)
		}
		if rec, err := chat.Harness.GetTask(nil, id); err != nil || rec.State.Status != durable.TaskStateTerminal {
			t.Fatalf("task = %+v err=%v", rec, err)
		}
	})

	t.Run("enables scheduling right after open", func(t *testing.T) {
		setup, err := testkit.NewChatSetup()
		if err != nil {
			t.Fatalf("NewChatSetup: %v", err)
		}
		faux := thcpAttachScript(setup)
		h, root, err := testkit.OpenChat(storage.NewMemoryStorage(), setup)
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		defer func() { _ = h.Close(nil) }()
		p := thcpManualPolicy()
		p.KeepRecentTokens = 10
		setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
		id, err := root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		if err := testkit.WaitFor(func() (bool, error) {
			rec, err := h.GetTask(nil, id)
			if err != nil || rec == nil {
				return false, err
			}
			return rec.State.Status == durable.TaskStateTerminal, nil
		}); err != nil {
			t.Fatalf("WaitFor terminal: %v", err)
		}
		rec, err := h.GetTask(nil, id)
		if err != nil || rec.State.Outcome == nil || rec.State.Outcome.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("rec = %+v err=%v", rec, err)
		}
		if res := thcpCompactionResult(t, rec.State.Outcome); res.EntryId != nil || res.SubmissionId != nil {
			t.Fatalf("expected empty result, got %+v", res)
		}
		if len(faux.SummaryRequests()) != 0 {
			t.Fatalf("summaryRequests = %+v", faux.SummaryRequests())
		}
	})
}

func TestHarnessCompactionOutcomes(t *testing.T) {
	t.Run("completes without a summary when there is nothing to compact", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpTurn(t, chat, "hi", "hello")
		id, err := chat.Root.Compact(nil, nil)
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		res := thcpCompactionResult(t, thcpResult(t, chat, id))
		if res.EntryId != nil || res.SubmissionId != nil {
			t.Fatalf("expected empty result, got %+v", res)
		}
		if len(chat.Faux.SummaryRequests()) != 0 || len(thcpLive(t, chat).Compactions) != 0 {
			t.Fatalf("unexpected requests/compactions")
		}
	})

	t.Run("asks beforeCompact: the first decision wins, a throw is reported and skipped", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		var mu sync.Mutex
		var seen []durable.CompactionHookInput
		if _, err := testkit.AddHooks(chat.Setup.Registry, durable.CompactionTask, durable.CompactionHooks{
			BeforeCompact: func(_ durable.CompactionHookInput, _ durable.HookApi, _ chord.Context) (*durable.BeforeCompactDecision, error) {
				return nil, errors.New("hook broke")
			},
		}); err != nil {
			t.Fatalf("AddHooks 1: %v", err)
		}
		if _, err := testkit.AddHooks(chat.Setup.Registry, durable.CompactionTask, durable.CompactionHooks{
			BeforeCompact: func(in durable.CompactionHookInput, _ durable.HookApi, _ chord.Context) (*durable.BeforeCompactDecision, error) {
				mu.Lock()
				seen = append(seen, in)
				mu.Unlock()
				return &durable.BeforeCompactDecision{Summary: thcpPtr("FROM HOOK")}, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks 2: %v", err)
		}
		if _, err := testkit.AddHooks(chat.Setup.Registry, durable.CompactionTask, durable.CompactionHooks{
			BeforeCompact: func(_ durable.CompactionHookInput, _ durable.HookApi, _ chord.Context) (*durable.BeforeCompactDecision, error) {
				return &durable.BeforeCompactDecision{Decline: true}, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks 3: %v", err)
		}
		thcpHistory(t, chat)
		id, err := chat.Root.Compact(thcpPtr("why"), nil)
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		if out := thcpResult(t, chat, id); out.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("out = %+v", out)
		}
		if len(chat.Faux.SummaryRequests()) != 0 {
			t.Fatalf("unexpected summary request")
		}
		view, _ := chat.Root.Context(nil)
		if !strings.Contains(thcpUserText(view.Messages[0]), "<summary>\nFROM HOOK\n</summary>") {
			t.Fatalf("view.Messages[0] = %q", thcpUserText(view.Messages[0]))
		}
		reports := chat.Setup.Reports()
		if len(reports) != 1 || !strings.Contains(reports[0].Error(), "hook broke") {
			t.Fatalf("reports = %v", reports)
		}
		mu.Lock()
		comp := seen[0]
		mu.Unlock()
		if comp.Reason != durable.CompactionReasonManual || comp.Instructions == nil || *comp.Instructions != "why" {
			t.Fatalf("comp = %+v", comp)
		}
		if !slices.Equal(thgrEntryKinds(comp.Entries), []string{"pi.user", "pi.system", "pi.assistant", "pi.user", "pi.assistant"}) {
			t.Fatalf("entries kinds = %v", thgrEntryKinds(comp.Entries))
		}
		var texts []string
		for _, m := range comp.Messages {
			if m.GetRole() != "system" {
				texts = append(texts, thcpUserText(m))
			}
		}
		if !slices.Equal(texts, []string{thcpText("u1", 100), thcpText("a1", 100), thcpText("u2", 100), thcpText("a2", 100)}) {
			t.Fatalf("texts = %v", texts)
		}
	})

	t.Run("completes without a summary when a hook declines", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		if _, err := testkit.AddHooks(chat.Setup.Registry, durable.CompactionTask, durable.CompactionHooks{
			BeforeCompact: func(_ durable.CompactionHookInput, _ durable.HookApi, _ chord.Context) (*durable.BeforeCompactDecision, error) {
				return &durable.BeforeCompactDecision{Decline: true}, nil
			},
		}); err != nil {
			t.Fatalf("AddHooks: %v", err)
		}
		thcpHistory(t, chat)
		id, _ := chat.Root.Compact(nil, nil)
		res := thcpCompactionResult(t, thcpResult(t, chat, id))
		if res.EntryId != nil || res.SubmissionId != nil || len(chat.Faux.SummaryRequests()) != 0 {
			t.Fatalf("res=%+v reqs=%d", res, len(chat.Faux.SummaryRequests()))
		}
	})

	t.Run("fails with no_model without a configured model", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		if err := chat.Root.Configure(durable.AgentChange{Model: jsonx.Null[durable.ModelRef]()}, nil); err != nil {
			t.Fatalf("Configure: %v", err)
		}
		id, _ := chat.Root.Compact(nil, nil)
		out := thcpResult(t, chat, id)
		if out.Status != durable.TaskOutcomeFailed || out.Error == nil || !jsonx.DeepEqual(out.Error.Detail, jsonx.ObjectOf("reason", "no_model")) {
			t.Fatalf("out = %+v", out)
		}
		if len(thcpLive(t, chat).Compactions) != 0 {
			t.Fatalf("expected no compactions in live")
		}
	})

	compactionInputDelta := func(t *testing.T, chat *thcpChat, fn func()) int64 {
		before := thcpUsageInput(t, chat)
		fn()
		return thcpUsageInput(t, chat) - before
	}

	t.Run("retries a retryable error with the pinned request and counts every attempt once", func(t *testing.T) {
		single := thcpOpen(t)
		thcpHistory(t, single)
		_ = single.Root.Configure(durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelHigh)}, nil)
		single.Faux.PushSummary(thcpSummary())
		once := compactionInputDelta(t, single, func() {
			id, _ := single.Root.Compact(nil, nil)
			_ = thcpResult(t, single, id)
		})
		_ = single.Harness.Close(nil)
		if once <= 0 {
			t.Fatalf("once = %d", once)
		}

		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		_ = chat.Root.Configure(durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelHigh)}, nil)
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Stream = &durable.ConversationStreamOptions{
				TimeoutMs: thcpPtr(int64(1234)),
				Deferred:  &durable.ConversationDeferredOptions{Enabled: true},
			}
		})
		chat.Faux.PushSummary(func(_ context.Context, _ thcpRequest) (*ai.AssistantMessage, error) {
			_ = chat.Root.Configure(durable.AgentChange{ThinkingLevel: jsonx.Some(ai.ModelThinkingLevelLow)}, nil)
			chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) {
				s.Stream = &durable.ConversationStreamOptions{TimeoutMs: thcpPtr(int64(1))}
			})
			return thcpFailureMsg("overloaded"), nil
		}, thcpSummary())
		twice := compactionInputDelta(t, chat, func() {
			id, _ := chat.Root.Compact(nil, nil)
			if out := thcpResult(t, chat, id); out.Status != durable.TaskOutcomeCompleted {
				t.Fatalf("out = %+v", out)
			}
		})
		reqs := chat.Faux.SummaryRequests()
		if len(reqs) != 2 || twice != 2*once {
			t.Fatalf("reqs=%d twice=%d once=%d", len(reqs), twice, once)
		}
		for _, r := range reqs {
			if r.Options == nil || r.Options.Reasoning == nil || string(*r.Options.Reasoning) != "high" ||
				r.Options.TimeoutMs == nil || *r.Options.TimeoutMs != 1234 ||
				r.Options.CacheRetention == nil || *r.Options.CacheRetention != ai.CacheRetentionNone ||
				!r.Options.Deferred.IsZero() {
				t.Fatalf("req.Options = %+v", r.Options)
			}
		}
	})

	t.Run("adds no usage when a hook declines or supplies the summary", func(t *testing.T) {
		for _, decision := range []durable.BeforeCompactDecision{
			{Decline: true},
			{Summary: thcpPtr("HOOK")},
		} {
			d := decision
			chat := thcpOpen(t)
			_, _ = testkit.AddHooks(chat.Setup.Registry, durable.CompactionTask, durable.CompactionHooks{
				BeforeCompact: func(_ durable.CompactionHookInput, _ durable.HookApi, _ chord.Context) (*durable.BeforeCompactDecision, error) {
					return &d, nil
				},
			})
			thcpHistory(t, chat)
			delta := compactionInputDelta(t, chat, func() {
				id, _ := chat.Root.Compact(nil, nil)
				_ = thcpResult(t, chat, id)
			})
			_ = chat.Harness.Close(nil)
			if delta != 0 {
				t.Fatalf("expected 0 usage delta, got %d", delta)
			}
		}
	})

	t.Run("caps maxTokens at the model's output limit and sends no tools", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		_, _ = testkit.AddTool(chat.Setup.Registry, thcpEmptyTool("read", func(_ any, _ durable.ToolExecutionApi, _ chord.Context) (*durable.ToolExecutionResult, error) {
			return &durable.ToolExecutionResult{HasContent: true}, nil
		}))
		tools, _ := testkit.ToolsNamed(chat.Setup, "read")
		_ = chat.Root.Configure(durable.AgentChange{Tools: jsonx.Some(durable.AgentToolsSet(tools...))}, nil)
		thcpHistory(t, chat)
		p := thcpManualPolicy()
		p.ReserveTokens = 2000
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
		chat.Faux.PushSummary(thcpSummary())
		id, _ := chat.Root.Compact(nil, nil)
		_ = thcpResult(t, chat, id)
		req := chat.Faux.SummaryRequests()[0]
		if req.Options == nil || req.Options.MaxTokens == nil || *req.Options.MaxTokens != 900 || len(req.Messages) != 2 {
			t.Fatalf("req = %+v", req)
		}
		for _, m := range req.Messages {
			if sm, ok := m.(*ai.SystemMessage); ok && len(sm.ToolsAdded) > 0 {
				t.Fatalf("unexpected toolsAdded on system msg: %+v", sm.ToolsAdded)
			}
		}
	})

	toolStop := ai.StopReasonStop
	lengthStop := ai.StopReasonLength
	for _, tc := range []struct {
		name    string
		resp    *ai.AssistantMessage
		message string
		wantReq int
	}{
		{"retries run out", thcpFailureMsg("overloaded"), "Summarization failed: overloaded", 3},
		{"a non-retryable error", thcpFailureMsg("bad request"), "Summarization failed: bad request", 1},
		{"a length stop", providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxText("partial")}, &providers.FauxAssistantMessageOptions{StopReason: &lengthStop}), "Summarization hit the token limit; the summary is incomplete", 1},
		{"a tool call", providers.FauxAssistantMessage(providers.FauxAssistantBlocks{providers.FauxToolCall("read", jsonx.NewObject(), nil)}, &providers.FauxAssistantMessageOptions{StopReason: &toolStop}), "Summarization attempted to call a tool", 1},
		{"empty text", thcpAnswerMsg("  "), "Summarization produced no text", 1},
	} {
		t.Run("fails with model_error on "+tc.name, func(t *testing.T) {
			chat := thcpOpen(t)
			defer func() { _ = chat.Harness.Close(nil) }()
			thcpHistory(t, chat)
			for i := 0; i < 3; i++ {
				chat.Faux.PushSummary(thcpMsgStep(tc.resp))
			}
			id, _ := chat.Root.Compact(nil, nil)
			out := thcpResult(t, chat, id)
			if out.Status != durable.TaskOutcomeFailed || out.Error == nil || out.Error.Message != tc.message ||
				!jsonx.DeepEqual(out.Error.Detail, jsonx.ObjectOf("reason", "model_error")) {
				t.Fatalf("out = %+v, want %q", out, tc.message)
			}
			if got := len(chat.Faux.SummaryRequests()); got != tc.wantReq {
				t.Fatalf("summaryRequests = %d, want %d", got, tc.wantReq)
			}
			if len(thcpLive(t, chat).Compactions) != 0 || slices.Contains(thcpKinds(t, chat.Root), "pi.compaction") {
				t.Fatalf("unexpected live/compaction entry")
			}
		})
	}
}

func TestHarnessCompactionBackgroundThreshold(t *testing.T) {
	t.Run("starts above the background threshold without blocking the run; idle waits and Esc ignore it", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 2000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		bg := thcpBackgroundPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(bg) })
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		thcpTurn(t, chat, thcpText("u4", 100), thcpText("a4", 100))
		reached.Wait(t)
		tasks := thcpCompactionTasks(t, chat)
		if len(tasks) != 1 || !tasks[0].Background || tasks[0].Owner != nil {
			t.Fatalf("tasks = %+v", tasks)
		}
		var cin durable.CompactionInput
		_ = jsonx.Decode(tasks[0].Input, &cin)
		if cin.Reason != durable.CompactionReasonThreshold {
			t.Fatalf("input = %+v", cin)
		}
		live := thcpLive(t, chat)
		want := []durable.CompactionStatus{{TaskId: tasks[0].Id, Reason: durable.CompactionReasonThreshold, Blocking: false, Attempt: 1}}
		if !reflect.DeepEqual(live.Compactions, want) {
			t.Fatalf("live.Compactions = %+v, want %+v", live.Compactions, want)
		}
		if err := chat.Root.WaitForIdle(nil); err != nil {
			t.Fatalf("WaitForIdle: %v", err)
		}
		if err := chat.Root.Abort(nil); err != nil {
			t.Fatalf("Abort: %v", err)
		}
		if rec, _ := chat.Harness.GetTask(nil, tasks[0].Id); rec.State.Status != durable.TaskStateRunning {
			t.Fatalf("task state = %+v", rec.State)
		}
		gate.Resolve()
		if out := thcpResult(t, chat, tasks[0].Id); out.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("out = %+v", out)
		}
		ks := thcpKinds(t, chat.Root)
		if ks[len(ks)-1] != "pi.compaction" {
			t.Fatalf("kinds = %v", ks)
		}
		ctxView, _ := chat.Root.Context(nil)
		if !strings.Contains(thcpUserText(ctxView.Messages[0]), "SUMMARY") {
			t.Fatalf("ctxView[0] = %q", thcpUserText(ctxView.Messages[0]))
		}
	})

	bgDisabled := thcpBackgroundPolicy()
	bgDisabled.Enabled = false
	bgZero := thcpBackgroundPolicy()
	bgZero.BackgroundTokens = 0
	bgNoCut := thcpBackgroundPolicy()
	bgNoCut.KeepRecentTokens = 100_000
	for _, tc := range []struct {
		name   string
		policy durable.CompactionPolicy
	}{
		{"disabled", bgDisabled},
		{"backgroundTokens is 0", bgZero},
		{"there is no cut", bgNoCut},
	} {
		t.Run("does not start when "+tc.name, func(t *testing.T) {
			chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 2000})
			defer func() { _ = chat.Harness.Close(nil) }()
			thcpHistory(t, chat)
			p := tc.policy
			chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
			thcpTurn(t, chat, thcpText("u4", 100), thcpText("a4", 100))
			if len(thcpCompactionTasks(t, chat)) != 0 || len(chat.Faux.SummaryRequests()) != 0 {
				t.Fatalf("unexpected compaction")
			}
		})
	}

	t.Run("does not start while another compaction is listed", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 2000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		manual, _ := chat.Root.Compact(nil, nil)
		reached.Wait(t)
		bg := thcpBackgroundPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(bg) })
		thcpTurn(t, chat, thcpText("u4", 100), thcpText("a4", 100))
		tasks := thcpCompactionTasks(t, chat)
		if len(tasks) != 1 || tasks[0].Id != manual {
			t.Fatalf("tasks = %+v, want [%d]", tasks, manual)
		}
		_, _ = chat.Harness.AbortTask(nil, manual)
	})

	t.Run("stops through abortTask() and Conversation.abort() with background", func(t *testing.T) {
		for _, stop := range []string{"task", "conversation"} {
			chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 2000})
			thcpHistory(t, chat)
			bg := thcpBackgroundPolicy()
			chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(bg) })
			gate := thcpNewGate()
			reached := thcpNewGate()
			chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
			thcpTurn(t, chat, thcpText("u4", 100), thcpText("a4", 100))
			reached.Wait(t)
			tasks := thcpCompactionTasks(t, chat)
			if stop == "task" {
				_, _ = chat.Harness.AbortTask(nil, tasks[0].Id)
			} else {
				_ = chat.Root.Abort(nil, durable.ConversationAbortOptions{Background: true})
			}
			out := thcpResult(t, chat, tasks[0].Id)
			gate.Resolve()
			live := thcpLive(t, chat)
			_ = chat.Harness.Close(nil)
			if out.Status != durable.TaskOutcomeAborted || len(live.Compactions) != 0 {
				t.Fatalf("stop=%s out=%+v live=%+v", stop, out, live)
			}
		}
	})
}

func TestHarnessCompactionBlockingThreshold(t *testing.T) {
	t.Run("waits for its compaction, which appends the summary before the request", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 1000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		blk := thcpBlockingPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(blk) })
		_, _ = testkit.AddSection(chat.Setup.Registry, "extra", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "EXTRA", true, nil
		})
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		chat.Faux.PushAgent(thcpAnswer("a4"))
		input := thgrSubmitInput(t, chat.Root, thcpText("u4", 200))
		reached.Wait(t)
		children := thcpCompactionTasks(t, chat)
		child := children[0]
		genId := thcpLive(t, chat).Run.TaskId
		if child.Owner == nil || *child.Owner != genId || child.Background {
			t.Fatalf("child = %+v genId=%d", child, genId)
		}
		var cin durable.CompactionInput
		_ = jsonx.Decode(child.Input, &cin)
		if cin.Reason != durable.CompactionReasonThreshold {
			t.Fatalf("child input = %+v", cin)
		}
		genRec, _ := chat.Harness.GetTask(nil, genId)
		cp := thgrCheckpoint(t, chat.Harness, genId)
		if genRec.State.Status != durable.TaskStateWaiting || !slices.Equal(genRec.State.On, []durable.TaskId{child.Id}) ||
			cp.Phase != "prepare" || cp.Attempt != 1 || cp.Compacted == nil || *cp.Compacted != child.Id {
			t.Fatalf("gen state = %+v cp=%+v", genRec.State, cp)
		}
		wantComp := []durable.CompactionStatus{{TaskId: child.Id, Reason: durable.CompactionReasonThreshold, Blocking: true, Attempt: 1}}
		if live := thcpLive(t, chat); !reflect.DeepEqual(live.Compactions, wantComp) {
			t.Fatalf("live.Compactions = %+v", live.Compactions)
		}
		if ks := thcpKinds(t, chat.Root); ks[len(ks)-1] != "pi.user" {
			t.Fatalf("last kind before wait = %v", ks)
		}
		gate.Resolve()
		if st, _ := input.Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("input = %+v", st)
		}
		if out := thcpResult(t, chat, child.Id); out.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("child out = %+v", out)
		}
		ks := thcpKinds(t, chat.Root)
		if !slices.Equal(ks[len(ks)-3:], []string{"pi.compaction", "pi.system", "pi.assistant"}) {
			t.Fatalf("tail kinds = %v", ks)
		}
		reqs := chat.Faux.AgentRequests()
		last := reqs[len(reqs)-1].Messages
		if !strings.Contains(thcpUserText(last[0]), "SUMMARY") {
			t.Fatalf("last[0] = %q", thcpUserText(last[0]))
		}
		var systems []*ai.SystemMessage
		for _, m := range last {
			if sm, ok := m.(*ai.SystemMessage); ok {
				systems = append(systems, sm)
			}
		}
		if len(systems) != 1 {
			t.Fatalf("systems = %d", len(systems))
		}
		pv, _ := systems[0].Sections.Get("preamble")
		ev, _ := systems[0].Sections.Get("extra")
		if pv == nil || *pv != "You are helpful." || ev == nil || *ev != "<extra>\nEXTRA\n</extra>" {
			t.Fatalf("sections = preamble:%v extra:%v", pv, ev)
		}
	})

	t.Run("sends the request once, without a second compaction, when the kept part is still above the threshold", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 1000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		p := thcpBlockingPolicy()
		p.KeepRecentTokens = 700
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
		chat.Faux.PushSummary(thcpSummary())
		thcpTurn(t, chat, thcpText("u4", 400), "a4")
		if len(chat.Faux.SummaryRequests()) != 1 {
			t.Fatalf("summaryRequests = %d", len(chat.Faux.SummaryRequests()))
		}
		count := 0
		for _, k := range thcpKinds(t, chat.Root) {
			if k == "pi.compaction" {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("pi.compaction count = %d", count)
		}
	})

	for _, tc := range []struct {
		name    string
		prepare func(*thcpChat)
	}{
		{
			"declines",
			func(chat *thcpChat) {
				_, _ = testkit.AddHooks(chat.Setup.Registry, durable.CompactionTask, durable.CompactionHooks{
					BeforeCompact: func(_ durable.CompactionHookInput, _ durable.HookApi, _ chord.Context) (*durable.BeforeCompactDecision, error) {
						return &durable.BeforeCompactDecision{Decline: true}, nil
					},
				})
			},
		},
		{"fails", func(chat *thcpChat) { chat.Faux.PushSummary(thcpFailure("bad request")) }},
	} {
		t.Run("sends the request anyway when its compaction "+tc.name, func(t *testing.T) {
			chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 1000})
			defer func() { _ = chat.Harness.Close(nil) }()
			thcpHistory(t, chat)
			blk := thcpBlockingPolicy()
			chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(blk) })
			tc.prepare(chat)
			thcpTurn(t, chat, thcpText("u4", 200), "a4")
			if slices.Contains(thcpKinds(t, chat.Root), "pi.compaction") {
				t.Fatalf("unexpected pi.compaction")
			}
			reqs := chat.Faux.AgentRequests()
			if thcpUserText(reqs[len(reqs)-1].Messages[0]) != thcpText("u1", 100) {
				t.Fatalf("first msg = %q", thcpUserText(reqs[len(reqs)-1].Messages[0]))
			}
		})
	}

	t.Run("sends the request anyway when its compaction is aborted directly", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 1000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		blk := thcpBlockingPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(blk) })
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		chat.Faux.PushAgent(thcpAnswer("a4"))
		input := thgrSubmitInput(t, chat.Root, thcpText("u4", 200))
		reached.Wait(t)
		child := thcpCompactionTasks(t, chat)[0]
		_, _ = chat.Harness.AbortTask(nil, child.Id)
		if st, _ := input.Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("input = %+v", st)
		}
		if slices.Contains(thcpKinds(t, chat.Root), "pi.compaction") {
			t.Fatalf("unexpected pi.compaction")
		}
	})

	t.Run("is aborted with its generation by Esc, before the generation's abort handler", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 1000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		blk := thcpBlockingPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(blk) })
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		var evMu sync.Mutex
		var events []durable.AgentEvent
		stream, err := durable.WatchEvents(chat.Harness, chat.Root.Id(), nil)
		if err != nil {
			t.Fatalf("WatchEvents: %v", err)
		}
		defer stream.Stop()
		_ = stream.Start(func(batch []durable.AgentEvent, _ chord.Context) error {
			evMu.Lock()
			events = append(events, batch...)
			evMu.Unlock()
			return nil
		})
		input := thgrSubmitInput(t, chat.Root, thcpText("u4", 200))
		reached.Wait(t)
		child := thcpCompactionTasks(t, chat)[0]
		_ = chat.Root.Abort(nil)
		if st, _ := input.Wait(nil); st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "aborted" {
			t.Fatalf("input = %+v", st)
		}
		if rec, _ := chat.Harness.GetTask(nil, child.Id); rec.State.Outcome == nil || rec.State.Outcome.Status != durable.TaskOutcomeAborted {
			t.Fatalf("child = %+v", rec.State)
		}
		_ = testkit.WaitFor(func() (bool, error) {
			evMu.Lock()
			defer evMu.Unlock()
			return slices.Contains(thcpEventTypes(events), "run_end"), nil
		})
		evMu.Lock()
		types := thcpEventTypes(events)
		evMu.Unlock()
		cIdx := slices.Index(types, "compaction_end")
		rIdx := slices.Index(types, "run_end")
		if cIdx < 0 || rIdx < 0 || cIdx >= rIdx {
			t.Fatalf("event order = %v", types)
		}
	})

	t.Run("wins over a background compaction still in flight, which then settles stale", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 2000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		bg := thcpBackgroundPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(bg) })
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("BACKGROUND"), reached))
		thcpTurn(t, chat, thcpText("u4", 100), thcpText("a4", 100))
		reached.Wait(t)
		background := thcpCompactionTasks(t, chat)[0]
		chat.Faux.PushSummary(thcpSummary("BLOCKING"))
		thcpTurn(t, chat, thcpText("u5", 1000), "a5")
		ctxView, _ := chat.Root.Context(nil)
		if !strings.Contains(thcpUserText(ctxView.Messages[0]), "BLOCKING") {
			t.Fatalf("ctxView[0] = %q", thcpUserText(ctxView.Messages[0]))
		}
		gate.Resolve()
		res := thcpCompactionResult(t, thcpResult(t, chat, background.Id))
		st, _ := thcpSubmission(t, chat.Harness, *res.SubmissionId).Status(nil)
		if st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "stale" {
			t.Fatalf("st = %+v", st)
		}
	})
}

func TestHarnessCompactionOverflow(t *testing.T) {
	enabledPolicy := func() durable.CompactionPolicy {
		p := thcpManualPolicy()
		p.Enabled = true
		return p
	}

	t.Run("compacts and retries with the same attempt, leaving the error out of the retry", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		p := enabledPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
		chat.Faux.PushSummary(thcpSummary())
		var attempt atomic.Int32
		chat.Faux.PushAgent(thcpFailure(thcpOverflow), func(_ context.Context, _ thcpRequest) (*ai.AssistantMessage, error) {
			if g := thcpLive(t, chat).Generation; g != nil {
				attempt.Store(int32(g.Attempt))
			}
			return thcpAnswerMsg("fits"), nil
		})
		st, _ := thgrSubmitInput(t, chat.Root, thcpText("u4", 100)).Wait(nil)
		if st.Status != durable.SubmissionStatusDone || attempt.Load() != 1 {
			t.Fatalf("st=%+v attempt=%d", st, attempt.Load())
		}
		ks := thcpKinds(t, chat.Root)
		if !slices.Equal(ks[len(ks)-5:], []string{"pi.user", "pi.assistant", "pi.compaction", "pi.system", "pi.assistant"}) {
			t.Fatalf("tail = %v", ks)
		}
		entries, _ := testkit.AllEntries(chat.Root)
		var found bool
		for _, e := range entries {
			if e.Kind == "pi.compaction" {
				var d map[string]any
				_ = jsonx.Decode(e.Data, &d)
				if d["reason"] == "overflow" {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("missing overflow compaction entry")
		}
		reqs := chat.Faux.AgentRequests()
		retry := reqs[len(reqs)-1].Messages
		if !strings.Contains(thcpUserText(retry[0]), "SUMMARY") {
			t.Fatalf("retry[0] = %q", thcpUserText(retry[0]))
		}
		for _, m := range retry {
			if am, ok := m.(*ai.AssistantMessage); ok && am.StopReason == ai.StopReasonError {
				t.Fatalf("unexpected error entry in retry")
			}
		}
	})

	t.Run("fails a second overflow with its error entry", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		p := enabledPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
		chat.Faux.PushSummary(thcpSummary())
		chat.Faux.PushAgent(thcpFailure(thcpOverflow), thcpFailure(thcpOverflow))
		st, _ := thgrSubmitInput(t, chat.Root, thcpText("u4", 100)).Wait(nil)
		if st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "model_error" || st.Detail != thcpOverflow {
			t.Fatalf("st = %+v", st)
		}
		ks := thcpKinds(t, chat.Root)
		cnt := 0
		for _, k := range ks {
			if k == "pi.compaction" {
				cnt++
			}
		}
		if cnt != 1 || ks[len(ks)-1] != "pi.assistant" {
			t.Fatalf("kinds = %v", ks)
		}
	})

	t.Run("fails without compacting when compaction is disabled, even for a retryable-looking overflow", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		chat.Faux.PushAgent(thcpFailure("overloaded: " + thcpOverflow))
		st, _ := thgrSubmitInput(t, chat.Root, thcpText("u4", 100)).Wait(nil)
		if st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "model_error" {
			t.Fatalf("st = %+v", st)
		}
		if len(chat.Faux.AgentRequests()) != 4 || len(chat.Faux.SummaryRequests()) != 0 {
			t.Fatalf("agentReqs=%d summaryReqs=%d", len(chat.Faux.AgentRequests()), len(chat.Faux.SummaryRequests()))
		}
	})

	t.Run("fails after a blocking threshold compaction in the same generation", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 1000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		blk := thcpBlockingPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(blk) })
		chat.Faux.PushSummary(thcpSummary())
		chat.Faux.PushAgent(thcpFailure(thcpOverflow))
		st, _ := thgrSubmitInput(t, chat.Root, thcpText("u4", 200)).Wait(nil)
		if st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "model_error" {
			t.Fatalf("st = %+v", st)
		}
		if len(chat.Faux.SummaryRequests()) != 1 {
			t.Fatalf("summaryRequests = %d", len(chat.Faux.SummaryRequests()))
		}
	})

	for _, tc := range []struct {
		name    string
		prepare func(*thcpChat)
		reqs    int
	}{
		{
			"declines",
			func(chat *thcpChat) {
				_, _ = testkit.AddHooks(chat.Setup.Registry, durable.CompactionTask, durable.CompactionHooks{
					BeforeCompact: func(_ durable.CompactionHookInput, _ durable.HookApi, _ chord.Context) (*durable.BeforeCompactDecision, error) {
						return &durable.BeforeCompactDecision{Decline: true}, nil
					},
				})
			},
			0,
		},
		{"fails", func(chat *thcpChat) { chat.Faux.PushSummary(thcpFailure("bad request")) }, 1},
		{
			"cannot cut",
			func(chat *thcpChat) {
				p := enabledPolicy()
				p.KeepRecentTokens = 100_000
				chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
			},
			0,
		},
	} {
		t.Run("fails with the overflow text when compaction "+tc.name, func(t *testing.T) {
			chat := thcpOpen(t)
			defer func() { _ = chat.Harness.Close(nil) }()
			thcpTurn(t, chat, thcpText("u1", 100), thcpText("a1", 100))
			p := enabledPolicy()
			chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
			tc.prepare(chat)
			chat.Faux.PushAgent(thcpFailure(thcpOverflow))
			st, _ := thgrSubmitInput(t, chat.Root, thcpText("u4", 100)).Wait(nil)
			if st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "model_error" || st.Detail != thcpOverflow {
				t.Fatalf("st = %+v", st)
			}
			if got := len(chat.Faux.SummaryRequests()); got != tc.reqs {
				t.Fatalf("summaryRequests = %d, want %d", got, tc.reqs)
			}
		})
	}
}

func TestHarnessCompactionInteractions(t *testing.T) {
	for _, clock := range []string{"real", "fixed"} {
		t.Run("ignores usage measured before a summary placed mid-run ("+clock+" clock)", func(t *testing.T) {
			cw := 2000
			setup, err := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
				Models: []providers.FauxModelDefinition{{Id: "faux-1", ContextWindow: &cw, MaxTokens: thcpPtr(900)}},
			})
			if err != nil {
				t.Fatalf("NewChatSetup: %v", err)
			}
			if clock == "fixed" {
				setup.SetNow(func() int64 { return 1000 })
			}
			chat := thcpOpen(t, thcpOpenOptions{ContextWindow: cw, Setup: setup})
			defer func() { _ = chat.Harness.Close(nil) }()
			toolGate := thcpNewGate()
			defer toolGate.Resolve()
			toolReached := thcpNewGate()
			_, _ = testkit.AddTool(chat.Setup.Registry, thcpEmptyTool("slow", func(_ any, _ durable.ToolExecutionApi, ctx chord.Context) (*durable.ToolExecutionResult, error) {
				toolReached.Resolve()
				select {
				case <-toolGate.Ch():
				case <-ctx.AbortSignal().Done():
					return nil, testkit.Aborted(ctx.AbortSignal())
				}
				return &durable.ToolExecutionResult{Content: ai.ContentList{&ai.TextContent{Type: "text", Text: thcpText("result", 200)}}, HasContent: true}, nil
			}))
			tools, _ := testkit.ToolsNamed(chat.Setup, "slow")
			_ = chat.Root.Configure(durable.AgentChange{Tools: jsonx.Some(durable.AgentToolsSet(tools...))}, nil)
			thcpTurn(t, chat, thcpText("u1", 220), thcpText("a1", 220))
			thcpTurn(t, chat, thcpText("u2", 220), thcpText("a2", 220))
			thcpTurn(t, chat, thcpText("u3", 220), thcpText("a3", 220))
			p := thcpBackgroundPolicy()
			p.BackgroundTokens = 600
			chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
			toolUse := ai.StopReasonToolUse
			chat.Faux.PushAgent(thcpMsgStep(providers.FauxAssistantMessage(
				providers.FauxAssistantBlocks{providers.FauxToolCall("slow", jsonx.NewObject(), nil)},
				&providers.FauxAssistantMessageOptions{StopReason: &toolUse},
			)))
			chat.Faux.PushAgent(thcpAnswer("done"))
			sumGate := thcpNewGate()
			defer sumGate.Resolve()
			sumReached := thcpNewGate()
			chat.Faux.PushSummary(thcpGated(sumGate, thcpAnswerMsg("SUMMARY"), sumReached))
			input := thgrSubmitInput(t, chat.Root, thcpText("u4", 50))
			toolReached.Wait(t)
			sumReached.Wait(t)
			background := thcpCompactionTasks(t, chat)[0]
			sumGate.Resolve()
			queued := thcpCompactionResult(t, thcpResult(t, chat, background.Id))
			sub := thcpSubmission(t, chat.Harness, *queued.SubmissionId)
			if st, _ := sub.Status(nil); st.Status != durable.SubmissionStatusQueued {
				t.Fatalf("sub status = %+v", st)
			}
			toolGate.Resolve()
			if st, _ := input.Wait(nil); st.Status != durable.SubmissionStatusDone {
				t.Fatalf("input = %+v", st)
			}
			if len(chat.Faux.SummaryRequests()) != 1 || len(thcpCompactionTasks(t, chat)) != 0 {
				t.Fatalf("summaryReqs=%d tasks=%d", len(chat.Faux.SummaryRequests()), len(thcpCompactionTasks(t, chat)))
			}
			reqs := chat.Faux.AgentRequests()
			if !strings.Contains(thcpUserText(reqs[len(reqs)-1].Messages[0]), "SUMMARY") {
				t.Fatalf("last request[0] = %q", thcpUserText(reqs[len(reqs)-1].Messages[0]))
			}
		})
	}

	t.Run("rebaselines the system prompt over kept system deltas", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		var mu sync.Mutex
		mood := "cheerful"
		_, _ = testkit.AddSection(chat.Setup.Registry, "mood", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			mu.Lock()
			defer mu.Unlock()
			return mood, true, nil
		}, testkit.AddSectionOptions{Tag: thcpPtr(false)})
		thcpTurn(t, chat, thcpText("u1", 100), thcpText("a1", 100))
		thcpTurn(t, chat, thcpText("u2", 100), thcpText("a2", 100))
		mu.Lock()
		mood = "terse"
		mu.Unlock()
		thcpTurn(t, chat, thcpText("u3", 100), thcpText("a3", 100))
		p := thcpManualPolicy()
		p.KeepRecentTokens = 250
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
		chat.Faux.PushSummary(thcpSummary())
		id, _ := chat.Root.Compact(nil, nil)
		_ = thcpResult(t, chat, id)
		ctxView, _ := chat.Root.Context(nil)
		hasSys := false
		for _, r := range ctxView.Entries {
			if r.Kind == "pi.system" {
				hasSys = true
			}
		}
		if !hasSys {
			t.Fatalf("expected kept pi.system in entries")
		}
		thcpTurn(t, chat, "next", "ok")
		reqs := chat.Faux.AgentRequests()
		var systems []*ai.SystemMessage
		for _, m := range reqs[len(reqs)-1].Messages {
			if sm, ok := m.(*ai.SystemMessage); ok {
				systems = append(systems, sm)
			}
		}
		if len(systems) != 1 {
			t.Fatalf("systems = %d", len(systems))
		}
		pv, _ := systems[0].Sections.Get("preamble")
		mv, _ := systems[0].Sections.Get("mood")
		if pv == nil || *pv != "You are helpful." || mv == nil || *mv != "terse" {
			t.Fatalf("sections preamble=%v mood=%v", pv, mv)
		}
	})

	t.Run("summarizes a replaced entry's replacement and shows it to the hook", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		entries, _ := testkit.AllEntries(chat.Root)
		u1 := entries[0]
		if _, err := chat.Root.Submit(durable.SubmissionDraft{
			Type: durable.SubmissionTypeWrite,
			Entry: &durable.EntryDraft{
				Kind: "app.redact",
				Edits: []durable.ContextEdit{{
					Target:   u1.Id,
					Action:   durable.ContextEditActionReplace,
					Messages: ai.MessageList{thcpUnitUser("REDACTED")},
				}},
			},
		}, nil); err != nil {
			t.Fatalf("Submit redact: %v", err)
		}
		var mu sync.Mutex
		var hookMsgs []ai.Message
		_, _ = testkit.AddHooks(chat.Setup.Registry, durable.CompactionTask, durable.CompactionHooks{
			BeforeCompact: func(in durable.CompactionHookInput, _ durable.HookApi, _ chord.Context) (*durable.BeforeCompactDecision, error) {
				mu.Lock()
				hookMsgs = in.Messages
				mu.Unlock()
				return nil, nil
			},
		})
		chat.Faux.PushSummary(thcpSummary())
		id, _ := chat.Root.Compact(nil, nil)
		_ = thcpResult(t, chat, id)
		mu.Lock()
		firstText := thcpUserText(hookMsgs[0])
		mu.Unlock()
		if firstText != "REDACTED" {
			t.Fatalf("hookMsgs[0] = %q", firstText)
		}
		if !strings.Contains(thcpUserText(chat.Faux.SummaryRequests()[0].Messages[1]), "[User]: REDACTED") {
			t.Fatalf("summary prompt = %q", thcpUserText(chat.Faux.SummaryRequests()[0].Messages[1]))
		}
	})

	t.Run("compacts a fork whose cut falls on a parent entry", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		entries, _ := testkit.AllEntries(chat.Root)
		fork, err := chat.Root.Fork(entries[len(entries)-1].Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, nil)
		if err != nil {
			t.Fatalf("Fork: %v", err)
		}
		chat.Faux.PushSummary(thcpSummary())
		id, _ := fork.Compact(nil, nil)
		res := thcpCompactionResult(t, thcpResult(t, chat, id))
		if st, _ := thcpSubmission(t, chat.Harness, *res.SubmissionId).Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("st = %+v", st)
		}
		var u3 durable.EntryRecord
		for _, r := range entries {
			if len(r.Model) > 0 && strings.HasPrefix(thcpUserText(r.Model[0]), "u3") {
				u3 = r
				break
			}
		}
		view, _ := fork.Context(nil)
		if view.Head == nil || view.Head.Kind != "pi.compaction" || view.Head.Head == nil || *view.Head.Head != u3.Id || view.Head.ConversationId != fork.Id() {
			t.Fatalf("fork head = %+v", view.Head)
		}
		var kept []string
		for _, m := range view.Messages[1:] {
			kept = append(kept, thcpUserText(m))
		}
		if !slices.Equal(kept, []string{thcpText("u3", 100), thcpText("a3", 100)}) {
			t.Fatalf("kept = %v", kept)
		}
		if slices.Contains(thcpKinds(t, chat.Root), "pi.compaction") {
			t.Fatalf("parent touched")
		}
		state, _ := fork.ViewState(nil)
		defer state.Dispose()
		val, _ := state.Value()
		if !reflect.DeepEqual(val.Entries, view.Entries) {
			t.Fatalf("viewState entries != view.Entries")
		}
	})

	t.Run("settles stale in a fork reset while it summarizes", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		entries, _ := testkit.AllEntries(chat.Root)
		fork, _ := chat.Root.Fork(entries[len(entries)-1].Id, durable.ConversationCreateOptions{
			Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnershipOwnerless},
		}, nil)
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		id, _ := fork.Compact(nil, nil)
		reached.Wait(t)
		_ = fork.Reset(nil, nil)
		gate.Resolve()
		res := thcpCompactionResult(t, thcpResult(t, chat, id))
		st, _ := thcpSubmission(t, chat.Harness, *res.SubmissionId).Status(nil)
		if st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "stale" {
			t.Fatalf("st = %+v", st)
		}
		if cv, _ := fork.Context(nil); cv.Head == nil || cv.Head.Kind != "pi.reset" {
			t.Fatalf("fork head = %+v", cv.Head)
		}
	})

	t.Run("places an older queued summary and the current one together when idle admission drains the inbox", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushAgent(thcpGated(gate, thcpFailureMsg("bad request"), reached))
		failed := thgrSubmitInput(t, chat.Root, "fails")
		reached.Wait(t)
		chat.Faux.PushSummary(thcpSummary("OLDER"))
		olderId, _ := chat.Root.Compact(nil, nil)
		older := thcpResult(t, chat, olderId)
		gate.Resolve()
		_, _ = failed.Wait(nil)
		chat.Faux.PushSummary(thcpSummary("CURRENT"))
		currId, _ := chat.Root.Compact(nil, nil)
		current := thcpResult(t, chat, currId)
		for _, out := range []*durable.TaskOutcome{older, current} {
			res := thcpCompactionResult(t, out)
			if st, _ := thcpSubmission(t, chat.Harness, *res.SubmissionId).Status(nil); st.Status != durable.SubmissionStatusDone {
				t.Fatalf("st = %+v", st)
			}
		}
		cv, _ := chat.Root.Context(nil)
		if !strings.Contains(thcpUserText(cv.Messages[0]), "CURRENT") {
			t.Fatalf("cv[0] = %q", thcpUserText(cv.Messages[0]))
		}
	})

	t.Run("places a hook's summary and holds while work the hook created runs", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		childGate := thcpNewGate()
		defer childGate.Resolve()
		childTask := thcpDefineChildTask(childGate)
		_, _ = testkit.AddTask(chat.Setup.Registry, childTask)
		_, _ = testkit.AddHooks(chat.Setup.Registry, durable.CompactionTask, durable.CompactionHooks{
			BeforeCompact: func(_ durable.CompactionHookInput, api durable.HookApi, hookCtx chord.Context) (*durable.BeforeCompactDecision, error) {
				tid := api.TaskId()
				cid := chat.Root.Id()
				if err := chat.Harness.Commit(hookCtx, func(tx durable.Tx) error {
					_, err := tx.CreateTask(childTask, jsonx.NewObject(), durable.TaskOptions{
						Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipTask, TaskId: tid},
						ConversationId: &cid,
					})
					return err
				}); err != nil {
					return nil, err
				}
				return &durable.BeforeCompactDecision{Summary: thcpPtr("HOOK")}, nil
			},
		})
		thcpHistory(t, chat)
		id, _ := chat.Root.Compact(nil, nil)
		if err := testkit.WaitFor(func() (bool, error) {
			rec, _ := chat.Harness.GetTask(nil, id)
			return rec != nil && rec.State.Status == durable.TaskStateCompleting, nil
		}); err != nil {
			t.Fatalf("WaitFor completing: %v", err)
		}
		ks := thcpKinds(t, chat.Root)
		if ks[len(ks)-1] != "pi.compaction" || len(thcpLive(t, chat).Compactions) != 0 {
			t.Fatalf("ks=%v live=%+v", ks, thcpLive(t, chat))
		}
		childGate.Resolve()
		if out := thcpResult(t, chat, id); out.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("out = %+v", out)
		}
	})

	t.Run("removes the status of a faulted compaction", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		chat.Models.SetFailGetModel(errors.New("models broke"))
		id, _ := chat.Root.Compact(nil, nil)
		out := thcpResult(t, chat, id)
		chat.Models.SetFailGetModel(nil)
		if out.Status != durable.TaskOutcomeFaulted || out.Error == nil || out.Error.Message != "models broke" {
			t.Fatalf("out = %+v", out)
		}
		if len(thcpLive(t, chat).Compactions) != 0 {
			t.Fatalf("live.Compactions = %+v", thcpLive(t, chat).Compactions)
		}
	})
}

func thcpDefineChildTask(gate *thcpGate) durable.AnyTask {
	type step struct {
		Phase string `json:"phase"`
	}
	return durable.DefineTask(durable.TaskDefinition[durable.JsonObject, step, any, struct{}]{
		Name:    "test.child",
		Version: 1,
		Initial: func(_ durable.JsonObject) step { return step{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonObject, step, any, struct{}]{
			"run": func(ctx chord.Context, _ durable.RunningTask[durable.JsonObject, step], rt *durable.TaskRuntime[durable.JsonObject, step, any, struct{}]) error {
				select {
				case <-gate.Ch():
				case <-rt.Signal().Done():
					return rt.Signal().Err()
				}
				return rt.Commit(ctx, func(_ durable.Tx, cp durable.RunningTask[durable.JsonObject, step]) (*durable.NextTaskState[step, any], error) {
					next := testkit.Completed[step, any](cp.Checkpoint, nil)
					return &next, nil
				})
			},
		},
		Abort: func(ctx chord.Context, _ durable.RunningTask[durable.JsonObject, step], rt *durable.TaskRuntime[durable.JsonObject, step, any, struct{}]) error {
			return rt.Commit(ctx, func(_ durable.Tx, cp durable.RunningTask[durable.JsonObject, step]) (*durable.NextTaskState[step, any], error) {
				next := testkit.AbortedWith[step, any](cp.Checkpoint, "")
				return &next, nil
			})
		},
	})
}

func TestHarnessCompactionEventsAndLiveStatus(t *testing.T) {
	t.Run("reports start and end, and the retry backoff in a late joiner's snapshot", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Retry = &durable.ConversationRetryPolicyPatch{
				Enabled:     thcpPtr(true),
				MaxRetries:  thcpPtr(2),
				BaseDelayMs: thcpPtr(int64(60000)),
			}
		})
		var evMu sync.Mutex
		var events []durable.AgentEvent
		stream, _ := durable.WatchEvents(chat.Harness, chat.Root.Id(), nil)
		defer stream.Stop()
		_ = stream.Start(func(batch []durable.AgentEvent, _ chord.Context) error {
			evMu.Lock()
			events = append(events, batch...)
			evMu.Unlock()
			return nil
		})
		chat.Faux.PushSummary(thcpFailure("overloaded"))
		id, _ := chat.Root.Compact(nil, nil)
		if err := testkit.WaitFor(func() (bool, error) {
			cs := thcpLive(t, chat).Compactions
			return len(cs) > 0 && cs[0].Retry != nil, nil
		}); err != nil {
			t.Fatalf("WaitFor retry: %v", err)
		}
		late, err := durable.WatchEvents(chat.Harness, chat.Root.Id(), nil)
		if err != nil {
			t.Fatalf("late WatchEvents: %v", err)
		}
		snap := late.Snapshot()
		late.Stop()
		if len(snap.Compactions) != 1 || snap.Compactions[0].TaskId != id || snap.Compactions[0].Reason != durable.CompactionReasonManual ||
			snap.Compactions[0].Blocking || snap.Compactions[0].Attempt != 1 ||
			snap.Compactions[0].Retry == nil || snap.Compactions[0].Retry.Error != "overloaded" {
			t.Fatalf("late.Snapshot.Compactions = %+v", snap.Compactions)
		}
		_, _ = chat.Harness.AbortTask(nil, id)
		_ = testkit.WaitFor(func() (bool, error) {
			evMu.Lock()
			defer evMu.Unlock()
			return slices.Contains(thcpEventTypes(events), "compaction_end"), nil
		})
		evMu.Lock()
		var compEvents []durable.AgentEvent
		for _, e := range events {
			if strings.HasPrefix(e.AgentEventType(), "compaction_") {
				compEvents = append(compEvents, e)
			}
		}
		evMu.Unlock()
		if len(compEvents) != 2 ||
			!reflect.DeepEqual(compEvents[0], &durable.CompactionStartEvent{Type: "compaction_start", TaskId: id, Reason: durable.CompactionReasonManual, Blocking: false}) ||
			!reflect.DeepEqual(compEvents[1], &durable.CompactionEndEvent{Type: "compaction_end", TaskId: id, Reason: durable.CompactionReasonManual}) {
			t.Fatalf("compEvents = %#v", compEvents)
		}
	})

	t.Run("lists concurrent compactions in task ID order", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		g1, g2 := thcpNewGate(), thcpNewGate()
		defer g1.Resolve()
		defer g2.Resolve()
		r1, r2 := thcpNewGate(), thcpNewGate()
		chat.Faux.PushSummary(thcpGated(g1, thcpAnswerMsg("S"), r1), thcpGated(g2, thcpAnswerMsg("S"), r2))
		a, _ := chat.Root.Compact(nil, nil)
		b, _ := chat.Root.Compact(nil, nil)
		r1.Wait(t)
		r2.Wait(t)
		var ids []durable.TaskId
		for _, st := range thcpLive(t, chat).Compactions {
			ids = append(ids, st.TaskId)
		}
		if !slices.Equal(ids, []durable.TaskId{a, b}) {
			t.Fatalf("compaction task IDs = %v, want [%d, %d]", ids, a, b)
		}
		_ = chat.Root.Abort(nil)
		if len(thcpLive(t, chat).Compactions) != 0 {
			t.Fatalf("expected empty compactions after abort")
		}
	})
}

func TestHarnessCompactionRecovery(t *testing.T) {
	openSqlite := func(t *testing.T, path string, setup *testkit.ChatSetup, faux *thcpScript, cwZero bool) *thcpChat {
		t.Helper()
		st, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("OpenNodeSqliteStorage: %v", err)
		}
		if setup == nil {
			var cwOpt *providers.RegisterFauxProviderOptions
			if !cwZero {
				cw := 100_000
				cwOpt = &providers.RegisterFauxProviderOptions{Models: []providers.FauxModelDefinition{{Id: "faux-1", ContextWindow: &cw, MaxTokens: thcpPtr(900)}}}
				_ = cwOpt
			}
			setup, err = testkit.NewChatSetup()
			if err != nil {
				t.Fatalf("NewChatSetup: %v", err)
			}
			_, _ = testkit.AddSection(setup.Registry, "preamble", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
				return "You are helpful.", true, nil
			}, testkit.AddSectionOptions{Tag: thcpPtr(false)})
			faux = thcpAttachScript(setup)
		}
		models := &thcpModelsWrapper{MutableModels: setup.Models}
		prev := setup.Models
		setup.Models = models
		h, root, err := testkit.OpenChat(st, setup)
		setup.Models = prev
		if err != nil {
			t.Fatalf("OpenChat: %v", err)
		}
		_ = h.Resume()
		return &thcpChat{Harness: h, Root: root, Setup: setup, Faux: faux, Models: models}
	}
	first := func(t *testing.T, path string) *thcpChat {
		chat := openSqlite(t, path, nil, nil, true)
		p := thcpManualPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Compaction = thcpPatchFromPolicy(p)
			s.Retry = &durable.ConversationRetryPolicyPatch{Enabled: thcpPtr(true), MaxRetries: thcpPtr(2), BaseDelayMs: thcpPtr(int64(1))}
		})
		thcpHistory(t, chat)
		return chat
	}

	t.Run("repeats nothing after reopen once the summary is placed", func(t *testing.T) {
		path := thcpSqlitePath(t)
		chat := first(t, path)
		closed := false
		defer func() {
			if !closed {
				_ = chat.Harness.Close(nil)
			}
		}()
		chat.Faux.PushSummary(thcpSummary())
		id, _ := chat.Root.Compact(nil, nil)
		_ = thcpResult(t, chat, id)
		entries, _ := testkit.AllEntries(chat.Root)
		usage := thcpUsage(t, chat)
		_ = chat.Harness.Close(nil)
		closed = true

		chat2 := openSqlite(t, path, chat.Setup, chat.Faux, true)
		defer func() { _ = chat2.Harness.Close(nil) }()
		_ = chat2.Harness.WaitForIdle(nil)
		if len(chat2.Faux.SummaryRequests()) != 1 {
			t.Fatalf("summaryRequests = %d", len(chat2.Faux.SummaryRequests()))
		}
		afterEntries, _ := testkit.AllEntries(chat2.Root)
		if !reflect.DeepEqual(afterEntries, entries) {
			t.Fatalf("entries changed on reopen")
		}
		if gotUsage := thcpUsage(t, chat2); !reflect.DeepEqual(gotUsage, usage) {
			t.Fatalf("usage changed: %+v != %+v", gotUsage, usage)
		}
		insp, _ := chat2.Harness.Inspect(nil)
		if len(insp.Tasks) != 0 || len(insp.Submissions) != 0 {
			t.Fatalf("insp = %+v", insp)
		}
	})

	t.Run("fails an overflow run with its text when its compaction fails after reopen", func(t *testing.T) {
		path := thcpSqlitePath(t)
		chat := first(t, path)
		closed := false
		defer func() {
			if !closed {
				_ = chat.Harness.Close(nil)
			}
		}()
		p := thcpManualPolicy()
		p.Enabled = true
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		chat.Faux.PushAgent(thcpFailure(thcpOverflow))
		input := thgrSubmitInput(t, chat.Root, thcpText("u4", 100))
		reached.Wait(t)
		genId := thcpLive(t, chat).Run.TaskId
		genRec, _ := chat.Harness.GetTask(nil, genId)
		cp := thgrCheckpoint(t, chat.Harness, genId)
		if genRec.State.Status != durable.TaskStateWaiting || cp.Phase != "prepare" || cp.Overflow == nil || *cp.Overflow != thcpOverflow {
			t.Fatalf("gen state=%+v cp=%+v", genRec.State, cp)
		}
		_ = chat.Harness.Close(nil)
		closed = true

		chat2 := openSqlite(t, path, chat.Setup, chat.Faux, true)
		defer func() { _ = chat2.Harness.Close(nil) }()
		chat2.Faux.PushSummary(thcpFailure("bad request"))
		st, _ := thcpSubmission(t, chat2.Harness, input.Id()).Wait(nil)
		if st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "model_error" || st.Detail != thcpOverflow {
			t.Fatalf("st = %+v", st)
		}
	})

	t.Run("reruns select and its hook after a crash in select", func(t *testing.T) {
		path := thcpSqlitePath(t)
		chat := first(t, path)
		closed := false
		defer func() {
			if !closed {
				_ = chat.Harness.Close(nil)
			}
		}()
		var calls atomic.Int32
		reached := thcpNewGate()
		_, _ = testkit.AddHooks(chat.Setup.Registry, durable.CompactionTask, durable.CompactionHooks{
			BeforeCompact: func(_ durable.CompactionHookInput, _ durable.HookApi, ctx chord.Context) (*durable.BeforeCompactDecision, error) {
				if calls.Add(1) == 1 {
					reached.Resolve()
					return nil, testkit.Aborted(ctx.AbortSignal())
				}
				return nil, nil
			},
		})
		id, _ := chat.Root.Compact(nil, nil)
		reached.Wait(t)
		_ = chat.Harness.Close(nil)
		closed = true

		chat2 := openSqlite(t, path, chat.Setup, chat.Faux, true)
		defer func() { _ = chat2.Harness.Close(nil) }()
		chat2.Faux.PushSummary(thcpSummary())
		if out := thcpResult(t, chat2, id); out.Status != durable.TaskOutcomeCompleted || calls.Load() != 2 {
			t.Fatalf("out=%+v calls=%d", out, calls.Load())
		}
	})

	t.Run("resends an interrupted summarization once and counts only the answered attempt", func(t *testing.T) {
		path := thcpSqlitePath(t)
		chat := first(t, path)
		closed := false
		defer func() {
			if !closed {
				_ = chat.Harness.Close(nil)
			}
		}()
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		id, _ := chat.Root.Compact(nil, nil)
		reached.Wait(t)
		usageBefore := thcpUsage(t, chat).Models["faux/faux-1"]
		if rec, _ := chat.Harness.GetTask(nil, id); rec != nil {
			var cp durable.CompactionCheckpoint
			_ = jsonx.Decode(rec.State.Checkpoint, &cp)
			if cp.Phase != "summarize" {
				t.Fatalf("cp = %+v", cp)
			}
		}
		_ = chat.Harness.Close(nil)
		closed = true

		chat2 := openSqlite(t, path, chat.Setup, chat.Faux, true)
		defer func() { _ = chat2.Harness.Close(nil) }()
		chat2.Faux.PushSummary(thcpSummary())
		if out := thcpResult(t, chat2, id); out.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("out = %+v", out)
		}
		reqs := chat2.Faux.SummaryRequests()
		if len(reqs) != 2 || len(reqs[0].Messages) != len(reqs[1].Messages) {
			t.Fatalf("reqs = %d", len(reqs))
		}
		for i := range reqs[0].Messages {
			if thcpUserText(reqs[0].Messages[i]) != thcpUserText(reqs[1].Messages[i]) {
				t.Fatalf("resent msg[%d] mismatch", i)
			}
		}
		usageAfter := thcpUsage(t, chat2).Models["faux/faux-1"]
		if usageAfter.Input <= usageBefore.Input || usageAfter.Output-usageBefore.Output != 2 {
			t.Fatalf("usageBefore=%+v usageAfter=%+v", usageBefore, usageAfter)
		}
	})

	t.Run("resumes a retry backoff after reopen", func(t *testing.T) {
		path := thcpSqlitePath(t)
		chat := first(t, path)
		closed := false
		defer func() {
			if !closed {
				_ = chat.Harness.Close(nil)
			}
		}()
		var now atomic.Int64
		now.Store(time.Now().UnixMilli())
		chat.Setup.SetNow(now.Load)
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Retry = &durable.ConversationRetryPolicyPatch{Enabled: thcpPtr(true), MaxRetries: thcpPtr(2), BaseDelayMs: thcpPtr(int64(60000))}
		})
		chat.Faux.PushSummary(thcpFailure("overloaded"))
		id, _ := chat.Root.Compact(nil, nil)
		_ = testkit.WaitFor(func() (bool, error) {
			cs := thcpLive(t, chat).Compactions
			return len(cs) > 0 && cs[0].Retry != nil, nil
		})
		rec, _ := chat.Harness.GetTask(nil, id)
		var cp durable.CompactionCheckpoint
		_ = jsonx.Decode(rec.State.Checkpoint, &cp)
		if cp.Phase != "retry" || cp.Attempt != 1 {
			t.Fatalf("cp = %+v", cp)
		}
		_ = chat.Harness.Close(nil)
		closed = true
		now.Add(120_000)

		chat2 := openSqlite(t, path, chat.Setup, chat.Faux, true)
		defer func() { _ = chat2.Harness.Close(nil) }()
		chat2.Faux.PushSummary(thcpSummary())
		if out := thcpResult(t, chat2, id); out.Status != durable.TaskOutcomeCompleted {
			t.Fatalf("out = %+v", out)
		}
		if len(chat2.Faux.SummaryRequests()) != 2 {
			t.Fatalf("summaryRequests = %d", len(chat2.Faux.SummaryRequests()))
		}
	})

	t.Run("keeps a generation waiting on its blocking compaction across reopen", func(t *testing.T) {
		path := thcpSqlitePath(t)
		chat := first(t, path)
		closed := false
		defer func() {
			if !closed {
				_ = chat.Harness.Close(nil)
			}
		}()
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		blk := thcpBlockingPolicy()
		blk.ReserveTokens = 128_000 - 700
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(blk) })
		input := thgrSubmitInput(t, chat.Root, thcpText("u4", 200))
		reached.Wait(t)
		_ = chat.Harness.Close(nil)
		closed = true

		chat2 := openSqlite(t, path, chat.Setup, chat.Faux, true)
		defer func() { _ = chat2.Harness.Close(nil) }()
		chat2.Faux.PushSummary(thcpSummary())
		chat2.Faux.PushAgent(thcpAnswer("a4"))
		if st, _ := thcpSubmission(t, chat2.Harness, input.Id()).Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("st = %+v", st)
		}
		ks := thcpKinds(t, chat2.Root)
		if !slices.Equal(ks[len(ks)-3:], []string{"pi.compaction", "pi.system", "pi.assistant"}) {
			t.Fatalf("tail kinds = %v", ks)
		}
	})

	t.Run("keeps a queued summary across reopen and places it at the next boundary", func(t *testing.T) {
		path := thcpSqlitePath(t)
		chat := first(t, path)
		closed := false
		defer func() {
			if !closed {
				_ = chat.Harness.Close(nil)
			}
		}()
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushAgent(thcpGated(gate, thcpAnswerMsg("never"), reached))
		input := thgrSubmitInput(t, chat.Root, "busy")
		reached.Wait(t)
		chat.Faux.PushSummary(thcpSummary())
		compId, _ := chat.Root.Compact(nil, nil)
		res := thcpCompactionResult(t, thcpResult(t, chat, compId))
		_ = chat.Harness.Close(nil)
		closed = true

		chat2 := openSqlite(t, path, chat.Setup, chat.Faux, true)
		defer func() { _ = chat2.Harness.Close(nil) }()
		chat2.Faux.PushAgent(thcpAnswer("answered"))
		if st, _ := thcpSubmission(t, chat2.Harness, input.Id()).Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("input st = %+v", st)
		}
		if st, _ := thcpSubmission(t, chat2.Harness, *res.SubmissionId).Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("comp st = %+v", st)
		}
	})
}

func TestHarnessCompactionInbox(t *testing.T) {
	queuedSummary := func(t *testing.T, chat *thcpChat, content ...string) durable.Submission {
		chat.Faux.PushSummary(thcpSummary(content...))
		id, _ := chat.Root.Compact(nil, nil)
		res := thcpCompactionResult(t, thcpResult(t, chat, id))
		return thcpSubmission(t, chat.Harness, *res.SubmissionId)
	}
	busy := func(t *testing.T, chat *thcpChat, reply ...*ai.AssistantMessage) (durable.Submission, func()) {
		r := thcpAnswerMsg("done")
		if len(reply) > 0 {
			r = reply[0]
		}
		gate := thcpNewGate()
		reached := thcpNewGate()
		chat.Faux.PushAgent(thcpGated(gate, r, reached))
		in := thgrSubmitInput(t, chat.Root, "busy")
		reached.Wait(t)
		return in, gate.Resolve
	}

	t.Run("places a reset queued after the summary last, and makes a summary queued after a reset stale", func(t *testing.T) {
		for _, order := range []string{"summary first", "reset first"} {
			chat := thcpOpen(t)
			thcpHistory(t, chat)
			runIn, release := busy(t, chat)
			var sub durable.Submission
			if order == "summary first" {
				sub = queuedSummary(t, chat)
				_ = chat.Root.Reset(nil, nil)
			} else {
				_ = chat.Root.Reset(nil, nil)
				sub = queuedSummary(t, chat)
			}
			release()
			_, _ = runIn.Wait(nil)
			settled, _ := sub.Wait(nil)
			want := durable.SubmissionStatusUnanswered
			if order == "summary first" {
				want = durable.SubmissionStatusDone
			}
			ks := thcpKinds(t, chat.Root)
			cv, _ := chat.Root.Context(nil)
			_ = chat.Harness.Close(nil)
			if settled.Status != want || ks[len(ks)-1] != "pi.reset" || cv.Head == nil || cv.Head.Kind != "pi.reset" {
				t.Fatalf("order=%s settled=%+v last=%s head=%+v", order, settled, ks[len(ks)-1], cv.Head)
			}
		}
	})

	t.Run("places a summary left queued by a failed run at the next submission, before its input", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		runIn, release := busy(t, chat, thcpFailureMsg("bad request"))
		sub := queuedSummary(t, chat)
		release()
		if st, _ := runIn.Wait(nil); st.Status != durable.SubmissionStatusUnanswered {
			t.Fatalf("runIn = %+v", st)
		}
		if st, _ := sub.Status(nil); st.Status != durable.SubmissionStatusQueued {
			t.Fatalf("sub = %+v", st)
		}
		thcpTurn(t, chat, "again", "ok")
		if st, _ := sub.Status(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("sub after = %+v", st)
		}
		reqs := chat.Faux.AgentRequests()
		last := reqs[len(reqs)-1].Messages
		if !strings.Contains(thcpUserText(last[0]), "SUMMARY") {
			t.Fatalf("last[0] = %q", thcpUserText(last[0]))
		}
		sawAgain := false
		for _, m := range last {
			if thcpUserText(m) == "again" {
				sawAgain = true
			}
		}
		if !sawAgain {
			t.Fatalf("again missing")
		}
	})

	t.Run("keeps the full retry budget after an overflow compaction", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		p := thcpManualPolicy()
		p.Enabled = true
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
		chat.Faux.PushSummary(thcpSummary())
		chat.Faux.PushAgent(thcpFailure("prompt is too long"), thcpFailure("overloaded"), thcpFailure("overloaded"), thcpAnswer("finally"))
		st, _ := thgrSubmitInput(t, chat.Root, thcpText("u4", 100)).Wait(nil)
		if st.Status != durable.SubmissionStatusDone {
			t.Fatalf("st = %+v", st)
		}
	})

	t.Run("loses an application edit placed while a compaction summarizes", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		entries, _ := testkit.AllEntries(chat.Root)
		u1 := entries[0]
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		id, _ := chat.Root.Compact(nil, nil)
		reached.Wait(t)
		_, _ = chat.Root.Submit(durable.SubmissionDraft{
			Type: durable.SubmissionTypeWrite,
			Entry: &durable.EntryDraft{
				Kind: "app.redact",
				Edits: []durable.ContextEdit{{
					Target:   u1.Id,
					Action:   durable.ContextEditActionReplace,
					Messages: ai.MessageList{thcpUnitUser("REDACTED")},
				}},
			},
		}, nil)
		gate.Resolve()
		_ = thcpResult(t, chat, id)
		if !strings.Contains(thcpUserText(chat.Faux.SummaryRequests()[0].Messages[1]), "[User]: u1 ") {
			t.Fatalf("prompt = %q", thcpUserText(chat.Faux.SummaryRequests()[0].Messages[1]))
		}
		cv, _ := chat.Root.Context(nil)
		for _, m := range cv.Messages {
			if thcpUserText(m) == "REDACTED" {
				t.Fatalf("unexpected REDACTED in context")
			}
		}
	})

	t.Run("keeps the kept entries mounted in the conversation view", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		state, err := chat.Root.ViewState(nil)
		if err != nil {
			t.Fatalf("ViewState: %v", err)
		}
		defer state.Dispose()
		chat.Faux.PushSummary(thcpSummary())
		id, _ := chat.Root.Compact(nil, nil)
		_ = thcpResult(t, chat, id)
		if err := testkit.WaitFor(func() (bool, error) {
			v, _ := state.Value()
			return len(v.Entries) > 0 && v.Entries[0].Kind == "pi.compaction", nil
		}); err != nil {
			t.Fatalf("WaitFor viewState: %v", err)
		}
		v, _ := state.Value()
		cv, _ := chat.Root.Context(nil)
		if !reflect.DeepEqual(v.Entries, cv.Entries) {
			t.Fatalf("viewState entries != context entries")
		}
	})
}

func TestHarnessCompactionEdgeCases(t *testing.T) {
	t.Run("keeps the one-compaction limit through a retry backoff", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 1000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		blk := thcpBlockingPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(blk) })
		var asked atomic.Int32
		_, _ = testkit.AddHooks(chat.Setup.Registry, durable.CompactionTask, durable.CompactionHooks{
			BeforeCompact: func(_ durable.CompactionHookInput, _ durable.HookApi, _ chord.Context) (*durable.BeforeCompactDecision, error) {
				asked.Add(1)
				return &durable.BeforeCompactDecision{Decline: true}, nil
			},
		})
		chat.Faux.PushAgent(thcpFailure("overloaded"))
		thcpTurn(t, chat, thcpText("u4", 200), "a4")
		if asked.Load() != 1 || len(chat.Faux.AgentRequests()) != 5 {
			t.Fatalf("asked=%d agentReqs=%d", asked.Load(), len(chat.Faux.AgentRequests()))
		}
	})

	t.Run("does not start a background compaction when a manual one was admitted during preparation", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 2000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		bg := thcpBackgroundPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(bg) })
		rendering := thcpNewGate()
		release := thcpNewGate()
		defer release.Resolve()
		var hold atomic.Bool
		hold.Store(true)
		_, _ = testkit.AddSection(chat.Setup.Registry, "slow", func(_ durable.PromptInput, ctx chord.Context) (string, bool, error) {
			if hold.CompareAndSwap(true, false) {
				rendering.Resolve()
				select {
				case <-release.Ch():
				case <-ctx.AbortSignal().Done():
					return "", false, testkit.Aborted(ctx.AbortSignal())
				}
			}
			return "slow", true, nil
		})
		sumGate := thcpNewGate()
		defer sumGate.Resolve()
		sumReached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(sumGate, thcpAnswerMsg("SUMMARY"), sumReached))
		chat.Faux.PushAgent(thcpAnswer("a4"))
		input := thgrSubmitInput(t, chat.Root, thcpText("u4", 100))
		rendering.Wait(t)
		manual, _ := chat.Root.Compact(nil, nil)
		sumReached.Wait(t)
		release.Resolve()
		if st, _ := input.Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("input = %+v", st)
		}
		tasks := thcpCompactionTasks(t, chat)
		if len(tasks) != 1 || tasks[0].Id != manual || len(chat.Faux.SummaryRequests()) != 1 {
			t.Fatalf("tasks=%+v reqs=%d", tasks, len(chat.Faux.SummaryRequests()))
		}
		_, _ = chat.Harness.AbortTask(nil, manual)
	})

	t.Run("starts no background compaction after a blocking one in the same generation", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 2000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		p := thcpBackgroundPolicy()
		p.BackgroundTokens = 1300
		p.KeepRecentTokens = 400
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
		chat.Faux.PushSummary(thcpSummary())
		thcpTurn(t, chat, thcpText("u4", 1000), "a4")
		if len(chat.Faux.SummaryRequests()) != 1 || len(thcpCompactionTasks(t, chat)) != 0 {
			t.Fatalf("summaryReqs=%d tasks=%d", len(chat.Faux.SummaryRequests()), len(thcpCompactionTasks(t, chat)))
		}
	})

	t.Run("sends the request anyway when its blocking compaction faults", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 1000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		blk := thcpBlockingPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(blk) })
		chat.Models.SetFailCompleteSimple(errors.New("no credentials"))
		thcpTurn(t, chat, thcpText("u4", 200), "a4")
		chat.Models.SetFailCompleteSimple(nil)
		if slices.Contains(thcpKinds(t, chat.Root), "pi.compaction") || len(thcpLive(t, chat).Compactions) != 0 {
			t.Fatalf("unexpected compaction or live status")
		}
	})

	t.Run("fails an overflow run with its text when its compaction is aborted directly or itself overflows", func(t *testing.T) {
		for _, how := range []string{"abort", "overflow"} {
			chat := thcpOpen(t)
			thcpHistory(t, chat)
			p := thcpManualPolicy()
			p.Enabled = true
			chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
			gate := thcpNewGate()
			reached := thcpNewGate()
			if how == "abort" {
				chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
			} else {
				chat.Faux.PushSummary(thcpFailure("prompt is too long for the summary"))
			}
			chat.Faux.PushAgent(thcpFailure(thcpOverflow))
			input := thgrSubmitInput(t, chat.Root, thcpText("u4", 100))
			if how == "abort" {
				reached.Wait(t)
				child := thcpCompactionTasks(t, chat)[0]
				_, _ = chat.Harness.AbortTask(nil, child.Id)
			}
			st, _ := input.Wait(nil)
			gate.Resolve()
			live := thcpLive(t, chat)
			_ = chat.Harness.Close(nil)
			if st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "model_error" ||
				st.Detail != thcpOverflow || len(live.Compactions) != 0 {
				t.Fatalf("how=%s st=%+v live=%+v", how, st, live)
			}
		}
	})

	t.Run("treats a length stop as an ordinary answer, not an overflow", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		p := thcpManualPolicy()
		p.Enabled = true
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
		lenStop := ai.StopReasonLength
		errTxt := thcpOverflow
		chat.Faux.PushAgent(thcpMsgStep(providers.FauxAssistantMessage(
			providers.FauxAssistantBlocks{providers.FauxText("cut short")},
			&providers.FauxAssistantMessageOptions{StopReason: &lenStop, ErrorMessage: &errTxt},
		)))
		if st, _ := thgrSubmitInput(t, chat.Root, "go").Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("st = %+v", st)
		}
		if len(chat.Faux.SummaryRequests()) != 0 {
			t.Fatalf("summaryRequests = %d", len(chat.Faux.SummaryRequests()))
		}
	})

	t.Run("orders the events of a summary placed at once", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		var evMu sync.Mutex
		var events []durable.AgentEvent
		stream, _ := durable.WatchEvents(chat.Harness, chat.Root.Id(), nil)
		defer stream.Stop()
		_ = stream.Start(func(batch []durable.AgentEvent, _ chord.Context) error {
			evMu.Lock()
			events = append(events, batch...)
			evMu.Unlock()
			return nil
		})
		chat.Faux.PushSummary(thcpSummary())
		id, _ := chat.Root.Compact(nil, nil)
		_ = thcpResult(t, chat, id)
		_ = testkit.WaitFor(func() (bool, error) {
			evMu.Lock()
			defer evMu.Unlock()
			return slices.Contains(thcpEventTypes(events), "compaction_end"), nil
		})
		evMu.Lock()
		types := thcpEventTypes(events)
		evMu.Unlock()
		end := slices.Index(types, "compaction_end")
		if end < 2 || end+3 > len(types) || !slices.Equal(types[end-2:end+3], []string{"message_start", "message_end", "compaction_end", "submission", "usage_changed"}) {
			t.Fatalf("events around end = %v", types)
		}
	})

	t.Run("puts compaction_start last in the batch of the preparation commit", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 2000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		bg := thcpBackgroundPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(bg) })
		var evMu sync.Mutex
		var batches [][]durable.AgentEvent
		stream, _ := durable.WatchEvents(chat.Harness, chat.Root.Id(), nil)
		defer stream.Stop()
		_ = stream.Start(func(batch []durable.AgentEvent, _ chord.Context) error {
			evMu.Lock()
			batches = append(batches, slices.Clone(batch))
			evMu.Unlock()
			return nil
		})
		_, _ = testkit.AddSection(chat.Setup.Registry, "extra", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			return "extra", true, nil
		})
		gate := thcpNewGate()
		defer gate.Resolve()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY")))
		thcpTurn(t, chat, thcpText("u4", 100), "a4")
		_ = testkit.WaitFor(func() (bool, error) {
			evMu.Lock()
			defer evMu.Unlock()
			for _, b := range batches {
				if slices.Contains(thcpEventTypes(b), "compaction_start") {
					return true, nil
				}
			}
			return false, nil
		})
		evMu.Lock()
		var gotBatch []string
		for _, b := range batches {
			if ts := thcpEventTypes(b); slices.Contains(ts, "compaction_start") {
				gotBatch = ts
				break
			}
		}
		evMu.Unlock()
		if !slices.Equal(gotBatch, []string{"message_start", "message_end", "compaction_start"}) {
			t.Fatalf("batch = %v", gotBatch)
		}
		_, _ = chat.Harness.AbortTask(nil, thcpCompactionTasks(t, chat)[0].Id)
	})

	t.Run("keeps a background summary queued through a retry backoff while a blocking compaction wins", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 2000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		bg := thcpBackgroundPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) {
			s.Compaction = thcpPatchFromPolicy(bg)
			s.Retry = &durable.ConversationRetryPolicyPatch{Enabled: thcpPtr(true), MaxRetries: thcpPtr(2), BaseDelayMs: thcpPtr(int64(300))}
		})
		sumGate := thcpNewGate()
		defer sumGate.Resolve()
		sumReached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(sumGate, thcpAnswerMsg("BACKGROUND"), sumReached), thcpSummary("BLOCKING"))
		failed := thcpNewGate()
		chat.Faux.PushAgent(func(_ context.Context, _ thcpRequest) (*ai.AssistantMessage, error) {
			failed.Resolve()
			return thcpFailureMsg("overloaded"), nil
		}, thcpAnswer("a4"))
		input := thgrSubmitInput(t, chat.Root, thcpText("u4", 100))
		failed.Wait(t)
		sumReached.Wait(t)
		_ = testkit.WaitFor(func() (bool, error) {
			g := thcpLive(t, chat).Generation
			return g != nil && g.Retry != nil, nil
		})
		background := thcpCompactionTasks(t, chat)[0]
		sumGate.Resolve()
		queued := thcpCompactionResult(t, thcpResult(t, chat, background.Id))
		sub := thcpSubmission(t, chat.Harness, *queued.SubmissionId)
		if st, _ := sub.Status(nil); st.Status != durable.SubmissionStatusQueued {
			t.Fatalf("sub = %+v", st)
		}
		p2 := thcpBackgroundPolicy()
		p2.ReserveTokens = 1500
		p2.KeepRecentTokens = 50
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p2) })
		if st, _ := input.Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("input = %+v", st)
		}
		reqs := chat.Faux.AgentRequests()
		if !strings.Contains(thcpUserText(reqs[len(reqs)-1].Messages[0]), "BLOCKING") {
			t.Fatalf("last req[0] = %q", thcpUserText(reqs[len(reqs)-1].Messages[0]))
		}
		if st, _ := sub.Wait(nil); st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "stale" {
			t.Fatalf("sub final = %+v", st)
		}
	})

	t.Run("summarizes the previous summary in a second compaction", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		chat.Faux.PushSummary(thcpSummary("FIRST"))
		id1, _ := chat.Root.Compact(nil, nil)
		_ = thcpResult(t, chat, id1)
		thcpTurn(t, chat, thcpText("u4", 100), thcpText("a4", 100))
		thcpTurn(t, chat, thcpText("u5", 100), thcpText("a5", 100))
		chat.Faux.PushSummary(thcpSummary("SECOND"))
		id2, _ := chat.Root.Compact(nil, nil)
		_ = thcpResult(t, chat, id2)
		prompt := thcpUserText(chat.Faux.SummaryRequests()[1].Messages[1])
		if !strings.HasPrefix(prompt, "<conversation>\n[User]: The conversation history before this point was compacted") || !strings.Contains(prompt, "FIRST") {
			t.Fatalf("prompt = %q", prompt)
		}
		cv, _ := chat.Root.Context(nil)
		if !strings.Contains(thcpUserText(cv.Messages[0]), "SECOND") {
			t.Fatalf("cv[0] = %q", thcpUserText(cv.Messages[0]))
		}
	})

	t.Run("leaves no usage, submission, summary, or outcome when the classifying commit is rejected", func(t *testing.T) {
		cs := testkit.NewControlledStorage()
		chat := thcpOpen(t, thcpOpenOptions{Storage: cs})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		before := thcpUsage(t, chat)
		chat.Faux.PushSummary(func(_ context.Context, _ thcpRequest) (*ai.AssistantMessage, error) {
			cs.FailNextCommit(durable.NewStorageRejected("rejected", nil))
			return thcpAnswerMsg("SUMMARY"), nil
		})
		id, _ := chat.Root.Compact(nil, nil)
		out := thcpResult(t, chat, id)
		if out.Status != durable.TaskOutcomeFaulted {
			t.Fatalf("out = %+v", out)
		}
		if got := thcpUsage(t, chat); !reflect.DeepEqual(got, before) {
			t.Fatalf("usage = %+v, want %+v", got, before)
		}
		if slices.Contains(thcpKinds(t, chat.Root), "pi.compaction") {
			t.Fatalf("unexpected pi.compaction")
		}
		insp, _ := chat.Harness.Inspect(nil)
		if len(insp.Submissions) != 0 || len(thcpLive(t, chat).Compactions) != 0 {
			t.Fatalf("insp=%+v live=%+v", insp, thcpLive(t, chat))
		}
		if rec, _ := cs.SubmissionByRequest(testkit.TestContext, chat.Root.Id(), "compaction:"+string(rune(id))); rec != nil {
			_ = rec
		}
	})
}

func TestHarnessCompactionBlockingAndManual(t *testing.T) {
	blockingRun := func(t *testing.T, chat *thcpChat) (durable.Submission, *thcpGate, durable.TaskId) {
		thcpHistory(t, chat)
		blk := thcpBlockingPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(blk) })
		gate := thcpNewGate()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("BLOCKING"), reached))
		input := thgrSubmitInput(t, chat.Root, thcpText("u4", 200))
		reached.Wait(t)
		blocking := thcpCompactionTasks(t, chat)[0].Id
		return input, gate, blocking
	}

	t.Run("places a manual summary selected before the blocking one landed; its equal cut replaces it", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 1000})
		defer func() { _ = chat.Harness.Close(nil) }()
		input, gate, _ := blockingRun(t, chat)
		defer gate.Resolve()
		chat.Faux.PushSummary(thcpSummary("MANUAL"))
		manId, _ := chat.Root.Compact(nil, nil)
		manRes := thcpCompactionResult(t, thcpResult(t, chat, manId))
		sub := thcpSubmission(t, chat.Harness, *manRes.SubmissionId)
		if st, _ := sub.Status(nil); st.Status != durable.SubmissionStatusQueued {
			t.Fatalf("sub = %+v", st)
		}
		chat.Faux.PushAgent(thcpAnswer("a4"))
		gate.Resolve()
		if st, _ := input.Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("input = %+v", st)
		}
		reqs := chat.Faux.AgentRequests()
		if !strings.Contains(thcpUserText(reqs[len(reqs)-1].Messages[0]), "BLOCKING") {
			t.Fatalf("last req[0] = %q", thcpUserText(reqs[len(reqs)-1].Messages[0]))
		}
		if st, _ := sub.Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("sub wait = %+v", st)
		}
		entries, _ := testkit.AllEntries(chat.Root)
		var markers []durable.EntryRecord
		for _, e := range entries {
			if e.Kind == "pi.compaction" {
				markers = append(markers, e)
			}
		}
		if len(markers) != 2 || *markers[1].Head != *markers[0].Head {
			t.Fatalf("markers = %+v", markers)
		}
		cv, _ := chat.Root.Context(nil)
		if !strings.Contains(thcpUserText(cv.Messages[0]), "MANUAL") {
			t.Fatalf("cv[0] = %q", thcpUserText(cv.Messages[0]))
		}
		for _, m := range cv.Messages {
			if strings.Contains(thcpUserText(m), "BLOCKING") {
				t.Fatalf("unexpected BLOCKING in context")
			}
		}
	})

	t.Run("finds nothing to compact for a manual compaction selected after the blocking summary landed", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 1000})
		defer func() { _ = chat.Harness.Close(nil) }()
		input, gate, _ := blockingRun(t, chat)
		defer gate.Resolve()
		ansGate := thcpNewGate()
		defer ansGate.Resolve()
		ansReached := thcpNewGate()
		chat.Faux.PushAgent(thcpGated(ansGate, thcpAnswerMsg("a4"), ansReached))
		gate.Resolve()
		ansReached.Wait(t)
		manId, _ := chat.Root.Compact(nil, nil)
		res := thcpCompactionResult(t, thcpResult(t, chat, manId))
		if res.EntryId != nil || res.SubmissionId != nil || len(chat.Faux.SummaryRequests()) != 1 {
			t.Fatalf("res=%+v reqs=%d", res, len(chat.Faux.SummaryRequests()))
		}
		ansGate.Resolve()
		if st, _ := input.Wait(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("input = %+v", st)
		}
	})

	t.Run("aborts the run and both compactions on Esc and appends nothing", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 1000})
		defer func() { _ = chat.Harness.Close(nil) }()
		input, gate, blocking := blockingRun(t, chat)
		defer gate.Resolve()
		manGate := thcpNewGate()
		defer manGate.Resolve()
		manReached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(manGate, thcpAnswerMsg("MANUAL"), manReached))
		manual, _ := chat.Root.Compact(nil, nil)
		manReached.Wait(t)
		var blks []bool
		for _, c := range thcpLive(t, chat).Compactions {
			blks = append(blks, c.Blocking)
		}
		if !slices.Equal(blks, []bool{true, false}) {
			t.Fatalf("blks = %v", blks)
		}
		_ = chat.Root.Abort(nil)
		if st, _ := input.Wait(nil); st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "aborted" {
			t.Fatalf("input = %+v", st)
		}
		if thcpResult(t, chat, blocking).Status != durable.TaskOutcomeAborted || thcpResult(t, chat, manual).Status != durable.TaskOutcomeAborted {
			t.Fatalf("compactions not aborted")
		}
		live := thcpLive(t, chat)
		insp, _ := chat.Harness.Inspect(nil)
		if len(live.Compactions) != 0 || live.Run != nil || slices.Contains(thcpKinds(t, chat.Root), "pi.compaction") || len(insp.Submissions) != 0 {
			t.Fatalf("live=%+v insp=%+v", live, insp)
		}
	})

	t.Run("places a summary that survived Esc at the next submission, before its input", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushAgent(thcpGated(gate, thcpAnswerMsg("never"), reached))
		busyIn := thgrSubmitInput(t, chat.Root, "busy")
		reached.Wait(t)
		chat.Faux.PushSummary(thcpSummary())
		compId, _ := chat.Root.Compact(nil, nil)
		res := thcpCompactionResult(t, thcpResult(t, chat, compId))
		sub := thcpSubmission(t, chat.Harness, *res.SubmissionId)
		_ = chat.Root.Abort(nil)
		if st, _ := busyIn.Wait(nil); st.Status != durable.SubmissionStatusUnanswered || st.Reason == nil || *st.Reason != "aborted" {
			t.Fatalf("busyIn = %+v", st)
		}
		if st, _ := sub.Status(nil); st.Status != durable.SubmissionStatusQueued {
			t.Fatalf("sub = %+v", st)
		}
		thcpTurn(t, chat, "u5", "a5")
		if st, _ := sub.Status(nil); st.Status != durable.SubmissionStatusDone {
			t.Fatalf("sub after = %+v", st)
		}
		reqs := chat.Faux.AgentRequests()
		last := reqs[len(reqs)-1].Messages
		if !strings.Contains(thcpUserText(last[0]), "SUMMARY") {
			t.Fatalf("last[0] = %q", thcpUserText(last[0]))
		}
		ks := thcpKinds(t, chat.Root)
		if !slices.Equal(ks[len(ks)-4:], []string{"pi.compaction", "pi.user", "pi.system", "pi.assistant"}) {
			t.Fatalf("tail = %v", ks)
		}
	})
}

func TestHarnessCompactionPinningSilentAndLatePolicy(t *testing.T) {
	t.Run("keeps the pinned model through a retry and advances the live attempt", func(t *testing.T) {
		setup, _ := testkit.NewChatSetup(&providers.RegisterFauxProviderOptions{
			Models: []providers.FauxModelDefinition{
				{Id: "faux-1", ContextWindow: thcpPtr(100_000), MaxTokens: thcpPtr(900)},
				{Id: "faux-2", ContextWindow: thcpPtr(100_000), MaxTokens: thcpPtr(900)},
			},
		})
		chat := thcpOpen(t, thcpOpenOptions{Setup: setup})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		var attempt atomic.Int32
		chat.Faux.PushSummary(func(_ context.Context, _ thcpRequest) (*ai.AssistantMessage, error) {
			_ = chat.Root.Configure(durable.AgentChange{Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-2"})}, nil)
			return thcpFailureMsg("overloaded"), nil
		}, func(_ context.Context, _ thcpRequest) (*ai.AssistantMessage, error) {
			if cs := thcpLive(t, chat).Compactions; len(cs) > 0 {
				attempt.Store(int32(cs[0].Attempt))
			}
			return thcpAnswerMsg("SUMMARY"), nil
		})
		id, _ := chat.Root.Compact(nil, nil)
		if out := thcpResult(t, chat, id); out.Status != durable.TaskOutcomeCompleted || attempt.Load() != 2 {
			t.Fatalf("out=%+v attempt=%d", out, attempt.Load())
		}
		reqs := chat.Faux.SummaryRequests()
		if reqs[0].Model != "faux-1" || reqs[1].Model != "faux-1" {
			t.Fatalf("models = [%s, %s]", reqs[0].Model, reqs[1].Model)
		}
		if u := thcpUsage(t, chat); len(u.Models) != 1 {
			t.Fatalf("usage models = %+v", u.Models)
		}
	})

	t.Run("shows a late joiner a compaction that is summarizing", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		gate := thcpNewGate()
		defer gate.Resolve()
		reached := thcpNewGate()
		chat.Faux.PushSummary(thcpGated(gate, thcpAnswerMsg("SUMMARY"), reached))
		id, _ := chat.Root.Compact(nil, nil)
		reached.Wait(t)
		stream, _ := durable.WatchEvents(chat.Harness, chat.Root.Id(), nil)
		snap := stream.Snapshot()
		stream.Stop()
		want := []durable.CompactionStatus{{TaskId: id, Reason: durable.CompactionReasonManual, Blocking: false, Attempt: 1}}
		if !reflect.DeepEqual(snap.Compactions, want) {
			t.Fatalf("snap.Compactions = %+v", snap.Compactions)
		}
		_, _ = chat.Harness.AbortTask(nil, id)
	})

	lenStop := ai.StopReasonLength
	for _, tc := range []struct {
		name string
		resp *ai.AssistantMessage
	}{
		{"a stop whose input exceeds the window", thcpAnswerMsg("fine")},
		{"a length stop that fills the window without output", providers.FauxAssistantMessage(providers.FauxAssistantBlocks{}, &providers.FauxAssistantMessageOptions{StopReason: &lenStop})},
	} {
		t.Run("treats silent overflow as an ordinary answer: "+tc.name, func(t *testing.T) {
			chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 300})
			defer func() { _ = chat.Harness.Close(nil) }()
			thcpHistory(t, chat)
			chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) {
				s.Compaction = &durable.CompactionPolicyPatch{
					Enabled:          thcpPtr(true),
					ReserveTokens:    thcpPtr(-100_000),
					KeepRecentTokens: thcpPtr(150),
					BackgroundTokens: thcpPtr(0),
				}
			})
			chat.Faux.PushAgent(thcpMsgStep(tc.resp))
			if st, _ := thgrSubmitInput(t, chat.Root, "go").Wait(nil); st.Status != durable.SubmissionStatusDone {
				t.Fatalf("st = %+v", st)
			}
			entries, _ := testkit.AllEntries(chat.Root)
			am := entries[len(entries)-1].Model[0].(*ai.AssistantMessage)
			if am.Usage.Input < 300 || len(chat.Faux.SummaryRequests()) != 0 {
				t.Fatalf("usage.Input=%d summaryReqs=%d", am.Usage.Input, len(chat.Faux.SummaryRequests()))
			}
		})
	}

	t.Run("sends the request when a blocking compaction finds nothing under a policy changed after preparation", func(t *testing.T) {
		chat := thcpOpen(t, thcpOpenOptions{ContextWindow: 1000})
		defer func() { _ = chat.Harness.Close(nil) }()
		thcpHistory(t, chat)
		blk := thcpBlockingPolicy()
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(blk) })
		var changed atomic.Bool
		_, _ = testkit.AddSection(chat.Setup.Registry, "policy", func(_ durable.PromptInput, _ chord.Context) (string, bool, error) {
			if changed.CompareAndSwap(false, true) {
				p := thcpBlockingPolicy()
				p.KeepRecentTokens = 100_000
				chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
			}
			return "p", true, nil
		})
		thcpTurn(t, chat, thcpText("u4", 200), "a4")
		if len(thcpCompactionTasks(t, chat)) != 0 || len(chat.Faux.SummaryRequests()) != 0 || slices.Contains(thcpKinds(t, chat.Root), "pi.compaction") {
			t.Fatalf("unexpected compaction")
		}
	})
}

func TestHarnessCompactionBlocked(t *testing.T) {
	t.Run("survives reopen blocked and is orphaned on abort with its status removed", func(t *testing.T) {
		path := thcpSqlitePath(t)
		setup, _ := testkit.NewChatSetup()
		st1, err := sqlite.OpenNodeSqliteStorage(path)
		if err != nil {
			t.Fatalf("OpenNodeSqliteStorage 1: %v", err)
		}
		h1, root1, err := testkit.OpenChat(st1, setup)
		if err != nil {
			t.Fatalf("OpenChat 1: %v", err)
		}
		closed1 := false
		defer func() {
			if !closed1 {
				_ = h1.Close(nil)
			}
		}()
		newer := durable.DefineTask(durable.TaskDefinition[durable.CompactionInput, durable.CompactionCheckpoint, durable.CompactionResult, durable.CompactionHooks]{
			Name:    "pi.compaction",
			Version: 2,
			Initial: durable.CompactionTask.Definition.Initial,
			Phases:  durable.CompactionTask.Definition.Phases,
			Abort:   durable.CompactionTask.Definition.Abort,
		})
		var id durable.TaskId
		if _, err := root1.Commit(nil, func(tx durable.Tx) (any, error) {
			cid := root1.Id()
			tid, err := tx.CreateTask(newer, durable.CompactionInput{Reason: durable.CompactionReasonManual}, durable.TaskOptions{
				Ownership:      durable.TaskOwnership{Kind: durable.TaskOwnershipConversation},
				ConversationId: &cid,
			})
			if err != nil {
				return nil, err
			}
			live, err := tx.Doc(durable.LiveDoc, root1.Id())
			if err != nil {
				return nil, err
			}
			live.Value.Set("compactions", []any{
				jsonx.ObjectOf("taskId", float64(tid), "reason", "manual", "blocking", false, "attempt", float64(1)),
			})
			id = tid
			return nil, nil
		}); err != nil {
			t.Fatalf("Commit setup: %v", err)
		}
		_ = h1.Close(nil)
		closed1 = true

		st2, _ := sqlite.OpenNodeSqliteStorage(path)
		h2, root2, err := testkit.OpenChat(st2, setup)
		if err != nil {
			t.Fatalf("OpenChat 2: %v", err)
		}
		defer func() { _ = h2.Close(nil) }()
		_ = h2.Resume()
		var evMu sync.Mutex
		var events []durable.AgentEvent
		stream, _ := durable.WatchEvents(h2, root2.Id(), nil)
		defer stream.Stop()
		_ = stream.Start(func(batch []durable.AgentEvent, _ chord.Context) error {
			evMu.Lock()
			events = append(events, batch...)
			evMu.Unlock()
			return nil
		})
		insp, _ := h2.Inspect(nil)
		var found *durable.TaskInspection
		for i := range insp.Tasks {
			if insp.Tasks[i].Record.Id == id {
				found = &insp.Tasks[i]
			}
		}
		if found == nil || found.State.Kind != durable.TaskInspectionBlocked || found.State.Reason != durable.TaskBlockedTaskTooOld {
			t.Fatalf("found = %+v", found)
		}
		_, _ = h2.AbortTask(nil, id)
		rec, _ := h2.WaitForTask(nil, id)
		if rec.State.Outcome == nil || rec.State.Outcome.Status != durable.TaskOutcomeOrphaned ||
			rec.State.Outcome.Reason == nil || *rec.State.Outcome.Reason != string(durable.TaskBlockedTaskTooOld) {
			t.Fatalf("outcome = %+v", rec.State.Outcome)
		}
		if len(thgrLive(t, h2, root2.Id()).Compactions) != 0 {
			t.Fatalf("live.Compactions not cleared")
		}
		if err := testkit.WaitFor(func() (bool, error) {
			evMu.Lock()
			defer evMu.Unlock()
			return slices.Contains(thcpEventTypes(events), "compaction_end"), nil
		}); err != nil {
			t.Fatalf("WaitFor compaction_end: %v", err)
		}
	})
}

func TestHarnessCompactionContextContributions(t *testing.T) {
	t.Run("apply edits carried by an older head marker in the range", func(t *testing.T) {
		chat := thcpOpen(t)
		defer func() { _ = chat.Harness.Close(nil) }()
		var aId, bId, cId durable.EntryId
		if _, err := chat.Root.Commit(nil, func(tx durable.Tx) (any, error) {
			a, err := tx.AppendEntry(chat.Root.Id(), durable.EntryDraft{Kind: "app.note", Model: ai.MessageList{thcpUnitUser("a")}})
			if err != nil {
				return nil, err
			}
			b, err := tx.AppendEntry(chat.Root.Id(), durable.EntryDraft{Kind: "app.note", Model: ai.MessageList{thcpUnitUser("b")}})
			if err != nil {
				return nil, err
			}
			if _, err := tx.AppendEntry(chat.Root.Id(), durable.EntryDraft{
				Kind:  "app.head",
				Head:  durable.EntryHeadAt(a.Id),
				Edits: []durable.ContextEdit{{Target: b.Id, Action: durable.ContextEditActionOmit}},
			}); err != nil {
				return nil, err
			}
			c, err := tx.AppendEntry(chat.Root.Id(), durable.EntryDraft{Kind: "app.note", Model: ai.MessageList{thcpUnitUser("c")}})
			if err != nil {
				return nil, err
			}
			if _, err := tx.AppendEntry(chat.Root.Id(), durable.EntryDraft{
				Kind:  "app.head",
				Head:  durable.EntryHeadAt(a.Id),
				Model: ai.MessageList{thcpUnitUser("H")},
			}); err != nil {
				return nil, err
			}
			aId, bId, cId = a.Id, b.Id, c.Id
			return nil, nil
		}); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		view, _ := chat.Root.Context(nil)
		var entryIds []durable.EntryId
		for _, r := range view.Entries[1:] {
			entryIds = append(entryIds, r.Id)
		}
		if !slices.Equal(entryIds, []durable.EntryId{aId, bId, cId}) {
			t.Fatalf("entryIds = %v, want [%d %d %d]", entryIds, aId, bId, cId)
		}
		var contribTexts [][]string
		for _, c := range view.Contributions {
			var ts []string
			for _, m := range c {
				ts = append(ts, thcpUserText(m))
			}
			contribTexts = append(contribTexts, ts)
		}
		if !reflect.DeepEqual(contribTexts, [][]string{{"H"}, {"a"}, nil, {"c"}}) {
			t.Fatalf("contribTexts = %+v", contribTexts)
		}
		var msgTexts []string
		for _, m := range view.Messages {
			msgTexts = append(msgTexts, thcpUserText(m))
		}
		if !slices.Equal(msgTexts, []string{"H", "a", "c"}) {
			t.Fatalf("msgTexts = %v", msgTexts)
		}
		p := thcpManualPolicy()
		p.KeepRecentTokens = 1
		chat.Setup.UpdateSettings(func(s *durable.HarnessSettings) { s.Compaction = thcpPatchFromPolicy(p) })
		chat.Faux.PushSummary(thcpSummary())
		id, _ := chat.Root.Compact(nil, nil)
		_ = thcpResult(t, chat, id)
		prompt := thcpUserText(chat.Faux.SummaryRequests()[0].Messages[1])
		if !strings.Contains(prompt, "<conversation>\n[User]: H\n\n[User]: a\n</conversation>") {
			t.Fatalf("prompt = %q", prompt)
		}
	})
}

var _ env.ExecutionEnv = nil
