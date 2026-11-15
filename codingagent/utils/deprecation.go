// Ported from packages/coding-agent/src/utils/deprecation.ts (pi v1.5.2).

package utils

import (
	"fmt"
	"os"
	"sync"

	"github.com/keejkrej/pi-go/internal/ansi"
)

var (
	deprMu      sync.Mutex
	deprEmitted = make(map[string]struct{})
	deprWarnFn  = func(line string) {
		fmt.Fprintln(os.Stderr, line)
	}
)

// WarnDeprecation emits each unique deprecation warning once to stderr.
func WarnDeprecation(message string) {
	deprMu.Lock()
	if _, seen := deprEmitted[message]; seen {
		deprMu.Unlock()
		return
	}
	deprEmitted[message] = struct{}{}
	write := deprWarnFn
	deprMu.Unlock()

	write(ansi.Yellow("Deprecation warning: " + message))
}

// ClearDeprecationWarningsForTests resets emitted deprecation warnings for tests.
func ClearDeprecationWarningsForTests() {
	deprMu.Lock()
	clear(deprEmitted)
	deprMu.Unlock()
}
