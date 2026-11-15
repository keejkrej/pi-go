// Ported from packages/server/src/transports/unix/address.ts (pi v1.0.0).

package unix

import (
	"path/filepath"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/protocol"
)

// GetUnixSocketPath derives the local Unix socket path for one logical server identity.
func GetUnixSocketPath(serverId protocol.ServerId, serverDirectory string) (string, error) {
	if !protocol.IsServerId(serverId) {
		return "", js.NewTypeError("Unix serverId must be a canonical lowercase UUIDv4")
	}
	return filepath.Join(serverDirectory, string(serverId)+".sock"), nil
}
