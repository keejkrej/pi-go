// Offline unit tests for cmd/pi-ai CLI helpers and command dispatch (pi v1.5.2).

package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tmainCaptureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	return buf.String()
}

func TestMainRun_HelpAndList(t *testing.T) {
	for _, args := range [][]string{{}, {"help"}, {"--help"}, {"-h"}} {
		var runErr error
		out := tmainCaptureStdout(t, func() {
			runErr = mainRun(args, strings.NewReader(""))
		})
		if runErr != nil {
			t.Fatalf("mainRun(%v) error: %v", args, runErr)
		}
		if !strings.Contains(out, "Usage: npx @earendil-works/pi-ai <command> [provider]") {
			t.Fatalf("mainRun(%v) missing usage header:\n%s", args, out)
		}
		if !strings.Contains(out, "anthropic") || !strings.Contains(out, "github-copilot") {
			t.Fatalf("mainRun(%v) missing OAuth providers:\n%s", args, out)
		}
	}

	var listErr error
	listOut := tmainCaptureStdout(t, func() {
		listErr = mainRun([]string{"list"}, strings.NewReader(""))
	})
	if listErr != nil {
		t.Fatalf("mainRun(list) error: %v", listErr)
	}
	if !strings.Contains(listOut, "anthropic") || !strings.Contains(listOut, "openai-codex") {
		t.Fatalf("mainRun(list) unexpected output:\n%s", listOut)
	}
}

func TestMainRun_UnknownCommandAndProvider(t *testing.T) {
	err := mainRun([]string{"unknown-cmd"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "Unknown command: unknown-cmd") {
		t.Fatalf("expected Unknown command error, got %v", err)
	}

	err = mainRun([]string{"login", "no-such-provider"}, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "Unknown provider: no-such-provider") {
		t.Fatalf("expected Unknown provider error, got %v", err)
	}
}

func TestMainSaveLoadAuth_PreservesUnknownKeysAndEnforces0600(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Create pre-existing auth.json with looser permissions and another provider key.
	if err := os.WriteFile(mainAuthFile, []byte(`{"existing":{"type":"api_key","key":"secret"}}`), 0o644); err != nil {
		t.Fatalf("seed auth.json: %v", err)
	}

	doc := mainLoadAuth()
	if _, ok := doc.Get("existing"); !ok {
		t.Fatal("expected existing key loaded")
	}
	newCred := jsonx.NewObject()
	newCred.Set("type", "oauth")
	newCred.Set("access", "tok")
	doc.Set("anthropic", newCred)

	if err := mainSaveAuth(doc); err != nil {
		t.Fatalf("mainSaveAuth error: %v", err)
	}

	info, err := os.Stat(mainAuthFile)
	if err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("auth.json mode = %#o, want 0600", mode)
	}

	reloaded := mainLoadAuth()
	if _, ok := reloaded.Get("existing"); !ok {
		t.Fatal("expected existing key preserved after save")
	}
	if _, ok := reloaded.Get("anthropic"); !ok {
		t.Fatal("expected anthropic key persisted")
	}
}
