// Ported from packages/ai/src/auth/context.ts (pi v1.5.2).

package ai

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
)

type acDefaultAuthContext struct{}

var (
	acGetenv  = os.Getenv
	acStat    = os.Stat
	acHomedir = os.UserHomeDir
)

// DefaultProviderAuthContext returns the default AuthContext backed by process
// environment variables (falling back via GetProviderEnvValue) and local file stat.
func DefaultProviderAuthContext() AuthContext {
	return acDefaultAuthContext{}
}

func (acDefaultAuthContext) Env(ctx context.Context, name string) (*string, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, uaAbortReason(ctx)
	}
	v := acGetenv(name)
	if v == "" {
		v = upeGetLinuxSandboxEnvValue(name)
	}
	if js.Trim(v) == "" {
		return nil, nil
	}
	return &v, nil
}

func (acDefaultAuthContext) FileExists(ctx context.Context, path string) (bool, error) {
	if ctx != nil && ctx.Err() != nil {
		return false, uaAbortReason(ctx)
	}
	resolved := path
	if strings.HasPrefix(resolved, "~") {
		home, err := acHomedir()
		if err != nil {
			return false, nil
		}
		if resolved == "~" {
			resolved = home
		} else if strings.HasPrefix(resolved, "~/") || strings.HasPrefix(resolved, `~\`) {
			resolved = filepath.Join(home, resolved[2:])
		} else {
			resolved = home + resolved[1:]
		}
	}
	_, err := acStat(resolved)
	return err == nil, nil
}
