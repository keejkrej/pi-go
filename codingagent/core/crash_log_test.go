package core

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

func TestCrashLogPathRecordCapAndClear(t *testing.T) {
	if got := CrashLogPath("/tmp/custom-agent"); got != filepath.Join("/tmp/custom-agent", "crashes.json") {
		t.Fatalf("unexpected CrashLogPath: %q", got)
	}
	if got := CrashLogPath(); got != filepath.Join(utils.GetAgentDir(), "crashes.json") {
		t.Fatalf("unexpected default CrashLogPath: %q", got)
	}

	logPath := filepath.Join(t.TempDir(), "nested", "crashes.json")
	if recs := ReadCrashLog(logPath); len(recs) != 0 {
		t.Fatalf("expected empty records on missing file, got %+v", recs)
	}

	for i := range 7 {
		sf := fmt.Sprintf("/tmp/session-%d.jsonl", i)
		rec := RecordCrash(RecordCrashInput{
			Kind:        CrashKindUncaughtException,
			Error:       fmt.Errorf("boom %d", i),
			SessionFile: &sf,
			Cwd:         "/repo",
		}, logPath)
		if rec == nil || rec.Kind != CrashKindUncaughtException {
			t.Fatalf("expected recorded crash, got %+v", rec)
		}
	}

	recs := ReadCrashLog(logPath)
	if len(recs) != 5 {
		t.Fatalf("expected crash log capped at 5 records, got %d", len(recs))
	}
	// Newest-first or chronological: either way check all remaining are from the last 5 recorded.
	foundLatest := false
	for _, r := range recs {
		if r.Message == "boom 6" {
			foundLatest = true
		}
		if r.Message == "boom 0" || r.Message == "boom 1" {
			t.Fatalf("oldest record %q should have been evicted", r.Message)
		}
	}
	if !foundLatest {
		t.Fatalf("expected latest crash record boom 6 in %+v", recs)
	}

	ClearCrashLog(logPath)
	if after := ReadCrashLog(logPath); len(after) != 0 {
		t.Fatalf("expected empty crash log after clear, got %+v", after)
	}
}

func TestTakeUnnotifiedCrashAndExpiry(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "crashes.json")

	recent := RecordCrash(RecordCrashInput{
		Kind:  CrashKindFatalError,
		Error: "fatal failure",
		Cwd:   "/repo",
	}, logPath)
	if recent == nil {
		t.Fatal("expected recent crash record")
	}

	parsed, err := time.Parse(time.RFC3339Nano, recent.Timestamp)
	if err != nil {
		t.Fatalf("unexpected timestamp %q: %v", recent.Timestamp, err)
	}
	nowMs := parsed.UnixMilli() + 1000

	got := TakeUnnotifiedCrashAt(nowMs, logPath)
	if got == nil || got.Message != "fatal failure" {
		t.Fatalf("expected unnotified crash, got %+v", got)
	}
	if again := TakeUnnotifiedCrashAt(nowMs, logPath); again != nil {
		t.Fatalf("expected nil on second TakeUnnotifiedCrashAt, got %+v", again)
	}

	// Expired (>7 days) crash should not be returned.
	logPath2 := filepath.Join(t.TempDir(), "crashes2.json")
	old := RecordCrash(RecordCrashInput{
		Kind:  CrashKindFatalError,
		Error: "stale crash",
		Cwd:   "/repo",
	}, logPath2)
	oldParsed, _ := time.Parse(time.RFC3339Nano, old.Timestamp)
	tooLateMs := oldParsed.UnixMilli() + 8*24*60*60*1000
	if stale := TakeUnnotifiedCrashAt(tooLateMs, logPath2); stale != nil {
		t.Fatalf("expected nil for expired crash, got %+v", stale)
	}
}

func TestFindExtensionStackMatches(t *testing.T) {
	stack := "Error: failed\n    at handler (/Users/dev/exts/my-ext/index.ts:12:3)\n    at node:internal/main:1:1"
	exts := []Extension{
		{
			Path:         "/Users/dev/exts/my-ext/index.ts",
			ResolvedPath: "/Users/dev/exts/my-ext/index.ts",
			SourceInfo: utils.CreateSyntheticSourceInfo("/Users/dev/exts/my-ext/index.ts", utils.SyntheticSourceOptions{
				Source: "/Users/dev/exts/my-ext",
				Scope:  utils.SourceScopeProject,
				Origin: utils.SourceOriginTopLevel,
			}),
		},
		{
			Path:         "/Users/dev/exts/other/index.ts",
			ResolvedPath: "/Users/dev/exts/other/index.ts",
			SourceInfo: utils.CreateSyntheticSourceInfo("/Users/dev/exts/other/index.ts", utils.SyntheticSourceOptions{
				Source: "/Users/dev/exts/other",
				Scope:  utils.SourceScopeUser,
				Origin: utils.SourceOriginTopLevel,
			}),
		},
	}
	matches := FindExtensionStackMatches(&stack, exts)
	if len(matches) != 1 || matches[0] == "" {
		t.Fatalf("expected 1 extension stack match, got %+v", matches)
	}
}
