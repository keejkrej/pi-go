package typebox

import (
	"sync"

	"github.com/keejkrej/pi-go/internal/jsre"
)

type compiledRE struct {
	re  *jsre.Regexp
	err error
}

var reCache sync.Map

func compileRE(pattern, flags string) (*jsre.Regexp, error) {
	key := pattern + "\x00" + flags
	if v, ok := reCache.Load(key); ok {
		c := v.(compiledRE)
		return c.re, c.err
	}
	re, err := jsre.Compile(pattern, flags)
	reCache.Store(key, compiledRE{re: re, err: err})
	return re, err
}

func reTest(pattern, flags, value string) (bool, error) {
	re, err := compileRE(pattern, flags)
	if err != nil {
		return false, err
	}
	return re.Test(value), nil
}

func mustRE(pattern, flags string) *jsre.Regexp {
	re, err := compileRE(pattern, flags)
	if err != nil {
		panic(err)
	}
	return re
}
