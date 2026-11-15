// Ported from packages/ai/src/api/strict-tool-limits.ts (pi v1.5.2).

package api

import (
	"container/list"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
)

// stlStrictToolLimitErrorMessages are error substrings indicating Claude rejected strict tools
// for exceeding its grammar/tool limits (observed: 20 strict tools, 16 optional/union parameters).
var stlStrictToolLimitErrorMessages = []string{
	"The compiled grammar is too large",
	"Too many strict tools (",
	"Schemas contains too many parameters with union types (",
}

const stlMaxSessionsOverStrictToolLimits = 10_000

var (
	stlMu       sync.Mutex
	stlOrder    = list.New()
	stlSessions = make(map[string]*list.Element)
)

func init() {
	ai.RegisterSessionResourceCleanup(func(sessionId *string) {
		stlMu.Lock()
		defer stlMu.Unlock()
		if sessionId != nil && *sessionId != "" {
			if elem, ok := stlSessions[*sessionId]; ok {
				stlOrder.Remove(elem)
				delete(stlSessions, *sessionId)
			}
		} else {
			stlOrder.Init()
			clear(stlSessions)
		}
	})
}

// IsStrictToolLimitErrorMessage returns true when a provider error message says the request's strict
// tools exceed the provider's limits.
func IsStrictToolLimitErrorMessage(message string) bool {
	for _, limitMessage := range stlStrictToolLimitErrorMessages {
		if strings.Contains(message, limitMessage) {
			return true
		}
	}
	return false
}

// MarkStrictToolLimitsExceeded records that a session's strict tools exceeded Claude's limits;
// does nothing without a session. Keeps at most 10,000 sessions.
//
// Entries are normally freed by CleanupSessionResources when a session ends. The cap bounds memory
// for library users that never call it; a dropped session just pays one more rejection.
func MarkStrictToolLimitsExceeded(sessionId *string) {
	if sessionId == nil {
		return
	}
	id := *sessionId
	stlMu.Lock()
	defer stlMu.Unlock()
	if _, exists := stlSessions[id]; exists {
		return
	}
	elem := stlOrder.PushBack(id)
	stlSessions[id] = elem
	if len(stlSessions) > stlMaxSessionsOverStrictToolLimits {
		if front := stlOrder.Front(); front != nil {
			oldest, _ := front.Value.(string)
			stlOrder.Remove(front)
			delete(stlSessions, oldest)
		}
	}
}

// HasExceededStrictToolLimits returns true when an earlier request of this session had its strict
// tools rejected for exceeding Claude's limits.
func HasExceededStrictToolLimits(sessionId *string) bool {
	if sessionId == nil {
		return false
	}
	stlMu.Lock()
	_, ok := stlSessions[*sessionId]
	stlMu.Unlock()
	return ok
}
