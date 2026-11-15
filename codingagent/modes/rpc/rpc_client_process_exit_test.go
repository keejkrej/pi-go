// Ported from packages/coding-agent/test/rpc-client-process-exit.test.ts (pi v1.5.2).

package rpc

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func trcpeWriteChildScript(t *testing.T, contents string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test script requires Unix")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "child.sh")
	if err := os.WriteFile(scriptPath, []byte(contents), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return scriptPath
}

func TestRpcClientRejectsInFlightRequestWhenChildExits(t *testing.T) {
	script := trcpeWriteChildScript(t, "#!/bin/sh\nIFS= read -r _line\necho 'child-err' >&2\nexit 43\n")
	client := NewRpcClient(&RpcClientOptions{
		CliPath:   script,
		StderrTee: io.Discard,
	})
	ctx := context.Background()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = client.Stop() }()

	_, err := client.GetCommands(ctx)
	if err == nil {
		t.Fatal("expected GetCommands to fail when child exits")
	}
	if !strings.Contains(err.Error(), "Agent process exited (code=43 signal=null)") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "child-err") {
		t.Fatalf("expected stderr in error, got: %v", err)
	}
}
