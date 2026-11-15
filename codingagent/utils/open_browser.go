// Ported from packages/coding-agent/src/utils/open-browser.ts (pi v1.5.2).

package utils

import (
	"os/exec"

	"github.com/keejkrej/pi-go/internal/js"
)

var (
	obPlatform = js.Platform
	obStart    = func(command string, args []string) {
		cmd := exec.Command(command, args...)
		cmd.Dir = "/"
		obConfigureCmd(cmd)
		_ = cmd.Start()
	}
)

// OpenBrowser opens a URL or file in the platform browser or default handler, best-effort without invoking a shell.
func OpenBrowser(target string) {
	var command string
	var args []string

	switch obPlatform() {
	case "darwin":
		command = "open"
		args = []string{target}
	case "win32":
		command = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", target}
	default:
		command = "xdg-open"
		args = []string{target}
	}

	obStart(command, args)
}
