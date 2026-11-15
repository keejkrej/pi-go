// Ported from packages/coding-agent/src/core/session-export.ts (pi v1.5.2).

package core

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// SessionExportTrailingEntries produces extra export-only JSONL objects appended after the branch.
type SessionExportTrailingEntries func(parentId *string, timestamp string) []any

// SerializeSessionBranch serializes the active branch and optional export-only entries as JSONL.
func SerializeSessionBranch(sm *SessionManager, createTrailingEntries ...SessionExportTrailingEntries) string {
	timestamp := smNowISO()
	ver := CurrentSessionVersion
	header := &SessionHeader{
		Type:      "session",
		Version:   &ver,
		Id:        sm.GetSessionId(),
		Timestamp: timestamp,
		Cwd:       sm.GetCwd(),
	}

	var lines []string
	if hLine, err := header.marshalJsonLine(); err == nil {
		lines = append(lines, hLine)
	}

	var parentId *string
	for _, entry := range sm.GetBranch() {
		cloned := entry.cloneWithParentId(parentId)
		if line, err := cloned.marshalJsonLine(); err == nil {
			lines = append(lines, line)
		}
		eid := entry.GetId()
		parentId = &eid
	}

	if len(createTrailingEntries) > 0 && createTrailingEntries[0] != nil {
		for _, extra := range createTrailingEntries[0](parentId, timestamp) {
			if line, err := jsonx.Stringify(extra); err == nil {
				lines = append(lines, line)
			}
		}
	}

	return strings.Join(lines, "\n") + "\n"
}

// ExportSessionToJsonl writes the active session branch to outputPath (or a timestamped JSONL file in cwd).
func ExportSessionToJsonl(sm *SessionManager, outputPath ...string) (string, error) {
	out := ""
	if len(outputPath) > 0 {
		out = outputPath[0]
	}
	return ExportSessionToJsonlWithTrailing(sm, out)
}

// ExportSessionToJsonlWithTrailing writes the active session branch and optional trailing entries to outputPath.
func ExportSessionToJsonlWithTrailing(
	sm *SessionManager,
	outputPath string,
	createTrailingEntries ...SessionExportTrailingEntries,
) (string, error) {
	target := outputPath
	if target == "" {
		safeTime := smFilenameSafeTimeRe.ReplaceAllString(smNowISO(), "-")
		target = "session-" + safeTime + ".jsonl"
	}
	filePath := smResolvePath(target)
	dir := filepath.Dir(filePath)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	content := SerializeSessionBranch(sm, createTrailingEntries...)
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return "", err
	}
	return filePath, nil
}
