// Ported from packages/coding-agent/test/experimental-cli-entry.test.ts (pi v1.0.0).

package utils_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/codingagent"
	"github.com/keejkrej/pi-go/codingagent/experimental"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	teceEntryModeEnv     = "PI_TEST_CLI_ENTRY_MODE"
	teceEntryArgsEnv     = "PI_TEST_CLI_ENTRY_ARGS"
	teceModeStable       = "stable"
	teceModeExperimental = "experimental"
)

func TestMain(m *testing.M) {
	mode := os.Getenv(teceEntryModeEnv)
	if mode != "" {
		rawArgs := os.Getenv(teceEntryArgsEnv)
		var args []string
		if rawArgs != "" {
			if err := jsonx.Unmarshal([]byte(rawArgs), &args); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "invalid entry args: %v\n", err)
				os.Exit(2)
			}
		}
		ctx := context.Background()
		switch mode {
		case teceModeStable:
			os.Exit(codingagent.Main(ctx, args))
		case teceModeExperimental:
			handled, code := experimental.RunExperimentalCliMain(ctx, args, experimental.CliCommandOptions{
				Fallback: func(fallbackCtx context.Context, fallbackArgs []string) int {
					return codingagent.Main(fallbackCtx, fallbackArgs)
				},
			})
			if !handled {
				os.Exit(0)
			}
			os.Exit(code)
		default:
			_, _ = fmt.Fprintf(os.Stderr, "unknown entry mode: %s\n", mode)
			os.Exit(2)
		}
	}
	os.Exit(m.Run())
}

func teceRunEntry(t *testing.T, mode string, experimentalEnabled bool) (int, string, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dir := t.TempDir()
	argsJson, err := jsonx.Marshal([]string{"server", "--server-id", "invalid", "--version"})
	if err != nil {
		t.Fatalf("jsonx.Marshal args: %v", err)
	}
	expVal := "0"
	if experimentalEnabled {
		expVal = "1"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe)
	cmd.Dir = dir
	cmd.Env = teceBuildEnv(map[string]string{
		teceEntryModeEnv:      mode,
		teceEntryArgsEnv:      string(argsJson),
		"HOME":                dir,
		"USERPROFILE":         dir,
		"PI_CODING_AGENT_DIR": filepath.Join(dir, "agent"),
		"PI_OFFLINE":          "1",
		"PI_EXPERIMENTAL":     expVal,
	})
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("exec %s: %v (stderr=%q)", mode, err, stderr.String())
		}
	}
	return code, stdout.String(), stderr.String()
}

func teceBuildEnv(overrides map[string]string) []string {
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, kv := range os.Environ() {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if _, replaced := overrides[key]; replaced {
			continue
		}
		env = append(env, kv)
	}
	for k, v := range overrides {
		env = append(env, k+"="+v)
	}
	return env
}

func teceRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected repo go.mod at %s: %v", root, err)
	}
	return root
}

func TestExperimentalCliEntrypoints(t *testing.T) {
	t.Run("does not dispatch experimental commands from the stable entrypoint", func(t *testing.T) {
		code, stdout, stderr := teceRunEntry(t, teceModeStable, true)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr=%q stdout=%q)", code, stderr, stdout)
		}
		if got := strings.TrimSpace(stdout); got != utils.VERSION {
			t.Fatalf("stdout = %q, want %q (stderr=%q)", got, utils.VERSION, stderr)
		}
	})

	t.Run("keeps experimental dispatch in the development entrypoint", func(t *testing.T) {
		code, stdout, stderr := teceRunEntry(t, teceModeExperimental, true)
		if code != 1 {
			t.Fatalf("exit code = %d, want 1 (stderr=%q stdout=%q)", code, stderr, stdout)
		}
		if !strings.Contains(stderr, "Invalid --server-id") {
			t.Fatalf("stderr = %q, want substring \"Invalid --server-id\"", stderr)
		}
		if strings.Contains(stdout, utils.VERSION) {
			t.Fatalf("stdout = %q, must not contain version %q", stdout, utils.VERSION)
		}
	})

	t.Run("falls back to the stable CLI when experiments are disabled", func(t *testing.T) {
		code, stdout, stderr := teceRunEntry(t, teceModeExperimental, false)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr=%q stdout=%q)", code, stderr, stdout)
		}
		if got := strings.TrimSpace(stdout); got != utils.VERSION {
			t.Fatalf("stdout = %q, want %q (stderr=%q)", got, utils.VERSION, stderr)
		}
	})

	// #9132: enabling experiments must not pull remote-server dependencies into the published CLI.
	t.Run("keeps experimental and remote-server packages out of published CLI deps", func(t *testing.T) {
		repoRoot := teceRepoRoot(t)
		cmd := exec.Command("go", "list", "-deps", "./codingagent", "./cmd/pi")
		cmd.Dir = repoRoot
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("go list -deps: %v (stderr=%s)", err, stderr.String())
		}
		forbiddenPrefixes := []string{
			"github.com/keejkrej/pi-go/codingagent/experimental",
			"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli",
			"github.com/keejkrej/pi-go/server",
			"github.com/keejkrej/pi-go/client",
			"github.com/keejkrej/pi-go/chord",
		}
		for _, dep := range strings.Split(stdout.String(), "\n") {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			for _, prefix := range forbiddenPrefixes {
				if dep == prefix || strings.HasPrefix(dep, prefix+"/") {
					t.Fatalf("published CLI unexpectedly depends on forbidden package %q", dep)
				}
			}
		}
	})
}
