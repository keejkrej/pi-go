// Ported from packages/coding-agent/src/core/radius.ts (pi v1.5.2).

package core

import (
	"os"

	"github.com/keejkrej/pi-go/ai/oauth"
)

const (
	// RadiusProviderId is the provider identifier for Radius.
	RadiusProviderId = "radius"
	// EnvRadiusGateway overrides the default Radius gateway URL.
	EnvRadiusGateway = "PI_RADIUS_GATEWAY"
)

// RadiusMcpUrl is the MCP endpoint of the gateway the built-in Radius provider signs in to.
var RadiusMcpUrl = oauth.NormalizeRadiusGatewayUrl(oauth.DefaultRadiusGateway) + "/mcp"

// GetRadiusGatewayUrl returns the Radius gateway origin, honoring the PI_RADIUS_GATEWAY override.
func GetRadiusGatewayUrl() string {
	if raw, ok := os.LookupEnv(EnvRadiusGateway); ok {
		return oauth.NormalizeRadiusGatewayUrl(raw)
	}
	return oauth.NormalizeRadiusGatewayUrl(oauth.DefaultRadiusGateway)
}
