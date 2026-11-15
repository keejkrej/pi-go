// Ported from packages/durable/src/harness/scheduler.ts (pi v1.0.0).

package durable

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	hsScanPageSize  = 256
	hsMaxTimerDelay = int64(2147483647)
)

var hsLiveStatuses = []TaskStateStatus{
	TaskStatePending,
	TaskStateRunning,
	TaskStateWaiting,
	TaskStateCompleting,
}

type hsInvocationBinding struct {
	signal context.Context
	check  func() error
}

type hsTaskNode struct {
	conversationId ConversationId
	owner          *TaskId
	background     bool
}

type hsUp struct {
	isTask         bool
	taskId         TaskId
	conversationId ConversationId
}

type hsStep struct {
	isUnknown      bool
	isTask         bool
	taskId         TaskId
	node           hsTaskNode
	conversationId ConversationId
}

type hsOverlay struct {
	tasks map[TaskId]TaskRecord
	edges map[ConversationId]*TaskId
}

type hsScope struct {
	roots          bool
	conversationId ConversationId
}

type hsIdleKey struct {
	hasId bool
	id    ConversationId
}

type hsInvocation struct {
	taskId         TaskId
	conversationId ConversationId
	mode           string
	cancel         context.CancelCauseFunc
	sigCtx         context.Context
	ctx            chord.Context
	done           chan struct{}
	doneOnce       sync.Once

	mu      sync.Mutex
	watches map[DocumentWatch]struct{}
	ended   bool
}

func (i *hsInvocation) finish() {
	i.doneOnce.Do(func() {
		close(i.done)
	})
}

func (i *hsInvocation) isEnded() bool {
	i.mu.Lock()
	ended := i.ended
	i.mu.Unlock()
	return ended
}

type hsPhaseState struct {
	scheduler *hsTaskScheduler
	inv       *hsInvocation

	mu        sync.Mutex
	snapshot  RegistrySnapshot
	task      AnyTask
	agentOnce sync.Once
	agentRes  *Agent
	agentErr  error
	agentDone chan struct{}
}

func (p *hsPhaseState) getSnapshot() RegistrySnapshot {
	p.mu.Lock()
	s := p.snapshot
	p.mu.Unlock()
	return s
}

func (p *hsPhaseState) getTask() AnyTask {
	p.mu.Lock()
	t := p.task
	p.mu.Unlock()
	return t
}

func (p *hsPhaseState) resetAgent() {
	p.mu.Lock()
	p.agentOnce = sync.Once{}
	p.agentRes = nil
	p.agentErr = nil
	p.agentDone = make(chan struct{})
	p.mu.Unlock()
}

func (p *hsPhaseState) resolveAgent(ctx chord.Context) (*Agent, error) {
	if p.inv.isEnded() {
		return nil, hsEndedError(p.inv.taskId)
	}
	p.mu.Lock()
	if p.agentDone == nil {
		p.agentDone = make(chan struct{})
	}
	once := &p.agentOnce
	done := p.agentDone
	snap := p.snapshot
	p.mu.Unlock()

	once.Do(func() {
		res, err := p.scheduler.opts.agent(p.inv.conversationId, snap, p.inv.ctx)
		p.mu.Lock()
		p.agentRes = res
		p.agentErr = err
		p.mu.Unlock()
		close(done)
	})

	sig := hutAbortSignal(ctx)
	if sig == nil || sig.Done() == nil {
		<-done
		p.mu.Lock()
		res, err := p.agentRes, p.agentErr
		p.mu.Unlock()
		return res, err
	}
	if err := sig.Err(); err != nil {
		return nil, hutSignalErr(sig)
	}
	select {
	case <-done:
		p.mu.Lock()
		res, err := p.agentRes, p.agentErr
		p.mu.Unlock()
		return res, err
	case <-sig.Done():
		return nil, hutSignalErr(sig)
	}
}

type hsReservation struct {
	inv      *hsInvocation
	task     AnyTask
	snapshot RegistrySnapshot
}

type hsPhaseResult struct {
	checkpoint JsonValue
	phase      string
	hasFailure bool
	failureErr error
}

type hsDecision struct {
	cont     bool
	hasFault bool
	faultErr error
}

type hsFit struct {
	task       AnyTask
	migrates   bool
	reason     TaskBlockedReason
	err        error
	hasBlocked bool
}

type hsResolution struct {
	ready  bool
	task   AnyTask
	record TaskRecord
	reason TaskBlockedReason
}

type hsTaskSchedulerOptions struct {
	session        *SessionImpl
	storage        Storage
	registry       RegistryReader
	models         ai.Models
	agent          func(id ConversationId, snapshot RegistrySnapshot, ctx chord.Context) (*Agent, error)
	settings       func() Settings
	env            func(id ConversationId, ctx chord.Context) (env.ExecutionEnv, error)
	now            func() int64
	report         func(err error)
	settleOutcome  func(tx *Transaction, record TaskRecord, outcome TaskOutcome) error
	withdrawInputs func(tx *Transaction, id ConversationId) error
	conversation   func(id ConversationId, binding hsInvocationBinding, ctx chord.Context) (ConversationHandle, error)
	ctx            chord.Context
}

type hsTaskScheduler struct {
	opts        hsTaskSchedulerOptions
	taskWaiters *hutWaiters[TaskId, *SettledTask]
	idleWaiters *hutWaiters[hsIdleKey, struct{}]

	mu               sync.Mutex
	live             map[TaskId]TaskRecord
	invocations      map[TaskId]*hsInvocation
	failedMigrations map[TaskId]struct {
		task AnyTask
		err  error
	}
	edges              map[ConversationId]*TaskId
	edgeKnown          map[ConversationId]bool
	conversationOwners map[TaskId]struct{}
	settled            map[TaskId]hsTaskNode
	failFastChecks     map[TaskId]struct{}
	reconcileScheduled bool
	cascadePending     bool
	unsubRegistry      func()
	enabled            bool
	closing            bool
	dirty              bool
	pumping            bool
}

func hsNewTaskScheduler(opts hsTaskSchedulerOptions) *hsTaskScheduler {
	return &hsTaskScheduler{
		opts:        opts,
		taskWaiters: hutNewWaiters[TaskId, *SettledTask](),
		idleWaiters: hutNewWaiters[hsIdleKey, struct{}](),
		live:        make(map[TaskId]TaskRecord),
		invocations: make(map[TaskId]*hsInvocation),
		failedMigrations: make(map[TaskId]struct {
			task AnyTask
			err  error
		}),
		edges:              make(map[ConversationId]*TaskId),
		edgeKnown:          make(map[ConversationId]bool),
		conversationOwners: make(map[TaskId]struct{}),
		settled:            make(map[TaskId]hsTaskNode),
		failFastChecks:     make(map[TaskId]struct{}),
		unsubRegistry:      func() {},
	}
}

func (s *hsTaskScheduler) Open(ctx chord.Context) error {
	s.opts.session.SubscribeCommits(func(pub CommitPublication, _ chord.Context) {
		s.observe(pub)
	})
	s.opts.session.SubscribeClose(func() {
		s.seal()
	})
	unsub := s.opts.registry.Subscribe(func() {
		s.kick()
	})
	s.mu.Lock()
	s.unsubRegistry = unsub
	s.mu.Unlock()

	if err := s.opts.session.CommitWith(ctx, func(tx *Transaction) error {
		var all []TaskRecord
		for _, st := range hsLiveStatuses {
			status := st
			items, err := hutScanAll(func(cursor Cursor) (Page[TaskRecord, Cursor], error) {
				return tx.ScanTasks(TaskQuery{Status: status}, hsScanPageSize, cursor)
			})
			if err != nil {
				return err
			}
			all = append(all, items...)
		}
		for _, rec := range all {
			s.mu.Lock()
			s.live[rec.Id] = rec
			if rec.State.Status == TaskStateWaiting && rec.State.Policy == JoinPolicyFailFast {
				s.failFastChecks[rec.Id] = struct{}{}
			}
			s.mu.Unlock()
			if rec.State.Status == TaskStateRunning {
				if err := tx.SetTask(hsWithState(rec, TaskState{
					Status:     TaskStatePending,
					Checkpoint: rec.State.Checkpoint,
				})); err != nil {
					return err
				}
			}
		}
		return nil
	}, nil); err != nil {
		return err
	}

	s.mu.Lock()
	s.cascadePending = true
	s.mu.Unlock()
	s.scheduleReconcile()
	return nil
}

func (s *hsTaskScheduler) Resume() {
	s.mu.Lock()
	s.enabled = true
	s.mu.Unlock()
	s.kick()
}

func (s *hsTaskScheduler) Join() {
	s.mu.Lock()
	dones := make([]chan struct{}, 0, len(s.invocations))
	for _, inv := range s.invocations {
		dones = append(dones, inv.done)
	}
	s.mu.Unlock()
	for _, ch := range dones {
		<-ch
	}
}

func (s *hsTaskScheduler) Abort(ctx chord.Context, id TaskId) (TaskAbortResult, error) {
	var result TaskAbortResult
	var runInv *hsInvocation
	if err := s.opts.session.CommitWith(ctx, func(tx *Transaction) error {
		current, err := tx.Task(id)
		if err != nil {
			return err
		}
		if current == nil {
			return fmt.Errorf("Task %d does not exist", id)
		}
		if current.State.Status == TaskStateTerminal {
			result = TaskAbortTerminal
			return nil
		}
		s.mu.Lock()
		inv := s.invocations[id]
		s.mu.Unlock()

		if inv == nil && current.State.Status != TaskStateCompleting {
			if _, err := s.loadScopes(false); err != nil {
				return err
			}
			s.mu.Lock()
			owned := s.ownedLiveLocked(nil)
			_, isOwnedLive := owned[id]
			res := hsResolution{}
			if !isOwnedLive {
				snap := s.opts.registry.Snapshot()
				res = s.resolveLocked(*current, snap)
			}
			s.mu.Unlock()

			if !isOwnedLive && !res.ready {
				reason := string(res.reason)
				if err := s.terminate(tx, *current, TaskOutcome{
					Status: TaskOutcomeOrphaned,
					Reason: &reason,
				}); err != nil {
					return err
				}
				result = TaskAbortMarked
				return nil
			}
		}
		if !current.AbortRequested {
			updated := *current
			updated.AbortRequested = true
			if err := tx.SetTask(updated); err != nil {
				return err
			}
		}
		result = TaskAbortMarked
		if inv != nil && inv.mode == "run" {
			runInv = inv
		}
		return nil
	}, nil); err != nil {
		return "", err
	}
	if runInv != nil {
		sig := hutAbortSignal(ctx)
		if sig == nil || sig.Done() == nil {
			<-runInv.done
		} else {
			select {
			case <-runInv.done:
			case <-sig.Done():
				return "", hutSignalErr(sig)
			}
		}
	}
	return result, nil
}

func (s *hsTaskScheduler) WaitForTask(ctx chord.Context, id TaskId) (*SettledTask, error) {
	var settled *SettledTask
	var handle *hutWaitHandle[TaskId, *SettledTask]
	if err := s.opts.session.ReadOnLine(func() error {
		s.mu.Lock()
		closing := s.closing
		_, isLive := s.live[id]
		s.mu.Unlock()
		if closing {
			return hutClosedError()
		}
		if isLive {
			var err error
			handle, err = s.taskWaiters.Register(id, ctx)
			return err
		}
		rec, err := s.opts.storage.Task(ctx, id)
		if err != nil {
			return err
		}
		if rec == nil {
			return fmt.Errorf("Task %d does not exist", id)
		}
		settled = rec
		return nil
	}); err != nil {
		return nil, err
	}
	if settled != nil {
		return settled, nil
	}
	return handle.Await(ctx)
}

func (s *hsTaskScheduler) WaitForIdle(ctx chord.Context, conversationId *ConversationId) error {
	var key hsIdleKey
	if conversationId != nil {
		key = hsIdleKey{hasId: true, id: *conversationId}
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return hutClosedError()
	}
	if s.idleLocked(conversationId) {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	handle, err := s.idleWaiters.Register(key, ctx)
	if err != nil {
		return err
	}
	// Re-check after registering so an intervening observe cannot miss resolving this waiter.
	s.mu.Lock()
	nowClosing := s.closing
	nowIdle := s.idleLocked(conversationId)
	s.mu.Unlock()
	if nowClosing {
		s.idleWaiters.RejectAll(hutClosedError())
	} else if nowIdle {
		s.idleWaiters.Resolve(key, struct{}{})
	} else {
		s.scheduleReconcile()
	}
	_, err = handle.Await(ctx)
	return err
}

func (s *hsTaskScheduler) AbortConversation(ctx chord.Context, conversationId ConversationId, background bool) error {
	var reached []TaskId
	if err := s.opts.session.CommitWith(ctx, func(tx *Transaction) error {
		queued, err := s.loadScopes(true)
		if err != nil {
			return err
		}
		scope := hsScope{conversationId: conversationId}
		s.mu.Lock()
		liveCopy := make([]TaskRecord, 0, len(s.live))
		for _, rec := range s.live {
			liveCopy = append(liveCopy, rec)
		}
		for _, rec := range liveCopy {
			if rec.Background && !background {
				continue
			}
			if in, known := s.inScopeLocked(hsParentOf(rec), scope, background); !known || !in {
				continue
			}
			reached = append(reached, rec.Id)
			if !rec.AbortRequested {
				updated := rec
				updated.AbortRequested = true
				s.mu.Unlock()
				if err := tx.SetTask(updated); err != nil {
					return err
				}
				s.mu.Lock()
			}
		}
		var toWithdraw []ConversationId
		for _, cid := range queued {
			if in, known := s.inScopeLocked(hsUp{conversationId: cid}, scope, background); known && in {
				toWithdraw = append(toWithdraw, cid)
			}
		}
		s.mu.Unlock()

		for _, cid := range toWithdraw {
			if err := s.opts.withdrawInputs(tx, cid); err != nil {
				return err
			}
		}
		return nil
	}, nil); err != nil {
		return err
	}
	if background {
		for _, id := range reached {
			if _, err := s.WaitForTask(ctx, id); err != nil {
				return err
			}
		}
	}
	cidCopy := conversationId
	return s.WaitForIdle(ctx, &cidCopy)
}

func (s *hsTaskScheduler) Inspect(snapshot RegistrySnapshot) (HarnessSchedulingState, []TaskInspection, error) {
	if _, err := s.loadScopes(false); err != nil {
		return "", nil, err
	}
	s.mu.Lock()
	owned := s.ownedLiveLocked(nil)
	tasks := make([]TaskInspection, 0, len(s.live))
	for _, rec := range s.live {
		tasks = append(tasks, TaskInspection{
			Record: rec,
			State:  s.inspectTaskLocked(rec, snapshot, owned),
		})
	}
	slices.SortFunc(tasks, func(a, b TaskInspection) int {
		return cmp.Compare(a.Record.Id, b.Record.Id)
	})
	scheduling := HarnessSchedulingPaused
	if s.closing {
		scheduling = HarnessSchedulingClosing
	} else if s.enabled {
		scheduling = HarnessSchedulingRunning
	}
	s.mu.Unlock()
	return scheduling, tasks, nil
}

func (s *hsTaskScheduler) inspectTaskLocked(rec TaskRecord, snapshot RegistrySnapshot, owned map[TaskId][]TaskId) TaskInspectionState {
	if _, active := s.invocations[rec.Id]; active {
		return TaskInspectionState{Kind: TaskInspectionRunning}
	}
	if rec.State.Status == TaskStateCompleting {
		return TaskInspectionState{Kind: TaskInspectionCompleting}
	}
	on := s.waitingOnLocked(rec, owned)
	if len(on) > 0 {
		return TaskInspectionState{Kind: TaskInspectionWaiting, On: on}
	}
	fit := s.fitLocked(rec, snapshot.Task(rec.Kind))
	if fit.hasBlocked {
		return TaskInspectionState{
			Kind:   TaskInspectionBlocked,
			Reason: fit.reason,
			Error:  fit.err,
		}
	}
	if fit.migrates && fit.task.erasedDefinition().Migrate == nil {
		return TaskInspectionState{
			Kind:   TaskInspectionBlocked,
			Reason: TaskBlockedMigrationFailed,
			Error:  hsMissingMigration(rec, fit.task.erasedDefinition()),
		}
	}
	return TaskInspectionState{
		Kind:     TaskInspectionReady,
		Migrates: fit.migrates,
	}
}

func (s *hsTaskScheduler) observe(pub CommitPublication) {
	var (
		updated          []TaskRecord
		failed           []TaskId
		settledToResolve []TaskRecord
		toAbort          []*hsInvocation
		changed          bool
		needReconcile    bool
	)

	s.mu.Lock()
	for _, ch := range pub.Changes {
		switch c := ch.(type) {
		case StorageWriteTask:
			changed = true
			rec := c.Value
			s.handleTaskChangeLocked(rec, &updated, &failed, &settledToResolve, &toAbort, &needReconcile)
		case *StorageWriteTask:
			if c == nil {
				continue
			}
			changed = true
			rec := c.Value
			s.handleTaskChangeLocked(rec, &updated, &failed, &settledToResolve, &toAbort, &needReconcile)
		case StorageWriteConversation:
			if !s.edgeKnown[c.Value.Id] {
				var ownerPtr *TaskId
				if c.Value.Owner != nil {
					oid := c.Value.Owner.TaskId
					ownerPtr = &oid
				}
				s.setEdgeLocked(c.Value.Id, ownerPtr)
			}
		case *StorageWriteConversation:
			if c != nil && !s.edgeKnown[c.Value.Id] {
				var ownerPtr *TaskId
				if c.Value.Owner != nil {
					oid := c.Value.Owner.TaskId
					ownerPtr = &oid
				}
				s.setEdgeLocked(c.Value.Id, ownerPtr)
			}
		}
	}

	for _, id := range failed {
		for _, rec := range s.live {
			if rec.State.Status == TaskStateWaiting && rec.State.Policy == JoinPolicyFailFast && hsContainsTaskId(rec.State.On, id) {
				s.failFastChecks[rec.Id] = struct{}{}
				needReconcile = true
			}
		}
	}

	for _, ch := range pub.Changes {
		var sub *SubmissionRecord
		switch c := ch.(type) {
		case StorageWriteSubmission:
			sub = &c.Value
		case *StorageWriteSubmission:
			sub = hsValueSubmission(c)
		}
		if sub == nil || sub.Status != SubmissionStatusQueued || sub.Type != SubmissionTypeInput {
			continue
		}
		up := hsUp{conversationId: sub.ConversationId}
		if !s.chainKnownLocked(up, nil) || s.belowCancelledLocked(up) {
			s.cascadePending = true
		}
	}

	for _, rec := range updated {
		if !s.chainKnownLocked(hsParentOf(rec), nil) {
			needReconcile = true
		} else if !rec.Background && !rec.AbortRequested && s.belowCancelledLocked(hsParentOf(rec)) {
			s.cascadePending = true
		}
	}
	if s.cascadePending {
		needReconcile = true
	}
	s.mu.Unlock()

	for _, inv := range toAbort {
		inv.cancel(errors.New("Task abort requested"))
	}
	for _, rec := range settledToResolve {
		copyRec := rec
		s.taskWaiters.Resolve(rec.Id, &copyRec)
	}
	if needReconcile {
		s.scheduleReconcile()
	}
	if !changed {
		return
	}
	s.resolveIdleWaiters()
	s.kick()
}

func hsValueSubmission(c *StorageWriteSubmission) *SubmissionRecord {
	if c == nil {
		return nil
	}
	return &c.Value
}

func (s *hsTaskScheduler) handleTaskChangeLocked(
	rec TaskRecord,
	updated *[]TaskRecord,
	failed *[]TaskId,
	settledToResolve *[]TaskRecord,
	toAbort *[]*hsInvocation,
	needReconcile *bool,
) {
	prev, hasPrev := s.live[rec.Id]
	if hsFailedOutcome(rec) && (!hasPrev || !hsFailedOutcome(prev)) {
		*failed = append(*failed, rec.Id)
	}
	if rec.State.Status == TaskStateTerminal {
		delete(s.live, rec.Id)
		delete(s.failedMigrations, rec.Id)
		delete(s.failFastChecks, rec.Id)
		if _, isOwner := s.conversationOwners[rec.Id]; isOwner {
			s.settled[rec.Id] = hsNodeOf(rec)
		}
		*settledToResolve = append(*settledToResolve, rec)
		*needReconcile = true
		return
	}
	if rec.AbortRequested && (!hasPrev || !prev.AbortRequested) {
		s.cascadePending = true
		if inv, ok := s.invocations[rec.Id]; ok && inv.mode == "run" {
			*toAbort = append(*toAbort, inv)
		}
	}
	status := rec.State.Status
	if status == TaskStateCompleting && (!hasPrev || prev.State.Status != TaskStateCompleting) {
		if hsCancellationIntent(rec) {
			s.cascadePending = true
		}
		*needReconcile = true
	}
	if status == TaskStateWaiting && rec.State.Policy == JoinPolicyFailFast && (!hasPrev || prev.State.Status != TaskStateWaiting) {
		s.failFastChecks[rec.Id] = struct{}{}
		*needReconcile = true
	}
	s.live[rec.Id] = rec
	*updated = append(*updated, rec)
}

func (s *hsTaskScheduler) resolveIdleWaiters() {
	for _, key := range s.idleWaiters.Keys() {
		var cidPtr *ConversationId
		if key.hasId {
			cid := key.id
			cidPtr = &cid
		}
		s.mu.Lock()
		idle := s.idleLocked(cidPtr)
		s.mu.Unlock()
		if idle {
			s.idleWaiters.Resolve(key, struct{}{})
		}
	}
}

func (s *hsTaskScheduler) ensurePumpLocked() {
	if s.pumping || s.closing {
		return
	}
	if s.reconcileScheduled || (s.dirty && s.enabled) {
		s.pumping = true
		go s.pump()
	}
}

func (s *hsTaskScheduler) scheduleReconcile() {
	s.mu.Lock()
	if s.reconcileScheduled || s.closing {
		s.mu.Unlock()
		return
	}
	s.reconcileScheduled = true
	s.ensurePumpLocked()
	s.mu.Unlock()
}

func (s *hsTaskScheduler) pump() {
	for {
		s.mu.Lock()
		if s.closing {
			s.reconcileScheduled = false
			s.pumping = false
			s.mu.Unlock()
			return
		}
		if s.reconcileScheduled {
			s.reconcileScheduled = false
			cascade := s.cascadePending
			s.cascadePending = false
			checks := make([]TaskId, 0, len(s.failFastChecks))
			for id := range s.failFastChecks {
				checks = append(checks, id)
			}
			s.failFastChecks = make(map[TaskId]struct{})
			s.mu.Unlock()

			if err := s.reconcileOnce(cascade, checks); err != nil {
				s.mu.Lock()
				s.cascadePending = true
				for _, id := range checks {
					s.failFastChecks[id] = struct{}{}
				}
				closing := s.closing
				hasMore := s.reconcileScheduled || (s.dirty && s.enabled && !s.closing)
				if !hasMore {
					s.pumping = false
				}
				s.mu.Unlock()
				if !closing {
					s.opts.report(err)
				}
				s.resolveIdleWaiters()
				if !hasMore {
					return
				}
				continue
			}
			s.resolveIdleWaiters()
			continue
		}
		if s.dirty && s.enabled {
			s.dirty = false
			s.mu.Unlock()

			reservations, err := s.reserve()
			if err != nil {
				s.mu.Lock()
				closing := s.closing
				hasMore := s.reconcileScheduled || (s.dirty && s.enabled && !s.closing)
				if !hasMore {
					s.pumping = false
				}
				s.mu.Unlock()
				if !closing {
					s.opts.report(err)
				}
				if !hasMore {
					return
				}
				continue
			}
			for _, res := range reservations {
				s.start(res)
			}
			continue
		}
		s.pumping = false
		s.mu.Unlock()
		return
	}
}

func (s *hsTaskScheduler) reconcileOnce(cascade bool, checks []TaskId) error {
	return s.opts.session.CommitWith(s.opts.ctx, func(tx *Transaction) error {
		s.mu.Lock()
		closing := s.closing
		s.mu.Unlock()
		if closing {
			return nil
		}
		queued, err := s.loadScopes(cascade)
		if err != nil {
			return err
		}
		marked := make(map[TaskId]struct{})
		mark := func(rec TaskRecord) error {
			if rec.AbortRequested {
				return nil
			}
			if _, ok := marked[rec.Id]; ok {
				return nil
			}
			marked[rec.Id] = struct{}{}
			updated := rec
			updated.AbortRequested = true
			return tx.SetTask(updated)
		}

		s.mu.Lock()
		var toMark []TaskRecord
		for _, rec := range s.live {
			if !rec.Background && s.belowCancelledLocked(hsParentOf(rec)) {
				toMark = append(toMark, rec)
			}
		}
		var failFastWaiters []TaskRecord
		for _, id := range checks {
			if w, ok := s.live[id]; ok && w.State.Status == TaskStateWaiting {
				failFastWaiters = append(failFastWaiters, w)
			}
		}
		s.mu.Unlock()

		for _, rec := range toMark {
			if err := mark(rec); err != nil {
				return err
			}
		}
		for _, waiter := range failFastWaiters {
			anyFailed, err := s.anyFailed(waiter.State.On)
			if err != nil {
				return err
			}
			if !anyFailed {
				continue
			}
			s.mu.Lock()
			var members []TaskRecord
			for _, mId := range waiter.State.On {
				if m, ok := s.live[mId]; ok && !hsFailedOutcome(m) {
					members = append(members, m)
				}
			}
			s.mu.Unlock()
			for _, m := range members {
				if err := mark(m); err != nil {
					return err
				}
			}
		}
		s.mu.Lock()
		var toWithdraw []ConversationId
		for _, cid := range queued {
			if s.belowCancelledLocked(hsUp{conversationId: cid}) {
				toWithdraw = append(toWithdraw, cid)
			}
		}
		s.mu.Unlock()
		for _, cid := range toWithdraw {
			if err := s.opts.withdrawInputs(tx, cid); err != nil {
				return err
			}
		}
		return s.finalize(tx)
	}, nil)
}

func (s *hsTaskScheduler) anyFailed(ids []TaskId) (bool, error) {
	for _, id := range ids {
		s.mu.Lock()
		rec, ok := s.live[id]
		s.mu.Unlock()
		if !ok {
			loaded, err := s.opts.storage.Task(s.opts.ctx, id)
			if err != nil {
				return false, err
			}
			if loaded == nil {
				continue
			}
			rec = *loaded
		}
		if hsFailedOutcome(rec) {
			return true, nil
		}
	}
	return false, nil
}

func (s *hsTaskScheduler) finalize(tx *Transaction) error {
	for {
		overlay := hsOverlayOf(tx)
		s.mu.Lock()
		owned := s.ownedLiveLocked(overlay)
		liveRecs := s.liveRecordsLocked(overlay)
		var done []TaskRecord
		for _, rec := range liveRecs {
			if rec.State.Status == TaskStateCompleting {
				if _, isOwned := owned[rec.Id]; !isOwned {
					done = append(done, rec)
				}
			}
		}
		s.mu.Unlock()
		if len(done) == 0 {
			return nil
		}
		for _, rec := range done {
			if rec.State.Outcome == nil {
				continue
			}
			outcome := *rec.State.Outcome
			if err := tx.SetTask(hsWithState(rec, TaskState{
				Status:  TaskStateTerminal,
				Outcome: &outcome,
			})); err != nil {
				return err
			}
			if outcome.Status == TaskOutcomeFaulted || outcome.Status == TaskOutcomeOrphaned {
				if err := s.opts.settleOutcome(tx, rec, outcome); err != nil {
					return err
				}
			}
		}
	}
}

func (s *hsTaskScheduler) loadScopes(queued bool) ([]ConversationId, error) {
	s.mu.Lock()
	var unknownParents []hsUp
	for _, rec := range s.live {
		p := hsParentOf(rec)
		if !s.chainKnownLocked(p, nil) {
			unknownParents = append(unknownParents, p)
		}
	}
	s.mu.Unlock()
	for _, p := range unknownParents {
		if err := s.loadChain(p, nil); err != nil {
			return nil, err
		}
	}
	if !queued {
		return nil, nil
	}
	subs, err := hutScanAll(func(cursor Cursor) (Page[SubmissionRecord, Cursor], error) {
		return s.opts.storage.ScanSubmissions(s.opts.ctx, SubmissionQuery{Status: SubmissionStatusQueued}, hsScanPageSize, cursor)
	})
	if err != nil {
		return nil, err
	}
	seen := make(map[ConversationId]struct{})
	var conversations []ConversationId
	for _, sub := range subs {
		if _, ok := seen[sub.ConversationId]; !ok {
			seen[sub.ConversationId] = struct{}{}
			conversations = append(conversations, sub.ConversationId)
		}
	}
	for _, cid := range conversations {
		if err := s.loadChain(hsUp{conversationId: cid}, nil); err != nil {
			return nil, err
		}
	}
	return conversations, nil
}

func (s *hsTaskScheduler) loadChain(start hsUp, overlay *hsOverlay) error {
	at := &start
	for at != nil {
		if at.isTask {
			s.mu.Lock()
			node, ok := s.nodeLocked(at.taskId, overlay)
			s.mu.Unlock()
			if !ok {
				rec, err := s.opts.storage.Task(s.opts.ctx, at.taskId)
				if err != nil {
					return err
				}
				if rec == nil {
					return nil
				}
				node = hsNodeOf(*rec)
				if rec.State.Status == TaskStateTerminal {
					s.mu.Lock()
					s.settled[rec.Id] = node
					s.mu.Unlock()
				}
			}
			next := hsParentOfNode(node)
			at = &next
		} else {
			s.mu.Lock()
			edge, known := s.edgeLocked(at.conversationId, overlay)
			s.mu.Unlock()
			if !known {
				rec, err := s.opts.storage.Conversation(s.opts.ctx, at.conversationId)
				if err != nil {
					return err
				}
				if rec != nil && rec.Owner != nil {
					oid := rec.Owner.TaskId
					edge = &oid
				} else {
					edge = nil
				}
				s.mu.Lock()
				s.setEdgeLocked(at.conversationId, edge)
				s.mu.Unlock()
			}
			if edge == nil {
				at = nil
			} else {
				at = &hsUp{isTask: true, taskId: *edge}
			}
		}
	}
	return nil
}

func (s *hsTaskScheduler) setEdgeLocked(cid ConversationId, owner *TaskId) {
	s.edges[cid] = owner
	s.edgeKnown[cid] = true
	if owner != nil {
		s.conversationOwners[*owner] = struct{}{}
	}
}

func (s *hsTaskScheduler) edgeLocked(cid ConversationId, overlay *hsOverlay) (*TaskId, bool) {
	if overlay != nil {
		if edge, ok := overlay.edges[cid]; ok {
			return edge, true
		}
	}
	if !s.edgeKnown[cid] {
		return nil, false
	}
	return s.edges[cid], true
}

func (s *hsTaskScheduler) nodeLocked(id TaskId, overlay *hsOverlay) (hsTaskNode, bool) {
	if overlay != nil {
		if rec, ok := overlay.tasks[id]; ok {
			return hsNodeOf(rec), true
		}
	}
	if rec, ok := s.live[id]; ok {
		return hsNodeOf(rec), true
	}
	node, ok := s.settled[id]
	return node, ok
}

func (s *hsTaskScheduler) aboveLocked(start hsUp, overlay *hsOverlay) []hsStep {
	var steps []hsStep
	at := &start
	for at != nil {
		if at.isTask {
			node, ok := s.nodeLocked(at.taskId, overlay)
			if !ok {
				steps = append(steps, hsStep{isUnknown: true})
				return steps
			}
			steps = append(steps, hsStep{isTask: true, taskId: at.taskId, node: node})
			next := hsParentOfNode(node)
			at = &next
		} else {
			steps = append(steps, hsStep{conversationId: at.conversationId})
			edge, known := s.edgeLocked(at.conversationId, overlay)
			if !known {
				steps = append(steps, hsStep{isUnknown: true})
				return steps
			}
			if edge == nil {
				at = nil
			} else {
				at = &hsUp{isTask: true, taskId: *edge}
			}
		}
	}
	return steps
}

func (s *hsTaskScheduler) chainKnownLocked(start hsUp, overlay *hsOverlay) bool {
	for _, st := range s.aboveLocked(start, overlay) {
		if st.isUnknown {
			return false
		}
	}
	return true
}

func (s *hsTaskScheduler) liveRecordsLocked(overlay *hsOverlay) []TaskRecord {
	var out []TaskRecord
	for _, rec := range s.live {
		cand := rec
		if overlay != nil {
			if o, ok := overlay.tasks[rec.Id]; ok {
				cand = o
			}
		}
		if cand.State.Status != TaskStateTerminal {
			out = append(out, cand)
		}
	}
	if overlay != nil {
		for _, rec := range overlay.tasks {
			if _, inLive := s.live[rec.Id]; !inLive && rec.State.Status != TaskStateTerminal {
				out = append(out, rec)
			}
		}
	}
	return out
}

func (s *hsTaskScheduler) ownedLiveLocked(overlay *hsOverlay) map[TaskId][]TaskId {
	owned := make(map[TaskId][]TaskId)
	for _, rec := range s.liveRecordsLocked(overlay) {
		if rec.Background {
			continue
		}
		for _, st := range s.aboveLocked(hsParentOf(rec), overlay) {
			if st.isUnknown {
				break
			}
			if !st.isTask {
				continue
			}
			owned[st.taskId] = append(owned[st.taskId], rec.Id)
			if st.node.background {
				break
			}
		}
	}
	return owned
}

func (s *hsTaskScheduler) inScopeLocked(start hsUp, scope hsScope, crossBackground bool) (in bool, known bool) {
	for _, st := range s.aboveLocked(start, nil) {
		if st.isUnknown {
			return false, false
		}
		if !st.isTask {
			if !scope.roots && st.conversationId == scope.conversationId {
				return true, true
			}
		} else if st.node.background && !crossBackground {
			return false, true
		}
	}
	return scope.roots, true
}

func (s *hsTaskScheduler) belowCancelledLocked(start hsUp) bool {
	for _, st := range s.aboveLocked(start, nil) {
		if st.isUnknown {
			return false
		}
		if !st.isTask {
			continue
		}
		if live, ok := s.live[st.taskId]; ok && hsCancellationIntent(live) {
			return true
		}
		if st.node.background {
			return false
		}
	}
	return false
}

func (s *hsTaskScheduler) seal() {
	s.mu.Lock()
	s.closing = true
	unsub := s.unsubRegistry
	s.unsubRegistry = func() {}
	invs := make([]*hsInvocation, 0, len(s.invocations))
	for _, inv := range s.invocations {
		invs = append(invs, inv)
	}
	s.mu.Unlock()

	unsub()
	err := hutClosedError()
	s.taskWaiters.RejectAll(err)
	s.idleWaiters.RejectAll(err)
	for _, inv := range invs {
		inv.cancel(err)
	}
}

func (s *hsTaskScheduler) kick() {
	s.mu.Lock()
	s.dirty = true
	if !s.enabled || s.closing {
		s.mu.Unlock()
		return
	}
	s.ensurePumpLocked()
	s.mu.Unlock()
}

func (s *hsTaskScheduler) reserve() ([]hsReservation, error) {
	var reservations []hsReservation
	err := s.opts.session.CommitWith(s.opts.ctx, func(tx *Transaction) error {
		s.mu.Lock()
		enabled := s.enabled
		closing := s.closing
		reconcile := s.reconcileScheduled
		if reconcile {
			s.dirty = true
		}
		s.mu.Unlock()
		if !enabled || closing || reconcile {
			return nil
		}
		if _, err := s.loadScopes(false); err != nil {
			return err
		}

		s.mu.Lock()
		owned := s.ownedLiveLocked(nil)
		var candidates []TaskRecord
		for _, rec := range s.live {
			if _, active := s.invocations[rec.Id]; active {
				continue
			}
			if len(s.waitingOnLocked(rec, owned)) > 0 || rec.State.Status == TaskStateCompleting {
				continue
			}
			candidates = append(candidates, rec)
		}
		s.mu.Unlock()

		var snapshot RegistrySnapshot
		for _, rec := range candidates {
			mode := "run"
			if rec.AbortRequested {
				mode = "abort"
			}
			if snapshot == nil {
				snapshot = s.opts.registry.Snapshot()
			}
			s.mu.Lock()
			res := s.resolveLocked(rec, snapshot)
			s.mu.Unlock()
			if !res.ready {
				if mode == "abort" {
					reason := string(res.reason)
					if err := s.terminate(tx, rec, TaskOutcome{
						Status: TaskOutcomeOrphaned,
						Reason: &reason,
					}); err != nil {
						return err
					}
				}
				continue
			}
			if res.record.Version != rec.Version || rec.State.Status != TaskStateRunning {
				updated := res.record
				updated.State = TaskState{
					Status:     TaskStateRunning,
					Checkpoint: res.record.State.Checkpoint,
				}
				if err := tx.SetTask(updated); err != nil {
					return err
				}
			}
			s.mu.Lock()
			inv := s.createInvocationLocked(rec, mode)
			s.mu.Unlock()
			reservations = append(reservations, hsReservation{
				inv:      inv,
				task:     res.task,
				snapshot: snapshot,
			})
		}
		return nil
	}, nil)
	if err != nil {
		for _, res := range reservations {
			s.mu.Lock()
			delete(s.invocations, res.inv.taskId)
			s.mu.Unlock()
			res.inv.finish()
		}
		return nil, err
	}
	return reservations, nil
}

func (s *hsTaskScheduler) waitingOnLocked(rec TaskRecord, owned map[TaskId][]TaskId) []TaskId {
	if rec.AbortRequested {
		return append([]TaskId(nil), owned[rec.Id]...)
	}
	if rec.State.Status != TaskStateWaiting {
		return nil
	}
	var live []TaskId
	for _, id := range rec.State.On {
		if _, ok := s.live[id]; ok {
			live = append(live, id)
		}
	}
	return live
}

func (s *hsTaskScheduler) resolveLocked(rec TaskRecord, snapshot RegistrySnapshot) hsResolution {
	fit := s.fitLocked(rec, snapshot.Task(rec.Kind))
	if fit.hasBlocked {
		return hsResolution{reason: fit.reason}
	}
	if !fit.migrates {
		return hsResolution{ready: true, task: fit.task, record: rec}
	}
	def := fit.task.erasedDefinition()
	if def.Migrate == nil {
		err := hsMissingMigration(rec, def)
		s.failedMigrations[rec.Id] = struct {
			task AnyTask
			err  error
		}{task: fit.task, err: err}
		s.opts.report(err)
		return hsResolution{reason: TaskBlockedMigrationFailed}
	}
	migInput, migCp, migErr := hsRunMigrate(def.Migrate, rec.Input, rec.State.Checkpoint, rec.Version)
	if migErr != nil {
		s.failedMigrations[rec.Id] = struct {
			task AnyTask
			err  error
		}{task: fit.task, err: migErr}
		s.opts.report(migErr)
		return hsResolution{reason: TaskBlockedMigrationFailed}
	}
	cpCopy, cpErr := stCopyStrictJsonValue(migCp)
	if cpErr != nil {
		s.failedMigrations[rec.Id] = struct {
			task AnyTask
			err  error
		}{task: fit.task, err: cpErr}
		s.opts.report(cpErr)
		return hsResolution{reason: TaskBlockedMigrationFailed}
	}
	inCopy, inErr := stCopyStrictJsonValue(migInput)
	if inErr != nil {
		s.failedMigrations[rec.Id] = struct {
			task AnyTask
			err  error
		}{task: fit.task, err: inErr}
		s.opts.report(inErr)
		return hsResolution{reason: TaskBlockedMigrationFailed}
	}
	nextRec := rec
	nextRec.Version = def.Version
	nextRec.Input = inCopy
	nextRec.State.Checkpoint = cpCopy
	return hsResolution{ready: true, task: fit.task, record: nextRec}
}

func hsRunMigrate(
	fn func(input JsonValue, checkpoint JsonValue, fromVersion int) (JsonValue, JsonValue, error),
	input JsonValue,
	checkpoint JsonValue,
	fromVersion int,
) (inOut JsonValue, cpOut JsonValue, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", r)
			}
		}
	}()
	return fn(input, checkpoint, fromVersion)
}

func (s *hsTaskScheduler) fitLocked(rec TaskRecord, task AnyTask) hsFit {
	if task == nil {
		return hsFit{hasBlocked: true, reason: TaskBlockedMissingTask}
	}
	version := task.TaskVersion()
	if version == rec.Version {
		return hsFit{task: task}
	}
	if version < rec.Version {
		return hsFit{hasBlocked: true, reason: TaskBlockedTaskTooOld}
	}
	if failed, ok := s.failedMigrations[rec.Id]; ok && failed.task == task {
		return hsFit{hasBlocked: true, reason: TaskBlockedMigrationFailed, err: failed.err}
	}
	return hsFit{task: task, migrates: true}
}

func (s *hsTaskScheduler) createInvocationLocked(rec TaskRecord, mode string) *hsInvocation {
	baseSig := hutAbortSignal(s.opts.ctx)
	var parent context.Context
	if baseSig != nil {
		parent = baseSig
	} else {
		parent = context.Background()
	}
	sigCtx, cancel := context.WithCancelCause(parent)
	inv := &hsInvocation{
		taskId:         rec.Id,
		conversationId: rec.ConversationId,
		mode:           mode,
		cancel:         cancel,
		sigCtx:         sigCtx,
		ctx:            chord.WithAbortSignal(sigCtx, s.opts.ctx),
		done:           make(chan struct{}),
		watches:        make(map[DocumentWatch]struct{}),
	}
	s.invocations[rec.Id] = inv
	return inv
}

func (s *hsTaskScheduler) start(res hsReservation) {
	inv := res.inv
	go func() {
		defer func() {
			s.end(inv)
			inv.finish()
			s.kick()
		}()
		var err error
		if inv.mode == "run" {
			err = s.run(res)
		} else {
			err = s.runAbort(res)
		}
		if err != nil {
			s.opts.report(err)
		}
	}()
}

func (s *hsTaskScheduler) run(res hsReservation) error {
	inv := res.inv
	phase := &hsPhaseState{
		scheduler: s,
		inv:       inv,
		snapshot:  res.snapshot,
		task:      res.task,
	}
	runtime := s.runtime(inv, phase)
	var reportedTask AnyTask
	hasReported := false
	var prev *hsPhaseResult

	for {
		current, err := s.step(inv, func(tx *Transaction, cur TaskRecord) hsDecision {
			return s.decide(tx, cur, prev, phase, &reportedTask, &hasReported)
		})
		if err != nil {
			return err
		}
		s.mu.Lock()
		closing := s.closing
		s.mu.Unlock()
		if current == nil || closing {
			return nil
		}
		phaseName := hsPhaseName(current.State.Checkpoint)
		phase.resetAgent()
		handler := phase.getTask().erasedDefinition().Phases[phaseName]
		if handler == nil {
			prev = &hsPhaseResult{
				checkpoint: current.State.Checkpoint,
				phase:      phaseName,
				hasFailure: true,
				failureErr: fmt.Errorf("Task %s has no phase %s", current.Kind, phaseName),
			}
			continue
		}
		callErr := hsRunPhaseHandler(handler, inv.ctx, current, runtime)
		if callErr != nil {
			prev = &hsPhaseResult{
				checkpoint: current.State.Checkpoint,
				phase:      phaseName,
				hasFailure: true,
				failureErr: callErr,
			}
		} else {
			prev = &hsPhaseResult{
				checkpoint: current.State.Checkpoint,
				phase:      phaseName,
			}
		}
	}
}

func hsRunPhaseHandler(
	handler AnyPhaseHandler,
	ctx chord.Context,
	task *RunningTaskRecord,
	runtime AnyTaskRuntime,
) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", r)
			}
		}
	}()
	return handler(ctx, task, runtime)
}

func (s *hsTaskScheduler) decide(
	tx *Transaction,
	current TaskRecord,
	prev *hsPhaseResult,
	phase *hsPhaseState,
	reportedTask *AnyTask,
	hasReported *bool,
) hsDecision {
	if current.AbortRequested {
		return hsDecision{cont: false}
	}
	if prev == nil {
		return hsDecision{cont: true}
	}
	if prev.hasFailure {
		return hsDecision{hasFault: true, faultErr: prev.failureErr}
	}
	if hsJsonEqual(current.State.Checkpoint, prev.checkpoint) {
		return hsDecision{
			hasFault: true,
			faultErr: fmt.Errorf("Task %s phase %s returned without durable progress", current.Kind, prev.phase),
		}
	}
	nextSnap := s.opts.registry.Snapshot()
	phase.mu.Lock()
	curTask := phase.task
	phase.snapshot = nextSnap
	phase.mu.Unlock()

	next := nextSnap.Task(current.Kind)
	if next != curTask {
		if next != nil && hsCanReserve(next, current) {
			if err := tx.SetTask(hsWithState(current, TaskState{
				Status:     TaskStatePending,
				Checkpoint: current.State.Checkpoint,
			})); err != nil {
				return hsDecision{hasFault: true, faultErr: err}
			}
			return hsDecision{cont: false}
		}
		if !*hasReported || *reportedTask != next {
			*hasReported = true
			*reportedTask = next
			cause := "incompatible_task"
			if next == nil {
				cause = "missing_task"
			}
			s.opts.report(fmt.Errorf("Task %d keeps running under its old %s definition (%s)", current.Id, current.Kind, cause))
		}
	}
	return hsDecision{cont: true}
}

func (s *hsTaskScheduler) runAbort(res hsReservation) error {
	inv := res.inv
	s.mu.Lock()
	curRec, hasCur := s.live[inv.taskId]
	closing := s.closing
	s.mu.Unlock()
	if !hasCur || closing {
		return nil
	}
	var failure error
	phase := &hsPhaseState{
		scheduler: s,
		inv:       inv,
		snapshot:  res.snapshot,
		task:      res.task,
	}
	runtime := s.runtime(inv, phase)
	abortHandler := res.task.erasedDefinition().Abort
	if abortHandler != nil {
		failure = hsRunPhaseHandler(abortHandler, inv.ctx, &curRec, runtime)
	}
	if failure == nil {
		failure = fmt.Errorf("Abort handler of task %d returned without a terminal outcome", inv.taskId)
	}
	_, err := s.step(inv, func(_ *Transaction, _ TaskRecord) hsDecision {
		return hsDecision{hasFault: true, faultErr: failure}
	})
	return err
}

func (s *hsTaskScheduler) step(
	inv *hsInvocation,
	decide func(tx *Transaction, current TaskRecord) hsDecision,
) (*RunningTaskRecord, error) {
	var selected *RunningTaskRecord
	err := s.opts.session.CommitWith(s.opts.ctx, func(tx *Transaction) error {
		s.mu.Lock()
		found, ok := s.live[inv.taskId]
		closing := s.closing
		s.mu.Unlock()

		var dec hsDecision
		if ok && found.State.Status == TaskStateRunning && !closing {
			dec = decide(tx, found)
		}
		if dec.cont {
			copyRec := found
			selected = &copyRec
			return nil
		}
		s.end(inv)
		if dec.hasFault {
			msg := ""
			if dec.faultErr != nil {
				msg = dec.faultErr.Error()
			}
			return s.terminate(tx, found, TaskOutcome{
				Status: TaskOutcomeFaulted,
				Error:  &TaskOutcomeError{Message: msg},
			})
		}
		return nil
	}, nil)
	if err != nil {
		s.end(inv)
		s.mu.Lock()
		closing := s.closing
		s.mu.Unlock()
		if !closing {
			s.opts.report(err)
		}
		return nil, nil
	}
	return selected, nil
}

func (s *hsTaskScheduler) terminate(tx *Transaction, rec TaskRecord, outcome TaskOutcome) error {
	if _, err := s.loadScopes(false); err != nil {
		return err
	}
	overlay := hsOverlayOf(tx)
	s.mu.Lock()
	owned := s.ownedLiveLocked(overlay)
	_, hasOwned := owned[rec.Id]
	s.mu.Unlock()
	if hasOwned {
		return tx.SetTask(hsWithState(rec, TaskState{
			Status:  TaskStateCompleting,
			Outcome: &outcome,
		}))
	}
	if err := tx.SetTask(hsWithState(rec, TaskState{
		Status:  TaskStateTerminal,
		Outcome: &outcome,
	})); err != nil {
		return err
	}
	return s.opts.settleOutcome(tx, rec, outcome)
}

func (s *hsTaskScheduler) commitState(tx *Transaction, inv *hsInvocation, current TaskRecord, next TaskState) error {
	if next.Status == TaskStateWaiting {
		if err := s.validateWait(tx, inv, current, next.On, next.Policy); err != nil {
			return err
		}
	}
	if next.Status == TaskStateTerminal {
		overlay := hsOverlayOf(tx)
		if _, err := s.loadScopes(false); err != nil {
			return err
		}
		for _, rec := range overlay.tasks {
			if err := s.loadChain(hsParentOf(rec), overlay); err != nil {
				return err
			}
		}
		s.mu.Lock()
		owned := s.ownedLiveLocked(overlay)
		_, hasOwned := owned[current.Id]
		s.mu.Unlock()
		if hasOwned {
			return tx.SetTask(hsWithState(current, TaskState{
				Status:  TaskStateCompleting,
				Outcome: next.Outcome,
			}))
		}
	}
	return tx.SetTask(hsWithState(current, next))
}

func (s *hsTaskScheduler) validateWait(
	tx *Transaction,
	inv *hsInvocation,
	current TaskRecord,
	on []TaskId,
	policy JoinPolicy,
) error {
	if inv.mode == "abort" {
		return fmt.Errorf("Abort handler of task %d cannot wait", current.Id)
	}
	overlay := hsOverlayOf(tx)
	if err := s.loadChain(hsParentOf(current), nil); err != nil {
		return err
	}
	s.mu.Lock()
	owners := make(map[TaskId]struct{})
	for _, st := range s.aboveLocked(hsParentOf(current), nil) {
		if st.isTask {
			owners[st.taskId] = struct{}{}
		}
	}
	s.mu.Unlock()

	for _, id := range on {
		if id == current.Id {
			return fmt.Errorf("Task %d cannot wait on itself or its owner %d", current.Id, id)
		}
		if _, isOwner := owners[id]; isOwner {
			return fmt.Errorf("Task %d cannot wait on itself or its owner %d", current.Id, id)
		}
		var member *TaskRecord
		if m, ok := overlay.tasks[id]; ok {
			member = &m
		} else {
			s.mu.Lock()
			if m, ok := s.live[id]; ok {
				copyM := m
				member = &copyM
			}
			s.mu.Unlock()
		}
		if member == nil {
			loaded, err := s.opts.storage.Task(s.opts.ctx, id)
			if err != nil {
				return err
			}
			member = loaded
		}
		if member == nil {
			return fmt.Errorf("Task %d does not exist", id)
		}
		if policy == JoinPolicyFailFast && (member.Owner == nil || *member.Owner != current.Id) {
			return fmt.Errorf("Task %d can wait failFast only on tasks it owns; %d is not one", current.Id, id)
		}
	}
	return nil
}

func (s *hsTaskScheduler) end(inv *hsInvocation) {
	inv.mu.Lock()
	if inv.ended {
		inv.mu.Unlock()
		return
	}
	inv.ended = true
	watches := make([]DocumentWatch, 0, len(inv.watches))
	for w := range inv.watches {
		watches = append(watches, w)
	}
	inv.mu.Unlock()

	s.mu.Lock()
	if s.invocations[inv.taskId] == inv {
		delete(s.invocations, inv.taskId)
	}
	s.mu.Unlock()

	for _, w := range watches {
		w.Stop()
	}
	inv.cancel(hsEndedError(inv.taskId))
}

func (s *hsTaskScheduler) idleLocked(conversationId *ConversationId) bool {
	scope := hsScope{roots: conversationId == nil}
	if conversationId != nil {
		scope.conversationId = *conversationId
	}
	for _, rec := range s.live {
		if rec.Background {
			continue
		}
		in, known := s.inScopeLocked(hsParentOf(rec), scope, false)
		if !known || in {
			return false
		}
	}
	return true
}

func (s *hsTaskScheduler) runtime(inv *hsInvocation, phase *hsPhaseState) AnyTaskRuntime {
	return &hsTaskRuntime{
		s:     s,
		inv:   inv,
		phase: phase,
	}
}

type hsHookRunner struct {
	s     *hsTaskScheduler
	inv   *hsInvocation
	phase *hsPhaseState
}

func (r *hsHookRunner) EachRaw(invoke func(handler any) error) error {
	agent, err := r.phase.resolveAgent(r.inv.ctx)
	if err != nil {
		return err
	}
	handlers := AgentHooks(agent, r.phase.getTask().TaskKind())
	for _, h := range handlers {
		if err := invoke(h); err != nil {
			if r.inv.sigCtx.Err() != nil {
				return err
			}
			r.s.opts.report(err)
		}
	}
	return nil
}

type hsTaskRuntime struct {
	s     *hsTaskScheduler
	inv   *hsInvocation
	phase *hsPhaseState
}

func (r *hsTaskRuntime) TaskId() TaskId                 { return r.inv.taskId }
func (r *hsTaskRuntime) ConversationId() ConversationId { return r.inv.conversationId }
func (r *hsTaskRuntime) Signal() context.Context        { return r.inv.sigCtx }
func (r *hsTaskRuntime) Registry() RegistrySnapshot     { return r.phase.getSnapshot() }
func (r *hsTaskRuntime) Agent(ctx chord.Context) (*Agent, error) {
	return r.phase.resolveAgent(ctx)
}
func (r *hsTaskRuntime) Settings() Settings { return r.s.opts.settings() }
func (r *hsTaskRuntime) Models() ai.Models  { return r.s.opts.models }
func (r *hsTaskRuntime) Env(ctx chord.Context) (env.ExecutionEnv, error) {
	if r.inv.isEnded() {
		return nil, hsEndedError(r.inv.taskId)
	}
	return r.s.opts.env(r.inv.conversationId, ctx)
}
func (r *hsTaskRuntime) HooksAny() AnyHookRunner {
	return &hsHookRunner{s: r.s, inv: r.inv, phase: r.phase}
}

func (r *hsTaskRuntime) Snapshot(ctx chord.Context, token AnyDocToken, args ...any) (JsonObject, error) {
	if r.inv.isEnded() {
		return nil, hsEndedError(r.inv.taskId)
	}
	return r.s.opts.session.Snapshot(ctx, token, args...)
}

func (r *hsTaskRuntime) SnapshotAsOf(ctx chord.Context, token AnyDocToken, cid ConversationId, args ...any) (JsonObject, error) {
	if r.inv.isEnded() {
		return nil, hsEndedError(r.inv.taskId)
	}
	return r.s.opts.session.SnapshotAsOf(ctx, token, cid, args...)
}

func (r *hsTaskRuntime) WatchDoc(ctx chord.Context, token AnyDocToken, args ...any) (DocumentWatch, error) {
	if r.inv.isEnded() {
		return nil, hsEndedError(r.inv.taskId)
	}
	watch, err := r.s.opts.session.WatchDoc(ctx, token, args...)
	if err != nil || watch == nil {
		return watch, err
	}
	r.inv.mu.Lock()
	if r.inv.ended {
		r.inv.mu.Unlock()
		watch.Stop()
		return nil, hsEndedError(r.inv.taskId)
	}
	r.inv.watches[watch] = struct{}{}
	r.inv.mu.Unlock()
	go func() {
		<-watch.Done()
		r.inv.mu.Lock()
		delete(r.inv.watches, watch)
		r.inv.mu.Unlock()
	}()
	return watch, nil
}

func (r *hsTaskRuntime) Commit(ctx chord.Context, change func(tx Tx, current *RunningTaskRecord) (*TaskState, bool, error)) error {
	return r.s.gated(ctx, r.inv, func(tx *Transaction, current *RunningTaskRecord) error {
		next, update, err := change(tx, current)
		if err != nil {
			return err
		}
		if update && next != nil {
			return r.s.commitState(tx, r.inv, *current, *next)
		}
		return nil
	})
}

func (r *hsTaskRuntime) Memo(ctx chord.Context, name string) (JsonValue, bool, error) {
	if r.inv.isEnded() {
		return nil, false, hsEndedError(r.inv.taskId)
	}
	var val JsonValue
	var ok bool
	err := r.s.opts.session.ReadOnLine(func() error {
		r.s.mu.Lock()
		rec, exists := r.s.live[r.inv.taskId]
		r.s.mu.Unlock()
		if exists && rec.Memos != nil {
			val, ok = rec.Memos.Get(name)
		}
		return nil
	})
	return val, ok, err
}

func (r *hsTaskRuntime) MemoPut(ctx chord.Context, name string, candidate JsonValue) (JsonValue, error) {
	var winner JsonValue
	err := r.s.gated(ctx, r.inv, func(tx *Transaction, current *RunningTaskRecord) error {
		if current.Memos != nil {
			if existing, ok := current.Memos.Get(name); ok {
				winner = existing
				return nil
			}
		}
		memos := jsonx.NewObject()
		if current.Memos != nil {
			memos = current.Memos.Clone()
		}
		memos.Set(name, candidate)
		updated := *current
		updated.Memos = memos
		if err := tx.SetTask(updated); err != nil {
			return err
		}
		winner = candidate
		return nil
	})
	return winner, err
}

func (r *hsTaskRuntime) GetTask(ctx chord.Context, id TaskId) (*TaskRecord, error) {
	if r.inv.isEnded() {
		return nil, hsEndedError(r.inv.taskId)
	}
	var rec *TaskRecord
	err := r.s.opts.session.ReadOnLine(func() error {
		var e error
		rec, e = r.s.opts.storage.Task(ctx, id)
		return e
	})
	return rec, err
}

func (r *hsTaskRuntime) WaitForTask(ctx chord.Context, id TaskId) (*SettledTask, error) {
	if r.inv.isEnded() {
		return nil, hsEndedError(r.inv.taskId)
	}
	boundCtx := chord.WithAbortSignal(r.inv.sigCtx, ctx)
	return r.s.WaitForTask(boundCtx, id)
}

func (r *hsTaskRuntime) Outcomes(ctx chord.Context, ids []TaskId) ([]TaskOutcome, error) {
	if r.inv.isEnded() {
		return nil, hsEndedError(r.inv.taskId)
	}
	var outcomes []TaskOutcome
	err := r.s.opts.session.ReadOnLine(func() error {
		for _, id := range ids {
			rec, e := r.s.opts.storage.Task(ctx, id)
			if e != nil {
				return e
			}
			if rec == nil || rec.State.Status != TaskStateTerminal || rec.State.Outcome == nil {
				return fmt.Errorf("Task %d is not terminal", id)
			}
			outcomes = append(outcomes, *rec.State.Outcome)
		}
		return nil
	})
	return outcomes, err
}

func (r *hsTaskRuntime) Conversation(ctx chord.Context, id ConversationId) (ConversationHandle, error) {
	if r.inv.isEnded() {
		return nil, hsEndedError(r.inv.taskId)
	}
	binding := hsInvocationBinding{
		signal: r.inv.sigCtx,
		check: func() error {
			if r.inv.isEnded() {
				return hsEndedError(r.inv.taskId)
			}
			return nil
		},
	}
	return r.s.opts.conversation(id, binding, ctx)
}

func (r *hsTaskRuntime) Entry(ctx chord.Context, id EntryId) (*EntryRecord, error) {
	return r.TypedEntry(ctx, nil, id)
}

func (r *hsTaskRuntime) TypedEntry(ctx chord.Context, token *Entry, id EntryId) (*EntryRecord, error) {
	if r.inv.isEnded() {
		return nil, hsEndedError(r.inv.taskId)
	}
	var entry *EntryRecord
	err := r.s.opts.session.ReadOnLine(func() error {
		found, e := r.s.opts.storage.EntryInConversation(ctx, r.inv.conversationId, id)
		if e != nil {
			return e
		}
		if found == nil {
			return nil
		}
		if token == nil || found.Entry.Kind == token.Kind {
			copyE := found.Entry
			entry = &copyE
		}
		return nil
	})
	return entry, err
}

func (r *hsTaskRuntime) Context(ctx chord.Context, cid ConversationId, at ...EntryId) (*ContextView, error) {
	if r.inv.isEnded() {
		return nil, hsEndedError(r.inv.taskId)
	}
	return hxReadContext(ctx, r.s.opts.session, r.s.opts.storage, cid, at...)
}

func (r *hsTaskRuntime) Now() int64 {
	if r.inv.isEnded() {
		panic(hsEndedError(r.inv.taskId))
	}
	return r.s.opts.now()
}

func (r *hsTaskRuntime) Report(err error) {
	if r.inv.isEnded() {
		panic(hsEndedError(r.inv.taskId))
	}
	r.s.opts.report(err)
}

func (r *hsTaskRuntime) Sleep(ctx chord.Context, until int64) error {
	if r.inv.isEnded() {
		return hsEndedError(r.inv.taskId)
	}
	callerSig := hutAbortSignal(ctx)
	for {
		if err := r.inv.sigCtx.Err(); err != nil {
			return hutSignalErr(r.inv.sigCtx)
		}
		if callerSig != nil && callerSig.Err() != nil {
			return hutSignalErr(callerSig)
		}
		remaining := until - r.s.opts.now()
		if remaining <= 0 {
			return nil
		}
		step := remaining
		if step > hsMaxTimerDelay {
			step = hsMaxTimerDelay
		}
		timer := time.NewTimer(time.Duration(step) * time.Millisecond)
		var callerDone <-chan struct{}
		if callerSig != nil {
			callerDone = callerSig.Done()
		}
		select {
		case <-timer.C:
		case <-r.inv.sigCtx.Done():
			timer.Stop()
			return hutSignalErr(r.inv.sigCtx)
		case <-callerDone:
			timer.Stop()
			return hutSignalErr(callerSig)
		}
	}
}

func (s *hsTaskScheduler) gated(
	ctx chord.Context,
	inv *hsInvocation,
	change func(tx *Transaction, current *RunningTaskRecord) error,
) error {
	if inv.isEnded() {
		return hsEndedError(inv.taskId)
	}
	cid := inv.conversationId
	tid := inv.taskId
	return s.opts.session.CommitWith(ctx, func(tx *Transaction) error {
		if inv.isEnded() {
			return hsEndedError(inv.taskId)
		}
		s.mu.Lock()
		closing := s.closing
		found, exists := s.live[inv.taskId]
		s.mu.Unlock()
		if closing {
			return hutClosedError()
		}
		if !exists {
			return fmt.Errorf("Task %d is terminal", inv.taskId)
		}
		if found.State.Status != TaskStateRunning {
			return fmt.Errorf("Task %d is %s", inv.taskId, found.State.Status)
		}
		if inv.mode == "run" && found.AbortRequested {
			return fmt.Errorf("Task %d has a durable abort mark", inv.taskId)
		}
		return change(tx, &found)
	}, &TransactionScope{
		ConversationId: &cid,
		TaskId:         &tid,
	})
}

func hsCancellationIntent(rec TaskRecord) bool {
	return rec.State.Status != TaskStateTerminal && (rec.AbortRequested || hsFailedOutcome(rec))
}

func hsFailedOutcome(rec TaskRecord) bool {
	st := rec.State
	return (st.Status == TaskStateCompleting || st.Status == TaskStateTerminal) &&
		st.Outcome != nil &&
		st.Outcome.Status != TaskOutcomeCompleted
}

func hsParentOf(rec TaskRecord) hsUp {
	if rec.Owner != nil {
		return hsUp{isTask: true, taskId: *rec.Owner}
	}
	return hsUp{conversationId: rec.ConversationId}
}

func hsParentOfNode(node hsTaskNode) hsUp {
	if node.owner != nil {
		return hsUp{isTask: true, taskId: *node.owner}
	}
	return hsUp{conversationId: node.conversationId}
}

func hsNodeOf(rec TaskRecord) hsTaskNode {
	var ownerCopy *TaskId
	if rec.Owner != nil {
		oid := *rec.Owner
		ownerCopy = &oid
	}
	return hsTaskNode{
		conversationId: rec.ConversationId,
		owner:          ownerCopy,
		background:     rec.Background,
	}
}

func hsOverlayOf(tx *Transaction) *hsOverlay {
	tasks := make(map[TaskId]TaskRecord)
	for _, rec := range tx.StagedTasks() {
		tasks[rec.Id] = rec
	}
	edges := make(map[ConversationId]*TaskId)
	for _, conv := range tx.StagedConversations() {
		var ownerPtr *TaskId
		if conv.Owner != nil {
			oid := conv.Owner.TaskId
			ownerPtr = &oid
		}
		edges[conv.Id] = ownerPtr
	}
	return &hsOverlay{tasks: tasks, edges: edges}
}

func hsWithState(rec TaskRecord, state TaskState) TaskRecord {
	next := rec
	next.State = state
	if state.Status == TaskStateTerminal || state.Status == TaskStateCompleting {
		next.Memos = nil
	}
	return next
}

func hsCanReserve(task AnyTask, rec TaskRecord) bool {
	def := task.erasedDefinition()
	return def.Version == rec.Version || (def.Version > rec.Version && def.Migrate != nil)
}

func hsMissingMigration(rec TaskRecord, def *AnyTaskDefinition) error {
	return fmt.Errorf("Task %s version %d has no migration from %d", rec.Kind, def.Version, rec.Version)
}

func hsEndedError(id TaskId) error {
	return fmt.Errorf("Task %d invocation has ended", id)
}

func hsPhaseName(cp JsonValue) string {
	if obj, ok := cp.(*jsonx.Object); ok && obj != nil {
		if p, ok := obj.Get("phase"); ok {
			if s, isStr := p.(string); isStr {
				return s
			}
		}
	}
	return ""
}

func hsContainsTaskId(ids []TaskId, target TaskId) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func hsJsonEqual(a, b JsonValue) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	sa, erra := jsonx.Stringify(a)
	sb, errb := jsonx.Stringify(b)
	if erra == nil && errb == nil && sa == sb {
		return true
	}
	return jsonx.DeepEqual(a, b)
}
