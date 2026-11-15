// Ported from packages/coding-agent/src/core/agent-session.ts (pi v1.5.2, part 2).

package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

// ExecuteBashOptions configures AgentSession.ExecuteBash.
type ExecuteBashOptions struct {
	ExcludeFromContext bool
	Id                 *string
	Operations         BashOperations
}

// RecordBashResultOptions configures AgentSession.RecordBashResult.
type RecordBashResultOptions struct {
	ExcludeFromContext bool
}

// NavigateTreeOptions configures AgentSession.NavigateTree.
type NavigateTreeOptions struct {
	Summarize           bool
	CustomInstructions  *string
	ReplaceInstructions bool
	Label               *string
}

// NavigateTreeResult describes the outcome of AgentSession.NavigateTree.
type NavigateTreeResult struct {
	EditorText   *string             `json:"editorText,omitzero"`
	Cancelled    bool                `json:"cancelled"`
	Aborted      bool                `json:"aborted,omitzero"`
	SummaryEntry *BranchSummaryEntry `json:"summaryEntry,omitzero"`
}

// ForkableUserMessage identifies a persisted user prompt available for branching/forking.
type ForkableUserMessage struct {
	EntryId string `json:"entryId"`
	Text    string `json:"text"`
}

// ============================================================================
// Model & Thinking Management
// ============================================================================

func as2GetNextCycleIndex(currentIndex, length int, direction string) (int, bool) {
	if length == 0 {
		return 0, false
	}
	if currentIndex == -1 {
		if direction == "backward" {
			return length - 1, true
		}
		return 0, true
	}
	if length == 1 {
		return 0, false
	}
	if direction == "backward" {
		return (currentIndex - 1 + length) % length, true
	}
	return (currentIndex + 1) % length, true
}

func (s *AgentSession) as2EmitModelSelect(ctx context.Context, nextModel, previousModel *ai.Model, source string) {
	if ai.ModelsAreEqual(previousModel, nextModel) {
		return
	}
	if runner := s.currentExtensionRunner(); runner != nil {
		_ = runner.Emit(ctx, ModelSelectEvent{
			Type:          "model_select",
			Model:         nextModel,
			PreviousModel: previousModel,
			Source:        ModelSelectSource(source),
		})
	}
}

// SetModel validates authentication for model, switches the active agent model,
// records the change in session history, optionally persists to settings,
// applies model-appropriate thinking level, and emits model_select.
func (s *AgentSession) SetModel(ctx context.Context, model *ai.Model, options ...*ModelMutationOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if model == nil {
		return errors.New("No model provided")
	}
	var opts ModelMutationOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	if s.modelRuntime != nil {
		chk, err := s.modelRuntime.CheckAuth(ctx, string(model.Provider), nil)
		if err != nil {
			return err
		}
		if chk == nil {
			return fmt.Errorf("No API key for %s/%s", model.Provider, model.Id)
		}
	}

	previousModel := s.Model()
	thinkingLevel := s.as2GetThinkingLevelForModelSwitch(model, nil)
	if s.Agent != nil {
		s.Agent.SetModel(model)
	}
	if s.SessionManager != nil {
		s.SessionManager.AppendModelChange(string(model.Provider), model.Id)
	}
	if opts.Persist {
		if s.SettingsManager != nil {
			s.SettingsManager.SetDefaultModelAndProvider(string(model.Provider), model.Id)
		}
		s.as2AddPersistedDefaultToNonEmptyScope(model)
	}

	s.SetThinkingLevel(thinkingLevel)
	s.as2EmitModelSelect(ctx, model, previousModel, "set")
	return nil
}

func (s *AgentSession) as2AddPersistedDefaultToNonEmptyScope(model *ai.Model) {
	if model == nil {
		return
	}
	s.mu.Lock()
	if len(s.scopedModels) == 0 {
		s.mu.Unlock()
		return
	}
	for _, scoped := range s.scopedModels {
		if ai.ModelsAreEqual(scoped.Model, model) {
			s.mu.Unlock()
			return
		}
	}
	s.scopedModels = append(s.scopedModels, ScopedModel{Model: model})
	s.mu.Unlock()

	if s.SettingsManager == nil {
		return
	}
	enabledModels := s.SettingsManager.GetEnabledModels()
	if len(enabledModels) == 0 {
		return
	}
	ref := string(model.Provider) + "/" + model.Id
	for _, pat := range enabledModels {
		if strings.EqualFold(pat, ref) {
			return
		}
	}
	updated := make([]string, 0, len(enabledModels)+1)
	updated = append(updated, enabledModels...)
	updated = append(updated, ref)
	s.SettingsManager.SetEnabledModels(updated)
}

// CycleModel advances to the next or previous model across scoped models (if configured)
// or all available models in ModelRuntime.
func (s *AgentSession) CycleModel(ctx context.Context, direction string, options ...*ModelMutationOptions) (*ModelCycleResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if direction != "backward" {
		direction = "forward"
	}
	var opts ModelMutationOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	s.mu.Lock()
	hasScoped := len(s.scopedModels) > 0
	s.mu.Unlock()
	if hasScoped {
		return s.as2CycleScopedModel(ctx, direction, opts)
	}
	return s.as2CycleAvailableModel(ctx, direction, opts)
}

func (s *AgentSession) as2CycleScopedModel(ctx context.Context, direction string, opts ModelMutationOptions) (*ModelCycleResult, error) {
	if s.modelRuntime == nil {
		return nil, nil
	}
	available := s.modelRuntime.GetAvailableSnapshot()
	availableSet := make(map[string]struct{}, len(available))
	for _, m := range available {
		if m != nil {
			availableSet[string(m.Provider)+"\x00"+m.Id] = struct{}{}
		}
	}

	s.mu.Lock()
	allScoped := slices.Clone(s.scopedModels)
	s.mu.Unlock()

	var scoped []*ScopedModel
	for i := range allScoped {
		sm := allScoped[i]
		if sm.Model != nil {
			if _, ok := availableSet[string(sm.Model.Provider)+"\x00"+sm.Model.Id]; ok {
				cp := sm
				scoped = append(scoped, &cp)
			}
		}
	}

	currentModel := s.Model()
	currentIndex := -1
	for i, sm := range scoped {
		if ai.ModelsAreEqual(sm.Model, currentModel) {
			currentIndex = i
			break
		}
	}
	nextIndex, ok := as2GetNextCycleIndex(currentIndex, len(scoped), direction)
	if !ok {
		return nil, nil
	}
	next := scoped[nextIndex]
	thinkingLevel := s.as2GetThinkingLevelForModelSwitch(next.Model, next.ThinkingLevel)

	if s.Agent != nil {
		s.Agent.SetModel(next.Model)
	}
	if s.SessionManager != nil {
		s.SessionManager.AppendModelChange(string(next.Model.Provider), next.Model.Id)
	}
	if opts.Persist {
		if s.SettingsManager != nil {
			s.SettingsManager.SetDefaultModelAndProvider(string(next.Model.Provider), next.Model.Id)
		}
		s.as2AddPersistedDefaultToNonEmptyScope(next.Model)
	}

	s.SetThinkingLevel(thinkingLevel)
	s.as2EmitModelSelect(ctx, next.Model, currentModel, "cycle")

	return &ModelCycleResult{
		Model:         next.Model,
		ThinkingLevel: s.ThinkingLevel(),
		IsScoped:      true,
	}, nil
}

func (s *AgentSession) as2CycleAvailableModel(ctx context.Context, direction string, opts ModelMutationOptions) (*ModelCycleResult, error) {
	if s.modelRuntime == nil {
		return nil, nil
	}
	available := s.modelRuntime.GetAvailableSnapshot()
	currentModel := s.Model()
	currentIndex := -1
	for i, m := range available {
		if ai.ModelsAreEqual(m, currentModel) {
			currentIndex = i
			break
		}
	}
	nextIndex, ok := as2GetNextCycleIndex(currentIndex, len(available), direction)
	if !ok {
		return nil, nil
	}
	nextModel := available[nextIndex]
	thinkingLevel := s.as2GetThinkingLevelForModelSwitch(nextModel, nil)

	if s.Agent != nil {
		s.Agent.SetModel(nextModel)
	}
	if s.SessionManager != nil {
		s.SessionManager.AppendModelChange(string(nextModel.Provider), nextModel.Id)
	}
	if opts.Persist {
		if s.SettingsManager != nil {
			s.SettingsManager.SetDefaultModelAndProvider(string(nextModel.Provider), nextModel.Id)
		}
		s.as2AddPersistedDefaultToNonEmptyScope(nextModel)
	}

	s.SetThinkingLevel(thinkingLevel)
	s.as2EmitModelSelect(ctx, nextModel, currentModel, "cycle")

	return &ModelCycleResult{
		Model:         nextModel,
		ThinkingLevel: s.ThinkingLevel(),
		IsScoped:      false,
	}, nil
}

// ============================================================================
// Thinking Level & Queue Modes
// ============================================================================

// SetThinkingLevel clamps level to the active model's supported thinking levels,
// updates agent state, appends a session entry if changed, and emits events.
func (s *AgentSession) SetThinkingLevel(level agent.ThinkingLevel, options ...*ModelMutationOptions) {
	var opts ModelMutationOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	available := s.GetAvailableThinkingLevels()
	effective := level
	if !slices.Contains(available, level) {
		effective = s.as2ClampThinkingLevel(level, available)
	}

	previousLevel := s.ThinkingLevel()
	isChanging := effective != previousLevel
	if s.Agent != nil {
		s.Agent.SetThinkingLevel(effective)
	}
	if opts.Persist && s.SettingsManager != nil {
		s.SettingsManager.SetDefaultThinkingLevel(level)
	}
	if isChanging {
		if s.SessionManager != nil {
			s.SessionManager.AppendThinkingLevelChange(string(effective))
		}
		s.as1Emit(&AgentSessionEventThinkingLevelChanged{
			Type:  "thinking_level_changed",
			Level: effective,
		})
		if runner := s.currentExtensionRunner(); runner != nil {
			_ = runner.Emit(context.Background(), ThinkingLevelSelectEvent{
				Type:          "thinking_level_select",
				Level:         effective,
				PreviousLevel: previousLevel,
			})
		}
	}
}

// CycleThinkingLevel advances to the next thinking level supported by the current model.
func (s *AgentSession) CycleThinkingLevel(options ...*ModelMutationOptions) *agent.ThinkingLevel {
	if !s.SupportsThinking() {
		return nil
	}
	levels := s.GetAvailableThinkingLevels()
	if len(levels) == 0 {
		return nil
	}
	idx := slices.Index(levels, s.ThinkingLevel())
	nextLevel := levels[(idx+1)%len(levels)]
	s.SetThinkingLevel(nextLevel, options...)
	return &nextLevel
}

// GetAvailableThinkingLevels returns the thinking levels supported by the active model.
func (s *AgentSession) GetAvailableThinkingLevels() []agent.ThinkingLevel {
	model := s.Model()
	if model == nil {
		return slices.Clone(ThinkingLevelOptions)
	}
	return ai.GetSupportedThinkingLevels(model)
}

// SupportsThinking reports whether the active model supports reasoning/thinking levels.
func (s *AgentSession) SupportsThinking() bool {
	m := s.Model()
	return m != nil && m.Reasoning
}

// SupportsXhighThinking reports whether the active model exposes xhigh thinking.
func (s *AgentSession) SupportsXhighThinking() bool {
	return slices.Contains(s.GetAvailableThinkingLevels(), agent.ThinkingLevelXhigh)
}

func (s *AgentSession) as2GetThinkingLevelForModelSwitch(targetModel *ai.Model, explicit *agent.ThinkingLevel) agent.ThinkingLevel {
	if explicit != nil && *explicit != "" {
		return *explicit
	}
	if targetModel != nil && s.SettingsManager != nil {
		if perModel := s.SettingsManager.GetModelThinkingLevel(string(targetModel.Provider), targetModel.Id); perModel != nil {
			return *perModel
		}
	}
	if s.SettingsManager != nil {
		if def := s.SettingsManager.GetDefaultThinkingLevel(); def != nil {
			return *def
		}
	}
	if cur := s.ThinkingLevel(); cur != "" {
		return cur
	}
	return agent.ThinkingLevel(DefaultThinkingLevel)
}

func (s *AgentSession) as2ClampThinkingLevel(level agent.ThinkingLevel, _ []agent.ThinkingLevel) agent.ThinkingLevel {
	model := s.Model()
	if model == nil {
		return agent.ThinkingLevelOff
	}
	return ai.ClampThinkingLevel(model, level)
}

func (s *AgentSession) as2SyncQueueModesFromSettings() {
	if s.Agent == nil || s.SettingsManager == nil {
		return
	}
	s.Agent.SetSteeringMode(agent.QueueMode(s.SettingsManager.GetSteeringMode()))
	s.Agent.SetFollowUpMode(agent.QueueMode(s.SettingsManager.GetFollowUpMode()))
}

// SetSteeringMode updates and persists the steering queue delivery mode.
func (s *AgentSession) SetSteeringMode(mode agent.QueueMode) {
	if s.Agent != nil {
		s.Agent.SetSteeringMode(mode)
	}
	if s.SettingsManager != nil {
		s.SettingsManager.SetSteeringMode(mode)
	}
}

// SetFollowUpMode updates and persists the follow-up queue delivery mode.
func (s *AgentSession) SetFollowUpMode(mode agent.QueueMode) {
	if s.Agent != nil {
		s.Agent.SetFollowUpMode(mode)
	}
	if s.SettingsManager != nil {
		s.SettingsManager.SetFollowUpMode(mode)
	}
}

// ============================================================================
// Compaction
// ============================================================================

func as2RetryPolicyFromSettings(rs ResolvedRetrySettings) ai.RetryPolicy {
	maxDelay := int64(rs.MaxAgentDelayMs)
	p := ai.RetryPolicy{
		Enabled:     rs.Enabled,
		MaxRetries:  rs.MaxRetries,
		BaseDelayMs: int64(rs.BaseDelayMs),
	}
	if maxDelay > 0 {
		p.MaxAgentDelayMs = &maxDelay
	}
	return p
}

func as2EstimateMessagesTokens(messages []agent.AgentMessage) int {
	total := 0
	for _, msg := range messages {
		total += EstimateTokens(msg)
	}
	return total
}

func (s *AgentSession) as2EmitSessionCompactFailed(ctx context.Context, event SessionCompactFailedEvent) {
	runner := s.currentExtensionRunner()
	if runner == nil || !runner.HasHandlers("session_compact_failed") {
		return
	}
	event.Type = "session_compact_failed"
	_ = runner.Emit(ctx, event)
}

func (s *AgentSession) as2RunDefaultCompaction(
	ctx context.Context,
	preparation CompactionPreparation,
	model *ai.Model,
	customInstructions *string,
	reason string,
) (*CompactionResult, error) {
	auth, err := s.as1GetSummarizationRequestAuth(ctx, model)
	if err != nil {
		return nil, err
	}
	var streamFn agent.StreamFn
	if s.Agent != nil {
		streamFn = s.Agent.StreamFn()
	}
	var retryPolicy ai.RetryPolicy
	if s.SettingsManager != nil {
		rs := s.SettingsManager.GetRetrySettings()
		retryPolicy = as2RetryPolicyFromSettings(rs)
	}
	return Compact(ctx, preparation, auth.model, auth.apiKey, &CompactRunOptions{
		Headers:            auth.headers,
		CustomInstructions: customInstructions,
		ThinkingLevel:      &auth.thinkingLevel,
		StreamFn:           streamFn,
		Env:                auth.env,
		Retry:              &retryPolicy,
		Callbacks:          s.as2SummarizationRetryCallbacks("compaction", &reason),
	})
}

func (s *AgentSession) as2ClearManualCompactionState(token uint64) {
	s.mu.Lock()
	var cancel context.CancelFunc
	if s.compactionToken == token {
		cancel = s.compactionCancel
		s.compactionCancel = nil
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.as1ResolveIdleWaitIfIdle()
}

// Compact aborts any active run and performs manual session compaction.
func (s *AgentSession) Compact(ctx context.Context, customInstructions ...string) (*CompactionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Abort(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return nil, err
	}

	var customInstr *string
	if len(customInstructions) > 0 && customInstructions[0] != "" {
		v := customInstructions[0]
		customInstr = &v
	}

	compCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.compactionToken++
	token := s.compactionToken
	s.compactionCancel = cancel
	s.mu.Unlock()

	s.as1Emit(&AgentSessionEventCompactionStart{Type: "compaction_start", Reason: "manual"})

	fromExtension := false
	cancelledByExtension := false
	defer s.as2ClearManualCompactionState(token)

	failCompaction := func(err error) (*CompactionResult, error) {
		aborted := compCtx.Err() != nil || cancelledByExtension
		var errMsg *string
		if !aborted {
			msg := "Compaction failed: " + err.Error()
			errMsg = &msg
		}
		s.as2ClearManualCompactionState(token)
		s.as1Emit(&AgentSessionEventCompactionEnd{
			Type:         "compaction_end",
			Reason:       "manual",
			Aborted:      aborted,
			WillRetry:    false,
			ErrorMessage: errMsg,
		})
		s.as2EmitSessionCompactFailed(ctx, SessionCompactFailedEvent{
			Reason:        "manual",
			ErrorMessage:  errMsg,
			Aborted:       aborted,
			WillRetry:     false,
			FromExtension: fromExtension,
		})
		return nil, err
	}

	model := s.Model()
	if model == nil {
		return failCompaction(errors.New(FormatNoModelSelectedMessage()))
	}
	settings, settingsErr := s.SettingsManager.GetCompactionSettings(model)
	if settingsErr != nil {
		return failCompaction(settingsErr)
	}
	pathEntries := s.SessionManager.GetBranch()
	preparation := PrepareCompaction(pathEntries, settings)
	if preparation == nil {
		if len(pathEntries) > 0 {
			if _, ok := pathEntries[len(pathEntries)-1].(*CompactionEntry); ok {
				return failCompaction(errors.New("Already compacted"))
			}
		}
		return failCompaction(errors.New("Nothing to compact (session too small)"))
	}

	var extensionCompaction *CompactionResult
	if runner := s.currentExtensionRunner(); runner != nil && runner.HasHandlers("session_before_compact") {
		rawRes := runner.Emit(compCtx, SessionBeforeCompactEvent{
			Type:               "session_before_compact",
			Preparation:        *preparation,
			BranchEntries:      pathEntries,
			CustomInstructions: customInstr,
			Reason:             "manual",
			WillRetry:          false,
		})
		if extRes, ok := rawRes.(*SessionBeforeCompactResult); ok && extRes != nil {
			if extRes.Cancel {
				cancelledByExtension = true
				return failCompaction(errors.New("Compaction cancelled"))
			}
			if extRes.Compaction != nil {
				extensionCompaction = extRes.Compaction
				fromExtension = true
			}
		}
	}

	var compactResult *CompactionResult
	if extensionCompaction != nil {
		compactResult = extensionCompaction
	} else {
		res, err := s.as2RunDefaultCompaction(compCtx, *preparation, model, customInstr, "manual")
		if err != nil {
			return failCompaction(err)
		}
		compactResult = res
	}

	if err := compCtx.Err(); err != nil {
		return failCompaction(errors.New("Compaction cancelled"))
	}

	if _, err := s.SessionManager.AppendCompaction(
		compactResult.Summary,
		&compactResult.FirstKeptEntryId,
		compactResult.TokensBefore,
		&AppendCompactionOptions{
			Details:  compactResult.Details,
			FromHook: &fromExtension,
			Usage:    compactResult.Usage,
		},
	); err != nil {
		return failCompaction(err)
	}
	newEntries := s.SessionManager.GetEntries()
	s.as1RefreshFinalizedContext()
	estimatedAfter := as2EstimateMessagesTokens(s.SessionManager.BuildSessionProjection().Messages)

	var savedEntry *CompactionEntry
	for _, entry := range newEntries {
		if ce, ok := entry.(*CompactionEntry); ok && ce.Summary == compactResult.Summary {
			savedEntry = ce
		}
	}
	if runner := s.currentExtensionRunner(); runner != nil && savedEntry != nil {
		_ = runner.Emit(ctx, SessionCompactEvent{
			Type:            "session_compact",
			CompactionEntry: savedEntry,
			FromExtension:   fromExtension,
			Reason:          "manual",
			WillRetry:       false,
		})
	}

	result := &CompactionResult{
		Summary:              compactResult.Summary,
		FirstKeptEntryId:     compactResult.FirstKeptEntryId,
		TokensBefore:         compactResult.TokensBefore,
		EstimatedTokensAfter: &estimatedAfter,
		Usage:                compactResult.Usage,
		Details:              compactResult.Details,
	}

	s.as2ClearManualCompactionState(token)
	s.as1Emit(&AgentSessionEventCompactionEnd{
		Type:      "compaction_end",
		Reason:    "manual",
		Result:    result,
		Aborted:   false,
		WillRetry: false,
	})
	return result, nil
}

// AbortCompaction cancels any in-progress manual or automatic compaction.
func (s *AgentSession) AbortCompaction() {
	s.mu.Lock()
	c1 := s.compactionCancel
	c2 := s.autoCompactionCancel
	s.mu.Unlock()
	if c1 != nil {
		c1()
	}
	if c2 != nil {
		c2()
	}
}

// AbortBranchSummary cancels any in-progress branch summarization during tree navigation.
func (s *AgentSession) AbortBranchSummary() {
	s.mu.Lock()
	cancel := s.branchSummaryCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *AgentSession) as2CheckCompaction(ctx context.Context, assistantMsg *ai.AssistantMessage, skipAbortedCheck bool) (bool, error) {
	if assistantMsg == nil || s.SettingsManager == nil || s.SessionManager == nil {
		return false, nil
	}
	settings, err := s.SettingsManager.GetCompactionSettings(s.Model())
	if err != nil {
		return false, err
	}
	if !settings.Enabled {
		return false, nil
	}
	if skipAbortedCheck && assistantMsg.StopReason == ai.StopReasonAborted {
		return false, nil
	}

	messageModel := s.as1ModelForMessage(assistantMsg)
	sameModel := messageModel != nil
	var contextWindow int
	if messageModel != nil {
		contextWindow = messageModel.ContextWindow
	} else if active := s.Model(); active != nil {
		contextWindow = active.ContextWindow
	}

	branch := s.SessionManager.GetBranch()
	compactionEntry := GetLatestCompactionEntry(branch)
	if compactionEntry != nil {
		if ts, err := time.Parse(time.RFC3339Nano, compactionEntry.Timestamp); err == nil {
			if assistantMsg.Timestamp <= ts.UnixMilli() {
				return false, nil
			}
		}
	}

	currentProjection := s.SessionManager.BuildSessionProjection()
	assistantEntryId, hasEntryId := s.as1FindPersistedMessageEntryId(assistantMsg)
	assistantIsProjected := !hasEntryId
	if hasEntryId {
		for _, entry := range currentProjection.Entries {
			if entry.SourceEntry.GetId() == assistantEntryId {
				for _, m := range entry.Messages {
					if m != nil && m.GetRole() == "assistant" {
						assistantIsProjected = true
						break
					}
				}
			}
			if assistantIsProjected {
				break
			}
		}
	}

	assistantIndex := -1
	if hasEntryId {
		for i, entry := range branch {
			if entry.GetId() == assistantEntryId {
				assistantIndex = i
				break
			}
		}
	}
	var entriesAfterAssistant []SessionEntry
	if assistantIndex >= 0 {
		entriesAfterAssistant = branch[assistantIndex+1:]
	}
	hasPostAssistantContextEdit := false
	hasPostCompaction := false
	var latestAssistantEdit *ContextEditEntry
	for _, entry := range entriesAfterAssistant {
		switch e := entry.(type) {
		case *ContextEditEntry:
			hasPostAssistantContextEdit = true
			if e.TargetId == assistantEntryId {
				latestAssistantEdit = e
			}
		case *CompactionEntry:
			hasPostCompaction = true
		}
	}
	assistantRetainedForExplicitRecovery := !hasEntryId ||
		(!hasPostCompaction && (latestAssistantEdit == nil || latestAssistantEdit.Replacement != nil))
	assistantUsageMatchesProjection := assistantIsProjected && !hasPostAssistantContextEdit

	explicitOverflow := assistantMsg.StopReason == ai.StopReasonError && ai.IsContextOverflow(assistantMsg)
	contextOverflow := sameModel &&
		((explicitOverflow && assistantRetainedForExplicitRecovery) ||
			(assistantUsageMatchesProjection && ai.IsContextOverflow(assistantMsg, contextWindow)))
	recoverableLength := sameModel && assistantIsProjected && ai.IsRecoverableLength(assistantMsg, messageModel.MaxTokens)

	if contextOverflow || recoverableLength {
		willRetry := assistantMsg.StopReason != ai.StopReasonStop
		if !willRetry {
			return s.as2RunAutoCompaction(ctx, "overflow", false)
		}

		s.mu.Lock()
		alreadyAttempted := s.overflowRecoveryAttempt
		if !alreadyAttempted {
			s.overflowRecoveryAttempt = true
		}
		s.mu.Unlock()

		if alreadyAttempted {
			errText := "Truncated response recovery failed after one compact-and-retry attempt."
			if contextOverflow {
				errText = "Context overflow recovery failed after one compact-and-retry attempt. Try reducing context or switching to a larger-context model."
			}
			s.as1Emit(&AgentSessionEventCompactionEnd{
				Type:         "compaction_end",
				Reason:       "overflow",
				Aborted:      false,
				WillRetry:    false,
				ErrorMessage: &errText,
			})
			s.as2EmitSessionCompactFailed(ctx, SessionCompactFailedEvent{
				Reason:        "overflow",
				ErrorMessage:  &errText,
				Aborted:       false,
				WillRetry:     false,
				FromExtension: false,
			})
			return false, nil
		}

		if err := s.as1OmitRecoveryAttempt(assistantMsg); err != nil {
			return false, err
		}
		retry, err := s.as2RunAutoCompaction(ctx, "overflow", willRetry)
		if err != nil {
			return false, err
		}
		if retry {
			s.mu.Lock()
			s.failedResponse = assistantMsg
			s.mu.Unlock()
		}
		return retry, nil
	}

	var contextTokens int
	hasContextEdits := false
	for _, entry := range currentProjection.Entries {
		if entry.SourceEntry.GetType() == "context_edit" {
			hasContextEdits = true
			break
		}
	}
	directContextTokens := ai.CalculateContextTokens(&assistantMsg.Usage)
	if hasContextEdits {
		contextTokens = EstimateProjectedContextTokens(currentProjection, branch).Tokens
	} else if assistantMsg.StopReason == ai.StopReasonError || directContextTokens == 0 {
		messages := s.Messages()
		est := EstimateContextTokens(messages)
		if est.LastUsageIndex != nil {
			usageIdx := *est.LastUsageIndex
			if compactionEntry != nil && usageIdx >= 0 && usageIdx < len(messages) {
				if um, ok := messages[usageIdx].(*ai.AssistantMessage); ok && um != nil {
					if ts, err := time.Parse(time.RFC3339Nano, compactionEntry.Timestamp); err == nil {
						if um.Timestamp <= ts.UnixMilli() {
							return false, nil
						}
					}
				}
			}
		}
		contextTokens = est.Tokens
	} else {
		contextTokens = directContextTokens
	}

	if ShouldCompact(contextTokens, contextWindow, settings) {
		return s.as2RunAutoCompaction(ctx, "threshold", false)
	}
	return false, nil
}

func (s *AgentSession) as2RunAutoCompaction(ctx context.Context, reason string, willRetry bool) (bool, error) {
	model := s.Model()
	if model == nil || s.SettingsManager == nil || s.SessionManager == nil {
		return false, nil
	}
	settings, err := s.SettingsManager.GetCompactionSettings(model)
	if err != nil {
		return false, err
	}
	pathEntries := s.SessionManager.GetBranch()
	preparation := PrepareCompaction(pathEntries, settings)
	if preparation == nil {
		return false, nil
	}

	autoCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.autoCompactionToken++
	token := s.autoCompactionToken
	s.autoCompactionCancel = cancel
	s.mu.Unlock()

	started := true
	fromExtension := false
	cancelledByExtension := false

	defer func() {
		cancel()
		s.mu.Lock()
		if s.autoCompactionToken == token {
			s.autoCompactionCancel = nil
		}
		s.mu.Unlock()
		s.as1ResolveIdleWaitIfIdle()
	}()

	s.as1Emit(&AgentSessionEventCompactionStart{Type: "compaction_start", Reason: reason})

	failAuto := func(err error) (bool, error) {
		aborted := autoCtx.Err() != nil || cancelledByExtension
		if started {
			var errMsg *string
			if !aborted {
				prefix := "Auto-compaction failed: "
				if reason == "overflow" {
					prefix = "Context overflow recovery failed: "
				}
				msg := prefix + err.Error()
				errMsg = &msg
			}
			s.as1Emit(&AgentSessionEventCompactionEnd{
				Type:         "compaction_end",
				Reason:       reason,
				Aborted:      aborted,
				WillRetry:    false,
				ErrorMessage: errMsg,
			})
			s.as2EmitSessionCompactFailed(ctx, SessionCompactFailedEvent{
				Reason:        reason,
				ErrorMessage:  errMsg,
				Aborted:       aborted,
				WillRetry:     false,
				FromExtension: fromExtension,
			})
		}
		return false, nil
	}

	if err := autoCtx.Err(); err != nil {
		return failAuto(err)
	}

	var extensionCompaction *CompactionResult
	if runner := s.currentExtensionRunner(); runner != nil && runner.HasHandlers("session_before_compact") {
		rawRes := runner.Emit(autoCtx, SessionBeforeCompactEvent{
			Type:               "session_before_compact",
			Preparation:        *preparation,
			BranchEntries:      pathEntries,
			CustomInstructions: nil,
			Reason:             reason,
			WillRetry:          willRetry,
		})
		if extRes, ok := rawRes.(*SessionBeforeCompactResult); ok && extRes != nil {
			if extRes.Cancel {
				cancelledByExtension = true
				return failAuto(errors.New("Compaction cancelled"))
			}
			if extRes.Compaction != nil {
				extensionCompaction = extRes.Compaction
				fromExtension = true
			}
		}
	}

	if err := autoCtx.Err(); err != nil {
		return failAuto(err)
	}

	var compactResult *CompactionResult
	if extensionCompaction != nil {
		compactResult = extensionCompaction
	} else {
		res, err := s.as2RunDefaultCompaction(autoCtx, *preparation, model, nil, reason)
		if err != nil {
			return failAuto(err)
		}
		compactResult = res
	}

	if err := autoCtx.Err(); err != nil {
		return failAuto(err)
	}

	if _, err := s.SessionManager.AppendCompaction(
		compactResult.Summary,
		&compactResult.FirstKeptEntryId,
		compactResult.TokensBefore,
		&AppendCompactionOptions{
			Details:  compactResult.Details,
			FromHook: &fromExtension,
			Usage:    compactResult.Usage,
		},
	); err != nil {
		return failAuto(err)
	}
	newEntries := s.SessionManager.GetEntries()
	s.as1RefreshFinalizedContext()
	estimatedAfter := as2EstimateMessagesTokens(s.SessionManager.BuildSessionProjection().Messages)

	var savedEntry *CompactionEntry
	for _, entry := range newEntries {
		if ce, ok := entry.(*CompactionEntry); ok && ce.Summary == compactResult.Summary {
			savedEntry = ce
		}
	}
	if runner := s.currentExtensionRunner(); runner != nil && savedEntry != nil {
		_ = runner.Emit(ctx, SessionCompactEvent{
			Type:            "session_compact",
			CompactionEntry: savedEntry,
			FromExtension:   fromExtension,
			Reason:          reason,
			WillRetry:       willRetry,
		})
	}

	result := &CompactionResult{
		Summary:              compactResult.Summary,
		FirstKeptEntryId:     compactResult.FirstKeptEntryId,
		TokensBefore:         compactResult.TokensBefore,
		EstimatedTokensAfter: &estimatedAfter,
		Usage:                compactResult.Usage,
		Details:              compactResult.Details,
	}
	s.as1Emit(&AgentSessionEventCompactionEnd{
		Type:      "compaction_end",
		Reason:    reason,
		Result:    result,
		Aborted:   false,
		WillRetry: willRetry,
	})
	if willRetry {
		return true, nil
	}
	return s.Agent != nil && s.Agent.HasQueuedMessages(), nil
}

// AutoCompactionEnabled reports whether automatic compaction is enabled in settings.
func (s *AgentSession) AutoCompactionEnabled() bool {
	return s.SettingsManager != nil && s.SettingsManager.GetCompactionEnabled()
}

// SetAutoCompactionEnabled updates the auto-compaction setting.
func (s *AgentSession) SetAutoCompactionEnabled(enabled bool) {
	if s.SettingsManager != nil {
		s.SettingsManager.SetCompactionEnabled(enabled)
	}
}

// ============================================================================
// Auto-Retry
// ============================================================================

func (s *AgentSession) as2IsRetryableError(message *ai.AssistantMessage) bool {
	if message == nil {
		return false
	}
	var contextWindow int
	if m := s.as1ModelForMessage(message); m != nil {
		contextWindow = m.ContextWindow
	} else if active := s.Model(); active != nil {
		contextWindow = active.ContextWindow
	}
	if ai.IsContextOverflow(message, contextWindow) {
		return false
	}
	return ai.IsRetryableAssistantError(message)
}

func (s *AgentSession) as2SummarizationRetryCallbacks(source string, reason *string) *ai.RetryCallbacks {
	return &ai.RetryCallbacks{
		OnRetryScheduled: func(attempt, maxAttempts int, delayMs int64, errorMessage string) {
			s.as1Emit(&AgentSessionEventSummarizationRetryScheduled{
				Type:         "summarization_retry_scheduled",
				Attempt:      attempt,
				MaxAttempts:  maxAttempts,
				DelayMs:      delayMs,
				ErrorMessage: errorMessage,
			})
		},
		OnRetryAttemptStart: func() {
			s.as1Emit(&AgentSessionEventSummarizationRetryAttemptStart{
				Type:   "summarization_retry_attempt_start",
				Source: source,
				Reason: reason,
			})
		},
		OnRetryFinished: func(bool, int, *string) {
			s.as1Emit(&AgentSessionEventSummarizationRetryFinished{
				Type: "summarization_retry_finished",
			})
		},
	}
}

func (s *AgentSession) as2FinishCancelledRetryLocked() int {
	if s.retryAttempt == 0 {
		return 0
	}
	attempt := s.retryAttempt
	s.retryAttempt = 0
	return attempt
}

func (s *AgentSession) as2EmitCancelledRetryIfAny(attempt int) {
	if attempt == 0 {
		return
	}
	msg := "Retry cancelled"
	s.as1Emit(&AgentSessionEventAutoRetryEnd{
		Type:       "auto_retry_end",
		Success:    false,
		Attempt:    attempt,
		FinalError: &msg,
	})
}

func (s *AgentSession) as2PrepareRetry(ctx context.Context, message *ai.AssistantMessage) (bool, error) {
	if s.SettingsManager == nil {
		return false, nil
	}
	settings := s.SettingsManager.GetRetrySettings()
	if !settings.Enabled {
		return false, nil
	}

	retryCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.agentRunAbortRequested {
		cancelledAttempt := s.as2FinishCancelledRetryLocked()
		s.mu.Unlock()
		cancel()
		s.as2EmitCancelledRetryIfAny(cancelledAttempt)
		return false, nil
	}
	s.retryAttempt++
	attempt := s.retryAttempt
	if attempt > settings.MaxRetries {
		s.retryAttempt--
		s.mu.Unlock()
		cancel()
		return false, nil
	}
	s.retryToken++
	token := s.retryToken
	s.retryCancel = cancel
	s.mu.Unlock()

	rp := as2RetryPolicyFromSettings(settings)
	delayMs := ai.RetryDelayMs(&rp, attempt)
	errMsg := "Unknown error"
	if message != nil && message.ErrorMessage != nil && *message.ErrorMessage != "" {
		errMsg = *message.ErrorMessage
	}
	s.as1Emit(&AgentSessionEventAutoRetryStart{
		Type:         "auto_retry_start",
		Attempt:      attempt,
		MaxAttempts:  settings.MaxRetries,
		DelayMs:      delayMs,
		ErrorMessage: errMsg,
	})

	if err := s.as1OmitRecoveryAttempt(message); err != nil {
		s.mu.Lock()
		if s.retryToken == token {
			s.retryCancel = nil
		}
		s.mu.Unlock()
		cancel()
		return false, err
	}

	sleepErr := utils.Sleep(retryCtx, float64(delayMs))

	s.mu.Lock()
	if s.retryToken == token {
		s.retryCancel = nil
	}
	var cancelledAttempt int
	if sleepErr != nil || s.agentRunAbortRequested {
		cancelledAttempt = s.as2FinishCancelledRetryLocked()
	}
	s.mu.Unlock()
	cancel()

	if sleepErr != nil || cancelledAttempt > 0 {
		s.as2EmitCancelledRetryIfAny(cancelledAttempt)
		return false, nil
	}
	return true, nil
}

// AbortRetry cancels any active auto-retry backoff timer.
func (s *AgentSession) AbortRetry() {
	s.mu.Lock()
	cancel := s.retryCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// AutoRetryEnabled reports whether automatic error retries are enabled.
func (s *AgentSession) AutoRetryEnabled() bool {
	return s.SettingsManager != nil && s.SettingsManager.GetRetryEnabled()
}

// SetAutoRetryEnabled updates the auto-retry setting.
func (s *AgentSession) SetAutoRetryEnabled(enabled bool) {
	if s.SettingsManager != nil {
		s.SettingsManager.SetRetryEnabled(enabled)
	}
}

// ============================================================================
// Bash Execution
// ============================================================================

// ExecuteBash executes a shell command and records its output in session history.
func (s *AgentSession) ExecuteBash(ctx context.Context, command string, onChunk func(chunk string), options ...*ExecuteBashOptions) (BashResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var opts ExecuteBashOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	bashCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.nextBashId++
	id := s.nextBashId
	s.bashCancels[id] = cancel
	s.mu.Unlock()

	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.bashCancels, id)
		s.mu.Unlock()
	}()

	resolvedCmd := command
	var shellPath *string
	if s.SettingsManager != nil {
		if prefix := s.SettingsManager.GetShellCommandPrefix(); prefix != nil && *prefix != "" {
			resolvedCmd = *prefix + "\n" + command
		}
		shellPath = s.SettingsManager.GetShellPath()
	}

	ops := opts.Operations
	if ops == nil {
		if shellPath != nil && *shellPath != "" {
			ops = CreateLocalBashOperations(&LocalBashOperationsOptions{ShellPath: *shellPath})
		} else {
			ops = CreateLocalBashOperations()
		}
	}

	cwd := s.cwd
	if s.SessionManager != nil && s.SessionManager.GetCwd() != "" {
		cwd = s.SessionManager.GetCwd()
	}

	result, err := ExecuteBashWithOperations(bashCtx, resolvedCmd, cwd, ops, &BashExecutorOptions{
		OnChunk: func(delta string) {
			if onChunk != nil {
				onChunk(delta)
			}
			s.as1Emit(&AgentSessionEventBashExecutionUpdate{
				Type:  "bash_execution_update",
				Id:    opts.Id,
				Delta: delta,
			})
		},
	})
	if err != nil {
		return result, err
	}
	s.RecordBashResult(command, result, &RecordBashResultOptions{
		ExcludeFromContext: opts.ExcludeFromContext,
	})
	return result, nil
}

// RecordBashResult records a completed bash execution message in session history
// (deferring flush until run completion if streaming).
func (s *AgentSession) RecordBashResult(command string, result BashResult, options ...*RecordBashResultOptions) {
	var exclude *bool
	if len(options) > 0 && options[0] != nil && options[0].ExcludeFromContext {
		v := true
		exclude = &v
	}
	msg := &BashExecutionMessage{
		Role:               "bashExecution",
		Command:            command,
		Output:             result.Output,
		ExitCode:           result.ExitCode,
		Cancelled:          result.Cancelled,
		Truncated:          result.Truncated,
		FullOutputPath:     result.FullOutputPath,
		Timestamp:          time.Now().UnixMilli(),
		ExcludeFromContext: exclude,
	}
	if s.IsStreaming() {
		s.mu.Lock()
		s.pendingBashMessages = append(s.pendingBashMessages, msg)
		s.mu.Unlock()
		return
	}
	s.bashRecordMu.Lock()
	defer s.bashRecordMu.Unlock()
	if s.SessionManager != nil {
		s.SessionManager.AppendMessage(msg)
	}
	s.as1RefreshFinalizedContext()
}

// AbortBash cancels all active ExecuteBash invocations.
func (s *AgentSession) AbortBash() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.bashCancels))
	for _, fn := range s.bashCancels {
		cancels = append(cancels, fn)
	}
	s.mu.Unlock()
	for _, fn := range cancels {
		fn()
	}
}

// IsBashRunning reports whether at least one ExecuteBash invocation is active.
func (s *AgentSession) IsBashRunning() bool {
	s.mu.Lock()
	running := len(s.bashCancels) > 0
	s.mu.Unlock()
	return running
}

// HasPendingBashMessages reports whether bash messages are waiting to be flushed after streaming.
func (s *AgentSession) HasPendingBashMessages() bool {
	s.mu.Lock()
	has := len(s.pendingBashMessages) > 0
	s.mu.Unlock()
	return has
}

func (s *AgentSession) as2FlushPendingBashMessages() {
	s.mu.Lock()
	if len(s.pendingBashMessages) == 0 {
		s.mu.Unlock()
		return
	}
	pending := s.pendingBashMessages
	s.pendingBashMessages = nil
	s.mu.Unlock()

	s.bashRecordMu.Lock()
	defer s.bashRecordMu.Unlock()
	if s.SessionManager != nil {
		for _, m := range pending {
			s.SessionManager.AppendMessage(m)
		}
	}
	s.as1RefreshFinalizedContext()
}

// ============================================================================
// Session Metadata & Tree Navigation
// ============================================================================

// SetSessionName updates the session display name and emits session_info_changed.
func (s *AgentSession) SetSessionName(name string) {
	if s.SessionManager == nil {
		return
	}
	s.SessionManager.AppendSessionInfo(name)
	updatedName := s.SessionManager.GetSessionName()
	s.as1Emit(&AgentSessionEventSessionInfoChanged{
		Type: "session_info_changed",
		Name: updatedName,
	})
	if runner := s.currentExtensionRunner(); runner != nil {
		_ = runner.Emit(context.Background(), SessionInfoChangedEvent{
			Type: "session_info_changed",
			Name: updatedName,
		})
	}
}

// NavigateTree switches the active branch leaf within the same session file,
// optionally summarizing abandoned entries into a BranchSummaryEntry.
func (s *AgentSession) NavigateTree(ctx context.Context, targetId string, options ...*NavigateTreeOptions) (*NavigateTreeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var opts NavigateTreeOptions
	if len(options) > 0 && options[0] != nil {
		opts = *options[0]
	}

	s.mu.Lock()
	busy := s.isAgentRunActive || s.promptPreflightCount > 0
	s.mu.Unlock()
	if busy {
		return nil, errors.New("Wait for the current response to finish before navigating the session tree.")
	}
	if s.IsCompacting() {
		return nil, errors.New("Wait for the current compaction or tree navigation to finish before navigating the session tree.")
	}

	oldLeafId := s.SessionManager.GetLeafId()
	if oldLeafId != nil && targetId == *oldLeafId {
		return &NavigateTreeResult{Cancelled: false}, nil
	}
	if opts.Summarize && s.Model() == nil {
		return nil, errors.New("No model available for summarization")
	}

	targetEntry := s.SessionManager.GetEntry(targetId)
	if targetEntry == nil {
		return nil, fmt.Errorf("Entry %s not found", targetId)
	}

	collected := CollectEntriesForBranchSummary(s.SessionManager, oldLeafId, targetId)
	customInstructions := opts.CustomInstructions
	replaceInstructions := opts.ReplaceInstructions
	label := opts.Label

	prep := TreePreparation{
		TargetId:            targetId,
		OldLeafId:           oldLeafId,
		CommonAncestorId:    collected.CommonAncestorId,
		EntriesToSummarize:  collected.Entries,
		UserWantsSummary:    opts.Summarize,
		CustomInstructions:  customInstructions,
		ReplaceInstructions: replaceInstructions,
		Label:               label,
	}

	navCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.branchSummaryToken++
	token := s.branchSummaryToken
	s.branchSummaryCancel = cancel
	s.mu.Unlock()

	defer func() {
		cancel()
		s.mu.Lock()
		if s.branchSummaryToken == token {
			s.branchSummaryCancel = nil
		}
		s.mu.Unlock()
		s.as1ResolveIdleWaitIfIdle()
	}()

	var extSummary *SessionBeforeTreeSummary
	fromExtension := false
	if runner := s.currentExtensionRunner(); runner != nil && runner.HasHandlers("session_before_tree") {
		rawRes := runner.Emit(navCtx, SessionBeforeTreeEvent{
			Type:        "session_before_tree",
			Preparation: prep,
		})
		if extRes, ok := rawRes.(*SessionBeforeTreeResult); ok && extRes != nil {
			if extRes.Cancel {
				return &NavigateTreeResult{Cancelled: true}, nil
			}
			if extRes.Summary != nil && opts.Summarize {
				extSummary = extRes.Summary
				fromExtension = true
			}
			if extRes.CustomInstructions != nil {
				customInstructions = extRes.CustomInstructions
			}
			if extRes.ReplaceInstructions != nil {
				replaceInstructions = *extRes.ReplaceInstructions
			}
			if extRes.Label != nil {
				label = extRes.Label
			}
		}
	}

	var summaryText string
	var summaryDetails any
	var summaryUsage *ai.Usage

	if opts.Summarize && len(collected.Entries) > 0 && extSummary == nil {
		auth, err := s.as1GetSummarizationRequestAuth(navCtx, s.Model())
		if err != nil {
			if errors.Is(err, context.Canceled) || navCtx.Err() != nil {
				return &NavigateTreeResult{Cancelled: true, Aborted: true}, nil
			}
			return nil, err
		}
		var streamFn agent.StreamFn
		if s.Agent != nil {
			streamFn = s.Agent.StreamFn()
		}
		var retryPolicy ai.RetryPolicy
		var reserveTokens int
		if s.SettingsManager != nil {
			retryPolicy = as2RetryPolicyFromSettings(s.SettingsManager.GetRetrySettings())
			reserveTokens = s.SettingsManager.GetBranchSummarySettings().ReserveTokens
		}
		res, err := GenerateBranchSummary(navCtx, collected.Entries, GenerateBranchSummaryOptions{
			Model:               auth.model,
			ApiKey:              auth.apiKey,
			Headers:             auth.headers,
			Env:                 auth.env,
			ThinkingLevel:       &auth.thinkingLevel,
			CustomInstructions:  customInstructions,
			ReplaceInstructions: &replaceInstructions,
			ReserveTokens:       &reserveTokens,
			StreamFn:            streamFn,
			Retry:               &retryPolicy,
			Callbacks:           s.as2SummarizationRetryCallbacks("branchSummary", nil),
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || navCtx.Err() != nil {
				return &NavigateTreeResult{Cancelled: true, Aborted: true}, nil
			}
			return nil, err
		}
		if res.Aborted {
			return &NavigateTreeResult{Cancelled: true, Aborted: true}, nil
		}
		if res.Error != nil && *res.Error != "" {
			return nil, errors.New(*res.Error)
		}
		if res.Summary != nil {
			summaryText = *res.Summary
		}
		summaryUsage = res.Usage
		readFiles := res.ReadFiles
		if readFiles == nil {
			readFiles = []string{}
		}
		modifiedFiles := res.ModifiedFiles
		if modifiedFiles == nil {
			modifiedFiles = []string{}
		}
		summaryDetails = BranchSummaryDetails{
			ReadFiles:     readFiles,
			ModifiedFiles: modifiedFiles,
		}
	} else if extSummary != nil {
		summaryText = extSummary.Summary
		summaryDetails = extSummary.Details
		summaryUsage = extSummary.Usage
	}

	var newLeafId *string
	var editorText *string
	switch e := targetEntry.(type) {
	case *SessionMessageEntry:
		if um, ok := e.Message.(*ai.UserMessage); ok && um != nil {
			newLeafId = e.ParentId
			txt := ai.ContentText(um.Content, "")
			editorText = &txt
		} else {
			tid := targetId
			newLeafId = &tid
		}
	case *CustomMessageEntry:
		newLeafId = e.ParentId
		txt := ai.ContentText(e.Content, "")
		editorText = &txt
	default:
		tid := targetId
		newLeafId = &tid
	}

	var summaryEntry *BranchSummaryEntry
	if summaryText != "" {
		summaryId, bErr := s.SessionManager.BranchWithSummary(newLeafId, summaryText, &BranchWithSummaryOptions{
			Details:  summaryDetails,
			FromHook: &fromExtension,
			Usage:    summaryUsage,
		})
		if bErr != nil {
			return nil, bErr
		}
		if se, ok := s.SessionManager.GetEntry(summaryId).(*BranchSummaryEntry); ok {
			summaryEntry = se
		}
		if label != nil && *label != "" {
			s.SessionManager.AppendLabelChange(summaryId, label)
		}
	} else if newLeafId == nil {
		s.SessionManager.ResetLeaf()
	} else {
		if err := s.SessionManager.Branch(*newLeafId); err != nil {
			return nil, err
		}
	}

	if label != nil && *label != "" && summaryText == "" {
		s.SessionManager.AppendLabelChange(targetId, label)
	}

	s.as1RefreshFinalizedContext()
	s.as3RestoreToolsFromTranscript(nil)

	s.mu.Lock()
	if s.branchSummaryToken == token {
		s.branchSummaryCancel = nil
	}
	s.mu.Unlock()

	if runner := s.currentExtensionRunner(); runner != nil {
		fe := fromExtension && summaryText != ""
		_ = runner.Emit(ctx, SessionTreeEvent{
			Type:          "session_tree",
			NewLeafId:     s.SessionManager.GetLeafId(),
			OldLeafId:     oldLeafId,
			SummaryEntry:  summaryEntry,
			FromExtension: &fe,
		})
	}

	return &NavigateTreeResult{
		EditorText:   editorText,
		Cancelled:    false,
		SummaryEntry: summaryEntry,
	}, nil
}

// GetUserMessagesForForking returns all persisted user message texts paired with entry IDs.
func (s *AgentSession) GetUserMessagesForForking() []ForkableUserMessage {
	if s.SessionManager == nil {
		return nil
	}
	var out []ForkableUserMessage
	for _, entry := range s.SessionManager.GetEntries() {
		me, ok := entry.(*SessionMessageEntry)
		if !ok || me == nil || me.Message == nil || me.Message.GetRole() != "user" {
			continue
		}
		if um, ok := me.Message.(*ai.UserMessage); ok && um != nil {
			text := ai.ContentText(um.Content, "")
			if text != "" {
				out = append(out, ForkableUserMessage{
					EntryId: me.Id,
					Text:    text,
				})
			}
		}
	}
	return out
}
