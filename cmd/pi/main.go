// CLI binary entrypoint for pi.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/keejkrej/pi-go/codingagent"
	"github.com/keejkrej/pi-go/codingagent/cli"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

var (
	piSetupCli = cli.SetupCli
	piMain     = codingagent.Main
	piExit     = utils.ConfigExit
)

func piRun(args []string) int {
	piSetupCli()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return piMain(ctx, args)
}

func main() {
	piExit(piRun(os.Args[1:]))
}
