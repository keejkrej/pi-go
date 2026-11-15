package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tseStrPtr(s string) *string { return &s }

func tseUserMsg(text string, ts int64) *ai.UserMessage {
	return &ai.UserMessage{
		Role:      "user",
		Content:   ai.UserMessageContent{Text: tseStrPtr(text)},
		Timestamp: ts,
	}
}

func TestSerializeSessionBranchAndExportToJsonl(t *testing.T) {
	sm := SessionManagerInMemory("/workspace/project")
	u1, err := sm.AppendMessage(tseUserMsg("first root", 1))
	if err != nil {
		t.Fatal(err)
	}
	u2, err := sm.AppendMessage(tseUserMsg("branch A", 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := sm.Branch(u1); err != nil {
		t.Fatal(err)
	}
	u3, err := sm.AppendMessage(tseUserMsg("branch B", 3))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendLabelChange(u3, tseStrPtr("keep-me")); err != nil {
		t.Fatal(err)
	}

	serialized := SerializeSessionBranch(sm, func(parentId *string, timestamp string) []any {
		obj := jsonx.NewObject()
		obj.Set("type", "custom")
		obj.Set("id", "export1")
		if parentId == nil {
			obj.Set("parentId", nil)
		} else {
			obj.Set("parentId", *parentId)
		}
		obj.Set("timestamp", timestamp)
		obj.Set("customType", "share_meta")
		obj.Set("data", map[string]any{"source": "test"})
		return []any{obj}
	})

	if !strings.HasSuffix(serialized, "\n") {
		t.Fatal("expected trailing newline in serialized JSONL")
	}

	parsed := ParseSessionEntries(serialized)
	// header + u1 + u3 + label + trailing custom
	if len(parsed) != 5 {
		t.Fatalf("len(parsed) = %d, want 5; content:\n%s", len(parsed), serialized)
	}
	header, ok := parsed[0].(*SessionHeader)
	if !ok || header.Id != sm.GetSessionId() || header.Cwd != "/workspace/project" {
		t.Fatalf("header = %+v", parsed[0])
	}
	if header.Version == nil || *header.Version != CurrentSessionVersion {
		t.Fatalf("header.Version = %v, want %d", header.Version, CurrentSessionVersion)
	}

	e1 := parsed[1].(SessionEntry)
	e2 := parsed[2].(SessionEntry)
	e3 := parsed[3].(SessionEntry)
	e4 := parsed[4].(SessionEntry)
	if e1.GetId() != u1 || e1.GetParentId() != nil {
		t.Fatalf("e1 = (%s, %v)", e1.GetId(), e1.GetParentId())
	}
	if e2.GetId() != u3 || e2.GetParentId() == nil || *e2.GetParentId() != u1 {
		t.Fatalf("e2 = (%s, %v), want parent=%s", e2.GetId(), e2.GetParentId(), u1)
	}
	if e3.GetType() != "label" || e3.GetParentId() == nil || *e3.GetParentId() != u3 {
		t.Fatalf("e3 = (%s, %v)", e3.GetType(), e3.GetParentId())
	}
	if e4.GetType() != "custom" || e4.GetId() != "export1" || e4.GetParentId() == nil || *e4.GetParentId() != e3.GetId() {
		t.Fatalf("e4 = (%s, %s, %v)", e4.GetType(), e4.GetId(), e4.GetParentId())
	}
	if strings.Contains(serialized, u2) {
		t.Fatalf("did not expect inactive branch entry %s in export", u2)
	}

	// Verify ExportSessionToJsonl creates parent dirs and writes identical content.
	outDir := filepath.Join(t.TempDir(), "nested", "exports")
	outFile := filepath.Join(outDir, "exported.jsonl")
	writtenPath, err := ExportSessionToJsonl(sm, outFile)
	if err != nil {
		t.Fatal(err)
	}
	if writtenPath != outFile {
		t.Fatalf("writtenPath = %q, want %q", writtenPath, outFile)
	}
	raw, err := os.ReadFile(writtenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n") || len(ParseSessionEntries(string(raw))) != 4 {
		t.Fatalf("unexpected exported file content:\n%s", string(raw))
	}

	// Verify empty session export passes parentId=nil to trailing callback and writes default filename in cwd.
	emptySm := SessionManagerInMemory("/workspace/empty")
	var gotParent *string
	emptyJsonl := SerializeSessionBranch(emptySm, func(parentId *string, timestamp string) []any {
		gotParent = parentId
		return nil
	})
	if gotParent != nil || len(ParseSessionEntries(emptyJsonl)) != 1 {
		t.Fatalf("expected parentId=nil for empty branch export, got %v", gotParent)
	}

	cwdTmp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwdTmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	defaultPath, err := ExportSessionToJsonlWithTrailing(sm, "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(defaultPath) != cwdTmp && filepath.Base(filepath.Dir(defaultPath)) != filepath.Base(cwdTmp) {
		// Accept symlink-resolved cwd on macOS (/var -> /private/var)
		if !strings.HasSuffix(filepath.Dir(defaultPath), filepath.Base(cwdTmp)) {
			t.Fatalf("defaultPath = %q, expected under %q", defaultPath, cwdTmp)
		}
	}
	if !strings.HasPrefix(filepath.Base(defaultPath), "session-") || !strings.HasSuffix(defaultPath, ".jsonl") {
		t.Fatalf("unexpected default export filename %q", defaultPath)
	}
}
