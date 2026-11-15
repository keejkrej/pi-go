// Ported from packages/coding-agent/src/experimental/vacation/sessions.ts (pi v1.0.0).

package vacation

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/experimental/durableagent"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

// VacationSessionSummary aliases durableagent.DurableSessionSummary for vacation sessions.
type VacationSessionSummary = durableagent.DurableSessionSummary

// GetVacationSessionsDir returns the SQLite storage directory for vacation sessions.
func GetVacationSessionsDir(agentDir ...string) string {
	base := utils.GetAgentDir()
	if len(agentDir) > 0 && strings.TrimSpace(agentDir[0]) != "" {
		base = agentDir[0]
	}
	return filepath.Join(base, "vacation-sessions")
}

// NewVacationSessionPath allocates a new SQLite session path in the vacation sessions directory.
func NewVacationSessionPath(dir string) string {
	if strings.TrimSpace(dir) == "" {
		dir = GetVacationSessionsDir()
	}
	return durableagent.NewDurableSessionPath(dir)
}

// ListVacationSessions returns vacation session DB files ordered newest-first.
func ListVacationSessions(ctx context.Context, dir string) ([]VacationSessionSummary, error) {
	if strings.TrimSpace(dir) == "" {
		dir = GetVacationSessionsDir()
	}
	return durableagent.ListDurableSessions(ctx, dir)
}
