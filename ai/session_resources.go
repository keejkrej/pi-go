// Ported from packages/ai/src/session-resources.ts (pi v1.0.0).

package ai

// SessionResourceCleanup releases resources tied to one session.
// A nil sessionId means every session, matching an omitted argument.
type SessionResourceCleanup func(sessionId *string)

// RegisterSessionResourceCleanup registers cleanup and returns an unsubscribe
// function. Cleanups run in registration order.
func RegisterSessionResourceCleanup(cleanup SessionResourceCleanup) func() {
	panic("unported: RegisterSessionResourceCleanup")
}

// CleanupSessionResources runs every registered cleanup.
// Failures are collected; when any cleanup throws, the result is an error
// whose message is "Failed to cleanup session resources".
func CleanupSessionResources(sessionId *string) error {
	panic("unported: CleanupSessionResources")
}
