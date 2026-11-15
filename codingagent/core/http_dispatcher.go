// Ported from packages/coding-agent/src/core/http-dispatcher.ts (pi v1.5.2).

package core

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// DefaultHttpIdleTimeoutMs is the default HTTP idle timeout in milliseconds (5 minutes).
const DefaultHttpIdleTimeoutMs int64 = 300_000

// HttpIdleTimeoutChoice pairs a human-readable choice label with its idle timeout in milliseconds.
type HttpIdleTimeoutChoice struct {
	Label     string
	TimeoutMs int64
}

// HttpIdleTimeoutChoices lists preset HTTP idle timeout options shown in settings UIs.
var HttpIdleTimeoutChoices = []HttpIdleTimeoutChoice{
	{Label: "30 sec", TimeoutMs: 30_000},
	{Label: "1 min", TimeoutMs: 60_000},
	{Label: "2 min", TimeoutMs: 120_000},
	{Label: "5 min", TimeoutMs: 300_000},
	{Label: "disabled", TimeoutMs: 0},
}

// ParseHttpIdleTimeoutMs normalizes a numeric or string idle timeout value into non-negative milliseconds.
// Returns nil when value is invalid or negative.
func ParseHttpIdleTimeoutMs(value any) *int64 {
	switch v := value.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if strings.EqualFold(trimmed, "disabled") {
			var zero int64
			return &zero
		}
		if trimmed == "" || strings.ContainsAny(trimmed, "_IiNn") {
			return nil
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return nil
		}
		return ParseHttpIdleTimeoutMs(parsed)
	case int:
		if v < 0 {
			return nil
		}
		res := int64(v)
		return &res
	case int64:
		if v < 0 {
			return nil
		}
		res := v
		return &res
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return nil
		}
		res := int64(math.Floor(v))
		return &res
	case float32:
		return ParseHttpIdleTimeoutMs(float64(v))
	case *int64:
		if v == nil {
			return nil
		}
		return ParseHttpIdleTimeoutMs(*v)
	case *int:
		if v == nil {
			return nil
		}
		return ParseHttpIdleTimeoutMs(*v)
	case *float64:
		if v == nil {
			return nil
		}
		return ParseHttpIdleTimeoutMs(*v)
	default:
		return nil
	}
}

// FormatHttpIdleTimeoutMs formats an idle timeout in milliseconds for display.
func FormatHttpIdleTimeoutMs(timeoutMs int64) string {
	for _, choice := range HttpIdleTimeoutChoices {
		if choice.TimeoutMs == timeoutMs {
			return choice.Label
		}
	}
	return fmt.Sprintf("%s sec", jsonx.FormatNumber(float64(timeoutMs)/1000))
}

// ApplyHttpProxySettings sets HTTP_PROXY and HTTPS_PROXY if not already set in the environment.
func ApplyHttpProxySettings(httpProxy *string) {
	if httpProxy == nil {
		return
	}
	proxy := strings.TrimSpace(*httpProxy)
	if proxy == "" {
		return
	}
	if _, exists := os.LookupEnv("HTTP_PROXY"); !exists {
		_ = os.Setenv("HTTP_PROXY", proxy)
	}
	if _, exists := os.LookupEnv("HTTPS_PROXY"); !exists {
		_ = os.Setenv("HTTPS_PROXY", proxy)
	}
}

// ConfigureHttpDispatcher configures the shared pi-ai HTTP proxy/idle-timeout client.
func ConfigureHttpDispatcher(timeoutMs ...any) error {
	var raw any = DefaultHttpIdleTimeoutMs
	if len(timeoutMs) > 0 {
		raw = timeoutMs[0]
	}
	normalized := ParseHttpIdleTimeoutMs(raw)
	if normalized == nil {
		return fmt.Errorf("Invalid HTTP idle timeout: %v", raw)
	}
	ai.NodeHttpProxyConfigure(*normalized)
	return nil
}
