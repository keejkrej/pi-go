// Ported from packages/coding-agent/src/experimental/session-catalog.ts (pi v1.0.0).

package experimental

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	scMetadataFile = "meta.json"
	scStorageFile  = "session.sqlite"
)

var scSessionIdPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// SessionCatalogMetadata describes one server-hosted experimental session directory.
type SessionCatalogMetadata struct {
	Id        string `json:"id"`
	CreatedAt int64  `json:"createdAt"`
	Cwd       string `json:"cwd"`
	Path      string `json:"path"`
}

// SessionId implements server.SessionMetadata.
func (m SessionCatalogMetadata) SessionId() string {
	return m.Id
}

// SessionCreateCatalogOptions configures CreateCatalogSession.
type SessionCreateCatalogOptions struct {
	Id  *string
	Cwd string
}

// IsSessionId reports whether id matches the valid catalog session identifier syntax.
func IsSessionId(id string) bool {
	return scSessionIdPattern.MatchString(id)
}

// SessionStoragePath returns the SQLite database file path owned by a session worker.
func SessionStoragePath(metadata SessionCatalogMetadata) string {
	return filepath.Join(metadata.Path, scStorageFile)
}

// ListCatalogSessions enumerates valid sessions stored inside sessionDir.
func ListCatalogSessions(sessionDir string) ([]SessionCatalogMetadata, error) {
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sessions []SessionCatalogMetadata
	for _, entry := range entries {
		name := entry.Name()
		if !IsSessionId(name) {
			continue
		}
		meta, ok := ReadCatalogSession(sessionDir, name)
		if ok {
			sessions = append(sessions, meta)
		}
	}
	return sessions, nil
}

// ReadCatalogSession loads one session metadata record by ID from sessionDir.
func ReadCatalogSession(sessionDir string, id string) (SessionCatalogMetadata, bool) {
	if !IsSessionId(id) {
		return SessionCatalogMetadata{}, false
	}
	sessionPath := filepath.Join(sessionDir, id)
	raw, err := os.ReadFile(filepath.Join(sessionPath, scMetadataFile))
	if err != nil {
		return SessionCatalogMetadata{}, false
	}
	var payload struct {
		CreatedAt *float64 `json:"createdAt"`
		Cwd       *string  `json:"cwd"`
	}
	if err := jsonx.Unmarshal(raw, &payload); err != nil {
		return SessionCatalogMetadata{}, false
	}
	if payload.CreatedAt == nil || payload.Cwd == nil {
		return SessionCatalogMetadata{}, false
	}
	return SessionCatalogMetadata{
		Id:        id,
		CreatedAt: int64(*payload.CreatedAt),
		Cwd:       *payload.Cwd,
		Path:      sessionPath,
	}, true
}

// CreateCatalogSession creates a new session directory and writes meta.json atomically.
func CreateCatalogSession(sessionDir string, options SessionCreateCatalogOptions) (SessionCatalogMetadata, error) {
	id := uuid.NewString()
	if options.Id != nil {
		id = *options.Id
	}
	if !IsSessionId(id) {
		return SessionCatalogMetadata{}, fmt.Errorf("Invalid session ID: %s", id)
	}
	sessionPath := filepath.Join(sessionDir, id)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return SessionCatalogMetadata{}, err
	}
	if err := os.Mkdir(sessionPath, 0o755); err != nil {
		if os.IsExist(err) {
			return SessionCatalogMetadata{}, fmt.Errorf("Session %s already exists", id)
		}
		return SessionCatalogMetadata{}, err
	}

	nowMs := scNowUnixMilli()
	metadata := SessionCatalogMetadata{
		Id:        id,
		CreatedAt: nowMs,
		Cwd:       options.Cwd,
		Path:      sessionPath,
	}
	encoded, err := jsonx.MarshalIndent(map[string]any{
		"createdAt": metadata.CreatedAt,
		"cwd":       metadata.Cwd,
	}, "\t")
	if err != nil {
		_ = os.RemoveAll(sessionPath)
		return SessionCatalogMetadata{}, err
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(sessionPath, scMetadataFile), encoded, 0o644); err != nil {
		_ = os.RemoveAll(sessionPath)
		return SessionCatalogMetadata{}, err
	}
	return metadata, nil
}

// DeleteCatalogSession removes a session directory recursively.
func DeleteCatalogSession(metadata SessionCatalogMetadata) error {
	return os.RemoveAll(metadata.Path)
}

func scNowUnixMilli() int64 {
	return scUnixMilliFunc()
}

var scUnixMilliFunc = func() int64 {
	// Indirection exists for deterministic testing if needed; default uses OS clock.
	return scTimeNowMilli()
}

func scTimeNowMilli() int64 {
	return time.Now().UnixMilli()
}
