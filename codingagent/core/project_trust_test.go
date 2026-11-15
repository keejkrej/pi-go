package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

type tptMockUI struct {
	choice  *string
	err     error
	calls   int
	titles  []string
	options [][]string
}

func (u *tptMockUI) Select(_ context.Context, title string, options []string, _ ...*ExtensionUIDialogOptions) (*string, error) {
	u.calls++
	u.titles = append(u.titles, title)
	cp := append([]string(nil), options...)
	u.options = append(u.options, cp)
	if u.err != nil {
		return nil, u.err
	}
	return u.choice, nil
}

func (*tptMockUI) Confirm(_ context.Context, _, _ string, _ ...*ExtensionUIDialogOptions) (bool, error) {
	return false, nil
}

func (*tptMockUI) Input(_ context.Context, _ string, _ *string, _ ...*ExtensionUIDialogOptions) (*string, error) {
	return nil, nil
}

func (*tptMockUI) Notify(_ string, _ ...string) {}

func tptBoolPtr(v bool) *bool    { return &v }
func tptStrPtr(v string) *string { return &v }

func tptSetupFixture(t *testing.T) (projectDir string, store *ProjectTrustStore) {
	t.Helper()
	rootDir := t.TempDir()
	homeDir := filepath.Join(rootDir, "home")
	agentDir := filepath.Join(rootDir, "agent")
	projectDir = filepath.Join(rootDir, "project")
	for _, d := range []string{homeDir, agentDir, projectDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", homeDir)
	return projectDir, NewProjectTrustStore(agentDir)
}

func TestResolveProjectTrustedSavedDecisions(t *testing.T) {
	projectDir, store := tptSetupFixture(t)
	ctx := context.Background()

	// Without trust-requiring resources or saved decision, project defaults to trusted.
	trusted, err := ResolveProjectTrusted(ctx, ResolveProjectTrustedOptions{
		Cwd:        projectDir,
		TrustStore: store,
	})
	if err != nil || !trusted {
		t.Fatalf("expected trusted=true without resources, got trusted=%v err=%v", trusted, err)
	}

	// Saved false decision prevents trust even when project has no trust-requiring files.
	if err := store.Set(projectDir, tptBoolPtr(false)); err != nil {
		t.Fatal(err)
	}
	trusted, err = ResolveProjectTrusted(ctx, ResolveProjectTrustedOptions{
		Cwd:        projectDir,
		TrustStore: store,
	})
	if err != nil || trusted {
		t.Fatalf("expected saved false decision to prevent auto-trust, got trusted=%v err=%v", trusted, err)
	}

	// Create a trust-requiring .pi/settings.json and verify saved true decision avoids UI prompt.
	if err := os.MkdirAll(filepath.Join(projectDir, utils.ConfigDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, utils.ConfigDirName, "settings.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(projectDir, tptBoolPtr(true)); err != nil {
		t.Fatal(err)
	}
	ui := &tptMockUI{}
	trusted, err = ResolveProjectTrusted(ctx, ResolveProjectTrustedOptions{
		Cwd:        projectDir,
		TrustStore: store,
		ProjectTrustContext: ProjectTrustContext{
			Cwd:   projectDir,
			Mode:  ExtensionModeTui,
			HasUI: true,
			Ui:    ui,
		},
	})
	if err != nil || !trusted {
		t.Fatalf("expected saved true trust decision, got trusted=%v err=%v", trusted, err)
	}
	if ui.calls != 0 {
		t.Fatalf("expected no UI prompt when trust decision saved, got %d calls", ui.calls)
	}
}

func TestResolveProjectTrustedPoliciesOverridesAndPrompts(t *testing.T) {
	ctx := context.Background()

	// Helper to make fresh project with .pi/settings.json
	makeUntrustedProject := func(t *testing.T) (string, *ProjectTrustStore) {
		t.Helper()
		dir, st := tptSetupFixture(t)
		cfgDir := filepath.Join(dir, utils.ConfigDirName)
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir, st
	}

	// CLI override takes precedence over stored decision and policies.
	dir, store := makeUntrustedProject(t)
	if err := store.Set(dir, tptBoolPtr(false)); err != nil {
		t.Fatal(err)
	}
	trusted, err := ResolveProjectTrusted(ctx, ResolveProjectTrustedOptions{
		Cwd:           dir,
		TrustStore:    store,
		TrustOverride: tptBoolPtr(true),
	})
	if err != nil || !trusted {
		t.Fatalf("TrustOverride=true -> (%v, %v)", trusted, err)
	}
	trusted, err = ResolveProjectTrusted(ctx, ResolveProjectTrustedOptions{
		Cwd:           dir,
		TrustStore:    store,
		TrustOverride: tptBoolPtr(false),
	})
	if err != nil || trusted {
		t.Fatalf("TrustOverride=false -> (%v, %v)", trusted, err)
	}

	// DefaultProjectTrustAlways / Never without UI
	alwaysPolicy := DefaultProjectTrustAlways
	neverPolicy := DefaultProjectTrustNever
	dir2, store2 := makeUntrustedProject(t)
	trusted, err = ResolveProjectTrusted(ctx, ResolveProjectTrustedOptions{
		Cwd:                 dir2,
		TrustStore:          store2,
		DefaultProjectTrust: &alwaysPolicy,
		ExtensionsResult:    &LoadExtensionsResult{},
	})
	if err != nil || !trusted {
		t.Fatalf("DefaultProjectTrustAlways -> (%v, %v)", trusted, err)
	}
	trusted, err = ResolveProjectTrusted(ctx, ResolveProjectTrustedOptions{
		Cwd:                 dir2,
		TrustStore:          store2,
		DefaultProjectTrust: &neverPolicy,
	})
	if err != nil || trusted {
		t.Fatalf("DefaultProjectTrustNever -> (%v, %v)", trusted, err)
	}

	// Ask policy without UI returns false
	askPolicy := DefaultProjectTrustAsk
	trusted, err = ResolveProjectTrusted(ctx, ResolveProjectTrustedOptions{
		Cwd:                 dir2,
		TrustStore:          store2,
		DefaultProjectTrust: &askPolicy,
	})
	if err != nil || trusted {
		t.Fatalf("Ask without UI -> (%v, %v)", trusted, err)
	}

	// Ask policy with UI: session-only returns true without saving
	sessionUI := &tptMockUI{choice: tptStrPtr("Trust (this session only)")}
	trusted, err = ResolveProjectTrusted(ctx, ResolveProjectTrustedOptions{
		Cwd:        dir2,
		TrustStore: store2,
		ProjectTrustContext: ProjectTrustContext{
			Cwd:   dir2,
			Mode:  ExtensionModeTui,
			HasUI: true,
			Ui:    sessionUI,
		},
	})
	if err != nil || !trusted || sessionUI.calls != 1 {
		t.Fatalf("session-only UI -> (%v, %v, calls=%d)", trusted, err, sessionUI.calls)
	}
	if dec, err := store2.Get(dir2); err != nil || dec != nil {
		t.Fatalf("expected session-only not to persist decision, got (%v, %v)", dec, err)
	}

	// Ask policy with UI: nil cancel returns false
	cancelUI := &tptMockUI{choice: nil}
	trusted, err = ResolveProjectTrusted(ctx, ResolveProjectTrustedOptions{
		Cwd:        dir2,
		TrustStore: store2,
		ProjectTrustContext: ProjectTrustContext{
			Cwd:   dir2,
			Mode:  ExtensionModeTui,
			HasUI: true,
			Ui:    cancelUI,
		},
	})
	if err != nil || trusted {
		t.Fatalf("cancel UI -> (%v, %v)", trusted, err)
	}

	// Ask policy with UI: Trust saves true
	trustUI := &tptMockUI{choice: tptStrPtr("Trust")}
	trusted, err = ResolveProjectTrusted(ctx, ResolveProjectTrustedOptions{
		Cwd:        dir2,
		TrustStore: store2,
		ProjectTrustContext: ProjectTrustContext{
			Cwd:   dir2,
			Mode:  ExtensionModeTui,
			HasUI: true,
			Ui:    trustUI,
		},
	})
	if err != nil || !trusted {
		t.Fatalf("Trust UI -> (%v, %v)", trusted, err)
	}
	if dec, err := store2.Get(dir2); err != nil || dec == nil || !*dec {
		t.Fatalf("expected persisted true decision, got (%v, %v)", dec, err)
	}
}
