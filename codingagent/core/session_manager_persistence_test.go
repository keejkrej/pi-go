package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestLoadEntriesFromFileAndHeaderScanLimit(t *testing.T) {
	tmpDir := t.TempDir()

	// Empty / missing file -> empty entries
	if entries := LoadEntriesFromFile(filepath.Join(tmpDir, "missing.jsonl")); len(entries) != 0 {
		t.Fatalf("expected empty entries for missing file, got %d", len(entries))
	}
	emptyFile := filepath.Join(tmpDir, "empty.jsonl")
	if err := os.WriteFile(emptyFile, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if entries := LoadEntriesFromFile(emptyFile); len(entries) != 0 {
		t.Fatalf("expected empty entries for empty file, got %d", len(entries))
	}

	// Non-empty file with invalid first line returns empty entries
	badHeader := filepath.Join(tmpDir, "bad-header.jsonl")
	if err := os.WriteFile(badHeader, []byte("{\"type\":\"message\",\"id\":\"1\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if entries := LoadEntriesFromFile(badHeader); len(entries) != 0 {
		t.Fatalf("expected empty entries without session header, got %d", len(entries))
	}

	// Valid sparse file skips leading malformed lines before header, and ignores malformed middle lines
	sparseFile := filepath.Join(tmpDir, "sparse.jsonl")
	sparseContent := strings.Join([]string{
		"not json at all",
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp/proj"}`,
		"{broken middle json",
		`{"type":"message","id":"m1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"hi","timestamp":1}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sparseFile, []byte(sparseContent), 0o600); err != nil {
		t.Fatal(err)
	}
	entries := LoadEntriesFromFile(sparseFile)
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if _, ok := entries[0].(*SessionHeader); !ok {
		t.Fatalf("entries[0] = %T, want *SessionHeader", entries[0])
	}
	if msg, ok := entries[1].(*SessionMessageEntry); !ok || msg.Id != "m1" {
		t.Fatalf("entries[1] = %+v", entries[1])
	}

	// Opening invalid non-empty session file returns error instead of overwriting
	if _, err := SessionManagerOpen(badHeader); err == nil {
		t.Fatal("expected SessionManagerOpen error on non-empty file with invalid header")
	}

	// Oversized header >1MB: SessionManagerOpen still opens compatible files, while discovery skips >1MB headers.
	hugeHeaderFile := filepath.Join(tmpDir, "huge.jsonl")
	padding := strings.Repeat("x", smMaxHeaderScanBytes+128)
	hugeLine := `{"type":"session","version":3,"id":"s2","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp/proj","pad":"` + padding + `"}` + "\n"
	if err := os.WriteFile(hugeHeaderFile, []byte(hugeLine), 0o600); err != nil {
		t.Fatal(err)
	}
	if entries := LoadEntriesFromFile(hugeHeaderFile); len(entries) != 1 {
		t.Fatalf("expected LoadEntriesFromFile to parse oversized header, got %d", len(entries))
	}
	smOversized, err := SessionManagerOpen(hugeHeaderFile)
	if err != nil {
		t.Fatalf("unexpected error opening hugeHeaderFile: %v", err)
	}
	if smOversized.GetSessionId() != "s2" {
		t.Fatalf("GetSessionId = %q, want s2", smOversized.GetSessionId())
	}
	if foundHuge := SessionManagerFindById("/tmp/proj", "s2", tmpDir); foundHuge != nil {
		t.Fatalf("expected discovery scan limit to skip >1MB header, got %v", *foundHuge)
	}
}

func TestSessionManagerDelayedPersistenceAndWriteRollback(t *testing.T) {
	tmpRoot := t.TempDir()
	cwd := filepath.Join(tmpRoot, "work")
	sessionDir := filepath.Join(tmpRoot, "sessions")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	sm, err := SessionManagerCreate(cwd, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	sessionFile := sm.GetSessionFile()
	if sessionFile == nil || *sessionFile == "" {
		t.Fatal("expected non-empty planned sessionFile")
	}

	// Non-conversation entries delay file creation until first user/assistant/custom message.
	if _, err := sm.AppendThinkingLevelChange("medium"); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendModelChange("anthropic", "claude-sonnet-4-5"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(*sessionFile); !os.IsNotExist(err) {
		t.Fatalf("session file should not exist before first conversation message, stat err=%v", err)
	}

	// First user message flushes header + all prior entries atomically via O_EXCL.
	msgId, err := sm.AppendMessage(tsmcUserMsg("start conversation"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(*sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Fatal("persisted JSONL must end with trailing newline")
	}
	loaded := LoadEntriesFromFile(*sessionFile)
	if len(loaded) != 4 {
		t.Fatalf("expected header + 3 entries, got %d", len(loaded))
	}
	if loaded[3].(SessionEntry).GetId() != msgId {
		t.Fatalf("unexpected last entry ID: %s != %s", loaded[3].(SessionEntry).GetId(), msgId)
	}

	// Verify directory (rwx owner, matches Node mkdirSync) and readable/writable file modes.
	dirInfo, err := os.Stat(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm&0o700 != 0o700 {
		t.Fatalf("session dir mode = %04o, expected owner rwx", perm)
	}
	fileInfo, err := os.Stat(*sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fileInfo.Mode().Perm(); perm&0o600 != 0o600 {
		t.Fatalf("session file mode = %04o, expected owner rw", perm)
	}

	// Rollback on write failure: use a regular file as sessionDir so initial O_EXCL/MkdirAll fails deterministically (even under uid 0).
	blockingFile := filepath.Join(tmpRoot, "not-a-dir")
	if err := os.WriteFile(blockingFile, []byte("blocker"), 0o600); err != nil {
		t.Fatal(err)
	}
	failSm, err := SessionManagerCreate(cwd, blockingFile)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = failSm.AppendThinkingLevelChange("low")
	beforeCount := failSm.GetEntryCount()
	beforeLeaf := failSm.GetLeafId()
	if _, err := failSm.AppendMessage(tsmcUserMsg("should fail and rollback")); err == nil {
		t.Fatal("expected write failure when sessionDir is a regular file")
	}
	if failSm.GetEntryCount() != beforeCount {
		t.Fatalf("expected entry count rolled back to %d, got %d", beforeCount, failSm.GetEntryCount())
	}
	if (beforeLeaf == nil) != (failSm.GetLeafId() == nil) || (beforeLeaf != nil && *beforeLeaf != *failSm.GetLeafId()) {
		t.Fatalf("expected leafId rolled back to %v, got %v", beforeLeaf, failSm.GetLeafId())
	}
}

func TestCustomSessionIdValidationAndUUIDv7Default(t *testing.T) {
	if err := AssertValidSessionId("custom-id_123"); err != nil {
		t.Fatalf("unexpected error for valid custom session ID: %v", err)
	}
	for _, invalid := range []string{"", "   ", "bad/slash", `bad\back`, "..", "bad:colon", "bad id"} {
		if err := AssertValidSessionId(invalid); err == nil {
			t.Fatalf("expected error for invalid session ID %q", invalid)
		}
	}

	tmpDir := t.TempDir()
	// Default generated ID uses UUIDv7 format (version nibble '7' at index 14).
	defaultSm, err := SessionManagerCreate(tmpDir, filepath.Join(tmpDir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	defaultId := defaultSm.GetSessionId()
	if len(defaultId) != 36 || defaultId[14] != '7' {
		t.Fatalf("expected UUIDv7 default session ID, got %q", defaultId)
	}

	customId := "ticket-42-session"
	customSm, err := SessionManagerCreate(tmpDir, filepath.Join(tmpDir, "sessions2"), &NewSessionOptions{Id: &customId})
	if err != nil {
		t.Fatal(err)
	}
	if customSm.GetSessionId() != customId {
		t.Fatalf("GetSessionId = %q, want %q", customSm.GetSessionId(), customId)
	}

	badId := "invalid/id"
	if _, err := SessionManagerCreate(tmpDir, filepath.Join(tmpDir, "sessions2"), &NewSessionOptions{Id: &badId}); err == nil {
		t.Fatal("expected error for invalid NewSessionOptions.Id")
	}
}

func TestMigrationV1ToV3AndByteParityUnknownKeyPreservation(t *testing.T) {
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "v1-session.jsonl")

	// v1 file with custom key order, extra unknown fields, firstKeptEntryIndex, and hookMessage.
	v1Lines := []string{
		`{"customHeaderFirst":"keepMe","type":"session","id":"sess-v1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/proj","headerTail":42}`,
		`{"z_before":1,"type":"message","mid_unknown":"mval","timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"first","timestamp":1}}`,
		`{"type":"compaction","timestamp":"2026-01-01T00:00:02.000Z","summary":"sum","firstKeptEntryIndex":1,"tokensBefore":99,"compExtra":{"nested":true}}`,
		`{"a_first":"customPreserved","type":"message","timestamp":"2026-01-01T00:00:03.000Z","message":{"role":"hookMessage","customType":"my-hook","content":"from hook","display":true,"timestamp":3},"tailExtra":7}`,
	}
	if err := os.WriteFile(sessionFile, []byte(strings.Join(v1Lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Open migrates v1 -> v3 in-place and rewrites file.
	sm, err := SessionManagerOpen(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if h := sm.GetHeader(); h == nil || h.Version == nil || *h.Version != CurrentSessionVersion {
		t.Fatalf("expected migrated version %d, got %+v", CurrentSessionVersion, h)
	}

	entries := sm.GetEntries()
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}
	if entries[0].GetParentId() != nil {
		t.Fatalf("first migrated entry parentId = %v, want nil", entries[0].GetParentId())
	}
	if entries[1].GetParentId() == nil || *entries[1].GetParentId() != entries[0].GetId() {
		t.Fatalf("second migrated entry parentId mismatch")
	}
	comp, ok := entries[1].(*CompactionEntry)
	if !ok || comp.FirstKeptEntryId != entries[0].GetId() || comp.FirstKeptEntryIndex != nil {
		t.Fatalf("compaction migration mismatch: %+v", entries[1])
	}
	hookMsgEntry, ok := entries[2].(*SessionMessageEntry)
	if !ok {
		t.Fatalf("entries[2] = %T", entries[2])
	}
	cm, ok := hookMsgEntry.Message.(*CustomMessage)
	if !ok || cm.CustomType != "my-hook" {
		t.Fatalf("expected hookMessage migrated to CustomMessage, got %#v", hookMsgEntry.Message)
	}

	// Verify written JSONL preserved unknown keys and exact key positions on disk!
	rewrittenBytes, err := os.ReadFile(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(rewrittenBytes), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 rewritten lines, got %d:\n%s", len(lines), string(rewrittenBytes))
	}
	// Check key order of header preserved ("customHeaderFirst" before "type", "headerTail" after "cwd").
	if !(strings.Index(lines[0], `"customHeaderFirst":"keepMe"`) < strings.Index(lines[0], `"type":"session"`) &&
		strings.Index(lines[0], `"cwd":"/proj"`) < strings.Index(lines[0], `"headerTail":42`)) {
		t.Fatalf("header key order/unknown keys lost:\n%s", lines[0])
	}
	// Check key order of message line preserved ("z_before" first, "mid_unknown" retained).
	if !(strings.Index(lines[1], `"z_before":1`) < strings.Index(lines[1], `"type":"message"`) &&
		strings.Contains(lines[1], `"mid_unknown":"mval"`)) {
		t.Fatalf("message line key order/unknown keys lost:\n%s", lines[1])
	}
	// Check compaction unknown nested field kept and legacy firstKeptEntryIndex deleted.
	if strings.Contains(lines[2], `"firstKeptEntryIndex"`) || !strings.Contains(lines[2], `"compExtra":{"nested":true}`) {
		t.Fatalf("compaction migration line mismatch:\n%s", lines[2])
	}
	// Check hookMessage unknown outer keys preserved.
	if !(strings.Index(lines[3], `"a_first":"customPreserved"`) < strings.Index(lines[3], `"type":"message"`) &&
		strings.Contains(lines[3], `"tailExtra":7`) && strings.Contains(lines[3], `"role":"custom"`)) {
		t.Fatalf("hookMessage migration line mismatch:\n%s", lines[3])
	}

	// Idempotent migration on already-v3 entries changes nothing.
	rawCopy := LoadEntriesFromFile(sessionFile)
	MigrateSessionEntries(rawCopy)
	if len(rawCopy) != 4 {
		t.Fatalf("expected 4 entries after idempotent migrate, got %d", len(rawCopy))
	}

	// ForkFrom byte-exact raw line preservation test:
	forkDir := filepath.Join(tmpDir, "forked")
	forkedSm, err := SessionManagerForkFrom(sessionFile, "/target/cwd", forkDir)
	if err != nil {
		t.Fatal(err)
	}
	// Trigger persist via reading the forked file directly (ForkFrom writes immediately when source has entries!)
	forkedRaw, err := os.ReadFile(*forkedSm.GetSessionFile())
	if err != nil {
		t.Fatal(err)
	}
	forkLines := strings.Split(strings.TrimSuffix(string(forkedRaw), "\n"), "\n")
	if len(forkLines) != 4 {
		t.Fatalf("len(forkLines) = %d, want 4", len(forkLines))
	}
	parsedForkHeader, err := jsonx.Parse([]byte(forkLines[0]))
	if err != nil {
		t.Fatal(err)
	}
	forkHeaderObj, ok := parsedForkHeader.(*jsonx.Object)
	if !ok {
		t.Fatalf("expected *jsonx.Object fork header, got %T", parsedForkHeader)
	}
	if ps, _ := forkHeaderObj.Get("parentSession"); ps != sessionFile {
		t.Fatalf("forked parentSession = %v, want %s", ps, sessionFile)
	}
	// Copied entry lines from source are byte-exact!
	for idx := 1; idx <= 3; idx++ {
		if forkLines[idx] != lines[idx] {
			t.Fatalf("forked entry %d not byte-exact:\ngot:  %s\nwant: %s", idx, forkLines[idx], lines[idx])
		}
	}
}
