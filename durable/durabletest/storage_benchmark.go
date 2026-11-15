// Ported from packages/durable/src/testing/storage-benchmark.ts (pi v1.0.0).

package durabletest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// StorageBenchmarkScale defines record counts used to seed benchmark datasets.
type StorageBenchmarkScale struct {
	Name          string
	EntryCount    int
	TaskCount     int
	DocumentCount int
}

// StorageMemoryScales holds standard dataset scales for storage memory profiling.
var StorageMemoryScales = []StorageBenchmarkScale{
	{Name: "1k", EntryCount: 1_000, TaskCount: 200, DocumentCount: 200},
	{Name: "10k", EntryCount: 10_000, TaskCount: 2_000, DocumentCount: 2_000},
}

// TimingScale is the default dataset scale for read benchmarks.
var TimingScale = StorageBenchmarkScale{
	Name:          "timing",
	EntryCount:    1_000,
	TaskCount:     300,
	DocumentCount: 300,
}

var dtsbReplayTails = []int{0, 16, 128, 1_024}

const (
	dtsbHistorySegmentLength = 128
	dtsbForkDepth            = 8
	dtsbEntriesPerFork       = 32
	dtsbBatchSize            = 100
)

// StorageBenchmarkPrimaryRecordCount returns the total number of primary records created by SeedStorageBenchmark.
func StorageBenchmarkPrimaryRecordCount(scale StorageBenchmarkScale) int {
	return 1 +
		scale.EntryCount +
		scale.TaskCount +
		scale.DocumentCount +
		len(dtsbReplayTails) +
		1 +
		dtsbForkDepth*(1+dtsbEntriesPerFork)
}

// StorageBenchmarkDataset captures record IDs and commit sequences produced by SeedStorageBenchmark.
type StorageBenchmarkDataset struct {
	FirstEntryId          durable.EntryId
	FilteredTaskCount     int
	ExactDocumentId       durable.DocumentId
	ExactDocumentKey      string
	ReplayDocumentIds     map[int]durable.DocumentId
	HistoricalDocumentId  durable.DocumentId
	AncientAt             durable.Seq
	RecentAt              durable.Seq
	DeepestConversationId durable.ConversationId
	AncestorHeadEntryId   durable.EntryId
}

func dtsbTask(id durable.TaskId, index int) durable.TaskRecord {
	statuses := []durable.TaskStateStatus{
		durable.TaskStatePending,
		durable.TaskStateRunning,
		durable.TaskStateTerminal,
	}
	status := statuses[index%len(statuses)]
	kind := "benchmark.other"
	if index%4 == 0 {
		kind = "benchmark.filtered"
	}
	record := durable.TaskRecord{
		Id:             id,
		ConversationId: durable.RootConversationId,
		Kind:           kind,
		Version:        1,
		Input:          jsonx.ObjectOf("index", float64(index)),
		Background:     index%5 == 0,
		AbortRequested: index%7 == 0,
	}
	if status == durable.TaskStateTerminal {
		record.State = durable.TaskState{
			Status: durable.TaskStateTerminal,
			Outcome: &durable.TaskOutcome{
				Status: durable.TaskOutcomeCompleted,
				Result: jsonx.Some[durable.JsonValue](jsonx.ObjectOf("index", float64(index))),
			},
		}
		return record
	}
	record.State = durable.TaskState{
		Status: status,
		Checkpoint: jsonx.ObjectOf(
			"index", float64(index),
			"payload", strings.Repeat("x", 64),
		),
	}
	return record
}

// SeedStorageBenchmark seeds deterministic representative records using only the public Storage contract.
func SeedStorageBenchmark(storage durable.Storage, scales ...StorageBenchmarkScale) (StorageBenchmarkDataset, error) {
	scale := TimingScale
	if len(scales) > 0 {
		scale = scales[0]
	}
	ctx := chord.BackgroundContext

	if _, err := storage.Commit(ctx, []durable.StorageWrite{
		durable.StorageWriteConversation{
			Type:  durable.StorageWriteTypeConversation,
			Value: durable.ConversationRecord{Id: durable.RootConversationId},
		},
	}); err != nil {
		return StorageBenchmarkDataset{}, err
	}

	var firstEntryId durable.EntryId
	hasFirstEntry := false
	entryPad := strings.Repeat("x", 96)
	for start := 0; start < scale.EntryCount; start += dtsbBatchSize {
		end := start + dtsbBatchSize
		if end > scale.EntryCount {
			end = scale.EntryCount
		}
		writes := make([]durable.StorageWrite, 0, end-start)
		for index := start; index < end; index++ {
			rawId, err := storage.MintId()
			if err != nil {
				return StorageBenchmarkDataset{}, err
			}
			id := durable.IdFromNumber[durable.EntryId](rawId)
			if index == 0 {
				firstEntryId = id
				hasFirstEntry = true
			}
			var head *durable.EntryId
			if index == 0 {
				h := id
				head = &h
			}
			writes = append(writes, durable.StorageWriteEntry{
				Type: durable.StorageWriteTypeEntry,
				Value: durable.EntryRecord{
					Id:             id,
					ConversationId: durable.RootConversationId,
					Kind:           "benchmark.entry",
					Head:           head,
					Data:           jsonx.ObjectOf("index", float64(index), "text", fmt.Sprintf("entry-%d-%s", index, entryPad)),
				},
			})
		}
		if _, err := storage.Commit(ctx, writes); err != nil {
			return StorageBenchmarkDataset{}, err
		}
	}

	for start := 0; start < scale.TaskCount; start += dtsbBatchSize {
		end := start + dtsbBatchSize
		if end > scale.TaskCount {
			end = scale.TaskCount
		}
		writes := make([]durable.StorageWrite, 0, end-start)
		for index := start; index < end; index++ {
			rawId, err := storage.MintId()
			if err != nil {
				return StorageBenchmarkDataset{}, err
			}
			writes = append(writes, durable.StorageWriteTask{
				Type:  durable.StorageWriteTypeTask,
				Value: dtsbTask(durable.IdFromNumber[durable.TaskId](rawId), index),
			})
		}
		if _, err := storage.Commit(ctx, writes); err != nil {
			return StorageBenchmarkDataset{}, err
		}
	}

	var exactDocumentId durable.DocumentId
	hasExactDoc := false
	docPad := strings.Repeat("x", 128)
	for start := 0; start < scale.DocumentCount; start += dtsbBatchSize {
		end := start + dtsbBatchSize
		if end > scale.DocumentCount {
			end = scale.DocumentCount
		}
		writes := make([]durable.StorageWrite, 0, end-start)
		for index := start; index < end; index++ {
			rawId, err := storage.MintId()
			if err != nil {
				return StorageBenchmarkDataset{}, err
			}
			id := durable.IdFromNumber[durable.DocumentId](rawId)
			exactDocumentId = id
			hasExactDoc = true
			key := fmt.Sprintf("key-%d", index)
			writes = append(writes, durable.StorageWriteDocumentCreate{
				Type: durable.StorageWriteTypeDocumentCreate,
				Record: durable.DocumentCreate{
					Id:    id,
					Kind:  "benchmark.family",
					Key:   &key,
					Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
				},
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentBase,
					Version: 1,
					Value:   jsonx.ObjectOf("index", float64(index), "text", docPad),
				},
			})
		}
		if _, err := storage.Commit(ctx, writes); err != nil {
			return StorageBenchmarkDataset{}, err
		}
	}

	type replayEntry struct {
		tail int
		id   durable.DocumentId
	}
	replayEntries := make([]replayEntry, len(dtsbReplayTails))
	replayWrites := make([]durable.StorageWrite, len(dtsbReplayTails))
	replayPad := strings.Repeat("x", 64)
	for i, tail := range dtsbReplayTails {
		rawId, err := storage.MintId()
		if err != nil {
			return StorageBenchmarkDataset{}, err
		}
		id := durable.IdFromNumber[durable.DocumentId](rawId)
		replayEntries[i] = replayEntry{tail: tail, id: id}
		key := strconv.Itoa(tail)
		replayWrites[i] = durable.StorageWriteDocumentCreate{
			Type: durable.StorageWriteTypeDocumentCreate,
			Record: durable.DocumentCreate{
				Id:      id,
				Kind:    "benchmark.replay",
				Key:     &key,
				Scope:   durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: durable.RootConversationId},
				History: durable.DocumentHistoryRewindable,
				Fork:    durable.DocumentForkAsOf,
			},
			Content: durable.DocumentContent{
				Kind:    durable.DocumentContentBase,
				Version: 1,
				Value:   jsonx.ObjectOf("count", float64(0), "text", replayPad),
			},
		}
	}
	if _, err := storage.Commit(ctx, replayWrites); err != nil {
		return StorageBenchmarkDataset{}, err
	}

	maxTail := dtsbReplayTails[len(dtsbReplayTails)-1]
	for count := 1; count <= maxTail; count++ {
		var writes []durable.StorageWrite
		for _, re := range replayEntries {
			if count <= re.tail {
				writes = append(writes, durable.StorageWriteDocumentChange{
					Type: durable.StorageWriteTypeDocumentChange,
					Id:   re.id,
					Content: durable.DocumentContent{
						Kind:    durable.DocumentContentDelta,
						Version: 1,
						Ops:     chord.OpList{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(count)}},
					},
				})
			}
		}
		if _, err := storage.Commit(ctx, writes); err != nil {
			return StorageBenchmarkDataset{}, err
		}
	}

	rawHistId, err := storage.MintId()
	if err != nil {
		return StorageBenchmarkDataset{}, err
	}
	historicalDocumentId := durable.IdFromNumber[durable.DocumentId](rawHistId)
	if _, err := storage.Commit(ctx, []durable.StorageWrite{
		durable.StorageWriteDocumentCreate{
			Type: durable.StorageWriteTypeDocumentCreate,
			Record: durable.DocumentCreate{
				Id:      historicalDocumentId,
				Kind:    "benchmark.history",
				Scope:   durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: durable.RootConversationId},
				History: durable.DocumentHistoryRewindable,
				Fork:    durable.DocumentForkAsOf,
			},
			Content: durable.DocumentContent{
				Kind:    durable.DocumentContentBase,
				Version: 1,
				Value:   jsonx.ObjectOf("count", float64(0)),
			},
		},
	}); err != nil {
		return StorageBenchmarkDataset{}, err
	}

	var ancientAt durable.Seq
	hasAncient := false
	for count := 1; count <= dtsbHistorySegmentLength; count++ {
		seq, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type: durable.StorageWriteTypeDocumentChange,
				Id:   historicalDocumentId,
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentDelta,
					Version: 1,
					Ops:     chord.OpList{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(count)}},
				},
			},
		})
		if err != nil {
			return StorageBenchmarkDataset{}, err
		}
		ancientAt = seq
		hasAncient = true
	}
	if _, err := storage.Commit(ctx, []durable.StorageWrite{
		durable.StorageWriteDocumentChange{
			Type: durable.StorageWriteTypeDocumentChange,
			Id:   historicalDocumentId,
			Content: durable.DocumentContent{
				Kind:    durable.DocumentContentBase,
				Version: 1,
				Value:   jsonx.ObjectOf("count", float64(dtsbHistorySegmentLength)),
			},
		},
	}); err != nil {
		return StorageBenchmarkDataset{}, err
	}
	if !hasAncient {
		return StorageBenchmarkDataset{}, errors.New("Benchmark history seed produced no commits")
	}

	recentAt := ancientAt
	for count := dtsbHistorySegmentLength + 1; count <= dtsbHistorySegmentLength*2; count++ {
		seq, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type: durable.StorageWriteTypeDocumentChange,
				Id:   historicalDocumentId,
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentDelta,
					Version: 1,
					Ops:     chord.OpList{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(count)}},
				},
			},
		})
		if err != nil {
			return StorageBenchmarkDataset{}, err
		}
		recentAt = seq
	}

	if !hasFirstEntry {
		return StorageBenchmarkDataset{}, errors.New("Benchmark scale must create entries")
	}
	parentConversationId := durable.RootConversationId
	parentAt := firstEntryId
	deepestConversationId := durable.RootConversationId
	for depth := 0; depth < dtsbForkDepth; depth++ {
		rawConvId, err := storage.MintId()
		if err != nil {
			return StorageBenchmarkDataset{}, err
		}
		convId := durable.IdFromNumber[durable.ConversationId](rawConvId)
		if _, err := storage.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteConversation{
				Type: durable.StorageWriteTypeConversation,
				Value: durable.ConversationRecord{
					Id:     convId,
					Parent: &durable.ConversationParent{ConversationId: parentConversationId, At: parentAt},
				},
			},
		}); err != nil {
			return StorageBenchmarkDataset{}, err
		}

		writes := make([]durable.StorageWrite, dtsbEntriesPerFork)
		var lastEntryId durable.EntryId
		for i := 0; i < dtsbEntriesPerFork; i++ {
			rawEntryId, err := storage.MintId()
			if err != nil {
				return StorageBenchmarkDataset{}, err
			}
			eid := durable.IdFromNumber[durable.EntryId](rawEntryId)
			lastEntryId = eid
			writes[i] = durable.StorageWriteEntry{
				Type: durable.StorageWriteTypeEntry,
				Value: durable.EntryRecord{
					Id:             eid,
					ConversationId: convId,
					Kind:           "benchmark.fork",
					Data:           jsonx.ObjectOf("depth", float64(depth), "index", float64(i)),
				},
			}
		}
		if _, err := storage.Commit(ctx, writes); err != nil {
			return StorageBenchmarkDataset{}, err
		}
		parentConversationId = convId
		parentAt = lastEntryId
		deepestConversationId = convId
	}

	if !hasExactDoc {
		return StorageBenchmarkDataset{}, errors.New("Benchmark scale must create documents")
	}

	replayMap := make(map[int]durable.DocumentId, len(replayEntries))
	for _, re := range replayEntries {
		replayMap[re.tail] = re.id
	}
	filteredTaskCount := (scale.TaskCount + 59) / 60
	if filteredTaskCount > 50 {
		filteredTaskCount = 50
	}

	return StorageBenchmarkDataset{
		FirstEntryId:          firstEntryId,
		FilteredTaskCount:     filteredTaskCount,
		ExactDocumentId:       exactDocumentId,
		ExactDocumentKey:      fmt.Sprintf("key-%d", scale.DocumentCount-1),
		ReplayDocumentIds:     replayMap,
		HistoricalDocumentId:  historicalDocumentId,
		AncientAt:             ancientAt,
		RecentAt:              recentAt,
		DeepestConversationId: deepestConversationId,
		AncestorHeadEntryId:   firstEntryId,
	}, nil
}

// StorageReadBenchmark describes one named read benchmark and its expected scalar check.
type StorageReadBenchmark struct {
	Name     string
	Run      func(storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error)
	Expected func(dataset StorageBenchmarkDataset) int64
}

func dtsbReadReplayBenchmark(tail int) StorageReadBenchmark {
	return StorageReadBenchmark{
		Name: fmt.Sprintf("document replay tail (%d)", tail),
		Run: func(storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
			doc, err := storage.Document(chord.BackgroundContext, dataset.ReplayDocumentIds[tail], durable.DocumentPointCurrent())
			if err != nil || doc == nil {
				return -1, err
			}
			v, _ := doc.Value.GetNumber("count")
			return int64(v), nil
		},
		Expected: func(_ StorageBenchmarkDataset) int64 { return int64(tail) },
	}
}

// StorageReadBenchmarks lists the standard read benchmarks for a seeded storage dataset.
var StorageReadBenchmarks = func() []StorageReadBenchmark {
	list := []StorageReadBenchmark{
		{
			Name: "exact entry lookup",
			Run: func(storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
				stored, err := storage.Entry(chord.BackgroundContext, dataset.FirstEntryId)
				if err != nil || stored == nil {
					return -1, err
				}
				return int64(stored.Entry.Id), nil
			},
			Expected: func(dataset StorageBenchmarkDataset) int64 { return int64(dataset.FirstEntryId) },
		},
		{
			Name: "entry page scan (100)",
			Run: func(storage durable.Storage, _ StorageBenchmarkDataset) (int64, error) {
				page, err := storage.ScanEntries(chord.BackgroundContext, durable.EntryQuery{ConversationId: durable.RootConversationId}, 100, nil)
				if err != nil {
					return 0, err
				}
				return int64(len(page.Items)), nil
			},
			Expected: func(_ StorageBenchmarkDataset) int64 { return 100 },
		},
		{
			Name: "filtered task scan (50)",
			Run: func(storage durable.Storage, _ StorageBenchmarkDataset) (int64, error) {
				kind := "benchmark.filtered"
				bg := true
				page, err := storage.ScanTasks(chord.BackgroundContext, durable.TaskQuery{
					Kind:       &kind,
					Status:     durable.TaskStatePending,
					Background: &bg,
				}, 50, nil)
				if err != nil {
					return 0, err
				}
				return int64(len(page.Items)), nil
			},
			Expected: func(dataset StorageBenchmarkDataset) int64 { return int64(dataset.FilteredTaskCount) },
		},
		{
			Name: "exact document address among many",
			Run: func(storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
				doc, err := storage.FindDocument(
					chord.BackgroundContext,
					durable.DocumentAddress{
						Kind:  "benchmark.family",
						Key:   &dataset.ExactDocumentKey,
						Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
					},
					durable.DocumentPointCurrent(),
				)
				if err != nil || doc == nil {
					return -1, err
				}
				return int64(doc.Id), nil
			},
			Expected: func(dataset StorageBenchmarkDataset) int64 { return int64(dataset.ExactDocumentId) },
		},
	}
	for _, tail := range dtsbReplayTails {
		list = append(list, dtsbReadReplayBenchmark(tail))
	}
	list = append(list,
		StorageReadBenchmark{
			Name: "ancient historical read before newer base",
			Run: func(storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
				doc, err := storage.Document(chord.BackgroundContext, dataset.HistoricalDocumentId, durable.DocumentPointSeq(dataset.AncientAt))
				if err != nil || doc == nil {
					return -1, err
				}
				v, _ := doc.Value.GetNumber("count")
				return int64(v), nil
			},
			Expected: func(_ StorageBenchmarkDataset) int64 { return dtsbHistorySegmentLength },
		},
		StorageReadBenchmark{
			Name: "recent historical read after newer base",
			Run: func(storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
				doc, err := storage.Document(chord.BackgroundContext, dataset.HistoricalDocumentId, durable.DocumentPointSeq(dataset.RecentAt))
				if err != nil || doc == nil {
					return -1, err
				}
				v, _ := doc.Value.GetNumber("count")
				return int64(v), nil
			},
			Expected: func(_ StorageBenchmarkDataset) int64 { return dtsbHistorySegmentLength * 2 },
		},
		StorageReadBenchmark{
			Name: "fork-depth history scan (100)",
			Run: func(storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
				page, err := storage.ScanEntries(chord.BackgroundContext, durable.EntryQuery{ConversationId: dataset.DeepestConversationId}, 100, nil)
				if err != nil {
					return 0, err
				}
				return int64(len(page.Items)), nil
			},
			Expected: func(_ StorageBenchmarkDataset) int64 { return 100 },
		},
		StorageReadBenchmark{
			Name: "fork-depth head lookup",
			Run: func(storage durable.Storage, dataset StorageBenchmarkDataset) (int64, error) {
				marker, err := storage.FindLatestHeadMarker(chord.BackgroundContext, dataset.DeepestConversationId, nil)
				if err != nil || marker == nil {
					return -1, err
				}
				return int64(marker.Id), nil
			},
			Expected: func(dataset StorageBenchmarkDataset) int64 { return int64(dataset.AncestorHeadEntryId) },
		},
	)
	return list
}()

// StorageWriteBenchmark describes one named write benchmark and its expected write count.
type StorageWriteBenchmark struct {
	Name     string
	Expected int64
	Run      func(storage durable.Storage) (int64, error)
}

// SeedStorageWriteBenchmark seeds the baseline state expected by every write benchmark sample.
func SeedStorageWriteBenchmark(storage durable.Storage) error {
	ctx := chord.BackgroundContext
	if _, err := storage.Commit(ctx, []durable.StorageWrite{
		durable.StorageWriteConversation{
			Type:  durable.StorageWriteTypeConversation,
			Value: durable.ConversationRecord{Id: durable.RootConversationId},
		},
	}); err != nil {
		return err
	}
	writes := make([]durable.StorageWrite, 100)
	for i := 0; i < 100; i++ {
		rawId, err := storage.MintId()
		if err != nil {
			return err
		}
		writes[i] = durable.StorageWriteEntry{
			Type: durable.StorageWriteTypeEntry,
			Value: durable.EntryRecord{
				Id:             durable.IdFromNumber[durable.EntryId](rawId),
				ConversationId: durable.RootConversationId,
				Kind:           "benchmark.baseline",
				Data:           jsonx.ObjectOf("index", float64(i)),
			},
		}
	}
	_, err := storage.Commit(ctx, writes)
	return err
}

// StorageWriteBenchmarks lists the standard write benchmarks for storage backends.
var StorageWriteBenchmarks = []StorageWriteBenchmark{
	{
		Name:     "commit one entry",
		Expected: 1,
		Run: func(storage durable.Storage) (int64, error) {
			rawId, err := storage.MintId()
			if err != nil {
				return 0, err
			}
			_, err = storage.Commit(chord.BackgroundContext, []durable.StorageWrite{
				durable.StorageWriteEntry{
					Type: durable.StorageWriteTypeEntry,
					Value: durable.EntryRecord{
						Id:             durable.IdFromNumber[durable.EntryId](rawId),
						ConversationId: durable.RootConversationId,
						Kind:           "benchmark.write",
						Data:           jsonx.ObjectOf("text", strings.Repeat("x", 128)),
					},
				},
			})
			if err != nil {
				return 0, err
			}
			return 1, nil
		},
	},
	{
		Name:     "commit 100 entries",
		Expected: 100,
		Run: func(storage durable.Storage) (int64, error) {
			writes := make([]durable.StorageWrite, 100)
			pad := strings.Repeat("x", 128)
			for i := 0; i < 100; i++ {
				rawId, err := storage.MintId()
				if err != nil {
					return 0, err
				}
				writes[i] = durable.StorageWriteEntry{
					Type: durable.StorageWriteTypeEntry,
					Value: durable.EntryRecord{
						Id:             durable.IdFromNumber[durable.EntryId](rawId),
						ConversationId: durable.RootConversationId,
						Kind:           "benchmark.write",
						Data:           jsonx.ObjectOf("index", float64(i), "text", pad),
					},
				}
			}
			if _, err := storage.Commit(chord.BackgroundContext, writes); err != nil {
				return 0, err
			}
			return int64(len(writes)), nil
		},
	},
	{
		Name:     "commit mixed entry/task/submission/document",
		Expected: 4,
		Run: func(storage durable.Storage) (int64, error) {
			rawEntry, err := storage.MintId()
			if err != nil {
				return 0, err
			}
			rawTask, err := storage.MintId()
			if err != nil {
				return 0, err
			}
			rawSub, err := storage.MintId()
			if err != nil {
				return 0, err
			}
			rawDoc, err := storage.MintId()
			if err != nil {
				return 0, err
			}
			entryId := durable.IdFromNumber[durable.EntryId](rawEntry)
			taskId := durable.IdFromNumber[durable.TaskId](rawTask)
			subId := durable.IdFromNumber[durable.SubmissionId](rawSub)
			docId := durable.IdFromNumber[durable.DocumentId](rawDoc)
			reqId := fmt.Sprintf("benchmark-%d", subId)
			docKey := strconv.FormatInt(int64(docId), 10)
			writes := []durable.StorageWrite{
				durable.StorageWriteEntry{
					Type: durable.StorageWriteTypeEntry,
					Value: durable.EntryRecord{
						Id:             entryId,
						ConversationId: durable.RootConversationId,
						Kind:           "benchmark.mixed",
					},
				},
				durable.StorageWriteTask{
					Type:  durable.StorageWriteTypeTask,
					Value: dtsbTask(taskId, int(taskId)),
				},
				durable.StorageWriteSubmission{
					Type: durable.StorageWriteTypeSubmission,
					Value: durable.SubmissionRecord{
						Id:             subId,
						ConversationId: durable.RootConversationId,
						RequestId:      &reqId,
						Type:           durable.SubmissionTypeWrite,
						Status:         durable.SubmissionStatusDone,
						Entry:          &entryId,
					},
				},
				durable.StorageWriteDocumentCreate{
					Type: durable.StorageWriteTypeDocumentCreate,
					Record: durable.DocumentCreate{
						Id:    docId,
						Kind:  "benchmark.mixed",
						Key:   &docKey,
						Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
					},
					Content: durable.DocumentContent{
						Kind:    durable.DocumentContentBase,
						Version: 1,
						Value:   jsonx.ObjectOf("entryId", float64(entryId), "taskId", float64(taskId)),
					},
				},
			}
			if _, err := storage.Commit(chord.BackgroundContext, writes); err != nil {
				return 0, err
			}
			return int64(len(writes)), nil
		},
	},
}
