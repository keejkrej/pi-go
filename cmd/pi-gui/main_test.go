package main

import (
	"context"
	"reflect"
	"testing"

	"github.com/keejkrej/pi-go/codingagent"
)

func tmpRestoreHooks(t *testing.T) {
	t.Helper()
	origSetup := mpSetupCli
	origMain := mpMain
	origExit := mpExit
	t.Cleanup(func() {
		mpSetupCli = origSetup
		mpMain = origMain
		mpExit = origExit
	})
}

func TestPiGuiRunPassesDefaultGuiModeAndArgs(t *testing.T) {
	tmpRestoreHooks(t)

	var order []string
	var gotArgs []string
	var gotOpts []*codingagent.MainOptions
	var gotCtx context.Context

	mpSetupCli = func() {
		order = append(order, "setup")
	}
	mpMain = func(ctx context.Context, args []string, opts ...*codingagent.MainOptions) int {
		order = append(order, "main")
		gotCtx = ctx
		gotArgs = append([]string(nil), args...)
		gotOpts = opts
		return 42
	}

	code := mpRun([]string{"--model", "test/model", "hello"})
	if code != 42 {
		t.Fatalf("mpRun() = %d, want 42", code)
	}
	if !reflect.DeepEqual(order, []string{"setup", "main"}) {
		t.Fatalf("call order = %v, want [setup main]", order)
	}
	if gotCtx == nil {
		t.Fatal("expected non-nil context")
	}
	if !reflect.DeepEqual(gotArgs, []string{"--model", "test/model", "hello"}) {
		t.Fatalf("args = %v, want [--model test/model hello]", gotArgs)
	}
	if len(gotOpts) != 1 || gotOpts[0] == nil || !gotOpts[0].DefaultGuiMode {
		t.Fatalf("opts = %#v, want single MainOptions{DefaultGuiMode: true}", gotOpts)
	}
}

func TestPiGuiMainCallsExitWithRunCode(t *testing.T) {
	tmpRestoreHooks(t)

	exitCode := -1
	mpSetupCli = func() {}
	mpMain = func(_ context.Context, _ []string, _ ...*codingagent.MainOptions) int {
		return 7
	}
	mpExit = func(code int) {
		exitCode = code
	}

	main()
	if exitCode != 7 {
		t.Fatalf("exitCode = %d, want 7", exitCode)
	}
}
