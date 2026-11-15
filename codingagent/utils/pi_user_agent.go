// Ported from packages/coding-agent/src/utils/pi-user-agent.ts (pi v1.5.2).

package utils

import "github.com/keejkrej/pi-go/ai"

// GetPiUserAgent formats pi's User-Agent for its own network requests,
// matching the detailed form pi-ai sends to providers.
func GetPiUserAgent(version string) string {
	return ai.FormatUserAgent(&ai.ClientIdentity{
		UserAgent: ai.UserAgentModeValue{Val: ai.UserAgentModeDetailed},
		Version:   &version,
	})
}
