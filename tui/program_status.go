// Ported from packages/tui/src/program-status.ts (pi v1.5.2).

package tui

import (
	"encoding/base64"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/js"
)

// ProgramStatusState is the OSC 7501 program execution state.
type ProgramStatusState string

const (
	ProgramStatusStateIdle    ProgramStatusState = "idle"
	ProgramStatusStateWorking ProgramStatusState = "working"
	ProgramStatusStateBlocked ProgramStatusState = "blocked"
	ProgramStatusStateDone    ProgramStatusState = "done"
	ProgramStatusStateError   ProgramStatusState = "error"
	ProgramStatusStateClear   ProgramStatusState = "clear"
)

// ProgramStatusKind categorizes a blocked ProgramStatus prompt.
type ProgramStatusKind string

const (
	ProgramStatusKindPermission ProgramStatusKind = "permission"
	ProgramStatusKindQuestion   ProgramStatusKind = "question"
	ProgramStatusKindAuth       ProgramStatusKind = "auth"
)

// ProgramStatus describes the semantic state emitted via OSC 7501.
type ProgramStatus struct {
	State   ProgramStatusState `json:"state"`
	App     string             `json:"app,omitzero"`
	Kind    ProgramStatusKind  `json:"kind,omitzero"`
	Message string             `json:"message,omitzero"`
}

const (
	ProgramStatusQuery = "\x1b]7501;?\x1b\\"
	psMaxMessageBytes  = 2048
)

var (
	psAppPattern   = regexp.MustCompile(`^[A-Za-z0-9_.+-]{1,32}$`)
	psReplyPattern = regexp.MustCompile("^\x1b\\]7501;\\?[^\x07\x1b]*(?:\x07|\x1b\\\\)$")
)

// IsProgramStatusReply reports whether sequence is an OSC 7501 query reply.
func IsProgramStatusReply(sequence string) bool {
	return psReplyPattern.MatchString(sequence)
}

// FormatProgramStatus encodes status as an OSC 7501 escape sequence.
func FormatProgramStatus(status ProgramStatus) string {
	parts := []string{"state=" + string(status.State)}
	if psAppPattern.MatchString(status.App) {
		parts = append(parts, "app="+status.App)
	}
	if status.State == ProgramStatusStateBlocked && (status.Kind == ProgramStatusKindPermission || status.Kind == ProgramStatusKindQuestion || status.Kind == ProgramStatusKindAuth) {
		parts = append(parts, "kind="+string(status.Kind))
	}
	if status.Message != "" {
		clean := js.Trim(ReplaceControlCharacters(status.Message))
		if clean != "" {
			truncated := psTruncateUTF8Bytes(clean, psMaxMessageBytes)
			encoded := base64.StdEncoding.EncodeToString([]byte(truncated))
			parts = append(parts, "msg="+encoded)
		}
	}
	return "\x1b]7501;" + strings.Join(parts, ":") + "\x1b\\"
}

func psTruncateUTF8Bytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	n := 0
	for i := 0; i < len(s); {
		_, size := utf8.DecodeRuneInString(s[i:])
		if n+size > maxBytes {
			break
		}
		n += size
		i += size
	}
	return s[:n]
}
