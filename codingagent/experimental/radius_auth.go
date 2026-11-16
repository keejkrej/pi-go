// Ported from packages/coding-agent/src/experimental/radius-auth.ts (pi v1.0.0).

package experimental

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/oauth"
	"github.com/keejkrej/pi-go/codingagent/cli"
	"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

// EnvRadiusGateway re-exports core.EnvRadiusGateway.
const EnvRadiusGateway = core.EnvRadiusGateway

// RadiusRelayAuth holds a normalized Radius gateway URL and bearer token.
type RadiusRelayAuth struct {
	Gateway string
	Token   string
}

// RadiusRelayAuthResolver resolves explicit or stored Radius credentials anew for each relay connection attempt.
type RadiusRelayAuthResolver struct {
	mu           sync.Mutex
	input        *experimentalcli.AuthInput
	gateway      string
	modelRuntime *core.ModelRuntime
	runtimeErr   error
	runtimeInit  bool
}

// NewRadiusRelayAuthResolver constructs a RadiusRelayAuthResolver for input and gateway (defaults to core.GetRadiusGatewayUrl()).
func NewRadiusRelayAuthResolver(input *experimentalcli.AuthInput, gateway ...string) *RadiusRelayAuthResolver {
	gw := core.GetRadiusGatewayUrl()
	if len(gateway) > 0 && strings.TrimSpace(gateway[0]) != "" {
		gw = gateway[0]
	}
	return &RadiusRelayAuthResolver{
		input:   input,
		gateway: oauth.NormalizeRadiusGatewayUrl(gw),
	}
}

// Gateway returns the normalized Radius gateway origin.
func (r *RadiusRelayAuthResolver) Gateway() string {
	return r.gateway
}

// Resolve resolves Radius credentials unless offline, honoring explicit auth flags or stored `/login radius` OAuth tokens.
func (r *RadiusRelayAuthResolver) Resolve(ctx context.Context, required bool) (*RadiusRelayAuth, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if _, offline := os.LookupEnv("PI_OFFLINE"); offline {
		if required {
			return nil, fmt.Errorf("Radius relay connections are unavailable in offline mode")
		}
		return nil, nil
	}

	explicit, hasExplicit, err := r.raExplicitToken(ctx)
	if err != nil {
		return nil, err
	}
	if hasExplicit {
		return &RadiusRelayAuth{Gateway: r.gateway, Token: explicit}, nil
	}

	runtime, err := r.raEnsureModelRuntime(ctx)
	if err != nil {
		return nil, err
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	minValidityMs := int64(5 * 60_000)
	authRes, authErr := runtime.GetAuth(ctx, core.RadiusProviderId, &ai.AuthResolutionOverrides{
		MinOAuthValidityMs: &minValidityMs,
	})
	if authErr == nil && authRes != nil {
		if tok := cli.GetAuthCredential(authRes); tok != nil && len(*tok) > 0 {
			return &RadiusRelayAuth{Gateway: r.gateway, Token: *tok}, nil
		}
	}
	if required {
		return nil, fmt.Errorf("Radius authentication is required; start Pi and run /login radius, then retry")
	}
	return nil, nil
}

// Close closes any lazily constructed ModelRuntime owned by this resolver.
func (r *RadiusRelayAuthResolver) Close() {
	r.mu.Lock()
	rt := r.modelRuntime
	r.modelRuntime = nil
	r.mu.Unlock()
	if rt != nil {
		rt.Close()
	}
}

func (r *RadiusRelayAuthResolver) raEnsureModelRuntime(ctx context.Context) (*core.ModelRuntime, error) {
	r.mu.Lock()
	if r.runtimeInit {
		rt, err := r.modelRuntime, r.runtimeErr
		r.mu.Unlock()
		return rt, err
	}
	r.mu.Unlock()

	refreshFalse := false
	rt, err := core.CreateModelRuntime(ctx, &core.CreateModelRuntimeOptions{
		RefreshOnCreate:   &refreshFalse,
		AllowModelNetwork: false,
	})

	r.mu.Lock()
	if !r.runtimeInit {
		r.modelRuntime = rt
		r.runtimeErr = err
		r.runtimeInit = true
	} else if rt != nil {
		// Another goroutine won the initialization race; close duplicate cleanly outside lock.
		defer rt.Close()
	}
	resRt, resErr := r.modelRuntime, r.runtimeErr
	r.mu.Unlock()
	return resRt, resErr
}

func (r *RadiusRelayAuthResolver) raExplicitToken(ctx context.Context) (string, bool, error) {
	if r.input == nil {
		return "", false, nil
	}
	var raw string
	switch r.input.Type {
	case experimentalcli.AuthInputTypeToken:
		raw = r.input.Token
	case experimentalcli.AuthInputTypeFile:
		resolvedPath, err := utils.ResolvePath(r.input.Path, "", nil)
		if err != nil {
			return "", false, err
		}
		if ctx != nil && ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		data, err := os.ReadFile(resolvedPath)
		if err != nil {
			return "", false, err
		}
		raw = string(data)
	default:
		return "", false, nil
	}
	token := strings.TrimSpace(raw)
	if token == "" {
		return "", false, fmt.Errorf("Radius authentication token must not be empty")
	}
	return token, true, nil
}
