// Ported from packages/coding-agent/test/experimental-session-support.ts (pi v1.0.0).

package testkit

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/experimental"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// ExperimentalSessionState holds persisted state read from a durable experimental session.
type ExperimentalSessionState struct {
	Model *durable.ModelRef `json:"model,omitzero"`
}

// CreateExperimentalSessions creates catalog session records for each id under sessionsRoot.
func CreateExperimentalSessions(sessionsRoot string, ids []string, cwd ...string) ([]experimental.SessionCatalogMetadata, error) {
	resolvedCwd := ""
	if len(cwd) > 0 {
		resolvedCwd = cwd[0]
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		resolvedCwd = wd
	}

	metadata := make([]experimental.SessionCatalogMetadata, 0, len(ids))
	for _, id := range ids {
		idCopy := id
		created, err := experimental.CreateCatalogSession(sessionsRoot, experimental.SessionCreateCatalogOptions{
			Id:  &idCopy,
			Cwd: resolvedCwd,
		})
		if err != nil {
			return nil, err
		}
		metadata = append(metadata, created)
	}
	return metadata, nil
}

// ConfigureExperimentalWorkerModel writes a test API key credential into auth.json under agentDir.
func ConfigureExperimentalWorkerModel(agentDir string) error {
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return err
	}
	payload, err := jsonx.Marshal(map[string]any{
		"anthropic": map[string]any{
			"type": "api_key",
			"key":  "test-key",
		},
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(agentDir, "auth.json"), payload, 0o600)
}

// ReadExperimentalSessionState reads the root conversation's model from durable storage while no worker owns it.
func ReadExperimentalSessionState(sessionsRoot string, sessionId string) (ExperimentalSessionState, error) {
	metadata, ok := experimental.ReadCatalogSession(sessionsRoot, sessionId)
	if !ok {
		return ExperimentalSessionState{}, fmt.Errorf("Expected Session %s", sessionId)
	}
	store, err := sqlite.OpenNodeSqliteStorage(experimental.SessionStoragePath(metadata))
	if err != nil {
		return ExperimentalSessionState{}, err
	}
	session := durable.NewSession(store)
	defer func() { _ = session.Close(chord.BackgroundContext) }()

	raw, err := session.Snapshot(chord.BackgroundContext, durable.AgentDoc, durable.RootConversationId)
	if err != nil {
		return ExperimentalSessionState{}, err
	}
	if raw == nil {
		return ExperimentalSessionState{}, nil
	}
	var agent durable.AgentState
	if err := jsonx.Decode(raw, &agent); err != nil {
		return ExperimentalSessionState{}, err
	}
	return ExperimentalSessionState{Model: agent.Model}, nil
}
