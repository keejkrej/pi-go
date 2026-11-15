// Ported from packages/coding-agent/src/core/crash-log.ts (pi v1.5.2).

package core

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// CrashLogKind distinguishes fatal crash classifications persisted to crashes.json.
type CrashLogKind = string

const (
	CrashKindUncaughtException CrashLogKind = "uncaught_exception"
	CrashKindFatalError        CrashLogKind = "fatal_error"
)

const (
	clMaxCrashRecords = 5
	clMaxAgeMs        = int64(7 * 24 * 60 * 60 * 1000)
)

// CrashRecord represents one persisted crash record in crashes.json.
type CrashRecord struct {
	Timestamp   string  `json:"timestamp"`
	Version     string  `json:"version"`
	Kind        string  `json:"kind"`
	Message     string  `json:"message"`
	Stack       *string `json:"stack"`
	SessionFile *string `json:"sessionFile"`
	Cwd         string  `json:"cwd"`
	Notified    *bool   `json:"notified,omitzero"`
}

// RecordCrashInput describes a fatal failure to record in the crash log.
type RecordCrashInput struct {
	Kind        string
	Error       any
	SessionFile *string
	Cwd         string
}

var (
	clDriveLetterRegex    = regexp.MustCompile(`^[a-zA-Z]:/`)
	clStackAtRegex        = regexp.MustCompile(`^\s+at\s`)
	clRemoteSourceRegex   = regexp.MustCompile(`^(?:npm:|git:|https?://|ssh://)`)
	clJsExtRegex          = regexp.MustCompile(`\.[cm]?[jt]s$`)
	clIndexEntryFileRegex = regexp.MustCompile(`/index\.[cm]?[jt]s$`)
)

// CrashLogPath returns the default crashes.json path inside agentDir (or GetAgentDir()).
func CrashLogPath(agentDir ...string) string {
	dir := utils.GetAgentDir()
	if len(agentDir) > 0 && agentDir[0] != "" {
		dir = agentDir[0]
	}
	return filepath.Join(dir, "crashes.json")
}

// ReadCrashLog reads and filters valid CrashRecord entries from crashes.json.
func ReadCrashLog(logPath ...string) []CrashRecord {
	p := CrashLogPath()
	if len(logPath) > 0 && logPath[0] != "" {
		p = logPath[0]
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return []CrashRecord{}
	}
	var records []CrashRecord
	if err := jsonx.Unmarshal(data, &records); err != nil || records == nil {
		return []CrashRecord{}
	}
	valid := make([]CrashRecord, 0, len(records))
	for _, r := range records {
		if r.Timestamp != "" && r.Message != "" {
			valid = append(valid, r)
		}
	}
	return valid
}

func clWriteCrashLog(records []CrashRecord, logPath string) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	data, err := jsonx.MarshalIndent(records, "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(logPath, append(data, '\n'), 0o644)
}

func clNormalizeStackPath(value string) string {
	s := strings.ReplaceAll(value, `\`, "/")
	return strings.TrimRight(s, "/")
}

func clIsWordBoundaryOrPunct(b byte) bool {
	return b == ':' || b == ')' || b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func clStackContainsPath(stack, targetPath string, includeDescendants bool) bool {
	target := clNormalizeStackPath(targetPath)
	if target == "" || utils.IsSyntheticPath(target) {
		return false
	}
	caseInsensitive := clDriveLetterRegex.MatchString(target)
	haystack := stack
	needle := target
	if caseInsensitive {
		haystack = strings.ToLower(stack)
		needle = strings.ToLower(target)
	}
	if includeDescendants {
		return strings.Contains(haystack, needle+"/")
	}

	offset := 0
	for {
		idx := strings.Index(haystack[offset:], needle)
		if idx == -1 {
			return false
		}
		absEnd := offset + idx + len(needle)
		if absEnd == len(haystack) || clIsWordBoundaryOrPunct(haystack[absEnd]) {
			return true
		}
		offset += idx + len(needle)
	}
}

// FindExtensionStackMatches returns extension labels whose source files appear in a stack trace.
func FindExtensionStackMatches(stack *string, extensions []Extension) []string {
	if stack == nil || *stack == "" {
		return []string{}
	}
	lines := strings.Split(*stack, "\n")
	var stackLines []string
	if len(lines) > 1 {
		for _, line := range lines[1:] {
			if !clStackAtRegex.MatchString(line) {
				continue
			}
			if decoded, err := url.PathUnescape(line); err == nil {
				line = decoded
			}
			stackLines = append(stackLines, line)
		}
	}
	normalizedStack := strings.ReplaceAll(strings.Join(stackLines, "\n"), `\`, "/")

	matches := []string{}
	seen := make(map[string]struct{})
	for _, ext := range extensions {
		resolvedPath := clNormalizeStackPath(ext.ResolvedPath)
		singleFilePackage := ext.SourceInfo.Origin == "package" &&
			!clRemoteSourceRegex.MatchString(ext.SourceInfo.Source) &&
			clJsExtRegex.MatchString(ext.SourceInfo.Source)
		var packageRoot string
		if ext.SourceInfo.Origin == "package" && !singleFilePackage && ext.SourceInfo.BaseDir != "" {
			packageRoot = ext.SourceInfo.BaseDir
		}
		slashIndex := strings.LastIndex(resolvedPath, "/")
		directoryEntry := clIndexEntryFileRegex.MatchString(resolvedPath)

		var matched bool
		switch {
		case packageRoot != "":
			matched = clStackContainsPath(normalizedStack, packageRoot, true)
		case directoryEntry && slashIndex != -1:
			matched = clStackContainsPath(normalizedStack, resolvedPath[:slashIndex], true)
		default:
			matched = clStackContainsPath(normalizedStack, resolvedPath, false)
		}
		if !matched {
			continue
		}

		label := ext.Path
		if ext.SourceInfo.Origin == "package" && ext.SourceInfo.Source != "" {
			label = ext.SourceInfo.Source
		}
		if _, exists := seen[label]; !exists {
			seen[label] = struct{}{}
			matches = append(matches, label)
		}
	}
	return matches
}

// RecordCrash writes a best-effort crash entry to crashes.json, keeping the latest 5 records.
func RecordCrash(crash RecordCrashInput, logPath ...string) *CrashRecord {
	p := CrashLogPath()
	if len(logPath) > 0 && logPath[0] != "" {
		p = logPath[0]
	}

	var msg string
	switch e := crash.Error.(type) {
	case nil:
		msg = "nil"
	case error:
		msg = e.Error()
		if msg == "" {
			msg = "error"
		}
	case string:
		msg = e
	default:
		msg = "error"
	}

	record := CrashRecord{
		Timestamp:   time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Version:     utils.VERSION,
		Kind:        crash.Kind,
		Message:     msg,
		Stack:       nil,
		SessionFile: crash.SessionFile,
		Cwd:         crash.Cwd,
	}

	existing := ReadCrashLog(p)
	combined := append(existing, record)
	if len(combined) > clMaxCrashRecords {
		combined = combined[len(combined)-clMaxCrashRecords:]
	}
	if err := clWriteCrashLog(combined, p); err != nil {
		return nil
	}
	return &record
}

// TakeUnnotifiedCrash returns the newest unnotified crash within 7 days and marks pending records notified.
func TakeUnnotifiedCrash(logPath ...string) *CrashRecord {
	return TakeUnnotifiedCrashAt(time.Now().UnixMilli(), logPath...)
}

// TakeUnnotifiedCrashAt returns the newest unnotified crash within 7 days relative to nowMs.
func TakeUnnotifiedCrashAt(nowMs int64, logPath ...string) *CrashRecord {
	p := CrashLogPath()
	if len(logPath) > 0 && logPath[0] != "" {
		p = logPath[0]
	}
	records := ReadCrashLog(p)
	if len(records) == 0 {
		return nil
	}
	var found *CrashRecord
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if rec.Notified != nil && *rec.Notified {
			continue
		}
		ts, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
		if err != nil {
			continue
		}
		if nowMs-ts.UnixMilli() <= clMaxAgeMs {
			copied := rec
			found = &copied
			break
		}
	}
	if found == nil {
		return nil
	}

	updated := make([]CrashRecord, len(records))
	truth := true
	for i, rec := range records {
		if rec.Notified != nil && *rec.Notified {
			updated[i] = rec
		} else {
			rec.Notified = &truth
			updated[i] = rec
		}
	}
	_ = clWriteCrashLog(updated, p)
	return found
}

// ClearCrashLog removes crashes.json if present (ignoring errors).
func ClearCrashLog(logPath ...string) {
	p := CrashLogPath()
	if len(logPath) > 0 && logPath[0] != "" {
		p = logPath[0]
	}
	_ = os.Remove(p)
}
