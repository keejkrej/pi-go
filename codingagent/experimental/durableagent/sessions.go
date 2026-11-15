// Ported from packages/coding-agent/src/experimental/durable/sessions.ts (pi v1.0.0).

package durableagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/lockfile"
)

// DurableSessionSummary describes a persisted SQLite durable session file.
type DurableSessionSummary struct {
	Id         string    `json:"id"`
	Path       string    `json:"path"`
	UpdatedAt  time.Time `json:"updatedAt"`
	SizeBytes  int64     `json:"sizeBytes"`
	EntryCount int       `json:"entryCount"`
	FirstText  string    `json:"firstText,omitzero"`
	Locked     bool      `json:"locked"`
}

// GetDurableSessionsDir returns the storage directory for standalone durableagent sessions.
func GetDurableSessionsDir(agentDir ...string) string {
	base := utils.GetAgentDir()
	if len(agentDir) > 0 && strings.TrimSpace(agentDir[0]) != "" {
		base = agentDir[0]
	}
	return filepath.Join(base, "durable-sessions")
}

// NewDurableSessionPath allocates a new SQLite session path under dir.
func NewDurableSessionPath(dir string) string {
	if strings.TrimSpace(dir) == "" {
		dir = GetDurableSessionsDir()
	}
	stamp := time.Now().UTC().Format("2006-01-02T15-04-05")
	id := uuid.NewString()[:8]
	return filepath.Join(dir, fmt.Sprintf("%s_%s.sqlite", stamp, id))
}

// OpenLockedSessionStorage acquires an exclusive lock on dbPath and opens SQLite storage.
func OpenLockedSessionStorage(ctx context.Context, dbPath string) (*sqlite.SqliteStorage, func() error, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, nil, fmt.Errorf("session database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, nil, err
	}
	// Ensure target file exists before proper-lockfile attempts to lock or realpath it.
	f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, nil, err
	}
	_ = f.Close()

	realpath := false
	releaseLock, err := lockfile.Lock(ctx, dbPath, &lockfile.Options{
		Realpath: &realpath,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("session file is locked (%s): %w", dbPath, err)
	}

	storage, err := sqlite.OpenNodeSqliteStorage(dbPath)
	if err != nil {
		_ = releaseLock()
		return nil, nil, err
	}
	return storage, func() error {
		return releaseLock()
	}, nil
}

// ListDurableSessions scans dir for durableagent SQLite session files sorted newest-first.
func ListDurableSessions(ctx context.Context, dir string) ([]DurableSessionSummary, error) {
	if strings.TrimSpace(dir) == "" {
		dir = GetDurableSessionsDir()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	summaries := make([]DurableSessionSummary, 0, len(entries))
	realpath := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sqlite") && !strings.HasSuffix(name, ".db") {
			continue
		}
		fullPath := filepath.Join(dir, name)
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		locked, _ := lockfile.CheckSync(fullPath, &lockfile.Options{Realpath: &realpath})
		id := strings.TrimSuffix(strings.TrimSuffix(name, ".sqlite"), ".db")
		summary := DurableSessionSummary{
			Id:        id,
			Path:      fullPath,
			UpdatedAt: info.ModTime(),
			SizeBytes: info.Size(),
			Locked:    locked,
		}
		if !locked {
			entryCount, firstText := sessPeekSummary(ctx, fullPath)
			summary.EntryCount = entryCount
			summary.FirstText = firstText
		}
		summaries = append(summaries, summary)
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})
	return summaries, nil
}

func sessPeekSummary(ctx context.Context, dbPath string) (int, string) {
	storage, err := sqlite.OpenNodeSqliteStorage(dbPath)
	if err != nil {
		return 0, ""
	}
	sess := durable.NewSession(storage)
	chordCtx := hsChordContext(ctx)
	defer func() {
		_ = sess.Close(chordCtx)
	}()

	var entryCount int
	var firstText string
	_ = sess.Commit(chordCtx, func(tx durable.Tx) error {
		convs, convErr := tx.ScanConversations(durable.ConversationQuery{}, 20, nil)
		if convErr != nil {
			return convErr
		}
		if len(convs.Items) == 0 {
			return nil
		}
		targetId := convs.Items[0].Id
		for _, c := range convs.Items {
			if c.Id == durable.RootConversationId {
				targetId = c.Id
				break
			}
		}
		page, scanErr := tx.ScanEntries(durable.EntryQuery{
			ConversationId: targetId,
		}, 50, nil)
		if scanErr != nil {
			return scanErr
		}
		entryCount = len(page.Items)
		// ScanEntries returns newest-first; scan oldest-to-newest for first user prompt.
		for i := len(page.Items) - 1; i >= 0; i-- {
			rec := page.Items[i]
			if rec.Kind == durable.UserEntry.Kind && len(rec.Model) > 0 {
				if txt := promptExtractMessageText(rec.Model[0]); txt != "" {
					firstText = txt
					break
				}
			}
		}
		return nil
	})
	return entryCount, firstText
}
