// Ported from packages/coding-agent/src/core/cache-warmer.ts (pi v1.5.2).

package core

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsre"
)

// CacheWarmingAction indicates whether a cache-warming refresh proceeds or stops.
type CacheWarmingAction = ai.CacheWarmingAction

// CacheWarmingDecision describes the inputs and outcome of a cache-warming evaluation.
type CacheWarmingDecision = ai.CacheWarmingDecision

func cwLastPromptTokens(entries []SessionEntry) int {
	for i := len(entries) - 1; i >= 0; i-- {
		sm, ok := entries[i].(*SessionMessageEntry)
		if !ok || sm == nil {
			continue
		}
		assistant, ok := sm.Message.(*ai.AssistantMessage)
		if !ok || assistant == nil {
			continue
		}
		return assistant.Usage.Input + assistant.Usage.CacheRead + assistant.Usage.CacheWrite
	}
	return 0
}

// CacheWarmingDecisionEvent is dispatched before each cache warm refresh.
type CacheWarmingDecisionEvent struct {
	Type                    string                `json:"type"`
	WarmCost                float64               `json:"warmCost"`
	MissCost                float64               `json:"missCost"`
	ContinuationProbability float64               `json:"continuationProbability"`
	Action                  ai.CacheWarmingAction `json:"action"`
}

// CacheWarmingDecisionEventResult lets an extension override the refresh action.
type CacheWarmingDecisionEventResult struct {
	Action *ai.CacheWarmingAction `json:"action,omitzero"`
}

// CacheWarmingStatus reports the current state of CacheWarmer.
type CacheWarmingStatus struct {
	State             string                   `json:"state"`
	Reason            *string                  `json:"reason,omitzero"`
	NextWarmAt        *int64                   `json:"nextWarmAt,omitzero"`
	Decision          *ai.CacheWarmingDecision `json:"decision,omitzero"`
	ExtensionOverride *bool                    `json:"extensionOverride,omitzero"`
}

// CacheWarmRequest identifies the model, context, and options whose prompt cache should stay warm.
type CacheWarmRequest struct {
	Model   *ai.Model                     `json:"model"`
	Context *ai.Context                   `json:"context"`
	Options *ai.ModelsSimpleStreamOptions `json:"options"`
}

// CacheWarmerStreamClient abstracts StreamSimple calls for CacheWarmer and unit tests.
type CacheWarmerStreamClient interface {
	StreamSimple(ctx context.Context, model *ai.Model, c *ai.Context, options *ai.ModelsSimpleStreamOptions) *ai.AssistantMessageEventStream
}

// CacheWarmerSessionStore abstracts the SessionManager methods used by CacheWarmer.
type CacheWarmerSessionStore interface {
	AppendUsage(kind, provider, model string, usage ai.Usage, note ...string) (*UsageEntry, error)
	GetBranch(fromId ...string) []SessionEntry
}

type cwActiveRun struct {
	CacheWarmRequest
	isCurrent         func() bool
	ttlMs             int64
	delayMs           int64
	refreshDeadlineAt int64
	startedAt         int64
	ctx               context.Context
	cancel            context.CancelFunc
	phase             ai.CacheWarmingPhase
	nextWarmAt        int64
	extensionOverride bool
	timer             *time.Timer
	refreshing        bool
}

// CacheWarmer keeps a prompt cache entry alive by re-sending a 1-token request before TTL expiry.
type CacheWarmer struct {
	mu             sync.Mutex
	run            *cwActiveRun
	inactive       CacheWarmingStatus
	models         CacheWarmerStreamClient
	sessionManager CacheWarmerSessionStore
	getMode        func() CacheWarmingMode
	decide         func(ctx context.Context, event CacheWarmingDecisionEvent) (ai.CacheWarmingAction, error)
	now            func() time.Time
	// OnWarmed is called after each successful warm refresh persists a UsageEntry.
	OnWarmed func(entry *UsageEntry)
}

// NewCacheWarmer constructs a CacheWarmer.
func NewCacheWarmer(
	models CacheWarmerStreamClient,
	sessionManager CacheWarmerSessionStore,
	getMode func() CacheWarmingMode,
	decide ...func(ctx context.Context, event CacheWarmingDecisionEvent) (ai.CacheWarmingAction, error),
) *CacheWarmer {
	reason := "waiting for first request"
	var decideFn func(ctx context.Context, event CacheWarmingDecisionEvent) (ai.CacheWarmingAction, error)
	if len(decide) > 0 && decide[0] != nil {
		decideFn = decide[0]
	} else {
		decideFn = func(_ context.Context, event CacheWarmingDecisionEvent) (ai.CacheWarmingAction, error) {
			return event.Action, nil
		}
	}
	return &CacheWarmer{
		inactive:       CacheWarmingStatus{State: "inactive", Reason: &reason},
		models:         models,
		sessionManager: sessionManager,
		getMode:        getMode,
		decide:         decideFn,
		now:            time.Now,
	}
}

func (c *CacheWarmer) nowMs() int64 {
	return c.now().UnixMilli()
}

// Status returns the current cache-warming status snapshot.
func (c *CacheWarmer) Status() CacheWarmingStatus {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.getMode() == "off" {
		reason := "cache warming disabled"
		return CacheWarmingStatus{State: "inactive", Reason: &reason}
	}
	run := c.run
	if run == nil {
		return c.inactive
	}
	if !run.isCurrent() {
		reason := "conversation context changed"
		return CacheWarmingStatus{State: "inactive", Reason: &reason}
	}
	decision := c.evaluateLocked(run)
	if !decision.EconomicsAvailable && !run.refreshing {
		reason := "cache economics unavailable"
		return CacheWarmingStatus{State: "inactive", Reason: &reason}
	}
	state := "scheduled"
	if run.refreshing {
		state = "refreshing"
	}
	nextWarmAt := run.nextWarmAt
	extOverride := run.extensionOverride
	return CacheWarmingStatus{
		State:             state,
		NextWarmAt:        &nextWarmAt,
		Decision:          &decision,
		ExtensionOverride: &extOverride,
	}
}

// Start begins keeping request's prompt cache entry warm while isCurrent returns true.
func (c *CacheWarmer) Start(request CacheWarmRequest, isCurrent func() bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.clearRunLocked()
	if c.getMode() == "off" {
		c.stopLocked("cache warming disabled", nil, nil)
		return
	}
	var simpleOpts *ai.SimpleStreamOptions
	if request.Options != nil {
		simpleOpts = &request.Options.SimpleStreamOptions
	}
	if !ai.IsCacheWarmingReplayable(request.Model, simpleOpts) {
		c.stopLocked("request cannot be replayed safely", nil, nil)
		return
	}
	ttlMs := ai.GetPromptCacheTtlMs(request.Model, simpleOpts)
	if ttlMs == nil {
		reason := "cache lifetime unavailable"
		if simpleOpts != nil && simpleOpts.CacheRetention != nil && *simpleOpts.CacheRetention == ai.CacheRetentionNone {
			reason = "request disabled prompt caching"
		}
		c.stopLocked(reason, nil, nil)
		return
	}
	delayMs := ai.GetCacheWarmingDelayMs(float64(*ttlMs))
	if delayMs == nil {
		c.stopLocked("cache lifetime unavailable", nil, nil)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	run := &cwActiveRun{
		CacheWarmRequest: request,
		isCurrent:        isCurrent,
		ttlMs:            *ttlMs,
		delayMs:          *delayMs,
		startedAt:        c.nowMs(),
		ctx:              ctx,
		cancel:           cancel,
		phase:            ai.CacheWarmingPhaseStreaming,
	}
	c.run = run
	c.scheduleLocked(run)
}

// OnAgentSettled notifies the warmer that the active agent turn settled into idle state.
func (c *CacheWarmer) OnAgentSettled() {
	c.mu.Lock()
	defer c.mu.Unlock()

	run := c.run
	if run == nil {
		return
	}
	if c.getMode() == "streaming" {
		c.stopLocked("agent run settled", nil, nil)
		return
	}
	run.phase = ai.CacheWarmingPhaseIdle
	deadline := run.startedAt + ai.CacheWarmingMaxIdleAgeMs
	if run.nextWarmAt > deadline || c.nowMs() >= deadline {
		c.stopLocked("30-minute idle safety limit reached", nil, nil)
	}
}

// OnModeChanged reconciles any active warming run after the cache warming setting changes.
func (c *CacheWarmer) OnModeChanged() {
	c.mu.Lock()
	defer c.mu.Unlock()

	run := c.run
	if run == nil {
		return
	}
	if reason := c.getModeStopReasonLocked(run); reason != "" {
		c.stopLocked(reason, nil, nil)
	}
}

// Cancel stops any active cache-warming timer and request.
func (c *CacheWarmer) Cancel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked("inactive", nil, nil)
}

func (c *CacheWarmer) clearRunLocked() {
	run := c.run
	if run == nil {
		return
	}
	c.run = nil
	if run.timer != nil {
		run.timer.Stop()
		run.timer = nil
	}
	run.cancel()
}

func (c *CacheWarmer) stopLocked(reason string, decision *ai.CacheWarmingDecision, extOverride *bool) {
	c.clearRunLocked()
	c.inactive = CacheWarmingStatus{
		State:             "inactive",
		Reason:            &reason,
		Decision:          decision,
		ExtensionOverride: extOverride,
	}
}

func (c *CacheWarmer) scheduleLocked(run *cwActiveRun) {
	run.extensionOverride = false
	run.refreshing = false
	now := c.nowMs()
	run.nextWarmAt = now + run.delayMs
	run.refreshDeadlineAt = run.nextWarmAt + (run.ttlMs-run.delayMs)/2

	maxAge := ai.CacheWarmingMaxAgeMs
	if run.phase == ai.CacheWarmingPhaseIdle {
		maxAge = ai.CacheWarmingMaxIdleAgeMs
	}
	deadline := run.startedAt + maxAge
	if run.nextWarmAt > deadline || now >= deadline {
		msg := "one-hour safety limit reached"
		if run.phase == ai.CacheWarmingPhaseIdle {
			msg = "30-minute idle safety limit reached"
		}
		c.stopLocked(msg, nil, nil)
		return
	}

	delay := max(run.nextWarmAt-now, 0)
	run.timer = time.AfterFunc(time.Duration(delay)*time.Millisecond, func() {
		c.refresh(run)
	})
}

func (c *CacheWarmer) refresh(run *cwActiveRun) {
	c.mu.Lock()
	if c.run != run {
		c.mu.Unlock()
		return
	}
	run.timer = nil
	run.refreshing = true
	if !c.validateRunLocked(run) || c.refreshDeadlineMissedLocked(run) {
		c.mu.Unlock()
		return
	}
	decision := c.evaluateLocked(run)
	decideFn := c.decide
	runCtx := run.ctx
	c.mu.Unlock()

	action := decision.Action
	if overridden, err := decideFn(runCtx, CacheWarmingDecisionEvent{
		Type:                    "cache_warming_decision",
		WarmCost:                decision.WarmCost,
		MissCost:                decision.MissCost,
		ContinuationProbability: decision.ContinuationProbability,
		Action:                  action,
	}); err == nil && overridden != "" {
		action = overridden
	}

	c.mu.Lock()
	if !c.validateRunLocked(run) || c.refreshDeadlineMissedLocked(run) {
		c.mu.Unlock()
		return
	}
	extOverride := action != decision.Action
	if action == ai.CacheWarmingActionStop {
		reason := "cache economics unavailable"
		if extOverride {
			reason = "stopped by extension"
		} else if decision.EconomicsAvailable {
			reason = "expected savings below threshold"
		}
		dCopy := decision
		c.stopLocked(reason, &dCopy, &extOverride)
		c.mu.Unlock()
		return
	}
	run.extensionOverride = extOverride
	var simpleOpts *ai.SimpleStreamOptions
	var transforms ai.ModelsRequestTransforms
	if run.Options != nil {
		simpleOpts = &run.Options.SimpleStreamOptions
		transforms = run.Options.ModelsRequestTransforms
	}
	warmSimple := ai.GetCacheWarmingRequestOptions(runCtx, simpleOpts)
	warmOpts := &ai.ModelsSimpleStreamOptions{
		SimpleStreamOptions:     *warmSimple,
		ModelsRequestTransforms: transforms,
	}
	modelsClient := c.models
	reqModel := run.Model
	reqContext := run.Context
	c.mu.Unlock()

	stream := modelsClient.StreamSimple(runCtx, reqModel, reqContext, warmOpts)
	msg, err := stream.Result(runCtx)

	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.validateRunLocked(run) {
		return
	}
	var onWarmedCb func(*UsageEntry)
	var warmedEntry *UsageEntry
	if err == nil && msg != nil && msg.StopReason != ai.StopReasonError && msg.StopReason != ai.StopReasonAborted {
		modelName := msg.Model
		if msg.ResponseModel != nil && *msg.ResponseModel != "" {
			modelName = *msg.ResponseModel
		}
		var noteArgs []string
		if extOverride {
			noteArgs = append(noteArgs, "extension override")
		}
		if entry, appendErr := c.sessionManager.AppendUsage("cache_warm", string(msg.Provider), modelName, msg.Usage, noteArgs...); appendErr == nil {
			warmedEntry = entry
		}
		onWarmedCb = c.OnWarmed
	}
	if c.run == run {
		c.scheduleLocked(run)
	}
	if warmedEntry != nil && onWarmedCb != nil {
		go onWarmedCb(warmedEntry)
	}
}

func (c *CacheWarmer) refreshDeadlineMissedLocked(run *cwActiveRun) bool {
	if c.nowMs() <= run.refreshDeadlineAt {
		return false
	}
	c.stopLocked("cache refresh deadline missed", nil, nil)
	return true
}

func (c *CacheWarmer) validateRunLocked(run *cwActiveRun) bool {
	if c.run != run {
		return false
	}
	reason := c.getModeStopReasonLocked(run)
	if reason == "" && !run.isCurrent() {
		reason = "conversation context changed"
	}
	if reason == "" {
		return true
	}
	c.stopLocked(reason, nil, nil)
	return false
}

func (c *CacheWarmer) getModeStopReasonLocked(run *cwActiveRun) string {
	mode := c.getMode()
	if mode == "off" {
		return "cache warming disabled"
	}
	if mode == "streaming" && run.phase == ai.CacheWarmingPhaseIdle {
		return "agent run settled"
	}
	return ""
}

func (c *CacheWarmer) evaluateLocked(run *cwActiveRun) ai.CacheWarmingDecision {
	return ai.EvaluateCacheWarming(run.Model, cwLastPromptTokens(c.sessionManager.GetBranch()), run.phase)
}

func cwFormatDollars(value float64) string {
	if value < 0 {
		return fmt.Sprintf("-$%.3f", math.Abs(value))
	}
	return fmt.Sprintf("$%.3f", value)
}

func cwFormatEconomics(decision ai.CacheWarmingDecision) string {
	if !decision.EconomicsAvailable {
		return "cache economics unavailable"
	}
	prob := int(math.Round(decision.ContinuationProbability * 100))
	probText := fmt.Sprintf("%d%% continuation probability", prob)
	if decision.Phase == ai.CacheWarmingPhaseStreaming {
		probText = fmt.Sprintf("%d%% continuation probability while agent is running", prob)
	}
	cmp := "<"
	if decision.Action == ai.CacheWarmingActionWarm {
		cmp = ">="
	}
	return fmt.Sprintf(
		"%s, expected savings %s %s $%.3f",
		probText,
		cwFormatDollars(decision.ExpectedSavings),
		cmp,
		ai.CacheWarmingMinimumExpectedSavings,
	)
}

func cwFormatDecisionTime(nextWarmAt *int64, now int64) string {
	if nextWarmAt == nil || *nextWarmAt <= now {
		return "Decision now"
	}
	remainingSec := int64(math.Ceil(float64(*nextWarmAt-now) / 1000.0))
	hours := remainingSec / 3600
	remainingSec %= 3600
	minutes := remainingSec / 60
	seconds := remainingSec % 60
	var parts []string
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if seconds > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	return "Decision in " + strings.Join(parts, " ")
}

// FormatCacheWarmingStatus renders a one-line /session status summary.
func FormatCacheWarmingStatus(status CacheWarmingStatus, now ...int64) string {
	nowMs := time.Now().UnixMilli()
	if len(now) > 0 {
		nowMs = now[0]
	}
	decision := status.Decision
	extOverride := status.ExtensionOverride != nil && *status.ExtensionOverride
	if decision == nil || (status.State == "inactive" && !decision.EconomicsAvailable && !extOverride) {
		reason := "unknown reason"
		if status.Reason != nil && *status.Reason != "" {
			reason = *status.Reason
		}
		return fmt.Sprintf("Inactive (%s)", reason)
	}
	var details string
	if extOverride {
		details = "extension override, " + cwFormatEconomics(*decision)
	} else {
		details = fmt.Sprintf("%s -> %s", cwFormatEconomics(*decision), decision.Action)
	}
	if status.State == "inactive" {
		return fmt.Sprintf("Stopped (%s)", details)
	}
	if status.State == "refreshing" {
		return fmt.Sprintf("Warming cache (%s)", details)
	}
	return fmt.Sprintf("%s (%s)", cwFormatDecisionTime(status.NextWarmAt, nowMs), details)
}

var cwTrailingUsageZeroRe = jsre.MustCompile(`(\.\d{3}\d*?)0+$`, "")

// FormatCacheWarmingUsage renders a one-line transcript cost summary for consecutive cache warms.
func FormatCacheWarmingUsage(totalCost float64, note ...string) string {
	noteText := ""
	if len(note) > 0 && note[0] != "" {
		noteText = fmt.Sprintf(" (%s)", note[0])
	}
	raw := fmt.Sprintf("%.6f", totalCost)
	cost := cwTrailingUsageZeroRe.Replace(raw, "$1")
	return fmt.Sprintf("Cache warmed%s: $%s", noteText, cost)
}
