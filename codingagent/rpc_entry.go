// Ported from packages/coding-agent/src/rpc-entry.ts (pi v1.5.2).

package codingagent

import (
	"context"

	"github.com/keejkrej/pi-go/codingagent/cli"
)

// RpcEntry runs pi directly in headless RPC mode by prepending "--mode rpc".
func RpcEntry(ctx context.Context, args []string, opts ...*MainOptions) int {
	cli.SetupCli()
	rpcArgs := make([]string, 0, len(args)+2)
	rpcArgs = append(rpcArgs, "--mode", "rpc")
	rpcArgs = append(rpcArgs, args...)
	return Main(ctx, rpcArgs, opts...)
}
