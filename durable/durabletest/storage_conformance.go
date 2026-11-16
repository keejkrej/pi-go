// Ported from packages/durable/src/testing/storage-conformance.ts (pi v1.0.0).

package durabletest

import (
	"errors"
	"fmt"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

var dtscCtx = chord.BackgroundContext

func dtscCreateRoot(storage durable.Storage) (durable.ConversationId, error) {
	if _, err := storage.Commit(dtscCtx, []durable.StorageWrite{
		durable.StorageWriteConversation{
			Type:  durable.StorageWriteTypeConversation,
			Value: durable.ConversationRecord{Id: durable.RootConversationId},
		},
	}); err != nil {
		return 0, err
	}
	return durable.RootConversationId, nil
}

func dtscPendingTask(id durable.TaskId, conversationId durable.ConversationId, phase ...string) durable.TaskRecord {
	p := "ready"
	if len(phase) > 0 && phase[0] != "" {
		p = phase[0]
	}
	return durable.TaskRecord{
		Id:             id,
		ConversationId: conversationId,
		Kind:           "test.task",
		Version:        1,
		Input:          jsonx.ObjectOf("value", float64(id)),
		State: durable.TaskState{
			Status:     durable.TaskStatePending,
			Checkpoint: jsonx.ObjectOf("phase", p),
		},
		Background:     false,
		AbortRequested: false,
	}
}

func dtscEntry(id durable.EntryId, conversationId durable.ConversationId, kind string, data durable.JsonValue, head *durable.EntryId) durable.EntryRecord {
	if kind == "" {
		kind = "message"
	}
	return durable.EntryRecord{
		Id:             id,
		ConversationId: conversationId,
		Kind:           kind,
		Data:           data,
		Head:           head,
	}
}

func dtscMintEntryId(storage durable.Storage) (durable.EntryId, error) {
	n, err := storage.MintId()
	return durable.IdFromNumber[durable.EntryId](n), err
}

func dtscMintConversationId(storage durable.Storage) (durable.ConversationId, error) {
	n, err := storage.MintId()
	return durable.IdFromNumber[durable.ConversationId](n), err
}

func dtscMintTaskId(storage durable.Storage) (durable.TaskId, error) {
	n, err := storage.MintId()
	return durable.IdFromNumber[durable.TaskId](n), err
}

func dtscMintSubmissionId(storage durable.Storage) (durable.SubmissionId, error) {
	n, err := storage.MintId()
	return durable.IdFromNumber[durable.SubmissionId](n), err
}

func dtscMintDocumentId(storage durable.Storage) (durable.DocumentId, error) {
	n, err := storage.MintId()
	return durable.IdFromNumber[durable.DocumentId](n), err
}

func dtscStrPtr(s string) *string { return &s }

func dtscCase(options StorageConformanceOptions, name string, fn func(storage durable.Storage) error) StorageConformanceCase {
	return StorageConformanceCase{
		Name: name,
		Run: func() error {
			return options.WithStorage(fn)
		},
	}
}

func dtscEntryIds(entries []durable.EntryRecord) []durable.EntryId {
	out := make([]durable.EntryId, len(entries))
	for i, e := range entries {
		out[i] = e.Id
	}
	return out
}

func dtscConversationIds(convs []durable.ConversationRecord) []durable.ConversationId {
	out := make([]durable.ConversationId, len(convs))
	for i, c := range convs {
		out[i] = c.Id
	}
	return out
}

func dtscTaskIds(tasks []durable.TaskRecord) []durable.TaskId {
	out := make([]durable.TaskId, len(tasks))
	for i, t := range tasks {
		out[i] = t.Id
	}
	return out
}

func dtscDocumentIds(docs []durable.DocumentRecord) []durable.DocumentId {
	out := make([]durable.DocumentId, len(docs))
	for i, d := range docs {
		out[i] = d.Id
	}
	return out
}

// CreateStorageConformance creates runner-independent conformance test cases for a Storage backend.
func CreateStorageConformance(options StorageConformanceOptions) []StorageConformanceCase {
	assert := options.Assertions

	return []StorageConformanceCase{
		dtscCase(options, "reserves ID 1 for the immutable root conversation", func(storage durable.Storage) error {
			minted, err := dtscMintConversationId(storage)
			assert.Ok(err == nil)
			assert.StrictEqual(minted, durable.ConversationId(2))

			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			assert.StrictEqual(rootId, durable.RootConversationId)

			conv, err := storage.Conversation(dtscCtx, durable.RootConversationId)
			assert.Ok(err == nil)
			assert.DeepEqual(conv, &durable.ConversationRecord{Id: durable.RootConversationId})

			assert.Rejects(func() error {
				_, err := storage.Commit(dtscCtx, []durable.StorageWrite{
					durable.StorageWriteConversation{
						Type:  durable.StorageWriteTypeConversation,
						Value: durable.ConversationRecord{Id: durable.RootConversationId},
					},
				})
				return err
			}, fmt.Sprintf("ID %d already belongs to conversation", durable.RootConversationId))
			return nil
		}),

		dtscCase(options, "commits mixed table writes atomically and rolls all of them back on failure", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			entryId, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			taskId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)
			submissionId, err := dtscMintSubmissionId(storage)
			assert.Ok(err == nil)

			task := dtscPendingTask(taskId, rootId)
			reqId := "request-1"
			input := durable.SubmissionRecord{
				Id:             submissionId,
				ConversationId: rootId,
				RequestId:      &reqId,
				Type:           durable.SubmissionTypeInput,
				Status:         durable.SubmissionStatusPlaced,
				Entry:          &entryId,
			}
			initialSeq, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{
					Type:  durable.StorageWriteTypeEntry,
					Value: dtscEntry(entryId, rootId, "user", jsonx.ObjectOf("text", "hello"), nil),
				},
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: task},
				durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: input},
			})
			assert.Ok(err == nil)

			gotEntry, err := storage.Entry(dtscCtx, entryId)
			assert.Ok(err == nil)
			assert.DeepEqual(gotEntry, &durable.StoredEntry{
				Entry:     dtscEntry(entryId, rootId, "user", jsonx.ObjectOf("text", "hello"), nil),
				CommitSeq: initialSeq,
			})
			gotTask, err := storage.Task(dtscCtx, taskId)
			assert.Ok(err == nil)
			assert.DeepEqual(gotTask, &task)
			gotSub, err := storage.Submission(dtscCtx, submissionId)
			assert.Ok(err == nil)
			assert.DeepEqual(gotSub, &input)

			transientEntryId, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			runningTask := task
			runningTask.State = durable.TaskState{
				Status:     durable.TaskStateRunning,
				Checkpoint: jsonx.ObjectOf("phase", "effect"),
			}
			doneInput := input
			doneInput.Status = durable.SubmissionStatusDone
			doneInput.Answer = &transientEntryId

			assert.Rejects(func() error {
				_, err := storage.Commit(dtscCtx, []durable.StorageWrite{
					durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: runningTask},
					durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: doneInput},
					durable.StorageWriteEntry{
						Type:  durable.StorageWriteTypeEntry,
						Value: dtscEntry(transientEntryId, rootId, "assistant", nil, nil),
					},
					durable.StorageWriteConversation{
						Type:  durable.StorageWriteTypeConversation,
						Value: durable.ConversationRecord{Id: rootId},
					},
				})
				return err
			}, fmt.Sprintf("ID %d already belongs to conversation", rootId))

			gotTaskAfter, err := storage.Task(dtscCtx, taskId)
			assert.Ok(err == nil)
			assert.DeepEqual(gotTaskAfter, &task)
			gotSubAfter, err := storage.Submission(dtscCtx, submissionId)
			assert.Ok(err == nil)
			assert.DeepEqual(gotSubAfter, &input)
			gotTransient, err := storage.Entry(dtscCtx, transientEntryId)
			assert.Ok(err == nil)
			assert.Ok(gotTransient == nil, "transient entry should be nil after rollback")

			afterId, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			afterRollbackSeq, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{
					Type:  durable.StorageWriteTypeEntry,
					Value: dtscEntry(afterId, rootId, "after-rollback", nil, nil),
				},
			})
			assert.Ok(err == nil)
			assert.GreaterThan(int64(afterRollbackSeq), int64(initialSeq))
			return nil
		}),

		dtscCase(options, "detaches retained writes and every returned record", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			entryId, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			taskId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)
			submissionId, err := dtscMintSubmissionId(storage)
			assert.Ok(err == nil)

			nestedArr := []any{float64(1), float64(2)}
			entryData := jsonx.ObjectOf("nested", nestedArr)
			nestedCheckpoint := jsonx.ObjectOf("count", float64(1))
			checkpoint := jsonx.ObjectOf("phase", "ready", "nested", nestedCheckpoint)
			codesArr := []any{"initial"}
			detail := jsonx.ObjectOf("codes", codesArr)

			storedEntry := dtscEntry(entryId, rootId, "note", entryData, nil)
			storedTask := dtscPendingTask(taskId, rootId)
			storedTask.State = durable.TaskState{Status: durable.TaskStatePending, Checkpoint: checkpoint}
			reason := "failed"
			storedInput := durable.SubmissionRecord{
				Id:             submissionId,
				ConversationId: rootId,
				Type:           durable.SubmissionTypeInput,
				Status:         durable.SubmissionStatusUnanswered,
				Reason:         &reason,
				Detail:         detail,
			}
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: storedEntry},
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: storedTask},
				durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: storedInput},
			})
			assert.Ok(err == nil)

			entryData.Set("nested", append(nestedArr, float64(3)))
			nestedCheckpoint.Set("count", float64(2))
			detail.Set("codes", append(codesArr, "mutated"))

			readStoredEntry, err := storage.Entry(dtscCtx, entryId)
			assert.Ok(err == nil)
			assert.DeepEqual(readStoredEntry.Entry.Data, jsonx.ObjectOf("nested", []any{float64(1), float64(2)}))
			readStoredTask, err := storage.Task(dtscCtx, taskId)
			assert.Ok(err == nil)
			assert.DeepEqual(readStoredTask.State, durable.TaskState{
				Status:     durable.TaskStatePending,
				Checkpoint: jsonx.ObjectOf("phase", "ready", "nested", jsonx.ObjectOf("count", float64(1))),
			})
			readStoredInput, err := storage.Submission(dtscCtx, submissionId)
			assert.Ok(err == nil)
			assert.DeepEqual(readStoredInput.Detail, jsonx.ObjectOf("codes", []any{"initial"}))

			readDataObj := readStoredEntry.Entry.Data.(*jsonx.Object)
			readArr, _ := readDataObj.GetArray("nested")
			readDataObj.Set("nested", append(readArr, float64(9)))

			readCp := readStoredTask.State.Checkpoint.(*jsonx.Object)
			readCpNested, _ := readCp.GetObject("nested")
			readCpNested.Set("count", float64(9))

			readDetailObj := readStoredInput.Detail.(*jsonx.Object)
			readCodes, _ := readDetailObj.GetArray("codes")
			readDetailObj.Set("codes", append(readCodes, "read mutation"))

			reReadEntry, err := storage.Entry(dtscCtx, entryId)
			assert.Ok(err == nil)
			assert.DeepEqual(reReadEntry.Entry.Data, jsonx.ObjectOf("nested", []any{float64(1), float64(2)}))
			reReadTask, err := storage.Task(dtscCtx, taskId)
			assert.Ok(err == nil)
			assert.DeepEqual(reReadTask.State, durable.TaskState{
				Status:     durable.TaskStatePending,
				Checkpoint: jsonx.ObjectOf("phase", "ready", "nested", jsonx.ObjectOf("count", float64(1))),
			})
			reReadInput, err := storage.Submission(dtscCtx, submissionId)
			assert.Ok(err == nil)
			assert.DeepEqual(reReadInput.Detail, jsonx.ObjectOf("codes", []any{"initial"}))
			return nil
		}),

		dtscCase(options, "detaches prototype-like JSON keys without changing object prototypes", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			entryId, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)

			parsed, err := jsonx.ParseString(`{"__proto__":{"polluted":false},"constructor":{"label":"stored"},"toString":"value"}`)
			assert.Ok(err == nil)
			data := parsed.(*jsonx.Object)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{
					Type:  durable.StorageWriteTypeEntry,
					Value: dtscEntry(entryId, rootId, "note", data, nil),
				},
			})
			assert.Ok(err == nil)

			protoObj, _ := data.GetObject("__proto__")
			protoObj.Set("polluted", true)
			ctorObj, _ := data.GetObject("constructor")
			ctorObj.Set("label", "mutated")

			firstEntry, err := storage.Entry(dtscCtx, entryId)
			assert.Ok(err == nil)
			firstRead := firstEntry.Entry.Data.(*jsonx.Object)
			assert.StrictEqual(firstRead.Has("__proto__"), true)
			firstProto, _ := firstRead.GetObject("__proto__")
			assert.DeepEqual(firstProto, jsonx.ObjectOf("polluted", false))
			firstCtor, _ := firstRead.GetObject("constructor")
			assert.DeepEqual(firstCtor, jsonx.ObjectOf("label", "stored"))
			firstToStr, _ := firstRead.GetString("toString")
			assert.StrictEqual(firstToStr, "value")

			firstProto.Set("polluted", true)
			secondEntry, err := storage.Entry(dtscCtx, entryId)
			assert.Ok(err == nil)
			secondRead := secondEntry.Entry.Data.(*jsonx.Object)
			secondProto, _ := secondRead.GetObject("__proto__")
			assert.DeepEqual(secondProto, jsonx.ObjectOf("polluted", false))
			secondCtor, _ := secondRead.GetObject("constructor")
			assert.DeepEqual(secondCtor, jsonx.ObjectOf("label", "stored"))
			secondToStr, _ := secondRead.GetString("toString")
			assert.StrictEqual(secondToStr, "value")
			return nil
		}),

		dtscCase(options, "indexes entries committed out of ID order", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			h10 := durable.IdFromNumber[durable.EntryId](10)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(durable.IdFromNumber[durable.EntryId](30), rootId, "", nil, nil)},
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(durable.IdFromNumber[durable.EntryId](10), rootId, "", nil, nil)},
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(durable.IdFromNumber[durable.EntryId](20), rootId, "marker", nil, &h10)},
			})
			assert.Ok(err == nil)

			page, err := storage.ScanEntries(dtscCtx, durable.EntryQuery{ConversationId: rootId}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscEntryIds(page.Items), []durable.EntryId{30, 20, 10})

			marker, err := storage.FindLatestHeadMarker(dtscCtx, rootId, nil)
			assert.Ok(err == nil)
			assert.StrictEqual(marker.Id, durable.EntryId(20))
			return nil
		}),

		dtscCase(options, "continues an entry cursor below its last item after a newer commit", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			oldestId, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			middleId, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			newestId, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(oldestId, rootId, "", nil, nil)},
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(middleId, rootId, "", nil, nil)},
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(newestId, rootId, "", nil, nil)},
			})
			assert.Ok(err == nil)

			first, err := storage.ScanEntries(dtscCtx, durable.EntryQuery{ConversationId: rootId}, 2, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscEntryIds(first.Items), []durable.EntryId{newestId, middleId})

			appendedId, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(appendedId, rootId, "", nil, nil)},
			})
			assert.Ok(err == nil)

			second, err := storage.ScanEntries(dtscCtx, durable.EntryQuery{ConversationId: rootId}, 2, first.Next)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscEntryIds(second.Items), []durable.EntryId{oldestId})
			assert.Ok(second.Next == nil, "expected second.Next to be nil")
			return nil
		}),

		dtscCase(options, "paginates conversations by opaque cursor in ascending ID order", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			secondId, err := dtscMintConversationId(storage)
			assert.Ok(err == nil)
			thirdId, err := dtscMintConversationId(storage)
			assert.Ok(err == nil)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteConversation{Type: durable.StorageWriteTypeConversation, Value: durable.ConversationRecord{Id: thirdId}},
				durable.StorageWriteConversation{Type: durable.StorageWriteTypeConversation, Value: durable.ConversationRecord{Id: secondId}},
			})
			assert.Ok(err == nil)

			first, err := storage.ScanConversations(dtscCtx, durable.ConversationQuery{}, 2, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscConversationIds(first.Items), []durable.ConversationId{rootId, secondId})
			assert.Ok(first.Next != nil, "expected first.Next to be non-nil")

			cursorJson, err := jsonx.Stringify(first.Next)
			assert.Ok(err == nil)
			parsedCursor, err := jsonx.ParseString(cursorJson)
			assert.Ok(err == nil)
			roundTrippedCursor := parsedCursor.(durable.Cursor)

			second, err := storage.ScanConversations(dtscCtx, durable.ConversationQuery{}, 2, roundTrippedCursor)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscConversationIds(second.Items), []durable.ConversationId{thirdId})
			assert.Ok(second.Next == nil, "expected second.Next to be nil")
			return nil
		}),

		dtscCase(options, "filters and pages conversations by durable owner edges", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			otherOwnerId, err := dtscMintConversationId(storage)
			assert.Ok(err == nil)
			firstTaskId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)
			secondTaskId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)
			firstId, err := dtscMintConversationId(storage)
			assert.Ok(err == nil)
			secondId, err := dtscMintConversationId(storage)
			assert.Ok(err == nil)
			thirdId, err := dtscMintConversationId(storage)
			assert.Ok(err == nil)

			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteConversation{Type: durable.StorageWriteTypeConversation, Value: durable.ConversationRecord{Id: otherOwnerId}},
				durable.StorageWriteConversation{Type: durable.StorageWriteTypeConversation, Value: durable.ConversationRecord{Id: firstId, Owner: &durable.ConversationOwner{ConversationId: rootId, TaskId: firstTaskId}}},
				durable.StorageWriteConversation{Type: durable.StorageWriteTypeConversation, Value: durable.ConversationRecord{Id: secondId, Owner: &durable.ConversationOwner{ConversationId: rootId, TaskId: secondTaskId}}},
				durable.StorageWriteConversation{Type: durable.StorageWriteTypeConversation, Value: durable.ConversationRecord{Id: thirdId, Owner: &durable.ConversationOwner{ConversationId: otherOwnerId, TaskId: firstTaskId}}},
			})
			assert.Ok(err == nil)

			first, err := storage.ScanConversations(dtscCtx, durable.ConversationQuery{OwnerConversationId: &rootId}, 1, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscConversationIds(first.Items), []durable.ConversationId{firstId})
			assert.Ok(first.Next != nil, "expected first.Next to be non-nil")

			second, err := storage.ScanConversations(dtscCtx, durable.ConversationQuery{OwnerConversationId: &rootId}, 1, first.Next)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscConversationIds(second.Items), []durable.ConversationId{secondId})
			assert.Ok(second.Next == nil, "expected second.Next to be nil")

			byTask, err := storage.ScanConversations(dtscCtx, durable.ConversationQuery{OwnerTaskId: &firstTaskId}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscConversationIds(byTask.Items), []durable.ConversationId{firstId, thirdId})

			byBoth, err := storage.ScanConversations(dtscCtx, durable.ConversationQuery{OwnerConversationId: &rootId, OwnerTaskId: &firstTaskId}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscConversationIds(byBoth.Items), []durable.ConversationId{firstId})
			return nil
		}),

		dtscCase(options, "scans deep fork history newest-first through every ancestor cap", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			rootFirst, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			rootForkPoint, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			rootExcludedSameCommit, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)

			rootEntriesSeq, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(rootFirst, rootId, "", nil, nil)},
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(rootForkPoint, rootId, "marker", nil, &rootFirst)},
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(rootExcludedSameCommit, rootId, "", nil, nil)},
			})
			assert.Ok(err == nil)

			childId, err := dtscMintConversationId(storage)
			assert.Ok(err == nil)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteConversation{
					Type:  durable.StorageWriteTypeConversation,
					Value: durable.ConversationRecord{Id: childId, Parent: &durable.ConversationParent{ConversationId: rootId, At: rootForkPoint}},
				},
			})
			assert.Ok(err == nil)

			childForkPoint, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			childExcluded, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(childForkPoint, childId, "note", nil, nil)},
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(childExcluded, childId, "", nil, nil)},
			})
			assert.Ok(err == nil)

			rootExcludedLater, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(rootExcludedLater, rootId, "", nil, nil)},
			})
			assert.Ok(err == nil)

			grandchildId, err := dtscMintConversationId(storage)
			assert.Ok(err == nil)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteConversation{
					Type:  durable.StorageWriteTypeConversation,
					Value: durable.ConversationRecord{Id: grandchildId, Parent: &durable.ConversationParent{ConversationId: childId, At: childForkPoint}},
				},
			})
			assert.Ok(err == nil)

			grandchildHead, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			grandchildTail, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			grandchildEntriesSeq, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(grandchildHead, grandchildId, "marker", nil, &grandchildHead)},
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(grandchildTail, grandchildId, "", nil, nil)},
			})
			assert.Ok(err == nil)

			childExcludedLater, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(childExcludedLater, childId, "", nil, nil)},
			})
			assert.Ok(err == nil)

			first, err := storage.ScanEntries(dtscCtx, durable.EntryQuery{ConversationId: grandchildId}, 2, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscEntryIds(first.Items), []durable.EntryId{grandchildTail, grandchildHead})

			second, err := storage.ScanEntries(dtscCtx, durable.EntryQuery{ConversationId: grandchildId}, 2, first.Next)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscEntryIds(second.Items), []durable.EntryId{childForkPoint, rootForkPoint})

			third, err := storage.ScanEntries(dtscCtx, durable.EntryQuery{ConversationId: grandchildId}, 2, second.Next)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscEntryIds(third.Items), []durable.EntryId{rootFirst})
			assert.Ok(third.Next == nil, "expected third.Next to be nil")

			currentMarker, err := storage.FindLatestHeadMarker(dtscCtx, grandchildId, nil)
			assert.Ok(err == nil)
			assert.StrictEqual(currentMarker.Id, grandchildHead)
			assert.StrictEqual(*currentMarker.Head, grandchildHead)

			historicalMarker, err := storage.FindLatestHeadMarker(dtscCtx, grandchildId, &childForkPoint)
			assert.Ok(err == nil)
			assert.StrictEqual(historicalMarker.Id, rootForkPoint)
			assert.StrictEqual(*historicalMarker.Head, rootFirst)

			noMarker, err := storage.FindLatestHeadMarker(dtscCtx, grandchildId, &rootFirst)
			assert.Ok(err == nil)
			assert.Ok(noMarker == nil, "expected noMarker to be nil")

			activeFirst, err := storage.ScanEntries(dtscCtx, durable.EntryQuery{ConversationId: grandchildId, MinEntryId: currentMarker.Head}, 1, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscEntryIds(activeFirst.Items), []durable.EntryId{grandchildTail})
			assert.Ok(activeFirst.Next != nil, "expected activeFirst.Next to be non-nil")

			activeSecond, err := storage.ScanEntries(dtscCtx, durable.EntryQuery{ConversationId: grandchildId, MinEntryId: currentMarker.Head}, 1, activeFirst.Next)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscEntryIds(activeSecond.Items), []durable.EntryId{grandchildHead})
			assert.Ok(activeSecond.Next == nil, "expected activeSecond.Next to be nil")

			ranged, err := storage.ScanEntries(dtscCtx, durable.EntryQuery{
				ConversationId: grandchildId,
				MinEntryId:     historicalMarker.Head,
				MaxEntryId:     &childForkPoint,
			}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscEntryIds(ranged.Items), []durable.EntryId{childForkPoint, rootForkPoint, rootFirst})

			eRootFirst, err := storage.Entry(dtscCtx, rootFirst)
			assert.Ok(err == nil)
			assert.DeepEqual(eRootFirst, &durable.StoredEntry{
				Entry:     dtscEntry(rootFirst, rootId, "", nil, nil),
				CommitSeq: rootEntriesSeq,
			})
			eRootForkPoint, err := storage.Entry(dtscCtx, rootForkPoint)
			assert.Ok(err == nil)
			assert.StrictEqual(eRootForkPoint.CommitSeq, rootEntriesSeq)
			eGrandchildHead, err := storage.Entry(dtscCtx, grandchildHead)
			assert.Ok(err == nil)
			assert.StrictEqual(eGrandchildHead.CommitSeq, grandchildEntriesSeq)
			eGrandchildTail, err := storage.Entry(dtscCtx, grandchildTail)
			assert.Ok(err == nil)
			assert.StrictEqual(eGrandchildTail.CommitSeq, grandchildEntriesSeq)
			missingEntry, err := storage.Entry(dtscCtx, durable.IdFromNumber[durable.EntryId](999_999))
			assert.Ok(err == nil)
			assert.Ok(missingEntry == nil)

			inConvRootFirst, err := storage.EntryInConversation(dtscCtx, grandchildId, rootFirst)
			assert.Ok(err == nil)
			assert.DeepEqual(inConvRootFirst, &durable.StoredEntry{
				Entry:     dtscEntry(rootFirst, rootId, "", nil, nil),
				CommitSeq: rootEntriesSeq,
			})
			inConvChildFork, err := storage.EntryInConversation(dtscCtx, grandchildId, childForkPoint)
			assert.Ok(err == nil)
			assert.StrictEqual(inConvChildFork.Entry.ConversationId, childId)
			inConvTail, err := storage.EntryInConversation(dtscCtx, grandchildId, grandchildTail)
			assert.Ok(err == nil)
			assert.StrictEqual(inConvTail.CommitSeq, grandchildEntriesSeq)

			for _, hidden := range []durable.EntryId{rootExcludedSameCommit, rootExcludedLater, childExcluded, childExcludedLater} {
				v, err := storage.EntryInConversation(dtscCtx, grandchildId, hidden)
				assert.Ok(err == nil)
				assert.Ok(v == nil)
			}
			v, err := storage.EntryInConversation(dtscCtx, rootId, grandchildHead)
			assert.Ok(err == nil)
			assert.Ok(v == nil)
			v, err = storage.EntryInConversation(dtscCtx, grandchildId, durable.IdFromNumber[durable.EntryId](999_999))
			assert.Ok(err == nil)
			assert.Ok(v == nil)

			assert.Rejects(func() error {
				_, err := storage.EntryInConversation(dtscCtx, durable.IdFromNumber[durable.ConversationId](999_999), rootFirst)
				return err
			}, "Unknown conversation")
			assert.Rejects(func() error {
				_, err := storage.ScanEntries(dtscCtx, durable.EntryQuery{ConversationId: durable.IdFromNumber[durable.ConversationId](999_999)}, 10, nil)
				return err
			}, "Unknown conversation")
			return nil
		}),

		dtscCase(options, "replaces complete task records and pages filtered task scans", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			firstId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)
			secondId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)
			thirdId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)

			first := dtscPendingTask(firstId, rootId)
			first.Memos = jsonx.ObjectOf("winner", "first")
			second := dtscPendingTask(secondId, rootId)
			second.Background = true
			third := dtscPendingTask(thirdId, rootId)
			third.AbortRequested = true

			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: first},
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: second},
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: third},
			})
			assert.Ok(err == nil)

			running := first
			running.State = durable.TaskState{
				Status:     durable.TaskStateRunning,
				Checkpoint: jsonx.ObjectOf("phase", "effect", "attempt", float64(1)),
			}
			running.AbortRequested = true
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: running},
			})
			assert.Ok(err == nil)
			gotRunning, err := storage.Task(dtscCtx, firstId)
			assert.Ok(err == nil)
			assert.DeepEqual(gotRunning, &running)

			terminal := durable.TaskRecord{
				Id:             firstId,
				ConversationId: rootId,
				Kind:           first.Kind,
				Version:        first.Version,
				Input:          first.Input,
				State: durable.TaskState{
					Status: durable.TaskStateTerminal,
					Outcome: &durable.TaskOutcome{
						Status: durable.TaskOutcomeCompleted,
						Result: jsonx.Some[durable.JsonValue](jsonx.ObjectOf("entryId", float64(99))),
					},
				},
				Background:     false,
				AbortRequested: true,
			}
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: terminal},
			})
			assert.Ok(err == nil)
			gotTerminal, err := storage.Task(dtscCtx, firstId)
			assert.Ok(err == nil)
			assert.DeepEqual(gotTerminal, &terminal)

			pendingPage, err := storage.ScanTasks(dtscCtx, durable.TaskQuery{Status: durable.TaskStatePending}, 1, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscTaskIds(pendingPage.Items), []durable.TaskId{secondId})
			assert.Ok(pendingPage.Next != nil)

			pendingPage2, err := storage.ScanTasks(dtscCtx, durable.TaskQuery{Status: durable.TaskStatePending}, 1, pendingPage.Next)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscTaskIds(pendingPage2.Items), []durable.TaskId{thirdId})

			abortTrue := true
			termAbort, err := storage.ScanTasks(dtscCtx, durable.TaskQuery{Status: durable.TaskStateTerminal, AbortRequested: &abortTrue}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(termAbort.Items, []durable.TaskRecord{terminal})

			bgTrue := true
			bgTasks, err := storage.ScanTasks(dtscCtx, durable.TaskQuery{Background: &bgTrue}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscTaskIds(bgTasks.Items), []durable.TaskId{secondId})
			return nil
		}),

		dtscCase(options, "stores owners and scans waiting and completing tasks by status", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			ownerId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)
			waitingId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)
			completingId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)

			owner := dtscPendingTask(ownerId, rootId)
			waiting := dtscPendingTask(waitingId, rootId)
			waiting.Owner = &ownerId
			waiting.State = durable.TaskState{
				Status:     durable.TaskStateWaiting,
				Checkpoint: jsonx.ObjectOf("phase", "next"),
				On:         []durable.TaskId{ownerId},
				Policy:     durable.JoinPolicyAllSettled,
			}
			waiting.Memos = jsonx.ObjectOf("kept", true)

			completingBase := dtscPendingTask(completingId, rootId)
			completing := durable.TaskRecord{
				Id:             completingBase.Id,
				ConversationId: completingBase.ConversationId,
				Kind:           completingBase.Kind,
				Version:        completingBase.Version,
				Input:          completingBase.Input,
				Background:     completingBase.Background,
				AbortRequested: completingBase.AbortRequested,
				Owner:          &ownerId,
				State: durable.TaskState{
					Status: durable.TaskStateCompleting,
					Outcome: &durable.TaskOutcome{
						Status: durable.TaskOutcomeFailed,
						Error:  &durable.TaskOutcomeError{Message: "held"},
					},
				},
			}

			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: owner},
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: waiting},
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: completing},
			})
			assert.Ok(err == nil)

			gotWaiting, err := storage.Task(dtscCtx, waitingId)
			assert.Ok(err == nil)
			assert.DeepEqual(gotWaiting, &waiting)
			gotCompleting, err := storage.Task(dtscCtx, completingId)
			assert.Ok(err == nil)
			assert.DeepEqual(gotCompleting, &completing)

			scanStatus := func(status durable.TaskStateStatus) []durable.TaskRecord {
				page, err := storage.ScanTasks(dtscCtx, durable.TaskQuery{Status: status}, 10, nil)
				assert.Ok(err == nil)
				return page.Items
			}
			assert.DeepEqual(scanStatus(durable.TaskStateWaiting), []durable.TaskRecord{waiting})
			assert.DeepEqual(scanStatus(durable.TaskStateCompleting), []durable.TaskRecord{completing})
			assert.DeepEqual(dtscTaskIds(scanStatus(durable.TaskStatePending)), []durable.TaskId{ownerId})

			terminal := completing
			terminal.State = durable.TaskState{
				Status:  durable.TaskStateTerminal,
				Outcome: completing.State.Outcome,
			}
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: terminal},
			})
			assert.Ok(err == nil)
			assert.DeepEqual(scanStatus(durable.TaskStateCompleting), []durable.TaskRecord{})
			assert.DeepEqual(scanStatus(durable.TaskStateTerminal), []durable.TaskRecord{terminal})
			return nil
		}),

		dtscCase(options, "indexes request IDs per conversation and replaces complete submission records", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			secondConvId, err := dtscMintConversationId(storage)
			assert.Ok(err == nil)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteConversation{Type: durable.StorageWriteTypeConversation, Value: durable.ConversationRecord{Id: secondConvId}},
			})
			assert.Ok(err == nil)

			firstId, err := dtscMintSubmissionId(storage)
			assert.Ok(err == nil)
			secondId, err := dtscMintSubmissionId(storage)
			assert.Ok(err == nil)
			otherConvId, err := dtscMintSubmissionId(storage)
			assert.Ok(err == nil)

			first := durable.SubmissionRecord{Id: firstId, ConversationId: rootId, RequestId: dtscStrPtr("same"), Type: durable.SubmissionTypeInput, Status: durable.SubmissionStatusQueued}
			second := durable.SubmissionRecord{Id: secondId, ConversationId: rootId, RequestId: dtscStrPtr("other"), Type: durable.SubmissionTypeInput, Status: durable.SubmissionStatusQueued}
			otherConv := durable.SubmissionRecord{Id: otherConvId, ConversationId: secondConvId, RequestId: dtscStrPtr("same"), Type: durable.SubmissionTypeInput, Status: durable.SubmissionStatusQueued}

			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: first},
				durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: second},
				durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: otherConv},
			})
			assert.Ok(err == nil)

			byReq1, err := storage.SubmissionByRequest(dtscCtx, rootId, "same")
			assert.Ok(err == nil)
			assert.DeepEqual(byReq1, &first)
			byReq2, err := storage.SubmissionByRequest(dtscCtx, secondConvId, "same")
			assert.Ok(err == nil)
			assert.DeepEqual(byReq2, &otherConv)

			placedEntryId, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			placedSecond := second
			placedSecond.Status = durable.SubmissionStatusPlaced
			placedSecond.Entry = &placedEntryId
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: placedSecond},
			})
			assert.Ok(err == nil)

			gotSecond, err := storage.Submission(dtscCtx, secondId)
			assert.Ok(err == nil)
			assert.DeepEqual(gotSecond, &placedSecond)
			byReqOther, err := storage.SubmissionByRequest(dtscCtx, rootId, "other")
			assert.Ok(err == nil)
			assert.DeepEqual(byReqOther, &placedSecond)

			collectIds := func(query durable.SubmissionQuery) []durable.SubmissionId {
				var found []durable.SubmissionId
				var cursor durable.Cursor
				for {
					page, err := storage.ScanSubmissions(dtscCtx, query, 1, cursor)
					assert.Ok(err == nil)
					for _, item := range page.Items {
						found = append(found, item.Id)
					}
					if page.Next == nil {
						break
					}
					cursor = page.Next
				}
				return found
			}
			assert.DeepEqual(collectIds(durable.SubmissionQuery{}), []durable.SubmissionId{firstId, secondId, otherConvId})
			assert.DeepEqual(collectIds(durable.SubmissionQuery{ConversationId: &rootId}), []durable.SubmissionId{firstId, secondId})
			assert.DeepEqual(collectIds(durable.SubmissionQuery{Status: durable.SubmissionStatusQueued}), []durable.SubmissionId{firstId, otherConvId})
			assert.DeepEqual(collectIds(durable.SubmissionQuery{Status: durable.SubmissionStatusPlaced}), []durable.SubmissionId{secondId})
			assert.DeepEqual(collectIds(durable.SubmissionQuery{ConversationId: &secondConvId, Status: durable.SubmissionStatusQueued}), []durable.SubmissionId{otherConvId})
			assert.StrictEqual(len(collectIds(durable.SubmissionQuery{ConversationId: &secondConvId, Status: durable.SubmissionStatusPlaced})), 0)

			placedPage, err := storage.ScanSubmissions(dtscCtx, durable.SubmissionQuery{Status: durable.SubmissionStatusPlaced}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(placedPage.Items, []durable.SubmissionRecord{placedSecond})
			return nil
		}),

		dtscCase(options, "stores passive write submissions without input-only lifecycle states", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			doneId, err := dtscMintSubmissionId(storage)
			assert.Ok(err == nil)
			failedId, err := dtscMintSubmissionId(storage)
			assert.Ok(err == nil)

			queuedDone := durable.SubmissionRecord{Id: doneId, ConversationId: rootId, RequestId: dtscStrPtr("passive-done"), Type: durable.SubmissionTypeWrite, Status: durable.SubmissionStatusQueued}
			queuedFailed := durable.SubmissionRecord{Id: failedId, ConversationId: rootId, RequestId: dtscStrPtr("passive-failed"), Type: durable.SubmissionTypeWrite, Status: durable.SubmissionStatusQueued}
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: queuedDone},
				durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: queuedFailed},
			})
			assert.Ok(err == nil)

			doneEntryId, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			done := queuedDone
			done.Status = durable.SubmissionStatusDone
			done.Entry = &doneEntryId

			unanswered := queuedFailed
			unanswered.Status = durable.SubmissionStatusUnanswered
			unanswered.Reason = dtscStrPtr("closed")
			unanswered.Detail = jsonx.ObjectOf("retryable", false)

			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: done},
				durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: unanswered},
			})
			assert.Ok(err == nil)

			gotDone, err := storage.Submission(dtscCtx, doneId)
			assert.Ok(err == nil)
			assert.DeepEqual(gotDone, &done)
			byDoneReq, err := storage.SubmissionByRequest(dtscCtx, rootId, "passive-done")
			assert.Ok(err == nil)
			assert.DeepEqual(byDoneReq, &done)
			gotFailed, err := storage.Submission(dtscCtx, failedId)
			assert.Ok(err == nil)
			assert.DeepEqual(gotFailed, &unanswered)
			byFailedReq, err := storage.SubmissionByRequest(dtscCtx, rootId, "passive-failed")
			assert.Ok(err == nil)
			assert.DeepEqual(byFailedReq, &unanswered)
			return nil
		}),

		dtscCase(options, "reconstructs rewindable documents and preserves half-open incarnations", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			firstId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			firstRecord := durable.DocumentCreate{
				Id:      firstId,
				Kind:    "conversation.notes",
				Scope:   durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: rootId},
				History: durable.DocumentHistoryRewindable,
				Fork:    durable.DocumentForkAsOf,
			}
			initialItems := []any{"a"}
			initial := jsonx.ObjectOf("items", initialItems, "nested", jsonx.ObjectOf("count", float64(1)))
			createdAt, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  firstRecord,
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: initial},
				},
			})
			assert.Ok(err == nil)

			appended := []chord.JsonValue{"b"}
			ops := chord.OpList{
				chord.OpSplice{Path: chord.Path{chord.SegString("items")}, Index: 1, Remove: 0, Items: appended},
				chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("nested"), chord.SegString("count")}, Value: float64(2)},
			}
			changedAt, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type:    durable.StorageWriteTypeDocumentChange,
					Id:      firstId,
					Content: durable.DocumentContent{Kind: durable.DocumentContentDelta, Version: 1, Ops: ops},
				},
			})
			assert.Ok(err == nil)

			initial.Set("items", append(initialItems, "caller mutation"))
			appended[0] = "caller mutation"

			docAtCreated, err := storage.Document(dtscCtx, firstId, durable.DocumentPointSeq(createdAt))
			assert.Ok(err == nil)
			assert.PartialDeepEqual(docAtCreated, jsonx.ObjectOf(
				"version", float64(1),
				"value", jsonx.ObjectOf("items", []any{"a"}, "nested", jsonx.ObjectOf("count", float64(1))),
				"deltasSinceBase", float64(0),
			))

			changed, err := storage.Document(dtscCtx, firstId, durable.DocumentPointSeq(changedAt))
			assert.Ok(err == nil)
			assert.DeepEqual(changed.Value, jsonx.ObjectOf("items", []any{"a", "b"}, "nested", jsonx.ObjectOf("count", float64(2))))
			assert.StrictEqual(changed.DeltasSinceBase, 1)
			changedItems, _ := changed.Value.GetArray("items")
			changed.Value.Set("items", append(changedItems, "read mutation"))

			currentDoc, err := storage.Document(dtscCtx, firstId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.DeepEqual(currentDoc.Value, jsonx.ObjectOf("items", []any{"a", "b"}, "nested", jsonx.ObjectOf("count", float64(2))))

			checkpointAt, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type: durable.StorageWriteTypeDocumentChange,
					Id:   firstId,
					Content: durable.DocumentContent{
						Kind:    durable.DocumentContentBase,
						Version: 2,
						Value:   jsonx.ObjectOf("items", []any{"checkpoint"}, "nested", jsonx.ObjectOf("count", float64(3))),
					},
				},
			})
			assert.Ok(err == nil)

			replacedAt, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type: durable.StorageWriteTypeDocumentChange,
					Id:   firstId,
					Content: durable.DocumentContent{
						Kind:    durable.DocumentContentDelta,
						Version: 2,
						Ops:     chord.OpList{chord.OpReplace{Value: jsonx.ObjectOf("items", []any{"replacement"}, "nested", jsonx.ObjectOf("count", float64(4)))}},
					},
				},
			})
			assert.Ok(err == nil)

			atChangedAgain, err := storage.Document(dtscCtx, firstId, durable.DocumentPointSeq(changedAt))
			assert.Ok(err == nil)
			assert.PartialDeepEqual(atChangedAgain, jsonx.ObjectOf(
				"version", float64(1),
				"value", jsonx.ObjectOf("items", []any{"a", "b"}, "nested", jsonx.ObjectOf("count", float64(2))),
			))
			atCheckpoint, err := storage.Document(dtscCtx, firstId, durable.DocumentPointSeq(checkpointAt))
			assert.Ok(err == nil)
			assert.PartialDeepEqual(atCheckpoint, jsonx.ObjectOf(
				"version", float64(2),
				"value", jsonx.ObjectOf("items", []any{"checkpoint"}, "nested", jsonx.ObjectOf("count", float64(3))),
				"deltasSinceBase", float64(0),
			))
			atReplaced, err := storage.Document(dtscCtx, firstId, durable.DocumentPointSeq(replacedAt))
			assert.Ok(err == nil)
			assert.PartialDeepEqual(atReplaced, jsonx.ObjectOf(
				"value", jsonx.ObjectOf("items", []any{"replacement"}, "nested", jsonx.ObjectOf("count", float64(4))),
				"deltasSinceBase", float64(1),
			))
			curAgain, err := storage.Document(dtscCtx, firstId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.StrictEqual(curAgain.DeltasSinceBase, 1)

			secondId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			secondRecord := firstRecord
			secondRecord.Id = secondId
			retiredAt, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  secondRecord,
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("items", []any{"new"})},
				},
				durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: firstId},
				durable.StorageWriteDocumentChange{
					Type:    durable.StorageWriteTypeDocumentChange,
					Id:      firstId,
					Content: durable.DocumentContent{Kind: durable.DocumentContentDelta, Version: 2, Ops: chord.OpList{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("retiring")}, Value: true}}},
				},
			})
			assert.Ok(err == nil)

			address := durable.DocumentAddress{Kind: firstRecord.Kind, Scope: firstRecord.Scope}
			foundBefore, err := storage.FindDocument(dtscCtx, address, durable.DocumentPointSeq(changedAt))
			assert.Ok(err == nil)
			assert.StrictEqual(foundBefore.Id, firstId)
			foundAfter, err := storage.FindDocument(dtscCtx, address, durable.DocumentPointSeq(retiredAt))
			assert.Ok(err == nil)
			assert.StrictEqual(foundAfter.Id, secondId)
			assert.StrictEqual(foundAfter.CreatedAt, retiredAt)

			scanBefore, err := storage.ScanDocuments(dtscCtx, durable.DocumentQuery{Scope: firstRecord.Scope, At: durable.DocumentPointSeq(changedAt)}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscDocumentIds(scanBefore.Items), []durable.DocumentId{firstId})

			scanAfter, err := storage.ScanDocuments(dtscCtx, durable.DocumentQuery{Scope: firstRecord.Scope, At: durable.DocumentPointSeq(retiredAt)}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscDocumentIds(scanAfter.Items), []durable.DocumentId{secondId})

			retiredDoc, err := storage.Document(dtscCtx, firstId, durable.DocumentPointSeq(retiredAt))
			assert.Ok(err == nil)
			assert.Ok(retiredDoc == nil)

			secondCur, err := storage.Document(dtscCtx, secondId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.DeepEqual(secondCur.Value, jsonx.ObjectOf("items", []any{"new"}))
			return nil
		}),

		dtscCase(options, "streams long document tails across root replacement deltas", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			id, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			record := durable.DocumentCreate{
				Id:      id,
				Kind:    "conversation.long-tail",
				Scope:   durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: rootId},
				History: durable.DocumentHistoryRewindable,
				Fork:    durable.DocumentForkAsOf,
			}
			makeRows := func(offset int, prefix string) []any {
				rows := make([]any, 512)
				for i := 0; i < 512; i++ {
					rows[i] = jsonx.ObjectOf("value", float64(offset+i), "stable", fmt.Sprintf("%s-%d", prefix, i))
				}
				return rows
			}
			initial := jsonx.ObjectOf("revision", float64(0), "rows", makeRows(0, "row"))
			createdAt, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  record,
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.CloneValue(initial).(durable.JsonObject)},
				},
			})
			assert.Ok(err == nil)

			beforeReplacement := jsonx.CloneValue(initial).(durable.JsonObject)
			beforeRows, _ := beforeReplacement.GetArray("rows")
			beforeReplacementAt := createdAt
			for rev := 1; rev <= 24; rev++ {
				idx := (rev * 17) % len(beforeRows)
				beforeRows[idx].(*jsonx.Object).Set("value", float64(-rev))
				beforeReplacement.Set("revision", float64(rev))
				beforeReplacementAt, err = storage.Commit(dtscCtx, []durable.StorageWrite{
					durable.StorageWriteDocumentChange{
						Type: durable.StorageWriteTypeDocumentChange,
						Id:   id,
						Content: durable.DocumentContent{
							Kind:    durable.DocumentContentDelta,
							Version: 1,
							Ops: chord.OpList{
								chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("rows"), chord.SegNumber(idx), chord.SegString("value")}, Value: float64(-rev)},
								chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("revision")}, Value: float64(rev)},
							},
						},
					},
				})
				assert.Ok(err == nil)
			}

			replacement := jsonx.ObjectOf("revision", float64(100), "rows", makeRows(10_000, "new"))
			replacementSnapshot := jsonx.CloneValue(replacement).(durable.JsonObject)
			replacementAt, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type: durable.StorageWriteTypeDocumentChange,
					Id:   id,
					Content: durable.DocumentContent{
						Kind:    durable.DocumentContentDelta,
						Version: 1,
						Ops:     chord.OpList{chord.OpReplace{Value: replacement}},
					},
				},
			})
			assert.Ok(err == nil)
			repRows, _ := replacement.GetArray("rows")
			repRows[0].(*jsonx.Object).Set("value", float64(-999))

			current := jsonx.CloneValue(replacementSnapshot).(durable.JsonObject)
			curRows, _ := current.GetArray("rows")
			for rev := 101; rev <= 124; rev++ {
				idx := (rev * 19) % len(curRows)
				curRows[idx].(*jsonx.Object).Set("value", float64(-rev))
				current.Set("revision", float64(rev))
				_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
					durable.StorageWriteDocumentChange{
						Type: durable.StorageWriteTypeDocumentChange,
						Id:   id,
						Content: durable.DocumentContent{
							Kind:    durable.DocumentContentDelta,
							Version: 1,
							Ops: chord.OpList{
								chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("rows"), chord.SegNumber(idx), chord.SegString("value")}, Value: float64(-rev)},
								chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("revision")}, Value: float64(rev)},
							},
						},
					},
				})
				assert.Ok(err == nil)
			}

			gotCreated, err := storage.Document(dtscCtx, id, durable.DocumentPointSeq(createdAt))
			assert.Ok(err == nil)
			assert.DeepEqual(gotCreated.Value, initial)
			gotBefore, err := storage.Document(dtscCtx, id, durable.DocumentPointSeq(beforeReplacementAt))
			assert.Ok(err == nil)
			assert.DeepEqual(gotBefore.Value, beforeReplacement)
			gotRep, err := storage.Document(dtscCtx, id, durable.DocumentPointSeq(replacementAt))
			assert.Ok(err == nil)
			assert.DeepEqual(gotRep.Value, replacementSnapshot)

			readCur, err := storage.Document(dtscCtx, id, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.DeepEqual(readCur.Value, current)
			readRows, _ := readCur.Value.GetArray("rows")
			readRows[0].(*jsonx.Object).Set("value", float64(-1_000))

			readCur2, err := storage.Document(dtscCtx, id, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.DeepEqual(readCur2.Value, current)
			return nil
		}),

		dtscCase(options, "copies stored document bases independently and rejects ambiguous sources", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			childId, err := dtscMintConversationId(storage)
			assert.Ok(err == nil)
			secondChildId, err := dtscMintConversationId(storage)
			assert.Ok(err == nil)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteConversation{Type: durable.StorageWriteTypeConversation, Value: durable.ConversationRecord{Id: childId}},
				durable.StorageWriteConversation{Type: durable.StorageWriteTypeConversation, Value: durable.ConversationRecord{Id: secondChildId}},
			})
			assert.Ok(err == nil)

			sourceId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			sourceRecord := durable.DocumentCreate{
				Id:      sourceId,
				Kind:    "copy.source",
				Scope:   durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: rootId},
				History: durable.DocumentHistoryRewindable,
				Fork:    durable.DocumentForkAsOf,
			}
			createdAt, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  sourceRecord,
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 2, Value: jsonx.ObjectOf("count", float64(1), "rows", []any{jsonx.ObjectOf("value", "base")})},
				},
			})
			assert.Ok(err == nil)

			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type: durable.StorageWriteTypeDocumentChange,
					Id:   sourceId,
					Content: durable.DocumentContent{
						Kind:    durable.DocumentContentDelta,
						Version: 2,
						Ops: chord.OpList{
							chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(2)},
							chord.OpSplice{Path: chord.Path{chord.SegString("rows")}, Index: 1, Remove: 0, Items: []chord.JsonValue{jsonx.ObjectOf("value", "current")}},
						},
					},
				},
			})
			assert.Ok(err == nil)

			historicalCopyId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			currentCopyId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			retiredCopyId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			childRecord := func(id durable.DocumentId, convId durable.ConversationId) durable.DocumentCreate {
				return durable.DocumentCreate{
					Id:      id,
					Kind:    sourceRecord.Kind,
					Scope:   durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: convId},
					History: durable.DocumentHistoryRewindable,
					Fork:    durable.DocumentForkAsOf,
				}
			}
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentCopy{Type: durable.StorageWriteTypeDocumentCopy, Record: childRecord(historicalCopyId, childId), Source: durable.DocumentCopySource{Id: sourceId, At: durable.DocumentPointSeq(createdAt)}},
				durable.StorageWriteDocumentCopy{Type: durable.StorageWriteTypeDocumentCopy, Record: childRecord(currentCopyId, secondChildId), Source: durable.DocumentCopySource{Id: sourceId, At: durable.DocumentPointCurrent()}},
				durable.StorageWriteDocumentCopy{Type: durable.StorageWriteTypeDocumentCopy, Record: childRecord(retiredCopyId, rootId), Source: durable.DocumentCopySource{Id: sourceId, At: durable.DocumentPointCurrent()}},
				durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: retiredCopyId},
			})
			assert.Ok(err == nil)

			histCopy, err := storage.Document(dtscCtx, historicalCopyId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.PartialDeepEqual(histCopy, jsonx.ObjectOf(
				"version", float64(2),
				"value", jsonx.ObjectOf("count", float64(1), "rows", []any{jsonx.ObjectOf("value", "base")}),
			))
			curCopy, err := storage.Document(dtscCtx, currentCopyId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.PartialDeepEqual(curCopy, jsonx.ObjectOf(
				"version", float64(2),
				"value", jsonx.ObjectOf("count", float64(2), "rows", []any{jsonx.ObjectOf("value", "base"), jsonx.ObjectOf("value", "current")}),
			))
			retiredCopy, err := storage.Document(dtscCtx, retiredCopyId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.Ok(retiredCopy == nil)

			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type:    durable.StorageWriteTypeDocumentChange,
					Id:      sourceId,
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 2, Value: jsonx.ObjectOf("count", float64(99), "rows", []any{})},
				},
				durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: sourceId},
			})
			assert.Ok(err == nil)
			curCopyAfterRetire, err := storage.Document(dtscCtx, currentCopyId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.DeepEqual(curCopyAfterRetire.Value, jsonx.ObjectOf("count", float64(2), "rows", []any{jsonx.ObjectOf("value", "base"), jsonx.ObjectOf("value", "current")}))

			latestSourceId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			latestCopyId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			latestSource := durable.DocumentCreate{
				Id:      latestSourceId,
				Kind:    "copy.latest",
				Scope:   durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: rootId},
				History: durable.DocumentHistoryLatest,
				Fork:    durable.DocumentForkCurrent,
			}
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{Type: durable.StorageWriteTypeDocumentCreate, Record: latestSource, Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 4, Value: jsonx.ObjectOf("retained", "copy")}},
			})
			assert.Ok(err == nil)
			latestCopyRecord := latestSource
			latestCopyRecord.Id = latestCopyId
			latestCopyRecord.Scope = durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: childId}
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentCopy{Type: durable.StorageWriteTypeDocumentCopy, Record: latestCopyRecord, Source: durable.DocumentCopySource{Id: latestSourceId, At: durable.DocumentPointCurrent()}},
			})
			assert.Ok(err == nil)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{Type: durable.StorageWriteTypeDocumentChange, Id: latestSourceId, Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 4, Value: jsonx.ObjectOf("retained", "source-only")}},
				durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: latestSourceId},
			})
			assert.Ok(err == nil)

			gotLatestCopy, err := storage.Document(dtscCtx, latestCopyId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.PartialDeepEqual(gotLatestCopy, jsonx.ObjectOf("version", float64(4), "value", jsonx.ObjectOf("retained", "copy")))

			conflictId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			_, conflictErr := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentCopy{Type: durable.StorageWriteTypeDocumentCopy, Record: childRecord(conflictId, childId), Source: durable.DocumentCopySource{Id: currentCopyId, At: durable.DocumentPointCurrent()}},
				durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: currentCopyId},
			})
			var rejected *durable.StorageRejected
			assert.Ok(errors.As(conflictErr, &rejected), fmt.Sprintf("expected StorageRejected, got %T: %v", conflictErr, conflictErr))
			conflictDoc, err := storage.Document(dtscCtx, conflictId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.Ok(conflictDoc == nil)
			curCopySafe, err := storage.Document(dtscCtx, currentCopyId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.DeepEqual(curCopySafe.Value, jsonx.ObjectOf("count", float64(2), "rows", []any{jsonx.ObjectOf("value", "base"), jsonx.ObjectOf("value", "current")}))

			mismatchId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			mismatchRecord := childRecord(mismatchId, childId)
			mismatchRecord.Kind = "copy.mismatch"
			_, mismatchErr := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentCopy{Type: durable.StorageWriteTypeDocumentCopy, Record: mismatchRecord, Source: durable.DocumentCopySource{Id: currentCopyId, At: durable.DocumentPointCurrent()}},
			})
			rejected = nil
			assert.Ok(errors.As(mismatchErr, &rejected), fmt.Sprintf("expected StorageRejected, got %T: %v", mismatchErr, mismatchErr))
			mismatchDoc, err := storage.Document(dtscCtx, mismatchId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.Ok(mismatchDoc == nil)
			return nil
		}),

		dtscCase(options, "uses bases for version transitions and rejects historical reads of current-only documents", func(storage durable.Storage) error {
			_, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			id, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			record := durable.DocumentCreate{
				Id:    id,
				Kind:  "session.settings",
				Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
			}
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{Type: durable.StorageWriteTypeDocumentCreate, Record: record, Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(1))}},
			})
			assert.Ok(err == nil)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{Type: durable.StorageWriteTypeDocumentChange, Id: id, Content: durable.DocumentContent{Kind: durable.DocumentContentDelta, Version: 1, Ops: chord.OpList{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(2)}}}},
			})
			assert.Ok(err == nil)
			migratedAt, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{Type: durable.StorageWriteTypeDocumentChange, Id: id, Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 2, Value: jsonx.ObjectOf("count", float64(3))}},
			})
			assert.Ok(err == nil)

			cur, err := storage.Document(dtscCtx, id, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.PartialDeepEqual(cur, jsonx.ObjectOf("version", float64(2), "value", jsonx.ObjectOf("count", float64(3))))

			assert.Rejects(func() error {
				_, err := storage.Document(dtscCtx, id, durable.DocumentPointSeq(migratedAt))
				return err
			}, "does not retain historical content")

			assert.Rejects(func() error {
				_, err := storage.Commit(dtscCtx, []durable.StorageWrite{
					durable.StorageWriteDocumentChange{Type: durable.StorageWriteTypeDocumentChange, Id: id, Content: durable.DocumentContent{Kind: durable.DocumentContentDelta, Version: 1, Ops: chord.OpList{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(4)}}}},
				})
				return err
			}, "version transition requires a base")

			curAfter, err := storage.Document(dtscCtx, id, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.DeepEqual(curAfter.Value, jsonx.ObjectOf("count", float64(3)))

			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: id},
			})
			assert.Ok(err == nil)
			afterRetire, err := storage.Document(dtscCtx, id, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.Ok(afterRetire == nil)
			return nil
		}),

		dtscCase(options, "indexes logical addresses and exact-scope scans independently", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			firstId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			secondId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			convDocId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			taskId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)
			taskSingletonId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			taskFamilyId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			taskOtherKindId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)

			createdAt, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: dtscPendingTask(taskId, rootId)},
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  durable.DocumentCreate{Id: firstId, Kind: "cache", Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}, Key: dtscStrPtr("__proto__")},
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("owner", "first")},
				},
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  durable.DocumentCreate{Id: secondId, Kind: "cache", Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}, Key: dtscStrPtr("constructor")},
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("owner", "second")},
				},
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  durable.DocumentCreate{Id: convDocId, Kind: "cache", Scope: durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: rootId}, History: durable.DocumentHistoryLatest, Fork: durable.DocumentForkCurrent, Key: dtscStrPtr("__proto__")},
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("owner", "conversation")},
				},
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  durable.DocumentCreate{Id: taskSingletonId, Kind: "task.cache", Scope: durable.DocumentScope{Kind: durable.DocumentScopeTask, TaskId: taskId}},
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("owner", "singleton")},
				},
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  durable.DocumentCreate{Id: taskFamilyId, Kind: "task.cache", Scope: durable.DocumentScope{Kind: durable.DocumentScopeTask, TaskId: taskId}, Key: dtscStrPtr("member")},
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("owner", "family")},
				},
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  durable.DocumentCreate{Id: taskOtherKindId, Kind: "task.other", Scope: durable.DocumentScope{Kind: durable.DocumentScopeTask, TaskId: taskId}},
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("owner", "other")},
				},
			})
			assert.Ok(err == nil)

			foundProto, err := storage.FindDocument(dtscCtx, durable.DocumentAddress{Kind: "cache", Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}, Key: dtscStrPtr("__proto__")}, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.StrictEqual(foundProto.Id, firstId)

			first, err := storage.ScanDocuments(dtscCtx, durable.DocumentQuery{Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}, At: durable.DocumentPointCurrent()}, 1, nil)
			assert.Ok(err == nil)
			assert.StrictEqual(len(first.Items), 1)

			second, err := storage.ScanDocuments(dtscCtx, durable.DocumentQuery{Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}, At: durable.DocumentPointCurrent()}, 1, first.Next)
			assert.Ok(err == nil)
			allSessionIds := append(dtscDocumentIds(first.Items), dtscDocumentIds(second.Items)...)
			assert.DeepEqual(allSessionIds, []durable.DocumentId{firstId, secondId})

			convDocs, err := storage.ScanDocuments(dtscCtx, durable.DocumentQuery{Scope: durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: rootId}, At: durable.DocumentPointCurrent()}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscDocumentIds(convDocs.Items), []durable.DocumentId{convDocId})

			foundTaskSingleton, err := storage.FindDocument(dtscCtx, durable.DocumentAddress{Kind: "task.cache", Scope: durable.DocumentScope{Kind: durable.DocumentScopeTask, TaskId: taskId}}, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.StrictEqual(foundTaskSingleton.Id, taskSingletonId)

			foundTaskFamily, err := storage.FindDocument(dtscCtx, durable.DocumentAddress{Kind: "task.cache", Scope: durable.DocumentScope{Kind: durable.DocumentScopeTask, TaskId: taskId}, Key: dtscStrPtr("member")}, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.StrictEqual(foundTaskFamily.Id, taskFamilyId)

			taskCacheDocs, err := storage.ScanDocuments(dtscCtx, durable.DocumentQuery{Scope: durable.DocumentScope{Kind: durable.DocumentScopeTask, TaskId: taskId}, At: durable.DocumentPointCurrent(), Kind: dtscStrPtr("task.cache")}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscDocumentIds(taskCacheDocs.Items), []durable.DocumentId{taskSingletonId, taskFamilyId})

			assert.Rejects(func() error {
				_, err := storage.Document(dtscCtx, taskSingletonId, durable.DocumentPointSeq(createdAt))
				return err
			}, "does not retain historical content")
			return nil
		}),

		dtscCase(options, "keeps document lifecycle failures atomic and gives create-plus-retire an empty lifetime", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			firstId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			secondId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			record := durable.DocumentCreate{
				Id:    firstId,
				Kind:  "singleton",
				Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
			}
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{Type: durable.StorageWriteTypeDocumentCreate, Record: record, Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("value", float64(1))}},
			})
			assert.Ok(err == nil)

			secondRec := record
			secondRec.Id = secondId
			assert.Rejects(func() error {
				_, err := storage.Commit(dtscCtx, []durable.StorageWrite{
					durable.StorageWriteDocumentCreate{Type: durable.StorageWriteTypeDocumentCreate, Record: secondRec, Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("value", float64(2))}},
					durable.StorageWriteDocumentChange{Type: durable.StorageWriteTypeDocumentChange, Id: firstId, Content: durable.DocumentContent{Kind: durable.DocumentContentDelta, Version: 1, Ops: chord.OpList{}}},
				})
				return err
			}, "already has a current incarnation")

			gotFirst, err := storage.Document(dtscCtx, firstId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.DeepEqual(gotFirst.Value, jsonx.ObjectOf("value", float64(1)))
			gotSecond, err := storage.Document(dtscCtx, secondId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.Ok(gotSecond == nil)

			emptyId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			emptyAt, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{
					Type: durable.StorageWriteTypeDocumentCreate,
					Record: durable.DocumentCreate{
						Id:      emptyId,
						Kind:    record.Kind,
						Key:     dtscStrPtr("empty"),
						Scope:   durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: rootId},
						History: durable.DocumentHistoryRewindable,
						Fork:    durable.DocumentForkInitial,
					},
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.NewObject()},
				},
				durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: emptyId},
			})
			assert.Ok(err == nil)

			dCur, err := storage.Document(dtscCtx, emptyId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.Ok(dCur == nil)
			dSeq, err := storage.Document(dtscCtx, emptyId, durable.DocumentPointSeq(emptyAt))
			assert.Ok(err == nil)
			assert.Ok(dSeq == nil)
			foundEmpty, err := storage.FindDocument(dtscCtx, durable.DocumentAddress{Kind: record.Kind, Scope: durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: rootId}, Key: dtscStrPtr("empty")}, durable.DocumentPointSeq(emptyAt))
			assert.Ok(err == nil)
			assert.Ok(foundEmpty == nil)
			return nil
		}),

		dtscCase(options, "rolls back record tables and secondary indexes when a document command fails", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			taskId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)
			submissionId, err := dtscMintSubmissionId(storage)
			assert.Ok(err == nil)
			documentId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)

			task := dtscPendingTask(taskId, rootId)
			submission := durable.SubmissionRecord{
				Id:             submissionId,
				ConversationId: rootId,
				RequestId:      dtscStrPtr("atomic"),
				Type:           durable.SubmissionTypeInput,
				Status:         durable.SubmissionStatusQueued,
			}
			record := durable.DocumentCreate{
				Id:    documentId,
				Kind:  "atomic",
				Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
			}
			baselineSeq, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: task},
				durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: submission},
				durable.StorageWriteDocumentCreate{Type: durable.StorageWriteTypeDocumentCreate, Record: record, Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(1))}},
			})
			assert.Ok(err == nil)

			entryId, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			conflictingDocumentId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			runningTask := task
			runningTask.State = durable.TaskState{Status: durable.TaskStateRunning, Checkpoint: jsonx.ObjectOf("phase", "effect")}
			unansweredSub := submission
			unansweredSub.Status = durable.SubmissionStatusUnanswered
			unansweredSub.Reason = dtscStrPtr("failed")
			conflictingRecord := record
			conflictingRecord.Id = conflictingDocumentId

			assert.Rejects(func() error {
				_, err := storage.Commit(dtscCtx, []durable.StorageWrite{
					durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: runningTask},
					durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: unansweredSub},
					durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(entryId, rootId, "transient", nil, nil)},
					durable.StorageWriteDocumentCreate{Type: durable.StorageWriteTypeDocumentCreate, Record: conflictingRecord, Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(2))}},
				})
				return err
			}, "already has a current incarnation")

			gotTask, err := storage.Task(dtscCtx, taskId)
			assert.Ok(err == nil)
			assert.DeepEqual(gotTask, &task)
			pendingPage, err := storage.ScanTasks(dtscCtx, durable.TaskQuery{Status: durable.TaskStatePending}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(pendingPage.Items, []durable.TaskRecord{task})
			gotSub, err := storage.SubmissionByRequest(dtscCtx, rootId, "atomic")
			assert.Ok(err == nil)
			assert.DeepEqual(gotSub, &submission)
			gotEntry, err := storage.Entry(dtscCtx, entryId)
			assert.Ok(err == nil)
			assert.Ok(gotEntry == nil)
			gotConflictDoc, err := storage.Document(dtscCtx, conflictingDocumentId, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.Ok(gotConflictDoc == nil)
			foundDoc, err := storage.FindDocument(dtscCtx, durable.DocumentAddress{Kind: record.Kind, Scope: record.Scope}, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.StrictEqual(foundDoc.Id, documentId)

			afterRollbackSeq, err := storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type:    durable.StorageWriteTypeDocumentChange,
					Id:      documentId,
					Content: durable.DocumentContent{Kind: durable.DocumentContentDelta, Version: 1, Ops: chord.OpList{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(3)}}},
				},
			})
			assert.Ok(err == nil)
			assert.GreaterThan(int64(afterRollbackSeq), int64(baselineSeq))
			return nil
		}),

		dtscCase(options, "keeps indexed string identities lossless", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			// Lone UTF-16 surrogates encoded in WTF-8 so JSON and JS surrogate boundaries stay lossless.
			first := "\xed\xa0\x80"
			second := "\xed\xa0\x81"

			firstTaskId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)
			secondTaskId, err := dtscMintTaskId(storage)
			assert.Ok(err == nil)
			firstSubmissionId, err := dtscMintSubmissionId(storage)
			assert.Ok(err == nil)
			secondSubmissionId, err := dtscMintSubmissionId(storage)
			assert.Ok(err == nil)
			firstKindDocId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			secondKindDocId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			firstKeyDocId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)
			secondKeyDocId, err := dtscMintDocumentId(storage)
			assert.Ok(err == nil)

			task1 := dtscPendingTask(firstTaskId, rootId)
			task1.Kind = first
			task2 := dtscPendingTask(secondTaskId, rootId)
			task2.Kind = second

			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: task1},
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: task2},
				durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: durable.SubmissionRecord{Id: firstSubmissionId, ConversationId: rootId, RequestId: &first, Type: durable.SubmissionTypeInput, Status: durable.SubmissionStatusQueued}},
				durable.StorageWriteSubmission{Type: durable.StorageWriteTypeSubmission, Value: durable.SubmissionRecord{Id: secondSubmissionId, ConversationId: rootId, RequestId: &second, Type: durable.SubmissionTypeInput, Status: durable.SubmissionStatusQueued}},
				durable.StorageWriteDocumentCreate{Type: durable.StorageWriteTypeDocumentCreate, Record: durable.DocumentCreate{Id: firstKindDocId, Kind: first, Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}}, Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("identity", "first kind")}},
				durable.StorageWriteDocumentCreate{Type: durable.StorageWriteTypeDocumentCreate, Record: durable.DocumentCreate{Id: secondKindDocId, Kind: second, Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}}, Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("identity", "second kind")}},
				durable.StorageWriteDocumentCreate{Type: durable.StorageWriteTypeDocumentCreate, Record: durable.DocumentCreate{Id: firstKeyDocId, Kind: "family", Key: &first, Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}}, Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("identity", "first key")}},
				durable.StorageWriteDocumentCreate{Type: durable.StorageWriteTypeDocumentCreate, Record: durable.DocumentCreate{Id: secondKeyDocId, Kind: "family", Key: &second, Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}}, Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("identity", "second key")}},
			})
			assert.Ok(err == nil)

			tasks1, err := storage.ScanTasks(dtscCtx, durable.TaskQuery{Kind: &first}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscTaskIds(tasks1.Items), []durable.TaskId{firstTaskId})
			tasks2, err := storage.ScanTasks(dtscCtx, durable.TaskQuery{Kind: &second}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscTaskIds(tasks2.Items), []durable.TaskId{secondTaskId})

			gotT1, err := storage.Task(dtscCtx, firstTaskId)
			assert.Ok(err == nil)
			assert.StrictEqual(gotT1.Kind, first)
			gotT2, err := storage.Task(dtscCtx, secondTaskId)
			assert.Ok(err == nil)
			assert.StrictEqual(gotT2.Kind, second)

			gotS1, err := storage.SubmissionByRequest(dtscCtx, rootId, first)
			assert.Ok(err == nil)
			assert.StrictEqual(*gotS1.RequestId, first)
			assert.StrictEqual(gotS1.Id, firstSubmissionId)
			gotS2, err := storage.SubmissionByRequest(dtscCtx, rootId, second)
			assert.Ok(err == nil)
			assert.StrictEqual(gotS2.Id, secondSubmissionId)

			dKind1, err := storage.FindDocument(dtscCtx, durable.DocumentAddress{Kind: first, Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}}, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.StrictEqual(dKind1.Id, firstKindDocId)
			dKind2, err := storage.FindDocument(dtscCtx, durable.DocumentAddress{Kind: second, Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}}, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.StrictEqual(dKind2.Id, secondKindDocId)
			dKey1, err := storage.FindDocument(dtscCtx, durable.DocumentAddress{Kind: "family", Key: &first, Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}}, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.StrictEqual(dKey1.Id, firstKeyDocId)
			dKey2, err := storage.FindDocument(dtscCtx, durable.DocumentAddress{Kind: "family", Key: &second, Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}}, durable.DocumentPointCurrent())
			assert.Ok(err == nil)
			assert.StrictEqual(dKey2.Id, secondKeyDocId)

			scanKind1, err := storage.ScanDocuments(dtscCtx, durable.DocumentQuery{Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession}, At: durable.DocumentPointCurrent(), Kind: &first}, 10, nil)
			assert.Ok(err == nil)
			assert.DeepEqual(dtscDocumentIds(scanKind1.Items), []durable.DocumentId{firstKindDocId})
			return nil
		}),

		dtscCase(options, "keeps one global record ID namespace and rejects exhausted ID minting", func(storage durable.Storage) error {
			rootId, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			explicitEntryId := durable.IdFromNumber[durable.EntryId](100)
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(explicitEntryId, rootId, "", nil, nil)},
			})
			assert.Ok(err == nil)
			minted, err := dtscMintEntryId(storage)
			assert.Ok(err == nil)
			assert.StrictEqual(minted, durable.EntryId(101))

			assert.Rejects(func() error {
				_, err := storage.Commit(dtscCtx, []durable.StorageWrite{
					durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: dtscPendingTask(durable.IdFromNumber[durable.TaskId](int64(explicitEntryId)), rootId)},
				})
				return err
			}, fmt.Sprintf("ID %d already belongs to entry", explicitEntryId))

			const maxSafeInteger int64 = 9007199254740991
			_, err = storage.Commit(dtscCtx, []durable.StorageWrite{
				durable.StorageWriteEntry{Type: durable.StorageWriteTypeEntry, Value: dtscEntry(durable.IdFromNumber[durable.EntryId](maxSafeInteger), rootId, "last-id", nil, nil)},
			})
			assert.Ok(err == nil)

			assert.Rejects(func() error {
				_, err := storage.MintId()
				return err
			}, "ID space is exhausted")
			assert.Rejects(func() error {
				_, err := storage.MintId()
				return err
			}, "ID space is exhausted")
			return nil
		}),

		dtscCase(options, "rejects every operation after close", func(storage durable.Storage) error {
			_, err := dtscCreateRoot(storage)
			assert.Ok(err == nil)
			err = storage.Close(dtscCtx)
			assert.Ok(err == nil)

			assert.Rejects(func() error {
				_, err := storage.Conversation(dtscCtx, durable.RootConversationId)
				return err
			}, "closed")
			assert.Rejects(func() error {
				_, err := storage.Commit(dtscCtx, []durable.StorageWrite{})
				return err
			}, "closed")
			assert.Rejects(func() error {
				_, err := storage.MintId()
				return err
			}, "closed")
			return nil
		}),
	}
}
