package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/keejkrej/pi-go/codingagent"
)

func TestPiRunDelegatesSetupAndMain(t *testing.T) {
	origSetup := piSetupCli
	origMain := piMain
	t.Cleanup(func() {
		piSetupCli = origSetup
		piMain = origMain
	})

	setupCalled := false
	var gotArgs []string
	var gotOpts []*codingagent.MainOptions
	var gotCtx context.Context

	piSetupCli = func() {
		setupCalled = true
	}
	piMain = func(ctx context.Context, args []string, opts ...*codingagent.MainOptions) int {
		gotCtx = ctx
		gotArgs = append([]string(nil), args...)
		gotOpts = opts
		return 7
	}

	code := piRun([]string{"--version"})
	if !setupCalled {
		t.Fatal("expected SetupCli to be called")
	}
	if gotCtx == nil {
		t.Fatal("expected non-nil signal context")
	}
	if !reflect.DeepEqual(gotArgs, []string{"--version"}) {
		t.Fatalf("unexpected args forwarded to Main: %v", gotArgs)
	}
	if len(gotOpts) != 0 {
		t.Fatalf("expected default pi binary not to pass DefaultGuiMode options, got %v", gotOpts)
	}
	if code != 7 {
		t.Fatalf("expected exit code 7, got %d", code)
	}
}
