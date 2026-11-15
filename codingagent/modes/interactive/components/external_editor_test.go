package components

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestExternalEditorParseCompleteBomAndErrors(t *testing.T) {
	origExec := eedExec
	t.Cleanup(func() { eedExec = origExec })

	// Quote-aware parsing preserves quoted flags and spaced arguments.
	parts := eedParseCommand(`code --wait -n 'foo bar' "baz \"qux\""`)
	want := []string{"code", "--wait", "-n", "foo bar", `baz "qux"`}
	if len(parts) != len(want) {
		t.Fatalf("parsed %v, want %v", parts, want)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("arg[%d] = %q, want %q", i, parts[i], want[i])
		}
	}

	// Empty command fails cleanly.
	resEmpty := EditInExternalEditor(ExternalEditorOptions{Command: "   ", Content: "orig"})
	if resEmpty.Status != ExternalEditorStatusFailed || resEmpty.Content != "orig" {
		t.Fatalf("unexpected empty command result: %+v", resEmpty)
	}

	// Successful edit reads edited file and strips UTF-8 BOM.
	var seenFile string
	eedExec = func(name string, args []string) error {
		if name != "mock-vim" || len(args) != 2 || args[0] != "--nofork" {
			return errors.New("unexpected command invocation")
		}
		seenFile = args[1]
		if _, err := os.Stat(seenFile); err != nil {
			return err
		}
		return os.WriteFile(seenFile, []byte("\xEF\xBB\xBFupdated text\n"), 0o600)
	}

	res := EditInExternalEditor(ExternalEditorOptions{
		Command: "mock-vim --nofork",
		Content: "draft",
	})
	if res.Status != ExternalEditorStatusComplete || res.Content != "updated text\n" {
		t.Fatalf("unexpected complete result: %+v", res)
	}
	if _, err := os.Stat(seenFile); !os.IsNotExist(err) {
		t.Fatalf("expected temp file %q cleaned up after EditInExternalEditor", seenFile)
	}

	// Non-zero exit preserves original content and returns failed status.
	eedExec = func(string, []string) error {
		return errors.New("exit status 1")
	}
	resFail := EditInExternalEditor(ExternalEditorOptions{
		Command: "mock-vim",
		Content: "keep-me",
	})
	if resFail.Status != ExternalEditorStatusFailed || resFail.Content != "keep-me" || !strings.Contains(resFail.Message, "exit status 1") {
		t.Fatalf("unexpected failure result: %+v", resFail)
	}
}
