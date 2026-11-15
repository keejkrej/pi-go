package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
)

func TestSessionManagerTreeTraversalOrphansAndSelfParent(t *testing.T) {
	sm := SessionManagerInMemory()
	rootId, err := sm.AppendMessage(tsmcUserMsg("root"))
	if err != nil {
		t.Fatal(err)
	}
	childA, err := sm.AppendMessage(tsmcAssistantMsg("branch A"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sm.Branch(rootId); err != nil {
		t.Fatal(err)
	}
	childB, err := sm.AppendMessage(tsmcAssistantMsg("branch B"))
	if err != nil {
		t.Fatal(err)
	}

	children := sm.GetChildren(rootId)
	if len(children) != 2 || children[0].GetId() != childA || children[1].GetId() != childB {
		t.Fatalf("GetChildren = %+v", children)
	}

	tree := sm.GetTree()
	if len(tree) != 1 || tree[0].Entry.GetId() != rootId || len(tree[0].Children) != 2 {
		t.Fatalf("unexpected tree: %+v", tree)
	}
	if tree[0].Children[0].Entry.GetId() != childA || tree[0].Children[1].Entry.GetId() != childB {
		t.Fatalf("unexpected child tree ordering: %+v", tree[0].Children)
	}

	// Self-parenting and missing-parent orphan entries appear as extra roots without infinite recursion.
	missingParent := "does-not-exist"
	selfId := "selfnode"
	ver := CurrentSessionVersion
	orphanSm, err := SessionManagerInMemoryWith("/proj", nil, []FileEntry{
		&SessionHeader{Type: "session", Version: &ver, Id: "s-orphan", Timestamp: "2026-01-01T00:00:00Z", Cwd: "/proj"},
		&CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom", Id: "r1", ParentId: nil, Timestamp: "2026-01-01T00:00:01Z"}, CustomType: "x"},
		&CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom", Id: "o1", ParentId: &missingParent, Timestamp: "2026-01-01T00:00:02Z"}, CustomType: "y"},
		&CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom", Id: selfId, ParentId: &selfId, Timestamp: "2026-01-01T00:00:03Z"}, CustomType: "z"},
	})
	if err != nil {
		t.Fatal(err)
	}
	orphanTree := orphanSm.GetTree()
	if len(orphanTree) != 3 {
		t.Fatalf("expected 3 root nodes (root, orphan, self-parent), got %d", len(orphanTree))
	}

	// Verify child timestamp sorting when children arrive out of chronological order.
	pId := "rootp"
	sortedSm, err := SessionManagerInMemoryWith("/proj", nil, []FileEntry{
		&SessionHeader{Type: "session", Version: &ver, Id: "s-sort", Timestamp: "2026-01-01T00:00:00Z", Cwd: "/proj"},
		&CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom", Id: pId, ParentId: nil, Timestamp: "2026-01-01T00:00:01Z"}, CustomType: "p"},
		&CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom", Id: "later", ParentId: &pId, Timestamp: "2026-01-01T00:00:05Z"}, CustomType: "c2"},
		&CustomEntry{SessionEntryBase: SessionEntryBase{Type: "custom", Id: "earlier", ParentId: &pId, Timestamp: "2026-01-01T00:00:02Z"}, CustomType: "c1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sortedTree := sortedSm.GetTree()
	if sortedTree[0].Children[0].Entry.GetId() != "earlier" || sortedTree[0].Children[1].Entry.GetId() != "later" {
		t.Fatalf("children not sorted chronologically: %+v", sortedTree[0].Children)
	}
}

func TestSessionManagerLabelsAndCreateBranchedSession(t *testing.T) {
	tmpDir := t.TempDir()
	cwd := filepath.Join(tmpDir, "work")
	sessionDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	sm, err := SessionManagerCreate(cwd, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	e1, err := sm.AppendMessage(tsmcUserMsg("first"))
	if err != nil {
		t.Fatal(err)
	}
	e2, err := sm.AppendMessage(tsmcAssistantMsg("reply"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendLabelChange(e1, tsmcStrPtr("checkpoint")); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendLabelChange(e2, tsmcStrPtr("temp-label")); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendLabelChange(e2, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.AppendLabelChange("missing", tsmcStrPtr("bad")); err == nil {
		t.Fatal("expected error labeling missing entry")
	}

	if lbl := sm.GetLabel(e1); lbl == nil || *lbl != "checkpoint" {
		t.Fatalf("GetLabel(e1) = %v, want checkpoint", lbl)
	}
	if lbl := sm.GetLabel(e2); lbl != nil {
		t.Fatalf("expected cleared label nil on e2, got %v", lbl)
	}

	tree := sm.GetTree()
	if tree[0].Label == nil || *tree[0].Label != "checkpoint" || tree[0].LabelTimestamp == nil {
		t.Fatalf("tree node label missing: %+v", tree[0])
	}

	// Branch off e2, preserving labels along e1->e2 while stripping historical label entries and setting ParentSession.
	origFile := *sm.GetSessionFile()
	newFile, err := sm.CreateBranchedSession(e2)
	if err != nil {
		t.Fatal(err)
	}
	if newFile == nil || *newFile == origFile {
		t.Fatalf("expected new branched session file, got %v", newFile)
	}
	if h := sm.GetHeader(); h == nil || h.ParentSession == nil || *h.ParentSession != origFile {
		t.Fatalf("expected ParentSession=%s, got %+v", origFile, h)
	}
	if lbl := sm.GetLabel(e1); lbl == nil || *lbl != "checkpoint" {
		t.Fatalf("expected preserved label on e1 in branched session, got %v", lbl)
	}
	if !sm.UsesDefaultSessionDir() {
		// sessionDir was explicit, not default
	} else {
		t.Fatal("expected UsesDefaultSessionDir()=false for explicit sessionDir")
	}

	// SessionInfo name lookup
	if _, err := sm.AppendSessionInfo("Renamed Session"); err != nil {
		t.Fatal(err)
	}
	if name := sm.GetSessionName(); name == nil || *name != "Renamed Session" {
		t.Fatalf("GetSessionName = %v", name)
	}
}

func TestSessionManagerFileDiscoveryListContinueAndFindById(t *testing.T) {
	tmpDir := t.TempDir()
	cwdA := filepath.Join(tmpDir, "projA")
	cwdB := filepath.Join(tmpDir, "projB")
	sessionDir := filepath.Join(tmpDir, "sessions")
	for _, d := range []string{cwdA, cwdB, sessionDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Empty dir -> ContinueRecent starts new session, FindById returns nil
	fresh, err := SessionManagerContinueRecent(cwdA, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.GetEntryCount() != 0 {
		t.Fatalf("expected empty ContinueRecent on empty dir, got %d entries", fresh.GetEntryCount())
	}
	if found := SessionManagerFindById(cwdA, "nonexistent", sessionDir); found != nil {
		t.Fatalf("expected nil from FindById, got %v", *found)
	}

	// Create two sessions for cwdA (one named) and one for cwdB inside shared sessionDir
	idA1 := "sess-a1"
	smA1, err := SessionManagerCreate(cwdA, sessionDir, &NewSessionOptions{Id: &idA1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := smA1.AppendMessage(tsmcUserMsg("alpha prompt")); err != nil {
		t.Fatal(err)
	}
	if _, err := smA1.AppendSessionInfo("Alpha Work"); err != nil {
		t.Fatal(err)
	}

	idB := "sess-b1"
	smB, err := SessionManagerCreate(cwdB, sessionDir, &NewSessionOptions{Id: &idB})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := smB.AppendMessage(tsmcUserMsg("beta prompt")); err != nil {
		t.Fatal(err)
	}

	// FindById performs exact header ID matching and filters by cwd when explicit sessionDir differs from default.
	if found := SessionManagerFindById(cwdA, idA1, sessionDir); found == nil || *found != *smA1.GetSessionFile() {
		t.Fatalf("FindById(cwdA, %s) = %v", idA1, found)
	}
	if foundPrefix := SessionManagerFindById(cwdA, "sess-a", sessionDir); foundPrefix != nil {
		t.Fatalf("expected nil for partial prefix lookup in FindById, got %v", *foundPrefix)
	}
	if wrongCwd := SessionManagerFindById(cwdA, idB, sessionDir); wrongCwd != nil {
		t.Fatalf("expected nil when finding cwdB session under cwdA, got %v", *wrongCwd)
	}

	// SessionManagerList returns only cwdA sessions; ListAll returns both.
	var progressCalls int
	listA, err := SessionManagerList(context.Background(), cwdA, sessionDir, func(loaded, total int, partial []SessionInfo) {
		progressCalls++
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(listA) != 1 || listA[0].Id != idA1 || listA[0].FirstMessage != "alpha prompt" ||
		listA[0].Name == nil || *listA[0].Name != "Alpha Work" {
		t.Fatalf("listA = %+v", listA)
	}
	if progressCalls == 0 {
		t.Fatal("expected progress callback invoked during List")
	}

	all, err := SessionManagerListAll(context.Background(), sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll len = %d, want 2", len(all))
	}

	// Context cancellation stops ListAll cleanly
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SessionManagerListAll(cancelledCtx, sessionDir); err == nil {
		t.Fatal("expected context error on cancelled ListAll")
	}

	// Open with CwdOverride updates cwd cleanly in memory without touching unrelated session
	override := "/override/cwd"
	opened, err := SessionManagerOpen(*smA1.GetSessionFile(), SessionOpenOptions{
		SessionDir:  sessionDir,
		CwdOverride: &override,
	})
	if err != nil {
		t.Fatal(err)
	}
	if opened.GetCwd() != override {
		t.Fatalf("opened.GetCwd() = %q, want %q", opened.GetCwd(), override)
	}
	if recent := FindMostRecentSession(sessionDir, cwdA); recent == nil || !strings.HasSuffix(*recent, ".jsonl") {
		t.Fatalf("FindMostRecentSession = %v", recent)
	}
	if cont, err := SessionManagerContinueRecent(cwdA, sessionDir); err != nil || cont.GetSessionId() != idA1 {
		t.Fatalf("ContinueRecent = (%v, %v), want %s", cont, err, idA1)
	}

	// Verify UsageEntry append & projection exclusion
	if ue, err := opened.AppendUsage("warm", "anthropic", "claude", ai.Usage{Input: 10}, "warmup"); err != nil || ue.Note == nil || *ue.Note != "warmup" {
		t.Fatalf("AppendUsage = (%+v, %v)", ue, err)
	}
	if roles := tsmcMsgRoles(opened.BuildSessionContext().Messages); !slices.Equal(roles, []string{"user"}) {
		t.Fatalf("UsageEntry leaked into BuildSessionContext: %v", roles)
	}
}
