// Ported from packages/coding-agent/src/extensions/mcp/log.ts (pi v1.5.2).

package mcpext

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

const logMaxBytes int64 = 5 * 1024 * 1024

func logIsRecord(value any) (*jsonx.Object, bool) {
	if value == nil {
		return nil, false
	}
	if obj, ok := value.(*jsonx.Object); ok {
		return obj, obj != nil
	}
	encoded, err := jsonx.Encode(value)
	if err != nil {
		return nil, false
	}
	obj, ok := encoded.(*jsonx.Object)
	return obj, ok && obj != nil
}

func logFormatData(data any) string {
	if s, ok := data.(string); ok {
		return s
	}
	if s, err := jsonx.Stringify(data); err == nil && s != "" {
		return s
	}
	return fmt.Sprint(data)
}

func logIndentContinuation(text string) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(normalized, "\n", "\n    ")
}

// FormatMcpLogMessage formats one notifications/message from server as a log line;
// continuation lines are indented.
func FormatMcpLogMessage(server string, params any, now ...time.Time) string {
	ts := time.Now().UTC()
	if len(now) > 0 && !now[0].IsZero() {
		ts = now[0].UTC()
	}

	var dataVal any = params
	level := "info"
	logger := ""
	if msgObj, ok := logIsRecord(params); ok {
		if lv, hasLevel := msgObj.GetString("level"); hasLevel && lv != "" {
			level = lv
		}
		if lg, hasLogger := msgObj.GetString("logger"); hasLogger && lg != "" {
			logger = " " + lg + ":"
		}
		if d, hasData := msgObj.Get("data"); hasData {
			dataVal = d
		} else {
			dataVal = nil
		}
	}

	text := logIndentContinuation(logFormatData(dataVal))
	return fmt.Sprintf("%s [%s] %s%s %s\n", jsonx.FormatISOTime(ts), server, level, logger, text)
}

// McpServerLog appends server log messages to one file. Write errors are ignored:
// logging must not break tools.
type McpServerLog struct {
	Path string
	mu   sync.Mutex
	size *int64
}

// NewMcpServerLog creates an McpServerLog writing to path.
func NewMcpServerLog(path string) *McpServerLog {
	return &McpServerLog{Path: path}
}

// Write formats params and synchronously appends a log line for server.
func (l *McpServerLog) Write(server string, params any) {
	if l == nil || l.Path == "" {
		return
	}
	line := FormatMcpLogMessage(server, params)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.size == nil {
		if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
			return
		}
		sz := l.logCurrentSize()
		l.size = &sz
	}
	if *l.size > logMaxBytes {
		// Another process may have rotated it already; check before renaming.
		if l.logCurrentSize() > logMaxBytes {
			_ = os.Rename(l.Path, l.Path+".1")
		}
		sz := l.logCurrentSize()
		l.size = &sz
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_, writeErr := f.WriteString(line)
	_ = f.Close()
	if writeErr != nil {
		return
	}
	sz := *l.size + int64(len(line))
	l.size = &sz
}

func (l *McpServerLog) logCurrentSize() int64 {
	info, err := os.Stat(l.Path)
	if err != nil {
		return 0
	}
	return info.Size()
}
