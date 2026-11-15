// Ported from packages/durable/test/jsonl-storage.test.ts (pi v1.0.0).

package jsonl_test

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/durabletest"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/durable/storage/jsonl"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tjstReopeningStorage struct {
	mu        sync.Mutex
	current   *jsonl.JsonlStorage
	directory string
	closed    bool
}

func tjstNewReopeningStorage(directory string) (*tjstReopeningStorage, error) {
	ctx := chord.BackgroundContext
	cur, err := jsonl.OpenJsonlStorage(directory, env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: directory}), ctx)
	if err != nil {
		return nil, err
	}
	return &tjstReopeningStorage{current: cur, directory: directory}, nil
}

func (r *tjstReopeningStorage) Commit(ctx chord.Context, writes []durable.StorageWrite) (durable.Seq, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return r.current.Commit(ctx, writes)
	}
	seq, err := r.current.Commit(ctx, writes)
	_ = r.current.Close(chord.BackgroundContext)
	reopened, reopenErr := jsonl.OpenJsonlStorage(r.directory, env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: r.directory}), chord.BackgroundContext)
	if reopenErr != nil {
		if err != nil {
			return 0, err
		}
		return 0, reopenErr
	}
	r.current = reopened
	return seq, err
}

func (r *tjstReopeningStorage) MintId() (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.MintId()
}
func (r *tjstReopeningStorage) Conversation(ctx chord.Context, id durable.ConversationId) (*durable.ConversationRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Conversation(ctx, id)
}
func (r *tjstReopeningStorage) ScanConversations(ctx chord.Context, query durable.ConversationQuery, limit int, cursor durable.Cursor) (durable.Page[durable.ConversationRecord, durable.Cursor], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.ScanConversations(ctx, query, limit, cursor)
}
func (r *tjstReopeningStorage) Entry(ctx chord.Context, id durable.EntryId) (*durable.StoredEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Entry(ctx, id)
}
func (r *tjstReopeningStorage) EntryInConversation(ctx chord.Context, conversationId durable.ConversationId, id durable.EntryId) (*durable.StoredEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.EntryInConversation(ctx, conversationId, id)
}
func (r *tjstReopeningStorage) FindLatestHeadMarker(ctx chord.Context, conversationId durable.ConversationId, atOrBeforeEntryId *durable.EntryId) (*durable.EntryRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.FindLatestHeadMarker(ctx, conversationId, atOrBeforeEntryId)
}
func (r *tjstReopeningStorage) ScanEntries(ctx chord.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.ScanEntries(ctx, query, limit, cursor)
}
func (r *tjstReopeningStorage) Task(ctx chord.Context, id durable.TaskId) (*durable.TaskRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Task(ctx, id)
}
func (r *tjstReopeningStorage) ScanTasks(ctx chord.Context, query durable.TaskQuery, limit int, cursor durable.Cursor) (durable.Page[durable.TaskRecord, durable.Cursor], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.ScanTasks(ctx, query, limit, cursor)
}
func (r *tjstReopeningStorage) Submission(ctx chord.Context, id durable.SubmissionId) (*durable.SubmissionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Submission(ctx, id)
}
func (r *tjstReopeningStorage) ScanSubmissions(ctx chord.Context, query durable.SubmissionQuery, limit int, cursor durable.Cursor) (durable.Page[durable.SubmissionRecord, durable.Cursor], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.ScanSubmissions(ctx, query, limit, cursor)
}
func (r *tjstReopeningStorage) SubmissionByRequest(ctx chord.Context, conversationId durable.ConversationId, requestId string) (*durable.SubmissionRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.SubmissionByRequest(ctx, conversationId, requestId)
}
func (r *tjstReopeningStorage) FindDocument(ctx chord.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.FindDocument(ctx, address, at)
}
func (r *tjstReopeningStorage) Document(ctx chord.Context, id durable.DocumentId, at durable.DocumentPoint) (*durable.StoredDocument, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.Document(ctx, id, at)
}
func (r *tjstReopeningStorage) ScanDocuments(ctx chord.Context, query durable.DocumentQuery, limit int, cursor durable.Cursor) (durable.Page[durable.DocumentRecord, durable.Cursor], error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current.ScanDocuments(ctx, query, limit, cursor)
}
func (r *tjstReopeningStorage) Close(ctx chord.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return r.current.Close(ctx)
}

type tjstFailure struct {
	operation string
	call      int
	mode      string
}

type tjstInstrumentedEnv struct {
	*env.NodeExecutionEnv

	mu          sync.Mutex
	operations  []string
	failure     *tjstFailure
	appendCalls int
	flushCalls  int
	writeCalls  int
	renameCalls int
	removeCalls int
}

func tjstNewInstrumentedEnv(cwd string) *tjstInstrumentedEnv {
	return &tjstInstrumentedEnv{
		NodeExecutionEnv: env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: cwd}),
	}
}

func (e *tjstInstrumentedEnv) fail(f tjstFailure) {
	e.mu.Lock()
	defer e.mu.Unlock()
	copy := f
	e.failure = &copy
	e.resetObservationsLocked()
}

func (e *tjstInstrumentedEnv) clear() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failure = nil
	e.resetObservationsLocked()
}

func (e *tjstInstrumentedEnv) resetObservationsLocked() {
	e.appendCalls = 0
	e.flushCalls = 0
	e.writeCalls = 0
	e.renameCalls = 0
	e.removeCalls = 0
	e.operations = nil
}

func (e *tjstInstrumentedEnv) opsSnapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.operations...)
}

func (e *tjstInstrumentedEnv) AppendFile(path string, content []byte, ctx chord.Context) env.Result[struct{}, *env.FileError] {
	e.mu.Lock()
	e.appendCalls++
	call := e.appendCalls
	e.operations = append(e.operations, "append:"+filepath.Base(path))
	failure := e.failure
	e.mu.Unlock()

	if failure == nil || failure.operation != "append" || failure.call != call {
		return e.NodeExecutionEnv.AppendFile(path, content, ctx)
	}
	if failure.mode == "before" {
		return env.Err[struct{}, *env.FileError](env.NewFileError(env.FileErrorUnknown, "injected append failure", path, nil))
	}
	if failure.mode == "short" {
		n := len(content) / 2
		if n < 1 {
			n = 1
		}
		if len(content) < n {
			n = len(content)
		}
		res := e.NodeExecutionEnv.AppendFile(path, content[:n], ctx)
		if !res.Ok {
			return res
		}
		return env.Err[struct{}, *env.FileError](env.NewFileError(env.FileErrorUnknown, "injected short append", path, nil))
	}
	res := e.NodeExecutionEnv.AppendFile(path, content, ctx)
	if !res.Ok {
		return res
	}
	return env.Err[struct{}, *env.FileError](env.NewFileError(env.FileErrorUnknown, "injected post-append failure", path, nil))
}

func (e *tjstInstrumentedEnv) FlushFile(path string, ctx chord.Context) env.Result[struct{}, *env.FileError] {
	e.mu.Lock()
	e.flushCalls++
	call := e.flushCalls
	e.operations = append(e.operations, "flush:"+filepath.Base(path))
	failure := e.failure
	e.mu.Unlock()

	if failure == nil || failure.operation != "flush" || failure.call != call {
		return e.NodeExecutionEnv.FlushFile(path, ctx)
	}
	if failure.mode == "after" {
		res := e.NodeExecutionEnv.FlushFile(path, ctx)
		if !res.Ok {
			return res
		}
	}
	return env.Err[struct{}, *env.FileError](env.NewFileError(env.FileErrorUnknown, "injected flush failure", path, nil))
}

func (e *tjstInstrumentedEnv) WriteFile(path string, content []byte, ctx chord.Context) env.Result[struct{}, *env.FileError] {
	e.mu.Lock()
	e.writeCalls++
	call := e.writeCalls
	e.operations = append(e.operations, "write:"+filepath.Base(path))
	failure := e.failure
	e.mu.Unlock()

	if failure == nil || failure.operation != "write" || failure.call != call {
		return e.NodeExecutionEnv.WriteFile(path, content, ctx)
	}
	if failure.mode == "before" {
		return env.Err[struct{}, *env.FileError](env.NewFileError(env.FileErrorUnknown, "injected write failure", path, nil))
	}
	if failure.mode == "short" {
		n := len(content) / 2
		if n < 1 {
			n = 1
		}
		if len(content) < n {
			n = len(content)
		}
		res := e.NodeExecutionEnv.WriteFile(path, content[:n], ctx)
		if !res.Ok {
			return res
		}
		return env.Err[struct{}, *env.FileError](env.NewFileError(env.FileErrorUnknown, "injected short write", path, nil))
	}
	res := e.NodeExecutionEnv.WriteFile(path, content, ctx)
	if !res.Ok {
		return res
	}
	return env.Err[struct{}, *env.FileError](env.NewFileError(env.FileErrorUnknown, "injected post-write failure", path, nil))
}

func (e *tjstInstrumentedEnv) RenameFile(sourcePath string, destinationPath string, ctx chord.Context) env.Result[struct{}, *env.FileError] {
	e.mu.Lock()
	e.renameCalls++
	call := e.renameCalls
	e.operations = append(e.operations, "rename:"+filepath.Base(sourcePath)+"->"+filepath.Base(destinationPath))
	failure := e.failure
	e.mu.Unlock()

	if failure == nil || failure.operation != "rename" || failure.call != call {
		return e.NodeExecutionEnv.RenameFile(sourcePath, destinationPath, ctx)
	}
	if failure.mode == "after" {
		res := e.NodeExecutionEnv.RenameFile(sourcePath, destinationPath, ctx)
		if !res.Ok {
			return res
		}
	}
	return env.Err[struct{}, *env.FileError](env.NewFileError(env.FileErrorUnknown, "injected rename failure", sourcePath, nil))
}

func (e *tjstInstrumentedEnv) Remove(path string, options *env.RemoveOptions, ctx chord.Context) env.Result[struct{}, *env.FileError] {
	e.mu.Lock()
	e.removeCalls++
	call := e.removeCalls
	e.operations = append(e.operations, "remove:"+filepath.Base(path))
	failure := e.failure
	e.mu.Unlock()

	if failure == nil || failure.operation != "remove" || failure.call != call {
		return e.NodeExecutionEnv.Remove(path, options, ctx)
	}
	if failure.mode == "after" {
		res := e.NodeExecutionEnv.Remove(path, options, ctx)
		if !res.Ok {
			return res
		}
	}
	return env.Err[struct{}, *env.FileError](env.NewFileError(env.FileErrorUnknown, "injected remove failure", path, nil))
}

func tjstPendingTask(id durable.TaskId, phase ...string) durable.TaskRecord {
	p := "ready"
	if len(phase) > 0 {
		p = phase[0]
	}
	return durable.TaskRecord{
		Id:             id,
		ConversationId: durable.RootConversationId,
		Kind:           "test.task",
		Version:        1,
		Input:          nil,
		State: durable.TaskState{
			Status:     durable.TaskStatePending,
			Checkpoint: jsonx.ObjectOf("phase", p),
		},
	}
}

func tjstTerminalTask(id durable.TaskId) durable.TaskRecord {
	return durable.TaskRecord{
		Id:             id,
		ConversationId: durable.RootConversationId,
		Kind:           "test.task",
		Version:        1,
		Input:          nil,
		State: durable.TaskState{
			Status: durable.TaskStateTerminal,
			Outcome: &durable.TaskOutcome{
				Status: durable.TaskOutcomeCompleted,
				Result: jsonx.Null[durable.JsonValue](),
			},
		},
	}
}

func tjstCreateRoot(t *testing.T, storage durable.Storage) {
	t.Helper()
	if _, err := storage.Commit(chord.BackgroundContext, []durable.StorageWrite{
		durable.StorageWriteConversation{
			Type:  durable.StorageWriteTypeConversation,
			Value: durable.ConversationRecord{Id: durable.RootConversationId},
		},
	}); err != nil {
		t.Fatalf("root commit failed: %v", err)
	}
}

func tjstSessionDoc(id durable.DocumentId, kind ...string) durable.DocumentCreate {
	k := "test.document"
	if len(kind) > 0 {
		k = kind[0]
	}
	return durable.DocumentCreate{
		Id:    id,
		Kind:  k,
		Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
	}
}

func tjstOpen(t *testing.T, directory string, fs env.FileSystem, opts ...jsonl.JsonlStorageOptions) *jsonl.JsonlStorage {
	t.Helper()
	if fs == nil {
		fs = env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: directory})
	}
	st, err := jsonl.OpenJsonlStorage(directory, fs, chord.BackgroundContext, opts...)
	if err != nil {
		t.Fatalf("OpenJsonlStorage failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(chord.BackgroundContext) })
	return st
}

func tjstReadLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFile %s failed: %v", path, err)
	}
	s := string(raw)
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func tjstFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func tjstReclaimFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s failed: %v", dir, err)
	}
	var out []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".reclaim") {
			out = append(out, entry.Name())
		}
	}
	return out
}

func TestJsonlStorageConformance(t *testing.T) {
	ctx := chord.BackgroundContext

	durabletest.RegisterStorageConformance(t, "JsonlStorage", func(use func(durable.Storage) error) error {
		dir, err := os.MkdirTemp("", "pi-durable-jsonl-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		st, err := jsonl.OpenNodeJsonlStorage(dir, ctx)
		if err != nil {
			return err
		}
		defer func() { _ = st.Close(ctx) }()
		return use(st)
	})

	durabletest.RegisterStorageConformance(t, "JsonlStorage across reopen", func(use func(durable.Storage) error) error {
		dir, err := os.MkdirTemp("", "pi-durable-jsonl-reopen-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		st, err := tjstNewReopeningStorage(dir)
		if err != nil {
			return err
		}
		defer func() { _ = st.Close(ctx) }()
		return use(st)
	})
}

func TestJsonlStoragePublicationAndRecovery(t *testing.T) {
	ctx := chord.BackgroundContext

	t.Run("opens through the Node adapter", func(t *testing.T) {
		dir := t.TempDir()
		st, err := jsonl.OpenNodeJsonlStorage(dir, ctx)
		if err != nil {
			t.Fatalf("OpenNodeJsonlStorage failed: %v", err)
		}
		defer func() { _ = st.Close(ctx) }()
		tjstCreateRoot(t, st)
		conv, err := st.Conversation(ctx, durable.RootConversationId)
		if err != nil || conv == nil || conv.Id != durable.RootConversationId {
			t.Fatalf("unexpected root conversation: %+v err=%v", conv, err)
		}
	})

	t.Run("publishes every commit before reclaiming current-only document and terminal-task sidecars", func(t *testing.T) {
		dir := t.TempDir()
		st := tjstOpen(t, dir, nil)
		tjstCreateRoot(t, st)
		taskRaw, _ := st.MintId()
		docRaw, _ := st.MintId()
		taskId := durable.IdFromNumber[durable.TaskId](taskRaw)
		docId := durable.IdFromNumber[durable.DocumentId](docRaw)

		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstPendingTask(taskId)},
		}); err != nil {
			t.Fatalf("task commit failed: %v", err)
		}
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentCreate{
				Type:    durable.StorageWriteTypeDocumentCreate,
				Record:  tjstSessionDoc(docId),
				Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(0))},
			},
		}); err != nil {
			t.Fatalf("doc create failed: %v", err)
		}
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type:    durable.StorageWriteTypeDocumentChange,
				Id:      docId,
				Content: durable.DocumentContent{Kind: durable.DocumentContentDelta, Version: 1},
			},
		}); err != nil {
			t.Fatalf("doc delta failed: %v", err)
		}
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type:    durable.StorageWriteTypeDocumentChange,
				Id:      docId,
				Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(1))},
			},
		}); err != nil {
			t.Fatalf("doc base failed: %v", err)
		}

		if lines := tjstReadLines(t, filepath.Join(dir, fmt.Sprintf("task-%d.jsonl", taskId))); len(lines) != 1 {
			t.Fatalf("expected 1 task line, got %d", len(lines))
		}
		if lines := tjstReadLines(t, filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId))); len(lines) != 1 {
			t.Fatalf("expected 1 doc line, got %d", len(lines))
		}

		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: docId},
		}); err != nil {
			t.Fatalf("doc retire failed: %v", err)
		}
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstTerminalTask(taskId)},
		}); err != nil {
			t.Fatalf("task terminal failed: %v", err)
		}

		mainLines := tjstReadLines(t, filepath.Join(dir, "main.jsonl"))
		if len(mainLines) != 7 {
			t.Fatalf("expected 7 main lines, got %d", len(mainLines))
		}
		if tjstFileExists(filepath.Join(dir, fmt.Sprintf("task-%d.jsonl", taskId))) {
			t.Fatalf("expected task sidecar reclaimed")
		}
		if tjstFileExists(filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId))) {
			t.Fatalf("expected doc sidecar reclaimed")
		}
		for i, line := range mainLines {
			parsed, err := jsonx.ParseString(line)
			if err != nil {
				t.Fatalf("parse main line %d failed: %v", i, err)
			}
			typ, _ := parsed.(*jsonx.Object).GetString("type")
			if typ != "commit" {
				t.Fatalf("expected main marker type commit at %d, got %q", i, typ)
			}
		}
	})

	t.Run("orders multiple live-task replacements in one sidecar by commit ordinal", func(t *testing.T) {
		dir := t.TempDir()
		st := tjstOpen(t, dir, nil)
		tjstCreateRoot(t, st)
		rawTask, _ := st.MintId()
		taskId := durable.IdFromNumber[durable.TaskId](rawTask)

		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstPendingTask(taskId, "first")},
			durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstPendingTask(taskId, "second")},
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
		if lines := tjstReadLines(t, filepath.Join(dir, fmt.Sprintf("task-%d.jsonl", taskId))); len(lines) != 2 {
			t.Fatalf("expected 2 sidecar lines, got %d", len(lines))
		}
		reopened := tjstOpen(t, dir, nil)
		got, err := reopened.Task(ctx, taskId)
		if err != nil || got == nil || !jsonx.DeepEqual(*got, tjstPendingTask(taskId, "second")) {
			t.Fatalf("expected phase second, got %+v err=%v", got, err)
		}
	})

	t.Run("removes a complete-plus-torn unconfirmed multi-record sidecar append", func(t *testing.T) {
		dir := t.TempDir()
		ie := tjstNewInstrumentedEnv(dir)
		st := tjstOpen(t, dir, ie)
		tjstCreateRoot(t, st)
		rawTask, _ := st.MintId()
		taskId := durable.IdFromNumber[durable.TaskId](rawTask)

		ie.fail(tjstFailure{operation: "append", call: 1, mode: "short"})
		_, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstPendingTask(taskId, "first")},
			durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstPendingTask(taskId, "second-"+strings.Repeat("x", 512))},
		})
		if err == nil || !strings.Contains(err.Error(), "poisoned") {
			t.Fatalf("expected poisoned error, got %v", err)
		}

		sidecarPath := filepath.Join(dir, fmt.Sprintf("task-%d.jsonl", taskId))
		partial, err := os.ReadFile(sidecarPath)
		if err != nil {
			t.Fatalf("read partial sidecar failed: %v", err)
		}
		if len(partial) == 0 || partial[len(partial)-1] == '\n' {
			t.Fatalf("expected partial sidecar without trailing LF")
		}
		var lfCount int
		for _, b := range partial {
			if b == '\n' {
				lfCount++
			}
		}
		if lfCount != 1 {
			t.Fatalf("expected 1 LF in partial sidecar, got %d", lfCount)
		}

		reopened := tjstOpen(t, dir, nil)
		got, err := reopened.Task(ctx, taskId)
		if err != nil || got != nil {
			t.Fatalf("expected nil recovered task, got %+v err=%v", got, err)
		}
		info, err := os.Stat(sidecarPath)
		if err != nil || info.Size() != 0 {
			t.Fatalf("expected 0-byte truncated sidecar, size=%v err=%v", info, err)
		}
	})

	t.Run("serializes the complete candidate before I/O and leaves preparation failures usable", func(t *testing.T) {
		dir := t.TempDir()
		ie := tjstNewInstrumentedEnv(dir)
		st := tjstOpen(t, dir, ie)
		tjstCreateRoot(t, st)
		ie.clear()

		rawId, _ := st.MintId()
		entryId := durable.IdFromNumber[durable.EntryId](rawId)

		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteEntry{
				Type: durable.StorageWriteTypeEntry,
				Value: durable.EntryRecord{
					Id:             entryId,
					ConversationId: durable.RootConversationId,
					Kind:           "bad",
					Data:           jsonx.ObjectOf("bad", big.NewInt(1)),
				},
			},
		}); err == nil {
			t.Fatalf("expected preparation failure on non-JSON data")
		}
		if len(ie.opsSnapshot()) != 0 {
			t.Fatalf("expected zero I/O operations on preparation failure, got %v", ie.opsSnapshot())
		}
		if got, err := st.Entry(ctx, entryId); err != nil || got != nil {
			t.Fatalf("expected nil entry, got %+v err=%v", got, err)
		}
		seq, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteEntry{
				Type:  durable.StorageWriteTypeEntry,
				Value: durable.EntryRecord{Id: entryId, ConversationId: durable.RootConversationId, Kind: "good"},
			},
		})
		if err != nil || seq != 2 {
			t.Fatalf("expected usable storage returning seq 2, got %d err=%v", seq, err)
		}
	})

	appendFailures := []tjstFailure{
		{operation: "append", call: 1, mode: "before"},
		{operation: "append", call: 1, mode: "after"},
		{operation: "append", call: 1, mode: "short"},
		{operation: "append", call: 2, mode: "before"},
		{operation: "append", call: 2, mode: "after"},
		{operation: "append", call: 3, mode: "before"},
		{operation: "append", call: 3, mode: "short"},
		{operation: "append", call: 3, mode: "after"},
	}
	for _, failure := range appendFailures {
		t.Run(fmt.Sprintf("poisons after %s failure at append %d and recovers only confirmed state", failure.mode, failure.call), func(t *testing.T) {
			dir := t.TempDir()
			ie := tjstNewInstrumentedEnv(dir)
			st := tjstOpen(t, dir, ie)
			tjstCreateRoot(t, st)
			raw1, _ := st.MintId()
			raw2, _ := st.MintId()
			firstId := durable.IdFromNumber[durable.DocumentId](raw1)
			secondId := durable.IdFromNumber[durable.DocumentId](raw2)

			ie.fail(failure)
			_, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  tjstSessionDoc(firstId, "first"),
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("text", "α")},
				},
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  tjstSessionDoc(secondId, "second"),
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("text", "β")},
				},
			})
			if err == nil || !strings.Contains(err.Error(), "poisoned") {
				t.Fatalf("expected poisoned error, got %v", err)
			}
			_, readErr := st.Document(ctx, firstId, durable.DocumentPointCurrent())
			if readErr == nil || !strings.Contains(readErr.Error(), "poisoned") {
				t.Fatalf("expected poisoned read error, got %v", readErr)
			}

			reopened := tjstOpen(t, dir, nil)
			markerSurvived := failure.call == 3 && failure.mode == "after"
			d1, err1 := reopened.Document(ctx, firstId, durable.DocumentPointCurrent())
			d2, err2 := reopened.Document(ctx, secondId, durable.DocumentPointCurrent())
			if err1 != nil || err2 != nil {
				t.Fatalf("reopened read failed: err1=%v err2=%v", err1, err2)
			}
			if markerSurvived {
				if d1 == nil || !jsonx.DeepEqual(d1.Value, jsonx.ObjectOf("text", "α")) {
					t.Fatalf("expected first doc α, got %+v", d1)
				}
				if d2 == nil || !jsonx.DeepEqual(d2.Value, jsonx.ObjectOf("text", "β")) {
					t.Fatalf("expected second doc β, got %+v", d2)
				}
			} else {
				if d1 != nil || d2 != nil {
					t.Fatalf("expected unconfirmed docs nil, got d1=%+v d2=%+v", d1, d2)
				}
			}
		})
	}

	flushFailures := []tjstFailure{
		{operation: "flush", call: 1, mode: "before"},
		{operation: "flush", call: 1, mode: "after"},
		{operation: "flush", call: 2, mode: "before"},
		{operation: "flush", call: 2, mode: "after"},
	}
	for _, failure := range flushFailures {
		t.Run(fmt.Sprintf("poisons after %s failure at flush %d and never writes a marker", failure.mode, failure.call), func(t *testing.T) {
			dir := t.TempDir()
			ie := tjstNewInstrumentedEnv(dir)
			st := tjstOpen(t, dir, ie, jsonl.JsonlStorageOptions{Fsync: true})
			tjstCreateRoot(t, st)
			raw1, _ := st.MintId()
			raw2, _ := st.MintId()
			firstId := durable.IdFromNumber[durable.DocumentId](raw1)
			secondId := durable.IdFromNumber[durable.DocumentId](raw2)

			ie.fail(failure)
			_, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  tjstSessionDoc(firstId, "flush.first"),
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.NewObject()},
				},
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  tjstSessionDoc(secondId, "flush.second"),
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.NewObject()},
				},
			})
			if err == nil || !strings.Contains(err.Error(), "poisoned") {
				t.Fatalf("expected poisoned error, got %v", err)
			}
			reopened := tjstOpen(t, dir, nil)
			if d1, err := reopened.Document(ctx, firstId, durable.DocumentPointCurrent()); err != nil || d1 != nil {
				t.Fatalf("expected nil firstId, got %+v err=%v", d1, err)
			}
			if d2, err := reopened.Document(ctx, secondId, durable.DocumentPointCurrent()); err != nil || d2 != nil {
				t.Fatalf("expected nil secondId, got %+v err=%v", d2, err)
			}
		})
	}

	t.Run("orders publication flushes exactly and flushes main only to authorize reclamation", func(t *testing.T) {
		for _, fsync := range []bool{false, true} {
			dir := t.TempDir()
			ie := tjstNewInstrumentedEnv(dir)
			st := tjstOpen(t, dir, ie, jsonl.JsonlStorageOptions{Fsync: fsync})
			tjstCreateRoot(t, st)
			raw1, _ := st.MintId()
			raw2, _ := st.MintId()
			firstId := durable.IdFromNumber[durable.DocumentId](raw1)
			secondId := durable.IdFromNumber[durable.DocumentId](raw2)

			ie.clear()
			if _, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  tjstSessionDoc(secondId, "second"),
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.NewObject()},
				},
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  tjstSessionDoc(firstId, "first"),
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.NewObject()},
				},
			}); err != nil {
				t.Fatalf("create failed: %v", err)
			}
			expected := []string{
				fmt.Sprintf("append:doc-%d.jsonl", secondId),
				fmt.Sprintf("append:doc-%d.jsonl", firstId),
			}
			if fsync {
				expected = append(expected, fmt.Sprintf("flush:doc-%d.jsonl", secondId), fmt.Sprintf("flush:doc-%d.jsonl", firstId))
			}
			expected = append(expected, "append:main.jsonl")
			if !reflect.DeepEqual(ie.opsSnapshot(), expected) {
				t.Fatalf("fsync=%v create ops mismatch: want %v, got %v", fsync, expected, ie.opsSnapshot())
			}

			ie.clear()
			if _, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type:    durable.StorageWriteTypeDocumentChange,
					Id:      firstId,
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("checkpoint", true)},
				},
			}); err != nil {
				t.Fatalf("base change failed: %v", err)
			}
			expectedReclaim := []string{fmt.Sprintf("append:doc-%d.jsonl", firstId)}
			if fsync {
				expectedReclaim = append(expectedReclaim, fmt.Sprintf("flush:doc-%d.jsonl", firstId))
			}
			expectedReclaim = append(expectedReclaim, "append:main.jsonl")
			if fsync {
				expectedReclaim = append(expectedReclaim, "flush:main.jsonl")
			}
			expectedReclaim = append(expectedReclaim, fmt.Sprintf("write:doc-%d.jsonl.reclaim", firstId))
			if fsync {
				expectedReclaim = append(expectedReclaim, fmt.Sprintf("flush:doc-%d.jsonl.reclaim", firstId))
			}
			expectedReclaim = append(expectedReclaim, fmt.Sprintf("rename:doc-%d.jsonl.reclaim->doc-%d.jsonl", firstId, firstId))
			if !reflect.DeepEqual(ie.opsSnapshot(), expectedReclaim) {
				t.Fatalf("fsync=%v reclaim ops mismatch: want %v, got %v", fsync, expectedReclaim, ie.opsSnapshot())
			}

			ie.clear()
			entryRaw, _ := st.MintId()
			if _, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteEntry{
					Type: durable.StorageWriteTypeEntry,
					Value: durable.EntryRecord{
						Id:             durable.IdFromNumber[durable.EntryId](entryRaw),
						ConversationId: durable.RootConversationId,
						Kind:           "main-only",
					},
				},
			}); err != nil {
				t.Fatalf("main-only failed: %v", err)
			}
			if !reflect.DeepEqual(ie.opsSnapshot(), []string{"append:main.jsonl"}) {
				t.Fatalf("expected only append:main.jsonl, got %v", ie.opsSnapshot())
			}

			taskRaw, _ := st.MintId()
			taskId := durable.IdFromNumber[durable.TaskId](taskRaw)
			if _, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstPendingTask(taskId)},
			}); err != nil {
				t.Fatalf("pending task failed: %v", err)
			}
			ie.clear()
			if _, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstTerminalTask(taskId)},
			}); err != nil {
				t.Fatalf("terminal task failed: %v", err)
			}
			expectedTerm := []string{"append:main.jsonl"}
			if fsync {
				expectedTerm = append(expectedTerm, "flush:main.jsonl")
			}
			expectedTerm = append(expectedTerm, fmt.Sprintf("remove:task-%d.jsonl", taskId))
			if !reflect.DeepEqual(ie.opsSnapshot(), expectedTerm) {
				t.Fatalf("fsync=%v terminal ops mismatch: want %v, got %v", fsync, expectedTerm, ie.opsSnapshot())
			}
		}
	})

	reclaimFailures := []tjstFailure{
		{operation: "write", call: 1, mode: "before"},
		{operation: "write", call: 1, mode: "short"},
		{operation: "write", call: 1, mode: "after"},
		{operation: "rename", call: 1, mode: "before"},
		{operation: "rename", call: 1, mode: "after"},
	}
	for _, failure := range reclaimFailures {
		t.Run(fmt.Sprintf("recovers a committed base across reclaim %s %s", failure.operation, failure.mode), func(t *testing.T) {
			dir := t.TempDir()
			ie := tjstNewInstrumentedEnv(dir)
			st := tjstOpen(t, dir, ie)
			tjstCreateRoot(t, st)
			rawId, _ := st.MintId()
			docId := durable.IdFromNumber[durable.DocumentId](rawId)

			if _, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  tjstSessionDoc(docId),
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(0))},
				},
			}); err != nil {
				t.Fatalf("create failed: %v", err)
			}
			if _, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type: durable.StorageWriteTypeDocumentChange,
					Id:   docId,
					Content: durable.DocumentContent{
						Kind:    durable.DocumentContentDelta,
						Version: 1,
						Ops:     []chord.Op{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(1)}},
					},
				},
			}); err != nil {
				t.Fatalf("delta failed: %v", err)
			}

			ie.fail(failure)
			seq, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type:    durable.StorageWriteTypeDocumentChange,
					Id:      docId,
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(2))},
				},
			})
			if err != nil || seq != 4 {
				t.Fatalf("expected committed seq 4 despite reclaim failure, got %d err=%v", seq, err)
			}
			doc, err := st.Document(ctx, docId, durable.DocumentPointCurrent())
			if err != nil || doc == nil || !jsonx.DeepEqual(doc.Value, jsonx.ObjectOf("count", float64(2))) {
				t.Fatalf("unexpected doc value: %+v err=%v", doc, err)
			}
			if err := st.Close(ctx); err != nil {
				t.Fatalf("close failed: %v", err)
			}

			reopened := tjstOpen(t, dir, nil)
			rdoc, err := reopened.Document(ctx, docId, durable.DocumentPointCurrent())
			if err != nil || rdoc == nil || !jsonx.DeepEqual(rdoc.Value, jsonx.ObjectOf("count", float64(2))) {
				t.Fatalf("unexpected reopened value: %+v err=%v", rdoc, err)
			}
			if lines := tjstReadLines(t, filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId))); len(lines) != 1 {
				t.Fatalf("expected 1 reclaimed line, got %d", len(lines))
			}
			if reclaims := tjstReclaimFiles(t, dir); len(reclaims) != 0 {
				t.Fatalf("expected no leftover .reclaim files, got %v", reclaims)
			}
		})
	}

	for _, mode := range []string{"before", "after"} {
		t.Run("recovers document-retirement reclamation across remove "+mode, func(t *testing.T) {
			dir := t.TempDir()
			ie := tjstNewInstrumentedEnv(dir)
			st := tjstOpen(t, dir, ie)
			tjstCreateRoot(t, st)
			taskRaw, _ := st.MintId()
			docRaw, _ := st.MintId()
			taskId := durable.IdFromNumber[durable.TaskId](taskRaw)
			docId := durable.IdFromNumber[durable.DocumentId](docRaw)

			if _, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstPendingTask(taskId)},
				durable.StorageWriteDocumentCreate{
					Type: durable.StorageWriteTypeDocumentCreate,
					Record: durable.DocumentCreate{
						Id:    docId,
						Kind:  "task.document",
						Scope: durable.DocumentScope{Kind: durable.DocumentScopeTask, TaskId: taskId},
					},
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(1))},
				},
			}); err != nil {
				t.Fatalf("create failed: %v", err)
			}
			ie.fail(tjstFailure{operation: "remove", call: 1, mode: mode})
			seq, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: docId},
			})
			if err != nil || seq != 3 {
				t.Fatalf("expected seq 3, got %d err=%v", seq, err)
			}
			if d, err := st.Document(ctx, docId, durable.DocumentPointCurrent()); err != nil || d != nil {
				t.Fatalf("expected retired document nil, got %+v err=%v", d, err)
			}
			_ = st.Close(ctx)

			reopened := tjstOpen(t, dir, nil)
			if d, err := reopened.Document(ctx, docId, durable.DocumentPointCurrent()); err != nil || d != nil {
				t.Fatalf("expected retired document nil after reopen, got %+v err=%v", d, err)
			}
			if task, err := reopened.Task(ctx, taskId); err != nil || task == nil || !jsonx.DeepEqual(*task, tjstPendingTask(taskId)) {
				t.Fatalf("expected pending task preserved, got %+v err=%v", task, err)
			}
			if tjstFileExists(filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId))) {
				t.Fatalf("expected doc sidecar removed on reopen")
			}
			if reclaims := tjstReclaimFiles(t, dir); len(reclaims) != 0 {
				t.Fatalf("expected no .reclaim files, got %v", reclaims)
			}
		})
	}

	for _, mode := range []string{"before", "after"} {
		t.Run("defers reclamation after authorizing-main flush "+mode+" failure", func(t *testing.T) {
			dir := t.TempDir()
			ie := tjstNewInstrumentedEnv(dir)
			st := tjstOpen(t, dir, ie, jsonl.JsonlStorageOptions{Fsync: true})
			tjstCreateRoot(t, st)
			rawId, _ := st.MintId()
			docId := durable.IdFromNumber[durable.DocumentId](rawId)

			if _, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  tjstSessionDoc(docId),
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(0))},
				},
			}); err != nil {
				t.Fatalf("create failed: %v", err)
			}
			ie.fail(tjstFailure{operation: "flush", call: 2, mode: mode})
			seq, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type:    durable.StorageWriteTypeDocumentChange,
					Id:      docId,
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(2))},
				},
			})
			if err != nil || seq != 3 {
				t.Fatalf("expected seq 3, got %d err=%v", seq, err)
			}
			wantOps := []string{
				fmt.Sprintf("append:doc-%d.jsonl", docId),
				fmt.Sprintf("flush:doc-%d.jsonl", docId),
				"append:main.jsonl",
				"flush:main.jsonl",
			}
			if !reflect.DeepEqual(ie.opsSnapshot(), wantOps) {
				t.Fatalf("expected ops %v, got %v", wantOps, ie.opsSnapshot())
			}
			if d, err := st.Document(ctx, docId, durable.DocumentPointCurrent()); err != nil || d == nil || !jsonx.DeepEqual(d.Value, jsonx.ObjectOf("count", float64(2))) {
				t.Fatalf("unexpected doc value: %+v err=%v", d, err)
			}
			if lines := tjstReadLines(t, filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId))); len(lines) != 2 {
				t.Fatalf("expected 2 lines while reclaim deferred, got %d", len(lines))
			}
			_ = st.Close(ctx)

			recEnv := tjstNewInstrumentedEnv(dir)
			recEnv.fail(tjstFailure{operation: "flush", call: 1, mode: mode})
			deferred := tjstOpen(t, dir, recEnv, jsonl.JsonlStorageOptions{Fsync: true})
			if d, err := deferred.Document(ctx, docId, durable.DocumentPointCurrent()); err != nil || d == nil || !jsonx.DeepEqual(d.Value, jsonx.ObjectOf("count", float64(2))) {
				t.Fatalf("unexpected deferred value: %+v err=%v", d, err)
			}
			if !reflect.DeepEqual(recEnv.opsSnapshot(), []string{"flush:main.jsonl"}) {
				t.Fatalf("expected only flush:main.jsonl, got %v", recEnv.opsSnapshot())
			}
			if lines := tjstReadLines(t, filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId))); len(lines) != 2 {
				t.Fatalf("expected 2 lines before main flush succeeds, got %d", len(lines))
			}
			_ = deferred.Close(ctx)

			reclaimed := tjstOpen(t, dir, nil, jsonl.JsonlStorageOptions{Fsync: true})
			if d, err := reclaimed.Document(ctx, docId, durable.DocumentPointCurrent()); err != nil || d == nil || !jsonx.DeepEqual(d.Value, jsonx.ObjectOf("count", float64(2))) {
				t.Fatalf("unexpected reclaimed value: %+v err=%v", d, err)
			}
			if lines := tjstReadLines(t, filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId))); len(lines) != 1 {
				t.Fatalf("expected 1 reclaimed line, got %d", len(lines))
			}
		})
	}

	for _, mode := range []string{"before", "after"} {
		t.Run("keeps a committed base usable after reclaim-temp flush "+mode+" failure", func(t *testing.T) {
			dir := t.TempDir()
			ie := tjstNewInstrumentedEnv(dir)
			st := tjstOpen(t, dir, ie, jsonl.JsonlStorageOptions{Fsync: true})
			tjstCreateRoot(t, st)
			rawId, _ := st.MintId()
			docId := durable.IdFromNumber[durable.DocumentId](rawId)

			if _, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentCreate{
					Type:    durable.StorageWriteTypeDocumentCreate,
					Record:  tjstSessionDoc(docId),
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(0))},
				},
			}); err != nil {
				t.Fatalf("create failed: %v", err)
			}
			ie.fail(tjstFailure{operation: "flush", call: 3, mode: mode})
			if seq, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type:    durable.StorageWriteTypeDocumentChange,
					Id:      docId,
					Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(2))},
				},
			}); err != nil || seq != 3 {
				t.Fatalf("expected seq 3, got %d err=%v", seq, err)
			}
			ie.clear()
			if _, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteDocumentChange{
					Type: durable.StorageWriteTypeDocumentChange,
					Id:   docId,
					Content: durable.DocumentContent{
						Kind:    durable.DocumentContentDelta,
						Version: 1,
						Ops:     []chord.Op{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(3)}},
					},
				},
			}); err != nil {
				t.Fatalf("follow-up delta failed: %v", err)
			}
			_ = st.Close(ctx)

			reopened := tjstOpen(t, dir, nil, jsonl.JsonlStorageOptions{Fsync: true})
			if d, err := reopened.Document(ctx, docId, durable.DocumentPointCurrent()); err != nil || d == nil || !jsonx.DeepEqual(d.Value, jsonx.ObjectOf("count", float64(3))) {
				t.Fatalf("expected count=3, got %+v err=%v", d, err)
			}
			if lines := tjstReadLines(t, filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId))); len(lines) != 2 {
				t.Fatalf("expected 2 lines (reclaimed base + delta), got %d", len(lines))
			}
		})
	}

	for _, mode := range []string{"before", "after"} {
		t.Run("recovers terminal-task reclamation across remove "+mode, func(t *testing.T) {
			dir := t.TempDir()
			ie := tjstNewInstrumentedEnv(dir)
			st := tjstOpen(t, dir, ie)
			tjstCreateRoot(t, st)
			taskRaw, _ := st.MintId()
			taskId := durable.IdFromNumber[durable.TaskId](taskRaw)
			if _, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstPendingTask(taskId)},
			}); err != nil {
				t.Fatalf("pending task failed: %v", err)
			}
			ie.fail(tjstFailure{operation: "remove", call: 1, mode: mode})
			if seq, err := st.Commit(ctx, []durable.StorageWrite{
				durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstTerminalTask(taskId)},
			}); err != nil || seq != 3 {
				t.Fatalf("expected seq 3, got %d err=%v", seq, err)
			}
			if task, err := st.Task(ctx, taskId); err != nil || task == nil || !jsonx.DeepEqual(*task, tjstTerminalTask(taskId)) {
				t.Fatalf("unexpected terminal task: %+v err=%v", task, err)
			}
			_ = st.Close(ctx)

			reopened := tjstOpen(t, dir, nil)
			if task, err := reopened.Task(ctx, taskId); err != nil || task == nil || !jsonx.DeepEqual(*task, tjstTerminalTask(taskId)) {
				t.Fatalf("unexpected reopened terminal task: %+v err=%v", task, err)
			}
			if tjstFileExists(filepath.Join(dir, fmt.Sprintf("task-%d.jsonl", taskId))) {
				t.Fatalf("expected task sidecar removed on reopen")
			}
			if reclaims := tjstReclaimFiles(t, dir); len(reclaims) != 0 {
				t.Fatalf("expected no .reclaim files, got %v", reclaims)
			}
		})
	}

	t.Run("appends later deltas to the replacement sidecar after a current-only base", func(t *testing.T) {
		dir := t.TempDir()
		st := tjstOpen(t, dir, nil)
		tjstCreateRoot(t, st)
		rawId, _ := st.MintId()
		docId := durable.IdFromNumber[durable.DocumentId](rawId)

		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentCreate{
				Type:    durable.StorageWriteTypeDocumentCreate,
				Record:  tjstSessionDoc(docId),
				Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(0))},
			},
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type:    durable.StorageWriteTypeDocumentChange,
				Id:      docId,
				Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(10))},
			},
		}); err != nil {
			t.Fatalf("base change failed: %v", err)
		}
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type: durable.StorageWriteTypeDocumentChange,
				Id:   docId,
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentDelta,
					Version: 1,
					Ops:     []chord.Op{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(11)}},
				},
			},
		}); err != nil {
			t.Fatalf("delta change failed: %v", err)
		}
		if lines := tjstReadLines(t, filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId))); len(lines) != 2 {
			t.Fatalf("expected 2 lines, got %d", len(lines))
		}
		reopened := tjstOpen(t, dir, nil)
		if d, err := reopened.Document(ctx, docId, durable.DocumentPointCurrent()); err != nil || d == nil || !jsonx.DeepEqual(d.Value, jsonx.ObjectOf("count", float64(11))) {
			t.Fatalf("expected count=11, got %+v err=%v", d, err)
		}
	})

	t.Run("never reclaims rewindable document history, including after a base and retirement", func(t *testing.T) {
		dir := t.TempDir()
		st := tjstOpen(t, dir, nil)
		tjstCreateRoot(t, st)
		rawId, _ := st.MintId()
		docId := durable.IdFromNumber[durable.DocumentId](rawId)

		rec := durable.DocumentCreate{
			Id:      docId,
			Kind:    "rewindable",
			Scope:   durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: durable.RootConversationId},
			History: durable.DocumentHistoryRewindable,
			Fork:    durable.DocumentForkAsOf,
		}
		createdAt, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentCreate{
				Type:    durable.StorageWriteTypeDocumentCreate,
				Record:  rec,
				Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(0))},
			},
		})
		if err != nil {
			t.Fatalf("create failed: %v", err)
		}
		changedAt, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type: durable.StorageWriteTypeDocumentChange,
				Id:   docId,
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentDelta,
					Version: 1,
					Ops:     []chord.Op{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(1)}},
				},
			},
		})
		if err != nil {
			t.Fatalf("delta failed: %v", err)
		}
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type:    durable.StorageWriteTypeDocumentChange,
				Id:      docId,
				Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(2))},
			},
		}); err != nil {
			t.Fatalf("base failed: %v", err)
		}
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: docId},
		}); err != nil {
			t.Fatalf("retire failed: %v", err)
		}

		sidecarPath := filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId))
		if lines := tjstReadLines(t, sidecarPath); len(lines) != 3 {
			t.Fatalf("expected 3 lines, got %d", len(lines))
		}
		reopened := tjstOpen(t, dir, nil)
		if d, err := reopened.Document(ctx, docId, durable.DocumentPointSeq(createdAt)); err != nil || d == nil || !jsonx.DeepEqual(d.Value, jsonx.ObjectOf("count", float64(0))) {
			t.Fatalf("unexpected doc at createdAt: %+v err=%v", d, err)
		}
		if d, err := reopened.Document(ctx, docId, durable.DocumentPointSeq(changedAt)); err != nil || d == nil || !jsonx.DeepEqual(d.Value, jsonx.ObjectOf("count", float64(1))) {
			t.Fatalf("unexpected doc at changedAt: %+v err=%v", d, err)
		}
		if d, err := reopened.Document(ctx, docId, durable.DocumentPointCurrent()); err != nil || d != nil {
			t.Fatalf("expected current retired doc nil, got %+v err=%v", d, err)
		}
		if lines := tjstReadLines(t, sidecarPath); len(lines) != 3 {
			t.Fatalf("expected 3 preserved lines, got %d", len(lines))
		}
	})

	t.Run("reclaims retired task-, session-, and latest-conversation document sidecars", func(t *testing.T) {
		dir := t.TempDir()
		st := tjstOpen(t, dir, nil)
		tjstCreateRoot(t, st)
		taskRaw, _ := st.MintId()
		sessRaw, _ := st.MintId()
		latestRaw, _ := st.MintId()
		taskDocRaw, _ := st.MintId()
		taskId := durable.IdFromNumber[durable.TaskId](taskRaw)
		sessId := durable.IdFromNumber[durable.DocumentId](sessRaw)
		latestId := durable.IdFromNumber[durable.DocumentId](latestRaw)
		taskDocId := durable.IdFromNumber[durable.DocumentId](taskDocRaw)

		createdAt, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstPendingTask(taskId)},
			durable.StorageWriteDocumentCreate{
				Type:    durable.StorageWriteTypeDocumentCreate,
				Record:  tjstSessionDoc(sessId, "session"),
				Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.NewObject()},
			},
			durable.StorageWriteDocumentCreate{
				Type: durable.StorageWriteTypeDocumentCreate,
				Record: durable.DocumentCreate{
					Id:      latestId,
					Kind:    "latest",
					Scope:   durable.DocumentScope{Kind: durable.DocumentScopeConversation, ConversationId: durable.RootConversationId},
					History: durable.DocumentHistoryLatest,
					Fork:    durable.DocumentForkCurrent,
				},
				Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.NewObject()},
			},
			durable.StorageWriteDocumentCreate{
				Type: durable.StorageWriteTypeDocumentCreate,
				Record: durable.DocumentCreate{
					Id:    taskDocId,
					Kind:  "task",
					Scope: durable.DocumentScope{Kind: durable.DocumentScopeTask, TaskId: taskId},
				},
				Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.NewObject()},
			},
		})
		if err != nil {
			t.Fatalf("create failed: %v", err)
		}
		retiredAt, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: sessId},
			durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: latestId},
			durable.StorageWriteDocumentRetire{Type: durable.StorageWriteTypeDocumentRetire, Id: taskDocId},
			durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstTerminalTask(taskId)},
		})
		if err != nil {
			t.Fatalf("retire failed: %v", err)
		}

		for _, name := range []string{
			fmt.Sprintf("doc-%d.jsonl", sessId),
			fmt.Sprintf("doc-%d.jsonl", latestId),
			fmt.Sprintf("doc-%d.jsonl", taskDocId),
			fmt.Sprintf("task-%d.jsonl", taskId),
		} {
			if tjstFileExists(filepath.Join(dir, name)) {
				t.Fatalf("expected reclaimed sidecar %s to be absent", name)
			}
		}

		reopened := tjstOpen(t, dir, nil)
		if task, err := reopened.Task(ctx, taskId); err != nil || task == nil || !jsonx.DeepEqual(*task, tjstTerminalTask(taskId)) {
			t.Fatalf("unexpected reopened task: %+v err=%v", task, err)
		}
		for _, id := range []durable.DocumentId{sessId, latestId, taskDocId} {
			if d, err := reopened.Document(ctx, id, durable.DocumentPointCurrent()); err != nil || d != nil {
				t.Fatalf("expected retired doc %d nil, got %+v err=%v", id, d, err)
			}
		}
		found, err := reopened.FindDocument(ctx, durable.DocumentAddress{
			Kind:  "session",
			Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
		}, durable.DocumentPointSeq(createdAt))
		if err != nil || found == nil || found.Id != sessId || found.CreatedAt != createdAt || found.RetiredAt == nil || *found.RetiredAt != retiredAt {
			t.Fatalf("unexpected found session doc at createdAt: %+v err=%v", found, err)
		}
		gone, err := reopened.FindDocument(ctx, durable.DocumentAddress{
			Kind:  "session",
			Scope: durable.DocumentScope{Kind: durable.DocumentScopeSession},
		}, durable.DocumentPointSeq(retiredAt))
		if err != nil || gone != nil {
			t.Fatalf("expected nil at retiredAt, got %+v err=%v", gone, err)
		}
	})

	t.Run("truncates torn UTF-8 tails at exact byte offsets and reuses the unconfirmed sequence", func(t *testing.T) {
		dir := t.TempDir()
		st := tjstOpen(t, dir, nil)
		tjstCreateRoot(t, st)
		rawDoc, _ := st.MintId()
		docId := durable.IdFromNumber[durable.DocumentId](rawDoc)

		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentCreate{
				Type:    durable.StorageWriteTypeDocumentCreate,
				Record:  tjstSessionDoc(docId),
				Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("text", "kept")},
			},
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		sidecarPath := filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId))
		mainPath := filepath.Join(dir, "main.jsonl")
		sidecarSize := tjstMustStatSize(t, sidecarPath)
		mainSize := tjstMustStatSize(t, mainPath)

		torn := []byte(`{"text":"€`)
		torn = torn[:len(torn)-1]
		tjstAppendRawBytes(t, sidecarPath, torn)
		tjstAppendRawBytes(t, mainPath, torn)

		reopened := tjstOpen(t, dir, nil)
		if got := tjstMustStatSize(t, sidecarPath); got != sidecarSize {
			t.Fatalf("expected sidecar truncated to %d bytes, got %d", sidecarSize, got)
		}
		if got := tjstMustStatSize(t, mainPath); got != mainSize {
			t.Fatalf("expected main truncated to %d bytes, got %d", mainSize, got)
		}
		seq, err := reopened.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type:    durable.StorageWriteTypeDocumentChange,
				Id:      docId,
				Content: durable.DocumentContent{Kind: durable.DocumentContentDelta, Version: 1},
			},
		})
		if err != nil || seq != 3 {
			t.Fatalf("expected reused seq 3, got %d err=%v", seq, err)
		}
	})

	t.Run("removes complete unconfirmed sidecar tails without resurrecting terminal tasks", func(t *testing.T) {
		dir := t.TempDir()
		st := tjstOpen(t, dir, nil)
		tjstCreateRoot(t, st)
		taskRaw, _ := st.MintId()
		taskId := durable.IdFromNumber[durable.TaskId](taskRaw)
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstPendingTask(taskId)},
		}); err != nil {
			t.Fatalf("pending failed: %v", err)
		}
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteTask{Type: durable.StorageWriteTypeTask, Value: tjstTerminalTask(taskId)},
		}); err != nil {
			t.Fatalf("terminal failed: %v", err)
		}

		sidecarPath := filepath.Join(dir, fmt.Sprintf("task-%d.jsonl", taskId))
		if tjstFileExists(sidecarPath) {
			t.Fatalf("expected reclaimed sidecar absent")
		}
		line, err := jsonx.Stringify(jsonx.ObjectOf(
			"format", float64(1),
			"type", "record",
			"seq", float64(4),
			"ordinal", float64(0),
			"payload", jsonx.ObjectOf("type", "task", "value", tjstPendingTask(taskId, "stale")),
		))
		if err != nil {
			t.Fatalf("stringify failed: %v", err)
		}
		tjstAppendRawBytes(t, sidecarPath, []byte(line+"\n"))

		reopened := tjstOpen(t, dir, nil)
		if tjstFileExists(sidecarPath) {
			t.Fatalf("expected unconfirmed tail removed")
		}
		if task, err := reopened.Task(ctx, taskId); err != nil || task == nil || !jsonx.DeepEqual(*task, tjstTerminalTask(taskId)) {
			t.Fatalf("expected terminal task preserved, got %+v err=%v", task, err)
		}
		if seq, err := reopened.Commit(ctx, nil); err != nil || seq != 4 {
			t.Fatalf("expected seq 4, got %d err=%v", seq, err)
		}
	})

	t.Run("fails open when confirmed sidecar data is missing", func(t *testing.T) {
		dir := t.TempDir()
		st := tjstOpen(t, dir, nil)
		tjstCreateRoot(t, st)
		rawDoc, _ := st.MintId()
		docId := durable.IdFromNumber[durable.DocumentId](rawDoc)
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentCreate{
				Type:    durable.StorageWriteTypeDocumentCreate,
				Record:  tjstSessionDoc(docId),
				Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.NewObject()},
			},
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId)), nil, 0o644); err != nil {
			t.Fatalf("wipe sidecar failed: %v", err)
		}
		_, err := jsonl.OpenJsonlStorage(dir, env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: dir}), ctx)
		if err == nil || !strings.Contains(err.Error(), "Missing confirmed sidecar record") {
			t.Fatalf("expected Missing confirmed sidecar record error, got %v", err)
		}
	})

	t.Run("rejects a confirmed record after an unconfirmed sidecar record", func(t *testing.T) {
		dir := t.TempDir()
		st := tjstOpen(t, dir, nil)
		tjstCreateRoot(t, st)
		rawDoc, _ := st.MintId()
		docId := durable.IdFromNumber[durable.DocumentId](rawDoc)
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentCreate{
				Type:    durable.StorageWriteTypeDocumentCreate,
				Record:  tjstSessionDoc(docId),
				Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.ObjectOf("count", float64(0))},
			},
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentChange{
				Type: durable.StorageWriteTypeDocumentChange,
				Id:   docId,
				Content: durable.DocumentContent{
					Kind:    durable.DocumentContentDelta,
					Version: 1,
					Ops:     []chord.Op{chord.OpSet{Path: chord.NonEmptyPath{chord.SegString("count")}, Value: float64(1)}},
				},
			},
		}); err != nil {
			t.Fatalf("delta failed: %v", err)
		}

		sidecarPath := filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId))
		lines := tjstReadLines(t, sidecarPath)
		unconfirmed, _ := jsonx.Stringify(jsonx.ObjectOf(
			"format", float64(1),
			"type", "record",
			"seq", float64(2),
			"ordinal", float64(999),
			"payload", jsonx.ObjectOf("type", "document", "id", float64(docId), "content", jsonx.ObjectOf("kind", "delta", "version", float64(1), "ops", []any{})),
		))
		if err := os.WriteFile(sidecarPath, []byte(lines[0]+"\n"+unconfirmed+"\n"+lines[1]+"\n"), 0o644); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		_, err := jsonl.OpenJsonlStorage(dir, env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: dir}), ctx)
		if err == nil || !strings.Contains(err.Error(), "Confirmed record follows an unconfirmed tail") {
			t.Fatalf("expected Confirmed record follows an unconfirmed tail, got %v", err)
		}
	})

	t.Run("rejects non-increasing main commit sequences", func(t *testing.T) {
		dir := t.TempDir()
		st := tjstOpen(t, dir, nil)
		tjstCreateRoot(t, st)
		mainPath := filepath.Join(dir, "main.jsonl")
		raw, err := os.ReadFile(mainPath)
		if err != nil {
			t.Fatalf("read main failed: %v", err)
		}
		tjstAppendRawBytes(t, mainPath, raw)
		_, err = jsonl.OpenJsonlStorage(dir, env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: dir}), ctx)
		if err == nil || !strings.Contains(err.Error(), "Commit sequence does not strictly increase") {
			t.Fatalf("expected non-increasing seq error, got %v", err)
		}
	})

	t.Run("rejects structurally invalid confirmed document content", func(t *testing.T) {
		dir := t.TempDir()
		st := tjstOpen(t, dir, nil)
		tjstCreateRoot(t, st)
		rawDoc, _ := st.MintId()
		docId := durable.IdFromNumber[durable.DocumentId](rawDoc)
		if _, err := st.Commit(ctx, []durable.StorageWrite{
			durable.StorageWriteDocumentCreate{
				Type:    durable.StorageWriteTypeDocumentCreate,
				Record:  tjstSessionDoc(docId),
				Content: durable.DocumentContent{Kind: durable.DocumentContentBase, Version: 1, Value: jsonx.NewObject()},
			},
		}); err != nil {
			t.Fatalf("create failed: %v", err)
		}

		sidecarPath := filepath.Join(dir, fmt.Sprintf("doc-%d.jsonl", docId))
		raw, _ := os.ReadFile(sidecarPath)
		parsedAny, err := jsonx.ParseString(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatalf("parse sidecar failed: %v", err)
		}
		recObj := parsedAny.(*jsonx.Object)
		payloadAny, _ := recObj.Get("payload")
		contentAny, _ := payloadAny.(*jsonx.Object).Get("content")
		contentAny.(*jsonx.Object).Delete("value")
		rewritten, _ := jsonx.Stringify(recObj)
		if err := os.WriteFile(sidecarPath, []byte(rewritten+"\n"), 0o644); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		_, err = jsonl.OpenJsonlStorage(dir, env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: dir}), ctx)
		if err == nil || !strings.Contains(err.Error(), "Invalid document content") {
			t.Fatalf("expected Invalid document content error, got %v", err)
		}
	})

	t.Run("rejects malformed complete main and sidecar lines", func(t *testing.T) {
		mainDir := t.TempDir()
		mainSt := tjstOpen(t, mainDir, nil)
		tjstCreateRoot(t, mainSt)
		tjstAppendRawBytes(t, filepath.Join(mainDir, "main.jsonl"), []byte("{bad}\n"))
		_, err := jsonl.OpenJsonlStorage(mainDir, env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: mainDir}), ctx)
		if err == nil || !strings.Contains(err.Error(), "Malformed complete main.jsonl") {
			t.Fatalf("expected Malformed complete main.jsonl error, got %v", err)
		}

		sidecarDir := t.TempDir()
		sidecarSt := tjstOpen(t, sidecarDir, nil)
		tjstCreateRoot(t, sidecarSt)
		if err := os.WriteFile(filepath.Join(sidecarDir, "doc-99.jsonl"), []byte("{bad}\n"), 0o644); err != nil {
			t.Fatalf("write bad sidecar failed: %v", err)
		}
		_, err = jsonl.OpenJsonlStorage(sidecarDir, env.NewNodeExecutionEnv(env.NodeExecutionEnvOptions{Cwd: sidecarDir}), ctx)
		if err == nil || !strings.Contains(err.Error(), "Malformed complete doc-99.jsonl") {
			t.Fatalf("expected Malformed complete doc-99.jsonl error, got %v", err)
		}
	})
}

func tjstMustStatSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s failed: %v", path, err)
	}
	return info.Size()
}

func tjstAppendRawBytes(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open %s failed: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		t.Fatalf("write %s failed: %v", path, err)
	}
}
