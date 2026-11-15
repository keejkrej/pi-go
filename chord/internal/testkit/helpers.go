// Ported from packages/chord/test/helpers.ts (pi v1.0.0).

package testkit

import "github.com/keejkrej/pi-go/chord"

// CreateLoopbackServiceTransport re-exports chord.CreateLoopbackServiceTransport for external test callers.
func CreateLoopbackServiceTransport(provider *chord.RemoteServiceProvider) chord.RemoteServiceTransport {
	return chord.CreateLoopbackServiceTransport(provider)
}
