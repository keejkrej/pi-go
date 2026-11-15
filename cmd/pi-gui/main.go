// Desktop GUI binary entrypoint for pi (defaulting to gui mode unless overridden by CLI flags).
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
	mpSetupCli = cli.SetupCli
	mpMain     = codingagent.Main
	mpExit     = utils.ConfigExit
)

func mpRun(args []string) int {
	mpSetupCli()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return mpMain(ctx, args, &codingagent.MainOptions{
		DefaultGuiMode: true,
	})
}

func main() {
	mpExit(mpRun(os.Args[1:]))
}
