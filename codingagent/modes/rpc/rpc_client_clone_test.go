// Ported from packages/coding-agent/test/rpc-client-clone.test.ts (pi v1.5.2).

package rpc

import (
	"context"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestRpcClientClone(t *testing.T) {
	client := NewRpcClient()
	var sent RpcCommand
	client.sendOverride = func(_ context.Context, cmd RpcCommand) (RpcResponse, error) {
		sent = cmd
		dataVal, err := jsonx.Parse([]byte(`{"cancelled":false}`))
		if err != nil {
			return RpcResponse{}, err
		}
		return RpcResponse{
			Type:    "response",
			Command: "clone",
			Success: true,
			Data:    jsonx.Some[any](dataVal),
		}, nil
	}

	got, err := client.Clone(context.Background())
	if err != nil {
		t.Fatalf("Clone failed: %v", err)
	}
	if sent == nil || sent.CommandType() != RpcCmdClone {
		t.Fatalf("expected clone command, got %#v", sent)
	}
	if got.Cancelled {
		t.Fatalf("expected cancelled=false, got %#v", got)
	}
}
