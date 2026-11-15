// Ported from packages/ai/src/utils/provider-env.ts (pi v1.5.2).

package ai

import (
	"os"
	"runtime"
	"strings"
	"sync"
)

var (
	upeProcEnvOnce sync.Once
	upeProcEnvMap  map[string]string
	upeGetenv      = os.Getenv
	upeEnviron     = os.Environ
	upeReadProcEnv = func() ([]byte, error) { return os.ReadFile("/proc/self/environ") }
)

func upeGetLinuxSandboxEnvValue(name string) string {
	if runtime.GOOS != "linux" || len(upeEnviron()) > 0 {
		return ""
	}
	upeProcEnvOnce.Do(func() {
		upeProcEnvMap = make(map[string]string)
		data, err := upeReadProcEnv()
		if err != nil {
			// /proc/self/environ may not exist or may not be readable.
			return
		}
		for _, entry := range strings.Split(string(data), "\x00") {
			idx := strings.IndexByte(entry, '=')
			if idx > 0 {
				upeProcEnvMap[entry[:idx]] = entry[idx+1:]
			}
		}
	})
	return upeProcEnvMap[name]
}

// GetProviderEnvValue resolves a provider environment variable from scoped overrides,
// then process env, then /proc/self/environ fallback on Linux when env is empty.
func GetProviderEnvValue(name string, env ProviderEnv) string {
	if env != nil {
		if v := env[name]; v != "" {
			return v
		}
	}
	if v := upeGetenv(name); v != "" {
		return v
	}
	if v := upeGetLinuxSandboxEnvValue(name); v != "" {
		return v
	}
	return ""
}
