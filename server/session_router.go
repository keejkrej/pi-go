// Ported from packages/server/src/session-router.ts (pi v1.0.0).

package server

import (
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/protocol"
)

type srAggregateError struct {
	Message string
	Errors  []error
}

func (e *srAggregateError) Error() string { return e.Message }

func (e *srAggregateError) Unwrap() []error { return e.Errors }

type srSessionCleanupError struct {
	Message string
	Errors  []error
}

func (e *srSessionCleanupError) Error() string { return e.Message }

func (e *srSessionCleanupError) Unwrap() []error { return e.Errors }

type srTrackedOp struct {
	done chan struct{}
	err  error
}

type srOpeningSession[M SessionMetadata] struct {
	done   chan struct{}
	hosted *srHostedSession[M]
	err    error
}

type srClientAttachment[M SessionMetadata] struct {
	id            string
	client        any
	session       *srHostedSession[M]
	wg            sync.WaitGroup
	acquiringDone chan struct{}
	acquireErr    error
	lease         RoutedSessionAttachment
	releasing     bool
	releaseOnce   sync.Once
	releaseDone   chan struct{}
	releaseErr    error
}

type srHostedSession[M SessionMetadata] struct {
	id          string
	handle      RoutedSessionHandle
	attachments map[*srClientAttachment[M]]struct{}
	stopWatch   chan struct{}
	stopOnce    sync.Once
}

func (h *srHostedSession[M]) stopWatcher() {
	h.stopOnce.Do(func() {
		close(h.stopWatch)
	})
}

type srSessionRouterOptions[M SessionMetadata] struct {
	Host              ServerHost[M]
	ServerId          protocol.ServerId
	IsClosing         func() bool
	PublishAttachment func(client any, attachment *protocol.SessionTarget, callContext chord.Context) error
	ReportError       func(err error)
}

type srSessionRouter[M SessionMetadata] struct {
	mu                  sync.Mutex
	options             srSessionRouterOptions[M]
	hostedSessions      map[string]*srHostedSession[M]
	openingSessions     map[string]*srOpeningSession[M]
	attachmentsByClient map[any]*srClientAttachment[M]
	disconnectedClients map[any]struct{}
	clientTails         map[any]chan struct{}
	activeOps           map[*srTrackedOp]struct{}
	closeStarted        bool
	closeDone           chan struct{}
	closeErr            error
}

func srNewSessionRouter[M SessionMetadata](options srSessionRouterOptions[M]) *srSessionRouter[M] {
	return &srSessionRouter[M]{
		options:             options,
		hostedSessions:      make(map[string]*srHostedSession[M]),
		openingSessions:     make(map[string]*srOpeningSession[M]),
		attachmentsByClient: make(map[any]*srClientAttachment[M]),
		disconnectedClients: make(map[any]struct{}),
		clientTails:         make(map[any]chan struct{}),
		activeOps:           make(map[*srTrackedOp]struct{}),
	}
}

func (r *srSessionRouter[M]) ExecuteServiceCall(
	call chord.ServiceCall,
	target protocol.RpcTarget,
	client any,
	publish chord.ServiceUpdatePublisher,
	callContext chord.Context,
) (chord.JsonValue, bool, error) {
	var attachment *srClientAttachment[M]
	var lease RoutedSessionAttachment

	if err := r.runForClient(client, func() error {
		att, err := r.requireAttachmentLockedCheck(client, target)
		if err != nil {
			return err
		}
		r.mu.Lock()
		if att.releasing || att.lease == nil {
			r.mu.Unlock()
			return NewServerDrainingError()
		}
		att.wg.Add(1)
		lease = att.lease
		attachment = att
		r.mu.Unlock()
		return nil
	}); err != nil {
		return nil, false, err
	}

	defer attachment.wg.Done()
	return lease.InvokeService(call, publish, callContext)
}

func (r *srSessionRouter[M]) AttachClient(client any, sessionId string, callContext chord.Context) error {
	if r.options.IsClosing() {
		return NewServerDrainingError()
	}
	return r.runForClient(client, func() error {
		return r.attachClientNow(client, sessionId, callContext)
	})
}

func (r *srSessionRouter[M]) DetachClient(client any, callContext chord.Context) error {
	return r.runForClient(client, func() error {
		r.mu.Lock()
		att := r.attachmentsByClient[client]
		r.mu.Unlock()
		if att != nil {
			return r.releaseAttachment(att, callContext, true)
		}
		return nil
	})
}

func (r *srSessionRouter[M]) RemoveSession(sessionId string, callContext chord.Context) error {
	if r.options.IsClosing() {
		return NewServerDrainingError()
	}
	r.mu.Lock()
	hosted := r.hostedSessions[sessionId]
	if hosted == nil {
		r.mu.Unlock()
		return nil
	}
	attachments := make([]*srClientAttachment[M], 0, len(hosted.attachments))
	for att := range hosted.attachments {
		attachments = append(attachments, att)
	}
	r.mu.Unlock()

	var errs []error
	if len(attachments) > 0 {
		errCh := make(chan error, len(attachments))
		var wg sync.WaitGroup
		for _, att := range attachments {
			wg.Add(1)
			go func(a *srClientAttachment[M]) {
				defer wg.Done()
				if err := r.releaseAttachment(a, callContext, true); err != nil {
					errCh <- err
				}
			}(att)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			errs = append(errs, err)
		}
	}

	hosted.stopWatcher()
	if err := hosted.handle.Close(callContext); err != nil {
		errs = append(errs, err)
	}

	r.mu.Lock()
	if r.hostedSessions[sessionId] == hosted {
		delete(r.hostedSessions, sessionId)
	}
	r.mu.Unlock()

	if len(errs) == 1 {
		return errs[0]
	}
	if len(errs) > 1 {
		return &srAggregateError{
			Message: fmt.Sprintf("Failed to close Session %s", sessionId),
			Errors:  errs,
		}
	}
	return nil
}

func (r *srSessionRouter[M]) Disconnect(client any, callContext chord.Context) error {
	r.mu.Lock()
	r.disconnectedClients[client] = struct{}{}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.disconnectedClients, client)
		r.mu.Unlock()
	}()

	return r.runForClient(client, func() error {
		r.mu.Lock()
		att := r.attachmentsByClient[client]
		r.mu.Unlock()
		if att != nil {
			return r.releaseAttachment(att, callContext, false)
		}
		return nil
	})
}

func (r *srSessionRouter[M]) Close(callContext chord.Context) error {
	r.mu.Lock()
	if r.closeStarted {
		done := r.closeDone
		r.mu.Unlock()
		<-done
		return r.closeErr
	}
	r.closeStarted = true
	done := make(chan struct{})
	r.closeDone = done
	r.mu.Unlock()

	err := r.closeInternal(callContext)
	r.closeErr = err
	close(done)
	return err
}

func (r *srSessionRouter[M]) closeInternal(callContext chord.Context) error {
	r.mu.Lock()
	ops := make([]*srTrackedOp, 0, len(r.activeOps))
	for op := range r.activeOps {
		ops = append(ops, op)
	}
	openings := make([]*srOpeningSession[M], 0, len(r.openingSessions))
	for _, op := range r.openingSessions {
		openings = append(openings, op)
	}
	r.mu.Unlock()

	var closeErrors []error
	for _, op := range ops {
		<-op.done
		if op.err != nil {
			r.options.ReportError(op.err)
			var cleanupErr *srSessionCleanupError
			if errors.As(op.err, &cleanupErr) {
				closeErrors = append(closeErrors, op.err)
			}
		}
	}
	for _, opening := range openings {
		<-opening.done
		if opening.err != nil {
			r.options.ReportError(opening.err)
			var cleanupErr *srSessionCleanupError
			if errors.As(opening.err, &cleanupErr) {
				closeErrors = append(closeErrors, opening.err)
			}
		}
	}

	r.mu.Lock()
	var attachments []*srClientAttachment[M]
	for _, session := range r.hostedSessions {
		for att := range session.attachments {
			attachments = append(attachments, att)
		}
	}
	r.mu.Unlock()

	if len(attachments) > 0 {
		errCh := make(chan error, len(attachments))
		var wg sync.WaitGroup
		for _, att := range attachments {
			wg.Add(1)
			go func(a *srClientAttachment[M]) {
				defer wg.Done()
				if err := r.releaseAttachment(a, callContext, true); err != nil {
					errCh <- err
				}
			}(att)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			closeErrors = append(closeErrors, err)
		}
	}

	r.mu.Lock()
	hosted := make([]*srHostedSession[M], 0, len(r.hostedSessions))
	for _, session := range r.hostedSessions {
		hosted = append(hosted, session)
	}
	r.mu.Unlock()

	if len(hosted) > 0 {
		type closeRes struct {
			session *srHostedSession[M]
			err     error
		}
		resCh := make(chan closeRes, len(hosted))
		var wg sync.WaitGroup
		for _, session := range hosted {
			wg.Add(1)
			go func(s *srHostedSession[M]) {
				defer wg.Done()
				s.stopWatcher()
				resCh <- closeRes{session: s, err: s.handle.Close(callContext)}
			}(session)
		}
		wg.Wait()
		close(resCh)
		for res := range resCh {
			if res.err == nil {
				r.mu.Lock()
				if r.hostedSessions[res.session.id] == res.session {
					delete(r.hostedSessions, res.session.id)
				}
				r.mu.Unlock()
				continue
			}
			r.options.ReportError(res.err)
			closeErrors = append(closeErrors, res.err)
		}
	}

	r.mu.Lock()
	clear(r.attachmentsByClient)
	clear(r.clientTails)
	r.mu.Unlock()

	if len(closeErrors) > 0 {
		return &srAggregateError{
			Message: "Failed to close routed Sessions",
			Errors:  closeErrors,
		}
	}
	return nil
}

func (r *srSessionRouter[M]) runForClient(client any, operation func() error) error {
	r.mu.Lock()
	prevTail := r.clientTails[client]
	myTurn := make(chan struct{})
	r.clientTails[client] = myTurn
	op := &srTrackedOp{done: make(chan struct{})}
	r.activeOps[op] = struct{}{}
	r.mu.Unlock()

	if prevTail != nil {
		<-prevTail
	}

	err := operation()
	op.err = err
	close(op.done)

	r.mu.Lock()
	if r.clientTails[client] == myTurn {
		delete(r.clientTails, client)
	}
	delete(r.activeOps, op)
	r.mu.Unlock()

	close(myTurn)
	return err
}

func (r *srSessionRouter[M]) attachClientNow(client any, sessionId string, callContext chord.Context) error {
	if r.isClientDisconnectedOrClosing(client) {
		return NewServerDrainingError()
	}

	r.mu.Lock()
	current := r.attachmentsByClient[client]
	alreadySame := current != nil && current.session.id == sessionId
	r.mu.Unlock()
	if alreadySame {
		return nil
	}

	hosted, err := r.acquire(sessionId, callContext)
	if err != nil {
		return err
	}
	if r.isClientDisconnectedOrClosing(client) {
		return NewServerDrainingError()
	}

	if current != nil {
		if err := r.releaseAttachment(current, callContext, false); err != nil {
			return err
		}
	}

	attachment := &srClientAttachment[M]{
		id:            uuid.NewString(),
		client:        client,
		session:       hosted,
		acquiringDone: make(chan struct{}),
		releaseDone:   make(chan struct{}),
	}

	r.mu.Lock()
	hosted.attachments[attachment] = struct{}{}
	r.mu.Unlock()

	lease, attachErr := hosted.handle.AttachClient(callContext)
	r.mu.Lock()
	attachment.lease = lease
	attachment.acquireErr = attachErr
	r.mu.Unlock()
	close(attachment.acquiringDone)

	if attachErr != nil {
		r.mu.Lock()
		delete(hosted.attachments, attachment)
		r.mu.Unlock()
		return attachErr
	}

	if termCh := hosted.handle.Terminated(); termCh != nil {
		select {
		case termErr, ok := <-termCh:
			if !ok {
				termErr = nil
			}
			r.invalidate(hosted, termErr)
		default:
		}
	}

	r.mu.Lock()
	_, attStillInHosted := hosted.attachments[attachment]
	sessionStillHosted := r.hostedSessions[hosted.id] == hosted
	_, clientDisc := r.disconnectedClients[client]
	r.mu.Unlock()

	if !sessionStillHosted || !attStillInHosted || clientDisc || r.options.IsClosing() {
		_ = r.releaseAttachment(attachment, callContext, true)
		return NewServerDrainingError()
	}

	r.mu.Lock()
	r.attachmentsByClient[client] = attachment
	r.mu.Unlock()

	return r.options.PublishAttachment(
		client,
		&protocol.SessionTarget{
			ServerId:     r.options.ServerId,
			SessionId:    sessionId,
			AttachmentId: attachment.id,
		},
		callContext,
	)
}

func (r *srSessionRouter[M]) requireAttachmentLockedCheck(client any, target protocol.RpcTarget) (*srClientAttachment[M], error) {
	if r.isClientDisconnectedOrClosing(client) {
		return nil, NewServerDrainingError()
	}
	sessionTarget, ok := target.(*protocol.SessionTarget)
	if !ok || sessionTarget == nil {
		return nil, NewSessionNotAttachedError()
	}
	r.mu.Lock()
	att := r.attachmentsByClient[client]
	r.mu.Unlock()
	if att == nil || att.session.id != sessionTarget.SessionId || att.id != sessionTarget.AttachmentId {
		return nil, NewSessionNotAttachedError()
	}
	return att, nil
}

func (r *srSessionRouter[M]) releaseAttachment(attachment *srClientAttachment[M], callContext chord.Context, publish bool) error {
	attachment.releaseOnce.Do(func() {
		defer close(attachment.releaseDone)
		r.mu.Lock()
		attachment.releasing = true
		r.mu.Unlock()

		var errs []error
		attachment.wg.Wait()

		<-attachment.acquiringDone
		r.mu.Lock()
		lease := attachment.lease
		r.mu.Unlock()
		if lease != nil {
			if err := lease.Release(callContext); err != nil {
				errs = append(errs, err)
			}
		}

		if err := r.clearAttachment(attachment, callContext, publish); err != nil {
			errs = append(errs, err)
		}

		if len(errs) == 1 {
			attachment.releaseErr = errs[0]
		} else if len(errs) > 1 {
			attachment.releaseErr = &srAggregateError{
				Message: "Failed to release Session attachment",
				Errors:  errs,
			}
		}
	})
	<-attachment.releaseDone
	return attachment.releaseErr
}

func (r *srSessionRouter[M]) clearAttachment(attachment *srClientAttachment[M], callContext chord.Context, publish bool) error {
	r.mu.Lock()
	delete(attachment.session.attachments, attachment)
	shouldPublish := false
	if r.attachmentsByClient[attachment.client] == attachment {
		delete(r.attachmentsByClient, attachment.client)
		shouldPublish = publish
	}
	r.mu.Unlock()

	if shouldPublish {
		return r.options.PublishAttachment(attachment.client, nil, callContext)
	}
	return nil
}

func (r *srSessionRouter[M]) acquire(sessionId string, callContext chord.Context) (*srHostedSession[M], error) {
	r.mu.Lock()
	if existing := r.hostedSessions[sessionId]; existing != nil {
		r.mu.Unlock()
		return existing, nil
	}
	if opening := r.openingSessions[sessionId]; opening != nil {
		r.mu.Unlock()
		<-opening.done
		return opening.hosted, opening.err
	}
	opening := &srOpeningSession[M]{done: make(chan struct{})}
	r.openingSessions[sessionId] = opening
	r.mu.Unlock()

	hosted, err := r.open(sessionId, callContext)

	r.mu.Lock()
	if r.openingSessions[sessionId] == opening {
		delete(r.openingSessions, sessionId)
	}
	opening.hosted = hosted
	opening.err = err
	r.mu.Unlock()
	close(opening.done)

	return hosted, err
}

func (r *srSessionRouter[M]) open(sessionId string, callContext chord.Context) (*srHostedSession[M], error) {
	metadata, err := r.options.Host.ResolveSession(sessionId, callContext)
	if err != nil {
		return nil, err
	}
	handle, err := r.options.Host.OpenSession(metadata, callContext)
	if err != nil {
		return nil, err
	}
	if r.options.IsClosing() {
		if closeErr := handle.Close(callContext); closeErr != nil {
			r.options.ReportError(closeErr)
			return nil, &srSessionCleanupError{
				Message: "Failed to close routed Session acquired while draining",
				Errors:  []error{NewServerDrainingError(), closeErr},
			}
		}
		return nil, NewServerDrainingError()
	}

	hosted := &srHostedSession[M]{
		id:          metadata.SessionId(),
		handle:      handle,
		attachments: make(map[*srClientAttachment[M]]struct{}),
		stopWatch:   make(chan struct{}),
	}
	r.mu.Lock()
	r.hostedSessions[hosted.id] = hosted
	r.mu.Unlock()

	if termCh := handle.Terminated(); termCh != nil {
		go func(h *srHostedSession[M], ch <-chan error) {
			select {
			case termErr, ok := <-ch:
				if !ok {
					termErr = nil
				}
				r.invalidate(h, termErr)
			case <-h.stopWatch:
			}
		}(hosted, termCh)
	}

	return hosted, nil
}

func (r *srSessionRouter[M]) invalidate(hosted *srHostedSession[M], err error) {
	r.mu.Lock()
	if r.hostedSessions[hosted.id] != hosted {
		r.mu.Unlock()
		return
	}
	delete(r.hostedSessions, hosted.id)
	attachments := make([]*srClientAttachment[M], 0, len(hosted.attachments))
	for att := range hosted.attachments {
		attachments = append(attachments, att)
	}
	r.mu.Unlock()

	hosted.stopWatcher()

	for _, att := range attachments {
		go func(a *srClientAttachment[M]) {
			if relErr := r.releaseAttachment(a, chord.BackgroundContext, true); relErr != nil {
				r.options.ReportError(relErr)
			}
		}(att)
	}
	if err != nil {
		r.options.ReportError(err)
	}
}

func (r *srSessionRouter[M]) isClientDisconnectedOrClosing(client any) bool {
	if r.options.IsClosing() {
		return true
	}
	r.mu.Lock()
	_, disc := r.disconnectedClients[client]
	r.mu.Unlock()
	return disc
}
