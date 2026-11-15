// Ported from packages/durable/src/harness/submissions.ts (pi v1.0.0).

package durable

import (
	"fmt"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type hsubSubmissions struct {
	session    *SessionImpl
	storage    Storage
	now        func() int64
	queueModes func() QueueModes
	resume     func()
	waiters    *hutWaiters[SubmissionId, *SettledSubmissionRecord]

	mu     sync.Mutex
	closed bool
}

func hsubNewSubmissions(
	session *SessionImpl,
	storage Storage,
	now func() int64,
	queueModes func() QueueModes,
	resume func(),
) *hsubSubmissions {
	s := &hsubSubmissions{
		session:    session,
		storage:    storage,
		now:        now,
		queueModes: queueModes,
		resume:     resume,
		waiters:    hutNewWaiters[SubmissionId, *SettledSubmissionRecord](),
	}
	session.SubscribeCommits(func(pub CommitPublication, _ chord.Context) {
		s.observe(pub)
	})
	session.SubscribeClose(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.waiters.RejectAll(hutClosedError())
	})
	return s
}

func (s *hsubSubmissions) Submit(ctx chord.Context, conversationId ConversationId, draft SubmissionDraft) (Submission, error) {
	s.resume()
	var id SubmissionId
	if err := s.session.Commit(ctx, func(tx Tx) error {
		var err error
		id, err = hsubAdmitSubmission(tx, conversationId, draft, s.now(), s.queueModes())
		return err
	}); err != nil {
		return nil, err
	}
	return &hsubSubmissionHandle{id: id, submissions: s}, nil
}

func (s *hsubSubmissions) Get(ctx chord.Context, id SubmissionId) (Submission, error) {
	var rec *SubmissionRecord
	if err := s.session.ReadOnLine(func() error {
		var err error
		rec, err = s.storage.Submission(ctx, id)
		return err
	}); err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	return &hsubSubmissionHandle{id: rec.Id, submissions: s}, nil
}

func (s *hsubSubmissions) Status(ctx chord.Context, id SubmissionId) (*SubmissionRecord, error) {
	var rec *SubmissionRecord
	if err := s.session.ReadOnLine(func() error {
		var err error
		rec, err = s.storage.Submission(ctx, id)
		return err
	}); err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, fmt.Errorf("Submission %d does not exist", id)
	}
	return rec, nil
}

func (s *hsubSubmissions) Wait(ctx chord.Context, id SubmissionId) (*SettledSubmissionRecord, error) {
	s.resume()
	var settled *SettledSubmissionRecord
	var handle *hutWaitHandle[SubmissionId, *SettledSubmissionRecord]
	if err := s.session.ReadOnLine(func() error {
		rec, err := s.storage.Submission(ctx, id)
		if err != nil {
			return err
		}
		if rec == nil {
			return fmt.Errorf("Submission %d does not exist", id)
		}
		if hsubIsSettled(*rec) {
			settled = rec
			return nil
		}
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return hutClosedError()
		}
		handle, err = s.waiters.Register(id, ctx)
		return err
	}); err != nil {
		return nil, err
	}
	if settled != nil {
		return settled, nil
	}
	return handle.Await(ctx)
}

func (s *hsubSubmissions) Abort(ctx chord.Context, id SubmissionId, conversationId *ConversationId) (SubmissionAbortResult, error) {
	var result SubmissionAbortResult
	if err := s.session.CommitWith(ctx, func(tx *Transaction) error {
		rec, err := tx.Submission(id)
		if err != nil {
			return err
		}
		if rec == nil || (conversationId != nil && rec.ConversationId != *conversationId) {
			result = SubmissionAbortNotFound
			return nil
		}
		if rec.Status == SubmissionStatusQueued {
			reason := "aborted"
			if err := tx.SettleSubmission(id, SubmissionSettlement{
				Status: SubmissionStatusUnanswered,
				Reason: &reason,
			}); err != nil {
				return err
			}
			if err := RemoveInboxItem(tx, rec.ConversationId, id); err != nil {
				return err
			}
			result = SubmissionAbortAborted
			return nil
		}
		if rec.Status == SubmissionStatusPlaced {
			result = SubmissionAbortAlreadyPlaced
			return nil
		}
		result = SubmissionAbortSettled
		return nil
	}, nil); err != nil {
		return "", err
	}
	return result, nil
}

func (s *hsubSubmissions) observe(pub CommitPublication) {
	for _, ch := range pub.Changes {
		switch c := ch.(type) {
		case StorageWriteSubmission:
			if hsubIsSettled(c.Value) {
				v := c.Value
				s.waiters.Resolve(c.Value.Id, &v)
			}
		case *StorageWriteSubmission:
			if c != nil && hsubIsSettled(c.Value) {
				v := c.Value
				s.waiters.Resolve(c.Value.Id, &v)
			}
		}
	}
}

type hsubSubmissionHandle struct {
	id          SubmissionId
	submissions *hsubSubmissions
}

func (h *hsubSubmissionHandle) Id() SubmissionId { return h.id }

func (h *hsubSubmissionHandle) Status(ctx chord.Context) (*SubmissionRecord, error) {
	return h.submissions.Status(ctx, h.id)
}

func (h *hsubSubmissionHandle) Wait(ctx chord.Context) (*SettledSubmissionRecord, error) {
	return h.submissions.Wait(ctx, h.id)
}

func (h *hsubSubmissionHandle) Abort(ctx chord.Context) (SubmissionAbortResult, error) {
	res, err := h.submissions.Abort(ctx, h.id, nil)
	if err != nil {
		return "", err
	}
	if res == SubmissionAbortNotFound {
		return "", fmt.Errorf("Submission %d does not exist", h.id)
	}
	return res, nil
}

func hsubIsSettled(rec SubmissionRecord) bool {
	return rec.Status == SubmissionStatusDone || rec.Status == SubmissionStatusUnanswered
}

func hsubAdmitSubmission(
	tx Tx,
	conversationId ConversationId,
	draft SubmissionDraft,
	now int64,
	queueModes QueueModes,
) (SubmissionId, error) {
	if draft.RequestId != "" {
		existing, err := tx.SubmissionByRequest(conversationId, draft.RequestId)
		if err != nil {
			return 0, err
		}
		if existing != nil {
			if existing.Type != draft.Type {
				return 0, fmt.Errorf("Request %s already identifies a submission of type %s", draft.RequestId, existing.Type)
			}
			return existing.Id, nil
		}
	}
	live, err := tx.Doc(LiveDoc, conversationId)
	if err != nil {
		return 0, err
	}
	rawRun, hasRun := live.Value.Get("run")
	busy := hasRun && rawRun != nil
	if busy && draft.Type == SubmissionTypeInput && draft.WhenBusy == SubmissionWhenBusyReject {
		return 0, NewConversationBusy(conversationId)
	}

	var reqIdPtr *string
	if draft.RequestId != "" {
		r := draft.RequestId
		reqIdPtr = &r
	}
	var boundary *Boundary
	if !busy {
		boundary, err = PrepareBoundary(tx, conversationId, queueModes)
		if err != nil {
			return 0, err
		}
	}
	if boundary == nil || len(hiItemsSlice(boundary.Inbox)) > 0 {
		sub, err := tx.CreateSubmission(SubmissionCreate{
			ConversationId: conversationId,
			RequestId:      reqIdPtr,
			Type:           draft.Type,
			Status:         SubmissionStatusQueued,
		})
		if err != nil {
			return 0, err
		}
		var rawPayload any
		if draft.Type == SubmissionTypeWrite {
			if draft.Entry == nil {
				return 0, fmt.Errorf("write submission missing entry")
			}
			rawPayload = *draft.Entry
		} else {
			rawPayload = draft.Content
		}
		encodedVal, err := stCopyStrictJsonValue(rawPayload)
		if err != nil {
			return 0, err
		}
		var inboxObj *jsonx.Object
		if boundary != nil {
			inboxObj = boundary.Inbox
		} else {
			docDraft, err := tx.Doc(InboxDoc, conversationId)
			if err != nil {
				return 0, err
			}
			inboxObj = docDraft.Value
		}
		items := hiItemsSlice(inboxObj)
		if draft.Type == SubmissionTypeWrite {
			items = append(items, jsonx.ObjectOf(
				"id", float64(sub.Id),
				"mode", string(InboxModeWrite),
				"entry", encodedVal,
			))
		} else {
			mode := InboxModeFollowUp
			if draft.WhenBusy == SubmissionWhenBusySteer {
				mode = InboxModeSteer
			}
			items = append(items, jsonx.ObjectOf(
				"id", float64(sub.Id),
				"mode", string(mode),
				"content", encodedVal,
			))
		}
		inboxObj.Set("items", items)
		if boundary == nil {
			return sub.Id, nil
		}
		applied, err := ApplyBoundary(tx, boundary, BoundaryFinal, now)
		if err != nil {
			return 0, err
		}
		if len(applied.Users) > 0 {
			if err := hgStartRun(tx, conversationId, live.Value, applied.Users); err != nil {
				return 0, err
			}
		}
		return sub.Id, nil
	}

	if draft.Type == SubmissionTypeWrite {
		if draft.Entry == nil {
			return 0, fmt.Errorf("write submission missing entry")
		}
		if IsStaleEntry(boundary, *draft.Entry) {
			reason := "stale"
			sub, err := tx.CreateSubmission(SubmissionCreate{
				ConversationId: conversationId,
				RequestId:      reqIdPtr,
				Type:           SubmissionTypeWrite,
				Status:         SubmissionStatusUnanswered,
				Reason:         &reason,
			})
			if err != nil {
				return 0, err
			}
			return sub.Id, nil
		}
		entry, err := tx.AppendEntry(conversationId, *draft.Entry)
		if err != nil {
			return 0, err
		}
		eid := entry.Id
		sub, err := tx.CreateSubmission(SubmissionCreate{
			ConversationId: conversationId,
			RequestId:      reqIdPtr,
			Type:           SubmissionTypeWrite,
			Status:         SubmissionStatusDone,
			Entry:          &eid,
		})
		if err != nil {
			return 0, err
		}
		return sub.Id, nil
	}

	message := &ai.UserMessage{
		Role:      "user",
		Content:   draft.Content,
		Timestamp: now,
	}
	entry, err := tx.AppendTypedEntry(UserEntry, conversationId, EntryDraft{
		Model: ai.MessageList{message},
	})
	if err != nil {
		return 0, err
	}
	eid := entry.Id
	sub, err := tx.CreateSubmission(SubmissionCreate{
		ConversationId: conversationId,
		RequestId:      reqIdPtr,
		Type:           SubmissionTypeInput,
		Status:         SubmissionStatusPlaced,
		Entry:          &eid,
	})
	if err != nil {
		return 0, err
	}
	if err := hgStartRun(tx, conversationId, live.Value, []SubmissionId{sub.Id}); err != nil {
		return 0, err
	}
	return sub.Id, nil
}
