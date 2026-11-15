package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

func TestProjectTrustStoreAndResources(t *testing.T) {
	t.Run("Get/Set/SetMany and nearest ancestor inheritance", func(t *testing.T) {
		agentDir := t.TempDir()
		root := t.TempDir()
		parent := filepath.Join(root, "parent")
		child := filepath.Join(parent, "child")
		grandchild := filepath.Join(child, "grandchild")
		for _, p := range []string{parent, child, grandchild} {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
		}

		store := NewProjectTrustStore(agentDir)
		dec, err := store.Get(child)
		if err != nil || dec != nil {
			t.Fatalf("expected undecided nil, got (%v, %v)", dec, err)
		}

		if err := store.Set(parent, tmBoolPtr(true)); err != nil {
			t.Fatal(err)
		}
		if err := store.Set(child, tmBoolPtr(false)); err != nil {
			t.Fatal(err)
		}

		if got, err := store.Get(grandchild); err != nil || got == nil || *got {
			t.Fatalf("expected child false for grandchild, got (%v, %v)", got, err)
		}

		// Clearing child via nil lets parent trust apply again.
		if err := store.Set(child, nil); err != nil {
			t.Fatal(err)
		}
		if got, err := store.Get(grandchild); err != nil || got == nil || !*got {
			t.Fatalf("expected parent true after clearing child, got (%v, %v)", got, err)
		}
	})

	t.Run("byte-exact trust.json UTF-16 sorted key order and trailing newline", func(t *testing.T) {
		agentDir := t.TempDir()
		root := t.TempDir()
		pathZ := filepath.Join(root, "z-dir")
		pathA := filepath.Join(root, "a-dir")
		if err := os.MkdirAll(pathZ, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(pathA, 0o755); err != nil {
			t.Fatal(err)
		}

		store := NewProjectTrustStore(agentDir)
		if err := store.SetMany([]ProjectTrustUpdate{
			{Path: pathZ, Decision: tmBoolPtr(true)},
			{Path: pathA, Decision: tmBoolPtr(false)},
		}); err != nil {
			t.Fatal(err)
		}

		rawBytes, err := os.ReadFile(filepath.Join(agentDir, "trust.json"))
		if err != nil {
			t.Fatal(err)
		}
		raw := string(rawBytes)
		if !strings.HasSuffix(raw, "\n") {
			t.Fatalf("trust.json must end with trailing newline, got %q", raw)
		}
		normA := tmNormalizeCwd(pathA)
		normZ := tmNormalizeCwd(pathZ)
		idxA := strings.Index(raw, normA)
		idxZ := strings.Index(raw, normZ)
		if idxA < 0 || idxZ < 0 || idxA > idxZ {
			t.Fatalf("expected %q before %q in trust.json:\n%s", normA, normZ, raw)
		}
	})

	t.Run("GetProjectTrustOptions formats labels and updates", func(t *testing.T) {
		root := t.TempDir()
		proj := filepath.Join(root, "repo")
		if err := os.MkdirAll(proj, 0o755); err != nil {
			t.Fatal(err)
		}

		opts := GetProjectTrustOptions(proj, ProjectTrustOptionsConfig{IncludeSessionOnly: true})
		if len(opts) != 5 {
			t.Fatalf("expected 5 options with parent + session-only, got %d", len(opts))
		}
		if opts[0].Label != "Trust" || !opts[0].Trusted {
			t.Fatalf("unexpected first option: %+v", opts[0])
		}
		if !strings.HasPrefix(opts[1].Label, "Trust parent folder (") || len(opts[1].Updates) != 2 || opts[1].Updates[1].Decision != nil {
			t.Fatalf("unexpected parent option: %+v", opts[1])
		}
		if opts[2].Label != "Trust (this session only)" || len(opts[2].Updates) != 0 {
			t.Fatalf("unexpected session-only trust option: %+v", opts[2])
		}
		if opts[3].Label != "Do not trust" || opts[3].Trusted {
			t.Fatalf("unexpected deny option: %+v", opts[3])
		}
		if opts[4].Label != "Do not trust (this session only)" || opts[4].Trusted {
			t.Fatalf("unexpected session-only deny option: %+v", opts[4])
		}
	})

	t.Run("HasTrustRequiringProjectResources ignores global ~/.agents/skills and detects project/ancestor resources", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		if HasTrustRequiringProjectResources(home) {
			t.Fatal("global ~/.agents/skills must not require project trust")
		}

		proj := filepath.Join(home, "work", "proj")
		nested := filepath.Join(proj, "sub")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		if HasTrustRequiringProjectResources(nested) {
			t.Fatal("clean dir should not require project trust")
		}

		if err := os.MkdirAll(filepath.Join(proj, utils.ConfigDirName), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proj, utils.ConfigDirName, "settings.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !HasTrustRequiringProjectResources(proj) {
			t.Fatal("expected .pi/settings.json to require trust")
		}
		if HasTrustRequiringProjectResources(nested) {
			t.Fatal("ancestor .pi/settings.json alone should not trigger nested dir unless .agents/skills")
		}

		if err := os.MkdirAll(filepath.Join(proj, ".agents", "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		if !HasTrustRequiringProjectResources(nested) {
			t.Fatal("ancestor .agents/skills should require project trust")
		}
	})
}
