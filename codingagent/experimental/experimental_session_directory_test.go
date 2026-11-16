// Ported from packages/coding-agent/test/experimental-session-directory.test.ts (pi v1.0.0).

package experimental_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/experimental"
)

func TestExperimentalServerSessionDirectory(t *testing.T) {
	t.Run("uses the experimental directory under the configured agent directory by default", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", "/tmp/pi-agent-config")
		got := experimental.ResolveSessionDirectory()
		want := filepath.Clean("/tmp/pi-agent-config/experimental/sessions")
		if got != want {
			t.Fatalf("ResolveSessionDirectory() = %q, want %q", got, want)
		}
	})

	t.Run("resolves an explicit relative directory from the current working directory", func(t *testing.T) {
		t.Setenv("PI_CODING_AGENT_DIR", "/tmp/pi-agent-config")
		got := experimental.ResolveSessionDirectory("relative/sessions")
		want, err := filepath.Abs("relative/sessions")
		if err != nil {
			t.Fatalf("Abs failed: %v", err)
		}
		if got != want {
			t.Fatalf("ResolveSessionDirectory(relative) = %q, want %q", got, want)
		}
	})

	t.Run("expands a tilde in an explicit directory", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("UserHomeDir failed: %v", err)
		}
		got := experimental.ResolveSessionDirectory("~/custom-sessions")
		want := filepath.Join(home, "custom-sessions")
		if got != want {
			t.Fatalf("ResolveSessionDirectory(~/custom-sessions) = %q, want %q", got, want)
		}
	})
}
