package tui_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

func TestFormatProgramStatus(t *testing.T) {
	t.Run("minimal idle and clear", func(t *testing.T) {
		if got := tui.FormatProgramStatus(tui.ProgramStatus{State: tui.ProgramStatusStateIdle}); got != "\x1b]7501;state=idle\x1b\\" {
			t.Fatalf("got %q", got)
		}
		if got := tui.FormatProgramStatus(tui.ProgramStatus{State: tui.ProgramStatusStateClear, App: "pi"}); got != "\x1b]7501;state=clear:app=pi\x1b\\" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("omits kind when state is not blocked and enforces 32-char app limit", func(t *testing.T) {
		got := tui.FormatProgramStatus(tui.ProgramStatus{
			State: tui.ProgramStatusStateWorking,
			App:   "pi",
			Kind:  tui.ProgramStatusKindAuth,
		})
		want := "\x1b]7501;state=working:app=pi\x1b\\"
		if got != want {
			t.Fatalf("working+auth: got %q, want %q", got, want)
		}

		app32 := "abcdefghijklmnopqrstuvwxyz123456"
		got32 := tui.FormatProgramStatus(tui.ProgramStatus{
			State: tui.ProgramStatusStateWorking,
			App:   app32,
		})
		want32 := "\x1b]7501;state=working:app=" + app32 + "\x1b\\"
		if got32 != want32 {
			t.Fatalf("32-char app: got %q, want %q", got32, want32)
		}

		app33 := app32 + "7"
		got33 := tui.FormatProgramStatus(tui.ProgramStatus{
			State: tui.ProgramStatusStateWorking,
			App:   app33,
		})
		want33 := "\x1b]7501;state=working\x1b\\"
		if got33 != want33 {
			t.Fatalf("33-char app: got %q, want %q", got33, want33)
		}
	})
	t.Run("sanitizes app, kind, control characters, and truncates long UTF-8 message", func(t *testing.T) {
		got := tui.FormatProgramStatus(tui.ProgramStatus{
			State:   tui.ProgramStatusStateBlocked,
			App:     "bad:app",
			Kind:    tui.ProgramStatusKindPermission,
			Message: "  Allow\n\x07bash?  ",
		})
		wantMsg := base64.StdEncoding.EncodeToString([]byte("Allow bash?"))
		want := "\x1b]7501;state=blocked:kind=permission:msg=" + wantMsg + "\x1b\\"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}

		// Long multi-byte UTF-8 truncation <= 2048 bytes on rune boundary.
		long := strings.Repeat("你", 1000) // 3000 bytes
		encoded := tui.FormatProgramStatus(tui.ProgramStatus{
			State:   tui.ProgramStatusStateWorking,
			App:     "pi_agent-1.0+go",
			Message: long,
		})
		prefix := "\x1b]7501;state=working:app=pi_agent-1.0+go:msg="
		if !strings.HasPrefix(encoded, prefix) || !strings.HasSuffix(encoded, "\x1b\\") {
			t.Fatalf("unexpected format: %q", encoded)
		}
		b64 := strings.TrimSuffix(strings.TrimPrefix(encoded, prefix), "\x1b\\")
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			t.Fatalf("invalid base64: %v", err)
		}
		if len(raw) > 2048 || len(raw)%3 != 0 {
			t.Fatalf("bad truncated byte length: %d", len(raw))
		}
	})

	t.Run("IsProgramStatusReply", func(t *testing.T) {
		if !tui.IsProgramStatusReply("\x1b]7501;?state=working\x07") {
			t.Fatal("expected BEL reply true")
		}
		if !tui.IsProgramStatusReply("\x1b]7501;?unsupported\x1b\\") {
			t.Fatal("expected ST reply true")
		}
		if tui.IsProgramStatusReply("\x1b]7501;state=working\x1b\\") {
			t.Fatal("non-reply status update should be false")
		}
	})
}
