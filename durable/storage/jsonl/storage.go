// Ported from packages/durable/src/storage/jsonl/storage.ts (pi v1.0.0).

package jsonl

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	jlFormatVersion = 1
	jlMainFile      = "main.jsonl"
	jlReclaimSuffix = ".reclaim"
)

// JsonlStorageOptions configures durable JSONL log persistence.
type JsonlStorageOptions struct {
	// Fsync flushes every affected sidecar before appending the main commit marker. Defaults to false.
	Fsync bool
}

// JsonlCorruptionError indicates corrupt persisted records detected while opening or recovering a JSONL directory.
type JsonlCorruptionError struct {
	message string
	cause   error
}

// NewJsonlCorruptionError constructs a JsonlCorruptionError.
func NewJsonlCorruptionError(message string, cause error) *JsonlCorruptionError {
	return &JsonlCorruptionError{message: message, cause: cause}
}

func (e *JsonlCorruptionError) Name() string  { return "JsonlCorruptionError" }
func (e *JsonlCorruptionError) Error() string { return e.message }
func (e *JsonlCorruptionError) Unwrap() error { return e.cause }

// JsonlStoragePoisonedError indicates a write failure left JSONL storage poisoned until reopened.
type JsonlStoragePoisonedError struct {
	cause error
}

// NewJsonlStoragePoisonedError constructs a JsonlStoragePoisonedError.
func NewJsonlStoragePoisonedError(cause error) *JsonlStoragePoisonedError {
	return &JsonlStoragePoisonedError{cause: cause}
}

func (e *JsonlStoragePoisonedError) Name() string { return "JsonlStoragePoisonedError" }
func (e *JsonlStoragePoisonedError) Error() string {
	return "JSONL storage is poisoned and must be reopened"
}
func (e *JsonlStoragePoisonedError) Unwrap() error { return e.cause }

type jlMainOp interface {
	jlOpType() string
}

type jlMainOpWrite struct {
	Write durable.StorageWrite
}

func (o jlMainOpWrite) jlOpType() string { return string(o.Write.WriteType()) }

type jlMainOpTaskSidecar struct {
	Type    string         `json:"type"` // "task.sidecar"
	Id      durable.TaskId `json:"id"`
	Ordinal int            `json:"ordinal"`
}

func (o jlMainOpTaskSidecar) jlOpType() string { return "task.sidecar" }

type jlMainOpDocCreate struct {
	Type    string                 `json:"type"` // "document.create"
	Record  durable.DocumentCreate `json:"record"`
	Ordinal int                    `json:"ordinal"`
}

func (o jlMainOpDocCreate) jlOpType() string { return "document.create" }

type jlMainOpDocChange struct {
	Type    string             `json:"type"` // "document.change"
	Id      durable.DocumentId `json:"id"`
	Ordinal int                `json:"ordinal"`
}

func (o jlMainOpDocChange) jlOpType() string { return "document.change" }

type jlMainMarker struct {
	Format int
	Type   string
	Seq    durable.Seq
	Writes []jlMainOp
}

type jlSidecarPayload struct {
	Type    string
	Task    *durable.TaskRecord
	DocId   durable.DocumentId
	Content *durable.DocumentContent
}

type jlSidecarRecord struct {
	Format  int
	Type    string
	Seq     durable.Seq
	Ordinal int
	Payload jlSidecarPayload
}

type jlParsedLine[T any] struct {
	value T
	start int64
}

type jlParsedFile[T any] struct {
	path  string
	lines []jlParsedLine[T]
}

type jlEncodedCommit struct {
	marker       string
	sidecars     map[string]string
	sidecarOrder []string
}

type jlSidecarWrite struct {
	file    string
	content string
	path    string
}

// JsonlStorage implements durable.Storage backed by an append-only JSONL directory and sidecar logs.
type JsonlStorage struct {
	fs        env.FileSystem
	directory string
	mainPath  string
	fsync     bool
	memory    *storage.MemoryStorage

	commitMu sync.Mutex

	mu                   sync.Mutex
	currentOnlyDocuments map[durable.DocumentId]struct{}
	liveTaskSidecars     map[durable.TaskId]struct{}
	closed               bool
	poisonError          *JsonlStoragePoisonedError
}

// OpenJsonlStorage opens or creates a JSONL storage directory using the supplied FileSystem.
func OpenJsonlStorage(
	directory string,
	fs env.FileSystem,
	ctx chord.Context,
	options ...JsonlStorageOptions,
) (*JsonlStorage, error) {
	absRes := fs.AbsolutePath(directory, ctx)
	absPath, err := env.GetOrThrow(absRes)
	if err != nil {
		return nil, jlWrapFsErr("path resolution", absRes.Error)
	}
	recTrue := true
	mkdirRes := fs.CreateDir(absPath, &env.CreateDirOptions{Recursive: &recTrue}, ctx)
	if _, err := env.GetOrThrow(mkdirRes); err != nil {
		return nil, jlWrapFsErr("directory creation", mkdirRes.Error)
	}
	mainRes := fs.JoinPath([]string{absPath, jlMainFile}, ctx)
	mainPath, err := env.GetOrThrow(mainRes)
	if err != nil {
		return nil, jlWrapFsErr("path join", mainRes.Error)
	}
	var fsync bool
	if len(options) > 0 {
		fsync = options[0].Fsync
	}
	s := &JsonlStorage{
		fs:                   fs,
		directory:            absPath,
		mainPath:             mainPath,
		fsync:                fsync,
		memory:               storage.NewMemoryStorage(),
		currentOnlyDocuments: make(map[durable.DocumentId]struct{}),
		liveTaskSidecars:     make(map[durable.TaskId]struct{}),
	}
	if err := s.recover(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Commit validates, writes sidecar records and commit marker, applies to in-memory index, and best-effort reclaims sidecars.
func (s *JsonlStorage) Commit(ctx chord.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	s.commitMu.Lock()
	defer s.commitMu.Unlock()

	if err := s.assertUsable(); err != nil {
		return 0, err
	}
	prepared, err := s.memory.PrepareCommit(writes)
	if err != nil {
		return 0, err
	}
	encoded, err := s.encodeCommit(prepared.Seq, prepared.Writes)
	if err != nil {
		return 0, err
	}
	reclamations := s.planReclamations(prepared.Writes, encoded)

	sidecarFiles := append([]string(nil), encoded.sidecarOrder...)

	sidecars := make([]jlSidecarWrite, len(sidecarFiles))
	for i, file := range sidecarFiles {
		p, err := s.resolveFile(file, ctx)
		if err != nil {
			return 0, err
		}
		sidecars[i] = jlSidecarWrite{
			file:    file,
			content: encoded.sidecars[file],
			path:    p,
		}
	}

	for _, sc := range sidecars {
		res := s.fs.AppendFile(sc.path, []byte(sc.content), ctx)
		if !res.Ok {
			return 0, s.poison(jlWrapFsErr(fmt.Sprintf("append to %s", sc.file), res.Error))
		}
	}
	if s.fsync {
		for _, sc := range sidecars {
			res := s.fs.FlushFile(sc.path, ctx)
			if !res.Ok {
				return 0, s.poison(jlWrapFsErr(fmt.Sprintf("flush of %s", sc.file), res.Error))
			}
		}
	}
	markerRes := s.fs.AppendFile(s.mainPath, []byte(encoded.marker), ctx)
	if !markerRes.Ok {
		return 0, s.poison(jlWrapFsErr(fmt.Sprintf("append to %s", jlMainFile), markerRes.Error))
	}

	seq := prepared.Apply()
	s.adoptSidecarState(prepared.Writes)
	s.reclaimSidecars(reclamations, ctx)
	return seq, nil
}

// MintId returns a fresh monotonic ID.
func (s *JsonlStorage) MintId() (int64, error) {
	if err := s.assertUsable(); err != nil {
		return 0, err
	}
	return s.memory.MintId()
}

// Conversation fetches one conversation record by ID.
func (s *JsonlStorage) Conversation(ctx chord.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	return s.memory.Conversation(ctx, id)
}

// ScanConversations lists matching conversations in ascending ID order.
func (s *JsonlStorage) ScanConversations(ctx chord.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	if err := s.assertUsable(); err != nil {
		return durable.Page[durable.ConversationRecord, durable.Cursor]{}, err
	}
	return s.memory.ScanConversations(ctx, query, limit, cursor)
}

// Entry fetches a transcript entry by global ID along with its commit sequence.
func (s *JsonlStorage) Entry(ctx chord.Context, id durable.EntryId) (*durable.StoredEntry, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	return s.memory.Entry(ctx, id)
}

// EntryInConversation fetches entry id if visible from conversationId's fork-aware history.
func (s *JsonlStorage) EntryInConversation(ctx chord.Context, conversationId durable.ConversationId, id durable.EntryId) (*durable.StoredEntry, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	return s.memory.EntryInConversation(ctx, conversationId, id)
}

// FindLatestHeadMarker returns the newest visible entry with a Head marker at or before atOrBeforeEntryId.
func (s *JsonlStorage) FindLatestHeadMarker(ctx chord.Context, conversationId durable.ConversationId, atOrBeforeEntryId *durable.EntryId) (*durable.EntryRecord, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	return s.memory.FindLatestHeadMarker(ctx, conversationId, atOrBeforeEntryId)
}

// ScanEntries lists fork-aware entries for a conversation in descending ID order.
func (s *JsonlStorage) ScanEntries(ctx chord.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	if err := s.assertUsable(); err != nil {
		return durable.Page[durable.EntryRecord, durable.Cursor]{}, err
	}
	return s.memory.ScanEntries(ctx, query, limit, cursor)
}

// Task fetches a task record by ID.
func (s *JsonlStorage) Task(ctx chord.Context, id durable.TaskId) (*durable.TaskRecord, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	return s.memory.Task(ctx, id)
}

// ScanTasks lists matching tasks in ascending ID order.
func (s *JsonlStorage) ScanTasks(ctx chord.Context, query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[durable.TaskRecord, durable.Cursor], error) {
	if err := s.assertUsable(); err != nil {
		return durable.Page[durable.TaskRecord, durable.Cursor]{}, err
	}
	return s.memory.ScanTasks(ctx, query, limit, cursor)
}

// Submission fetches a submission record by ID.
func (s *JsonlStorage) Submission(ctx chord.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	return s.memory.Submission(ctx, id)
}

// ScanSubmissions lists matching submissions in ascending ID order.
func (s *JsonlStorage) ScanSubmissions(ctx chord.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	if err := s.assertUsable(); err != nil {
		return durable.Page[durable.SubmissionRecord, durable.Cursor]{}, err
	}
	return s.memory.ScanSubmissions(ctx, query, limit, cursor)
}

// SubmissionByRequest finds a submission carrying conversationId and requestId.
func (s *JsonlStorage) SubmissionByRequest(ctx chord.Context, conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	return s.memory.SubmissionByRequest(ctx, conversationId, requestId)
}

// FindDocument locates the document record at address alive at point at.
func (s *JsonlStorage) FindDocument(ctx chord.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	return s.memory.FindDocument(ctx, address, at)
}

// Document materializes the stored document value for id at point at.
func (s *JsonlStorage) Document(ctx chord.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	if err := s.assertUsable(); err != nil {
		return nil, err
	}
	return s.memory.Document(ctx, id, at)
}

// ScanDocuments lists matching documents alive at query.At in ascending ID order.
func (s *JsonlStorage) ScanDocuments(ctx chord.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	if err := s.assertUsable(); err != nil {
		return durable.Page[durable.DocumentRecord, durable.Cursor]{}, err
	}
	return s.memory.ScanDocuments(ctx, query, limit, cursor)
}

// Close closes the JSONL storage handle and its underlying in-memory store.
func (s *JsonlStorage) Close(ctx chord.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	return s.memory.Close(ctx)
}

func (s *JsonlStorage) encodeCommit(seq durable.Seq, writes []durable.StorageWrite) (jlEncodedCommit, error) {
	mainWrites := make([]any, 0, len(writes))
	records := make(map[string][]any)
	var sidecarOrder []string
	nextOrdinal := 0

	addSidecar := func(file string, payload any) int {
		ordinal := nextOrdinal
		nextOrdinal++
		rec := jsonx.ObjectOf(
			"format", float64(jlFormatVersion),
			"type", "record",
			"seq", float64(seq),
			"ordinal", float64(ordinal),
			"payload", payload,
		)
		if _, exists := records[file]; !exists {
			sidecarOrder = append(sidecarOrder, file)
		}
		records[file] = append(records[file], rec)
		return ordinal
	}

	for _, w := range writes {
		switch cw := w.(type) {
		case durable.StorageWriteConversation,
			durable.StorageWriteEntry,
			durable.StorageWriteSubmission,
			durable.StorageWriteDocumentRetire:
			mainWrites = append(mainWrites, cw)

		case durable.StorageWriteTask:
			if cw.Value.State.Status == durable.TaskStateTerminal {
				mainWrites = append(mainWrites, cw)
			} else {
				ordinal := addSidecar(jlSidecarFileName("task", int64(cw.Value.Id)), jsonx.ObjectOf(
					"type", "task",
					"value", cw.Value,
				))
				mainWrites = append(mainWrites, jlMainOpTaskSidecar{
					Type:    "task.sidecar",
					Id:      cw.Value.Id,
					Ordinal: ordinal,
				})
			}

		case durable.StorageWriteDocumentCreate:
			ordinal := addSidecar(jlSidecarFileName("doc", int64(cw.Record.Id)), jsonx.ObjectOf(
				"type", "document",
				"id", float64(cw.Record.Id),
				"content", cw.Content,
			))
			mainWrites = append(mainWrites, jlMainOpDocCreate{
				Type:    "document.create",
				Record:  cw.Record,
				Ordinal: ordinal,
			})

		case durable.StorageWriteDocumentChange:
			ordinal := addSidecar(jlSidecarFileName("doc", int64(cw.Id)), jsonx.ObjectOf(
				"type", "document",
				"id", float64(cw.Id),
				"content", cw.Content,
			))
			mainWrites = append(mainWrites, jlMainOpDocChange{
				Type:    "document.change",
				Id:      cw.Id,
				Ordinal: ordinal,
			})
		}
	}

	sidecars := make(map[string]string, len(records))
	for file, recs := range records {
		var b strings.Builder
		for _, rec := range recs {
			line, err := jlJsonLine(rec)
			if err != nil {
				return jlEncodedCommit{}, err
			}
			b.WriteString(line)
		}
		sidecars[file] = b.String()
	}

	markerObj := jsonx.ObjectOf(
		"format", float64(jlFormatVersion),
		"type", "commit",
		"seq", float64(seq),
		"writes", mainWrites,
	)
	markerLine, err := jlJsonLine(markerObj)
	if err != nil {
		return jlEncodedCommit{}, err
	}
	return jlEncodedCommit{
		marker:       markerLine,
		sidecars:     sidecars,
		sidecarOrder: sidecarOrder,
	}, nil
}

func (s *JsonlStorage) planReclamations(writes []durable.StorageWrite, encoded jlEncodedCommit) map[string]string {
	createdCurrentOnlyDocs := make(map[durable.DocumentId]struct{})
	retiredDocs := make(map[durable.DocumentId]struct{})
	baseDocs := make(map[durable.DocumentId]struct{})
	finalTasks := make(map[durable.TaskId]durable.TaskRecord)

	for _, w := range writes {
		switch cw := w.(type) {
		case durable.StorageWriteDocumentCreate:
			if jlIsCurrentOnlyCreate(cw.Record) {
				createdCurrentOnlyDocs[cw.Record.Id] = struct{}{}
			}
		case durable.StorageWriteDocumentChange:
			if cw.Content.Kind == durable.DocumentContentBase {
				baseDocs[cw.Id] = struct{}{}
			}
		case durable.StorageWriteDocumentRetire:
			retiredDocs[cw.Id] = struct{}{}
		case durable.StorageWriteTask:
			finalTasks[cw.Value.Id] = cw.Value
		}
	}

	replacements := make(map[string]string)
	s.mu.Lock()
	isCurrentOnlyDoc := func(id durable.DocumentId) bool {
		if _, ok := s.currentOnlyDocuments[id]; ok {
			return true
		}
		_, ok := createdCurrentOnlyDocs[id]
		return ok
	}
	for id := range retiredDocs {
		if isCurrentOnlyDoc(id) {
			replacements[jlSidecarFileName("doc", int64(id))] = ""
		}
	}
	for id := range baseDocs {
		if !isCurrentOnlyDoc(id) {
			continue
		}
		if _, retired := retiredDocs[id]; retired {
			continue
		}
		file := jlSidecarFileName("doc", int64(id))
		if content, ok := encoded.sidecars[file]; ok {
			replacements[file] = content
		}
	}
	for id, t := range finalTasks {
		if t.State.Status == durable.TaskStateTerminal {
			file := jlSidecarFileName("task", int64(id))
			_, wasLive := s.liveTaskSidecars[id]
			_, inBatch := encoded.sidecars[file]
			if wasLive || inBatch {
				replacements[file] = ""
			}
		}
	}
	s.mu.Unlock()
	return replacements
}

func (s *JsonlStorage) adoptSidecarState(writes []durable.StorageWrite) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range writes {
		switch cw := w.(type) {
		case durable.StorageWriteDocumentCreate:
			if jlIsCurrentOnlyCreate(cw.Record) {
				s.currentOnlyDocuments[cw.Record.Id] = struct{}{}
			}
		case durable.StorageWriteTask:
			if cw.Value.State.Status == durable.TaskStateTerminal {
				delete(s.liveTaskSidecars, cw.Value.Id)
			} else {
				s.liveTaskSidecars[cw.Value.Id] = struct{}{}
			}
		}
	}
}

func (s *JsonlStorage) reclaimSidecars(replacements map[string]string, ctx chord.Context) {
	if len(replacements) == 0 {
		return
	}
	if s.fsync {
		if res := s.fs.FlushFile(s.mainPath, ctx); !res.Ok {
			return
		}
	}
	files := make([]string, 0, len(replacements))
	for file := range replacements {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		s.replaceSidecar(file, replacements[file], ctx)
	}
}

func (s *JsonlStorage) replaceSidecar(file, content string, ctx chord.Context) {
	pathRes := s.fs.JoinPath([]string{s.directory, file}, ctx)
	if !pathRes.Ok {
		return
	}
	if content == "" {
		_ = s.fs.Remove(pathRes.Value, &env.RemoveOptions{Force: true}, ctx)
		return
	}
	tmpRes := s.fs.JoinPath([]string{s.directory, file + jlReclaimSuffix}, ctx)
	if !tmpRes.Ok {
		return
	}
	if res := s.fs.WriteFile(tmpRes.Value, []byte(content), ctx); !res.Ok {
		return
	}
	if s.fsync {
		if res := s.fs.FlushFile(tmpRes.Value, ctx); !res.Ok {
			return
		}
	}
	_ = s.fs.RenameFile(tmpRes.Value, pathRes.Value, ctx)
}

func (s *JsonlStorage) recover(ctx chord.Context) error {
	main, err := jlReadLines(s.fs, s.mainPath, jlMainFile, ctx, jlParseMainMarker)
	if err != nil {
		return err
	}
	prevSeq := int64(0)
	for _, line := range main.lines {
		if int64(line.value.Seq) <= prevSeq {
			return NewJsonlCorruptionError(fmt.Sprintf("Commit sequence does not strictly increase in %s", jlMainFile), nil)
		}
		prevSeq = int64(line.value.Seq)
	}

	listRes := s.fs.ListDir(s.directory, ctx)
	if !listRes.Ok {
		return jlWrapFsErr("directory listing", listRes.Error)
	}
	var sidecarFiles []string
	for _, info := range listRes.Value {
		if info.Kind != env.FileKindFile {
			continue
		}
		if jlIsReclaimFileName(info.Name) {
			_ = s.fs.Remove(info.Path, &env.RemoveOptions{Force: true}, ctx)
			continue
		}
		if jlIsSidecarFileName(info.Name) {
			sidecarFiles = append(sidecarFiles, info.Name)
		}
	}
	sort.Strings(sidecarFiles)

	parsedFiles := make(map[string]jlParsedFile[jlSidecarRecord], len(sidecarFiles))
	recordByKey := make(map[string]jlParsedLine[jlSidecarRecord])
	for _, file := range sidecarFiles {
		pathRes := s.fs.JoinPath([]string{s.directory, file}, ctx)
		if !pathRes.Ok {
			return jlWrapFsErr("path join", pathRes.Error)
		}
		parsed, err := jlReadLines(s.fs, pathRes.Value, file, ctx, func(text string, lineNum int) (jlSidecarRecord, error) {
			return jlParseSidecarRecord(text, file, lineNum)
		})
		if err != nil {
			return err
		}
		parsedFiles[file] = parsed
		var hasPrev bool
		var prev jlSidecarRecord
		for _, line := range parsed.lines {
			if hasPrev && (line.value.Seq < prev.Seq || (line.value.Seq == prev.Seq && line.value.Ordinal <= prev.Ordinal)) {
				return NewJsonlCorruptionError(fmt.Sprintf("Sidecar records are out of order in %s", file), nil)
			}
			hasPrev = true
			prev = line.value
			recordByKey[jlSidecarKey(file, line.value.Seq, line.value.Ordinal)] = line
		}
	}

	currentOnlyDocs := make(map[durable.DocumentId]struct{})
	retiredDocs := make(map[durable.DocumentId]struct{})
	finalTaskIsLive := make(map[durable.TaskId]bool)
	for _, line := range main.lines {
		for _, op := range line.value.Writes {
			switch cop := op.(type) {
			case jlMainOpDocCreate:
				if jlIsCurrentOnlyCreate(cop.Record) {
					currentOnlyDocs[cop.Record.Id] = struct{}{}
				}
			case jlMainOpWrite:
				switch cw := cop.Write.(type) {
				case durable.StorageWriteDocumentRetire:
					retiredDocs[cw.Id] = struct{}{}
				case durable.StorageWriteTask:
					finalTaskIsLive[cw.Value.Id] = false
				}
			case jlMainOpTaskSidecar:
				finalTaskIsLive[cop.Id] = true
			}
		}
	}

	retiredCurrentOnlyDocs := make(map[durable.DocumentId]struct{})
	for id := range retiredDocs {
		if _, ok := currentOnlyDocs[id]; ok {
			retiredCurrentOnlyDocs[id] = struct{}{}
		}
	}

	latestBases := make(map[durable.DocumentId]jlSidecarRecord)
	for _, line := range main.lines {
		marker := line.value
		for _, op := range marker.Writes {
			var docId durable.DocumentId
			var ordinal int
			switch cop := op.(type) {
			case jlMainOpDocCreate:
				docId = cop.Record.Id
				ordinal = cop.Ordinal
			case jlMainOpDocChange:
				docId = cop.Id
				ordinal = cop.Ordinal
			default:
				continue
			}
			if _, ok := currentOnlyDocs[docId]; !ok {
				continue
			}
			key := jlSidecarKey(jlSidecarFileName("doc", int64(docId)), marker.Seq, ordinal)
			found, ok := recordByKey[key]
			if !ok {
				continue
			}
			rec := found.value
			if rec.Payload.Type != "document" || rec.Payload.DocId != docId || rec.Payload.Content == nil || rec.Payload.Content.Kind != durable.DocumentContentBase {
				continue
			}
			prev, hasPrev := latestBases[docId]
			if !hasPrev || rec.Seq > prev.Seq || (rec.Seq == prev.Seq && rec.Ordinal > prev.Ordinal) {
				latestBases[docId] = rec
			}
		}
	}

	isBeforeLatestBase := func(id durable.DocumentId, seq durable.Seq, ordinal int) bool {
		base, ok := latestBases[id]
		if !ok {
			return false
		}
		return seq < base.Seq || (seq == base.Seq && ordinal < base.Ordinal)
	}

	terminalTasks := make(map[durable.TaskId]struct{})
	for id, live := range finalTaskIsLive {
		if !live {
			terminalTasks[id] = struct{}{}
		}
	}

	confirmed := make(map[string]struct{})
	for _, line := range main.lines {
		marker := line.value
		var writes []durable.StorageWrite
		for _, op := range marker.Writes {
			switch cop := op.(type) {
			case jlMainOpWrite:
				writes = append(writes, cop.Write)

			case jlMainOpTaskSidecar:
				_, optional := terminalTasks[cop.Id]
				rec, ok, err := jlConfirmRecord(marker, cop.Ordinal, jlSidecarFileName("task", int64(cop.Id)), recordByKey, confirmed, optional)
				if err != nil {
					return err
				}
				if ok {
					if rec.Payload.Type != "task" || rec.Payload.Task == nil || rec.Payload.Task.Id != cop.Id {
						return NewJsonlCorruptionError(fmt.Sprintf("Confirmed task sidecar data does not match commit %d", marker.Seq), nil)
					}
					if !optional {
						writes = append(writes, durable.StorageWriteTask{
							Type:  durable.StorageWriteTypeTask,
							Value: *rec.Payload.Task,
						})
					}
				}

			case jlMainOpDocCreate, jlMainOpDocChange:
				var id durable.DocumentId
				var ordinal int
				var isCreate bool
				var createRec durable.DocumentCreate
				if cc, ok := cop.(jlMainOpDocCreate); ok {
					id = cc.Record.Id
					ordinal = cc.Ordinal
					isCreate = true
					createRec = cc.Record
				} else {
					ch := cop.(jlMainOpDocChange)
					id = ch.Id
					ordinal = ch.Ordinal
				}
				_, isRetCurrentOnly := retiredCurrentOnlyDocs[id]
				reclaimed := isRetCurrentOnly || isBeforeLatestBase(id, marker.Seq, ordinal)
				rec, ok, err := jlConfirmRecord(marker, ordinal, jlSidecarFileName("doc", int64(id)), recordByKey, confirmed, reclaimed)
				if err != nil {
					return err
				}
				var content *durable.DocumentContent
				if ok {
					if rec.Payload.Type != "document" || rec.Payload.DocId != id || rec.Payload.Content == nil {
						return NewJsonlCorruptionError(fmt.Sprintf("Confirmed document sidecar data does not match commit %d", marker.Seq), nil)
					}
					content = rec.Payload.Content
				}
				if isCreate {
					if content != nil && content.Kind != durable.DocumentContentBase {
						return NewJsonlCorruptionError(fmt.Sprintf("Document creation lacks a confirmed base in commit %d", marker.Seq), nil)
					}
					effectiveContent := durable.DocumentContent{
						Kind:    durable.DocumentContentBase,
						Version: 1,
						Value:   jsonx.NewObject(),
					}
					if !reclaimed && content != nil {
						effectiveContent = *content
					}
					writes = append(writes, durable.StorageWriteDocumentCreate{
						Type:    durable.StorageWriteTypeDocumentCreate,
						Record:  createRec,
						Content: effectiveContent,
					})
				} else if !reclaimed && content != nil {
					writes = append(writes, durable.StorageWriteDocumentChange{
						Type:    durable.StorageWriteTypeDocumentChange,
						Id:      id,
						Content: *content,
					})
				}
			}
		}
		prepared, err := s.memory.PrepareCommit(writes, marker.Seq)
		if err != nil {
			return NewJsonlCorruptionError(fmt.Sprintf("Invalid committed state at sequence %d", marker.Seq), err)
		}
		prepared.Apply()
	}

	reclamations := make(map[string]string)
	for _, file := range sidecarFiles {
		parsed := parsedFiles[file]
		unconfirmedAt := int64(-1)
		for _, line := range parsed.lines {
			key := jlSidecarKey(file, line.value.Seq, line.value.Ordinal)
			if _, isConf := confirmed[key]; isConf {
				if unconfirmedAt >= 0 {
					return NewJsonlCorruptionError(fmt.Sprintf("Confirmed record follows an unconfirmed tail in %s", file), nil)
				}
			} else if unconfirmedAt < 0 {
				unconfirmedAt = line.start
			}
		}
		if unconfirmedAt >= 0 {
			res := s.fs.TruncateFile(parsed.path, unconfirmedAt, ctx)
			if !res.Ok {
				return jlWrapFsErr(fmt.Sprintf("tail truncation of %s", file), res.Error)
			}
		}

		dashIdx := strings.IndexByte(file, '-')
		numStr := file[dashIdx+1 : len(file)-len(".jsonl")]
		numericId, _ := strconv.ParseInt(numStr, 10, 64)
		var confirmedLines []jlParsedLine[jlSidecarRecord]
		for _, line := range parsed.lines {
			if _, ok := confirmed[jlSidecarKey(file, line.value.Seq, line.value.Ordinal)]; ok {
				confirmedLines = append(confirmedLines, line)
			}
		}

		var retainedLines []jlParsedLine[jlSidecarRecord]
		hasRetained := false
		if strings.HasPrefix(file, "task-") {
			tid := durable.IdFromNumber[durable.TaskId](numericId)
			if _, ok := terminalTasks[tid]; ok {
				hasRetained = true
				retainedLines = nil
			}
		} else if strings.HasPrefix(file, "doc-") {
			did := durable.IdFromNumber[durable.DocumentId](numericId)
			if _, ok := retiredCurrentOnlyDocs[did]; ok {
				hasRetained = true
				retainedLines = nil
			} else if _, ok := latestBases[did]; ok {
				hasRetained = true
				for _, line := range confirmedLines {
					if !isBeforeLatestBase(did, line.value.Seq, line.value.Ordinal) {
						retainedLines = append(retainedLines, line)
					}
				}
			}
		}
		if hasRetained && (len(retainedLines) < len(confirmedLines) || len(retainedLines) == 0) {
			var b strings.Builder
			for _, line := range retainedLines {
				encodedLine, err := jlEncodeSidecarRecord(line.value)
				if err != nil {
					return err
				}
				b.WriteString(encodedLine)
			}
			reclamations[file] = b.String()
		}
	}
	s.reclaimSidecars(reclamations, ctx)

	s.mu.Lock()
	for id := range currentOnlyDocs {
		s.currentOnlyDocuments[id] = struct{}{}
	}
	for id, live := range finalTaskIsLive {
		if live {
			s.liveTaskSidecars[id] = struct{}{}
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *JsonlStorage) resolveFile(file string, ctx chord.Context) (string, error) {
	res := s.fs.JoinPath([]string{s.directory, file}, ctx)
	if !res.Ok {
		return "", jlWrapFsErr("path join", res.Error)
	}
	return res.Value, nil
}

func (s *JsonlStorage) poison(cause error) *JsonlStoragePoisonedError {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.poisonError == nil {
		s.poisonError = NewJsonlStoragePoisonedError(cause)
	}
	return s.poisonError
}

func (s *JsonlStorage) assertUsable() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("JsonlStorage is closed")
	}
	if s.poisonError != nil {
		return s.poisonError
	}
	return nil
}

func jlConfirmRecord(
	marker jlMainMarker,
	ordinal int,
	file string,
	recordByKey map[string]jlParsedLine[jlSidecarRecord],
	confirmed map[string]struct{},
	optional bool,
) (jlSidecarRecord, bool, error) {
	key := jlSidecarKey(file, marker.Seq, ordinal)
	if _, dup := confirmed[key]; dup {
		return jlSidecarRecord{}, false, NewJsonlCorruptionError("Sidecar record is confirmed more than once", nil)
	}
	line, ok := recordByKey[key]
	if !ok {
		if optional {
			return jlSidecarRecord{}, false, nil
		}
		return jlSidecarRecord{}, false, NewJsonlCorruptionError(fmt.Sprintf("Missing confirmed sidecar record %s at sequence %d", file, marker.Seq), nil)
	}
	confirmed[key] = struct{}{}
	return line.value, true, nil
}

func jlReadLines[T any](
	fs env.FileSystem,
	path string,
	name string,
	ctx chord.Context,
	parse func(text string, lineNum int) (T, error),
) (jlParsedFile[T], error) {
	readRes := fs.ReadBinaryFile(path, ctx)
	if !readRes.Ok {
		if readRes.Error != nil && readRes.Error.Code == env.FileErrorNotFound {
			return jlParsedFile[T]{path: path}, nil
		}
		return jlParsedFile[T]{}, jlWrapFsErr(fmt.Sprintf("read of %s", name), readRes.Error)
	}
	raw := readRes.Value
	completeSize := len(raw)
	if completeSize > 0 && raw[completeSize-1] != '\n' {
		lastNL := bytes.LastIndexByte(raw, '\n')
		completeSize = lastNL + 1 // 0 when lastNL == -1
		truncRes := fs.TruncateFile(path, int64(completeSize), ctx)
		if !truncRes.Ok {
			return jlParsedFile[T]{}, jlWrapFsErr(fmt.Sprintf("torn-line truncation of %s", name), truncRes.Error)
		}
	}

	var lines []jlParsedLine[T]
	start := 0
	lineNumber := 1
	for end := 0; end < completeSize; end++ {
		if raw[end] != '\n' {
			continue
		}
		slice := raw[start:end]
		if !utf8.Valid(slice) {
			return jlParsedFile[T]{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid UTF-8 in complete %s line %d", name, lineNumber), nil)
		}
		val, err := parse(string(slice), lineNumber)
		if err != nil {
			return jlParsedFile[T]{}, err
		}
		lines = append(lines, jlParsedLine[T]{
			value: val,
			start: int64(start),
		})
		start = end + 1
		lineNumber++
	}
	return jlParsedFile[T]{
		path:  path,
		lines: lines,
	}, nil
}

func jlParseJsonValue(text string, description string) (any, error) {
	val, err := jsonx.ParseString(text)
	if err != nil {
		return nil, NewJsonlCorruptionError(fmt.Sprintf("Malformed complete %s", description), err)
	}
	return val, nil
}

func jlIsSafeNonNegInt(v any) (int64, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f {
		return 0, false
	}
	if f < 0 || f > 9007199254740991 {
		return 0, false
	}
	return int64(f), true
}

func jlIsSafeInt(v any) (int64, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f {
		return 0, false
	}
	if f < -9007199254740991 || f > 9007199254740991 {
		return 0, false
	}
	return int64(f), true
}

func jlParseMainMarker(text string, lineNum int) (jlMainMarker, error) {
	desc := fmt.Sprintf("%s line %d", jlMainFile, lineNum)
	raw, err := jlParseJsonValue(text, desc)
	if err != nil {
		return jlMainMarker{}, err
	}
	obj, ok := raw.(*jsonx.Object)
	if !ok || obj == nil {
		return jlMainMarker{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid commit marker in %s", desc), nil)
	}
	fmtV, _ := obj.Get("format")
	fmtInt, okFmt := jlIsSafeInt(fmtV)
	typeV, _ := obj.Get("type")
	seqV, _ := obj.Get("seq")
	seqInt, okSeq := jlIsSafeInt(seqV)
	writesV, _ := obj.Get("writes")
	writesArr, okWrites := writesV.([]any)
	if !okFmt || fmtInt != jlFormatVersion || typeV != "commit" || !okSeq || seqInt < 1 || !okWrites {
		return jlMainMarker{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid commit marker in %s", desc), nil)
	}
	ops := make([]jlMainOp, len(writesArr))
	for i, w := range writesArr {
		op, err := jlValidateMainOp(w, desc)
		if err != nil {
			return jlMainMarker{}, err
		}
		ops[i] = op
	}
	return jlMainMarker{
		Format: jlFormatVersion,
		Type:   "commit",
		Seq:    durable.SeqFromNumber(seqInt),
		Writes: ops,
	}, nil
}

func jlValidateMainOp(v any, desc string) (jlMainOp, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid write in %s", desc), nil)
	}
	typV, _ := obj.Get("type")
	typ, okTyp := typV.(string)
	if !okTyp {
		return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid write in %s", desc), nil)
	}
	switch typ {
	case "conversation":
		valV, _ := obj.Get("value")
		valObj, okVal := valV.(*jsonx.Object)
		idV := any(nil)
		if okVal && valObj != nil {
			idV, _ = valObj.Get("id")
		}
		if _, okId := jlIsSafeInt(idV); !okVal || valObj == nil || !okId {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid %s write in %s", typ, desc), nil)
		}
		var out durable.StorageWriteConversation
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid %s write in %s", typ, desc), err)
		}
		return jlMainOpWrite{Write: out}, nil

	case "entry":
		valV, _ := obj.Get("value")
		valObj, okVal := valV.(*jsonx.Object)
		idV := any(nil)
		if okVal && valObj != nil {
			idV, _ = valObj.Get("id")
		}
		if _, okId := jlIsSafeInt(idV); !okVal || valObj == nil || !okId {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid %s write in %s", typ, desc), nil)
		}
		var out durable.StorageWriteEntry
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid %s write in %s", typ, desc), err)
		}
		return jlMainOpWrite{Write: out}, nil

	case "submission":
		valV, _ := obj.Get("value")
		valObj, okVal := valV.(*jsonx.Object)
		idV := any(nil)
		if okVal && valObj != nil {
			idV, _ = valObj.Get("id")
		}
		if _, okId := jlIsSafeInt(idV); !okVal || valObj == nil || !okId {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid %s write in %s", typ, desc), nil)
		}
		var out durable.StorageWriteSubmission
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid %s write in %s", typ, desc), err)
		}
		return jlMainOpWrite{Write: out}, nil

	case "task":
		valV, _ := obj.Get("value")
		valObj, okVal := valV.(*jsonx.Object)
		var idV, stateV, statusV any
		if okVal && valObj != nil {
			idV, _ = valObj.Get("id")
			stateV, _ = valObj.Get("state")
		}
		stateObj, okState := stateV.(*jsonx.Object)
		if okState && stateObj != nil {
			statusV, _ = stateObj.Get("status")
		}
		_, okId := jlIsSafeInt(idV)
		if !okVal || valObj == nil || !okId || !okState || stateObj == nil || statusV != "terminal" {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid terminal task write in %s", desc), nil)
		}
		var out durable.StorageWriteTask
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid terminal task write in %s", desc), err)
		}
		return jlMainOpWrite{Write: out}, nil

	case "document.retire":
		idV, _ := obj.Get("id")
		if _, okId := jlIsSafeInt(idV); !okId {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid document retirement in %s", desc), nil)
		}
		var out durable.StorageWriteDocumentRetire
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid document retirement in %s", desc), err)
		}
		return jlMainOpWrite{Write: out}, nil

	case "task.sidecar":
		idV, _ := obj.Get("id")
		ordV, _ := obj.Get("ordinal")
		_, okId := jlIsSafeInt(idV)
		_, okOrd := jlIsSafeNonNegInt(ordV)
		if !okId || !okOrd {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid task sidecar write in %s", desc), nil)
		}
		var out jlMainOpTaskSidecar
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid task sidecar write in %s", desc), err)
		}
		return out, nil

	case "document.create":
		recV, _ := obj.Get("record")
		recObj, okRec := recV.(*jsonx.Object)
		idV := any(nil)
		if okRec && recObj != nil {
			idV, _ = recObj.Get("id")
		}
		ordV, _ := obj.Get("ordinal")
		_, okId := jlIsSafeInt(idV)
		_, okOrd := jlIsSafeNonNegInt(ordV)
		if !okRec || recObj == nil || !okId || !okOrd {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid document creation in %s", desc), nil)
		}
		var out jlMainOpDocCreate
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid document creation in %s", desc), err)
		}
		return out, nil

	case "document.change":
		idV, _ := obj.Get("id")
		ordV, _ := obj.Get("ordinal")
		_, okId := jlIsSafeInt(idV)
		_, okOrd := jlIsSafeNonNegInt(ordV)
		if !okId || !okOrd {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid document change in %s", desc), nil)
		}
		var out jlMainOpDocChange
		if err := jsonx.Decode(obj, &out); err != nil {
			return nil, NewJsonlCorruptionError(fmt.Sprintf("Invalid document change in %s", desc), err)
		}
		return out, nil

	default:
		return nil, NewJsonlCorruptionError(fmt.Sprintf("Unknown write type in %s", desc), nil)
	}
}

func jlValidateDocumentContent(v any, desc string) (durable.DocumentContent, error) {
	obj, ok := v.(*jsonx.Object)
	if !ok || obj == nil {
		return durable.DocumentContent{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid document content in %s", desc), nil)
	}
	verV, _ := obj.Get("version")
	verInt, okVer := jlIsSafeInt(verV)
	if !okVer || verInt < 1 {
		return durable.DocumentContent{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid document content in %s", desc), nil)
	}
	kindV, _ := obj.Get("kind")
	if kindV == "base" {
		valV, _ := obj.Get("value")
		if valObj, okVal := valV.(*jsonx.Object); okVal && valObj != nil {
			var out durable.DocumentContent
			if err := jsonx.Decode(obj, &out); err != nil {
				return durable.DocumentContent{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid document content in %s", desc), err)
			}
			return out, nil
		}
	} else if kindV == "delta" {
		opsV, _ := obj.Get("ops")
		if _, okOps := opsV.([]any); okOps {
			var out durable.DocumentContent
			if err := jsonx.Decode(obj, &out); err != nil {
				return durable.DocumentContent{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid document content in %s", desc), err)
			}
			return out, nil
		}
	}
	return durable.DocumentContent{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid document content in %s", desc), nil)
}

func jlParseSidecarRecord(text string, file string, lineNum int) (jlSidecarRecord, error) {
	desc := fmt.Sprintf("%s line %d", file, lineNum)
	raw, err := jlParseJsonValue(text, desc)
	if err != nil {
		return jlSidecarRecord{}, err
	}
	obj, ok := raw.(*jsonx.Object)
	if !ok || obj == nil {
		return jlSidecarRecord{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid sidecar record in %s", desc), nil)
	}
	fmtV, _ := obj.Get("format")
	fmtInt, okFmt := jlIsSafeInt(fmtV)
	typeV, _ := obj.Get("type")
	seqV, _ := obj.Get("seq")
	seqInt, okSeq := jlIsSafeInt(seqV)
	ordV, _ := obj.Get("ordinal")
	ordInt, okOrd := jlIsSafeNonNegInt(ordV)
	payloadV, _ := obj.Get("payload")
	payloadObj, okPayload := payloadV.(*jsonx.Object)
	pTypeV := any(nil)
	if okPayload && payloadObj != nil {
		pTypeV, _ = payloadObj.Get("type")
	}
	pType, okPType := pTypeV.(string)
	if !okFmt || fmtInt != jlFormatVersion || typeV != "record" || !okSeq || seqInt < 1 || !okOrd || !okPayload || payloadObj == nil || !okPType {
		return jlSidecarRecord{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid sidecar record in %s", desc), nil)
	}

	out := jlSidecarRecord{
		Format:  jlFormatVersion,
		Type:    "record",
		Seq:     durable.SeqFromNumber(seqInt),
		Ordinal: int(ordInt),
		Payload: jlSidecarPayload{Type: pType},
	}

	switch pType {
	case "task":
		valV, _ := payloadObj.Get("value")
		valObj, okVal := valV.(*jsonx.Object)
		var idV, stateV, statusV any
		if okVal && valObj != nil {
			idV, _ = valObj.Get("id")
			stateV, _ = valObj.Get("state")
		}
		stateObj, okState := stateV.(*jsonx.Object)
		if okState && stateObj != nil {
			statusV, _ = stateObj.Get("status")
		}
		_, okId := jlIsSafeInt(idV)
		if !okVal || valObj == nil || !okId || !okState || stateObj == nil || statusV == "terminal" {
			return jlSidecarRecord{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid live task record in %s", desc), nil)
		}
		var t durable.TaskRecord
		if err := jsonx.Decode(valObj, &t); err != nil {
			return jlSidecarRecord{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid live task record in %s", desc), err)
		}
		out.Payload.Task = &t

	case "document":
		idV, _ := payloadObj.Get("id")
		idInt, okId := jlIsSafeInt(idV)
		if !okId {
			return jlSidecarRecord{}, NewJsonlCorruptionError(fmt.Sprintf("Invalid document record in %s", desc), nil)
		}
		contentV, _ := payloadObj.Get("content")
		content, err := jlValidateDocumentContent(contentV, desc)
		if err != nil {
			return jlSidecarRecord{}, err
		}
		out.Payload.DocId = durable.IdFromNumber[durable.DocumentId](idInt)
		out.Payload.Content = &content

	default:
		return jlSidecarRecord{}, NewJsonlCorruptionError(fmt.Sprintf("Unknown sidecar record type in %s", desc), nil)
	}
	return out, nil
}

func jlEncodeSidecarRecord(rec jlSidecarRecord) (string, error) {
	var payload any
	if rec.Payload.Type == "task" {
		payload = jsonx.ObjectOf("type", "task", "value", *rec.Payload.Task)
	} else {
		payload = jsonx.ObjectOf("type", "document", "id", float64(rec.Payload.DocId), "content", *rec.Payload.Content)
	}
	return jlJsonLine(jsonx.ObjectOf(
		"format", float64(rec.Format),
		"type", rec.Type,
		"seq", float64(rec.Seq),
		"ordinal", float64(rec.Ordinal),
		"payload", payload,
	))
}

func jlSidecarFileName(kind string, id int64) string {
	return fmt.Sprintf("%s-%d.jsonl", kind, id)
}

func jlIsCanonicalNonNegDigits(s string) bool {
	if s == "" {
		return false
	}
	if s == "0" {
		return true
	}
	if s[0] < '1' || s[0] > '9' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func jlIsSidecarFileName(name string) bool {
	var rest string
	switch {
	case strings.HasPrefix(name, "doc-"):
		rest = name[len("doc-"):]
	case strings.HasPrefix(name, "task-"):
		rest = name[len("task-"):]
	default:
		return false
	}
	if !strings.HasSuffix(rest, ".jsonl") {
		return false
	}
	return jlIsCanonicalNonNegDigits(rest[:len(rest)-len(".jsonl")])
}

func jlIsReclaimFileName(name string) bool {
	if !strings.HasSuffix(name, jlReclaimSuffix) {
		return false
	}
	return jlIsSidecarFileName(name[:len(name)-len(jlReclaimSuffix)])
}

func jlIsCurrentOnlyCreate(rec durable.DocumentCreate) bool {
	return rec.Scope.Kind != durable.DocumentScopeConversation || rec.History == durable.DocumentHistoryLatest
}

func jlSidecarKey(file string, seq durable.Seq, ordinal int) string {
	s, _ := jsonx.Stringify([]any{file, float64(seq), float64(ordinal)})
	return s
}

func jlJsonLine(v any) (string, error) {
	s, err := jsonx.Stringify(v)
	if err != nil {
		return "", err
	}
	return s + "\n", nil
}

func jlWrapFsErr(action string, err *env.FileError) error {
	return fmt.Errorf("JSONL %s failed: %w", action, err)
}

var _ durable.Storage = (*JsonlStorage)(nil)
