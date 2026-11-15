// Ported from packages/coding-agent/test/rpc-client-clear-queue.test.ts (pi v1.5.2).

package rpc

import (
	"context"
	"slices"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestRpcClientClearQueue(t *testing.T) {
	client := NewRpcClient()
	var sent RpcCommand
	client.sendOverride = func(_ context.Context, cmd RpcCommand) (RpcResponse, error) {
		sent = cmd
		dataVal, err := jsonx.Parse([]byte(`{"steering":["Change direction"],"followUp":["Summarize when finished"]}`))
		if err != nil {
			return RpcResponse{}, err
		}
		return RpcResponse{
			Type:    "response",
			Command: "clear_queue",
			Success: true,
			Data:    jsonx.Some[any](dataVal),
		}, nil
	}

	got, err := client.ClearQueue(context.Background())
	if err != nil {
		t.Fatalf("ClearQueue failed: %v", err)
	}
	if sent == nil || sent.CommandType() != RpcCmdClearQueue {
		t.Fatalf("expected clear_queue command, got %#v", sent)
	}
	want := core.QueuedMessagesText{
		Steering: []string{"Change direction"},
		FollowUp: []string{"Summarize when finished"},
	}
	if !slices.Equal(got.Steering, want.Steering) || !slices.Equal(got.FollowUp, want.FollowUp) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
