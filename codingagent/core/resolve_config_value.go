// Ported from packages/coding-agent/src/core/resolve-config-value.ts (pi v1.5.2).

package core

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/omap"
)

const rcvCommandTimeout = 10 * time.Second

var (
	rcvMu                 sync.Mutex
	rcvCommandResultCache = make(map[string]*string)

	rcvEnvVarNameRE       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	rcvEnvVarNamePrefixRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*`)
)

type rcvTemplatePart struct {
	isEnv bool
	value string
}

type rcvConfigValueRef struct {
	isCommand bool
	config    string
	parts     []rcvTemplatePart
}

func rcvAppendLiteral(parts []rcvTemplatePart, value string) []rcvTemplatePart {
	if value == "" {
		return parts
	}
	if n := len(parts); n > 0 && !parts[n-1].isEnv {
		parts[n-1].value += value
		return parts
	}
	return append(parts, rcvTemplatePart{isEnv: false, value: value})
}

func rcvParseTemplate(config string) []rcvTemplatePart {
	var parts []rcvTemplatePart
	index := 0

	for index < len(config) {
		relDollar := strings.IndexByte(config[index:], '$')
		if relDollar < 0 {
			parts = rcvAppendLiteral(parts, config[index:])
			break
		}
		dollarIndex := index + relDollar
		parts = rcvAppendLiteral(parts, config[index:dollarIndex])

		if dollarIndex+1 >= len(config) {
			parts = rcvAppendLiteral(parts, "$")
			index = dollarIndex + 1
			continue
		}

		nextByte := config[dollarIndex+1]
		if nextByte == '$' || nextByte == '!' {
			parts = rcvAppendLiteral(parts, string(nextByte))
			index = dollarIndex + 2
			continue
		}

		if nextByte == '{' {
			relEnd := strings.IndexByte(config[dollarIndex+2:], '}')
			if relEnd < 0 {
				parts = rcvAppendLiteral(parts, "$")
				index = dollarIndex + 1
				continue
			}
			endIndex := dollarIndex + 2 + relEnd
			name := config[dollarIndex+2 : endIndex]
			if rcvEnvVarNameRE.MatchString(name) {
				parts = append(parts, rcvTemplatePart{isEnv: true, value: name})
			} else {
				parts = rcvAppendLiteral(parts, config[dollarIndex:endIndex+1])
			}
			index = endIndex + 1
			continue
		}

		if match := rcvEnvVarNamePrefixRE.FindString(config[dollarIndex+1:]); match != "" {
			parts = append(parts, rcvTemplatePart{isEnv: true, value: match})
			index = dollarIndex + 1 + len(match)
			continue
		}

		parts = rcvAppendLiteral(parts, "$")
		index = dollarIndex + 1
	}

	return parts
}

func rcvParseReference(config string) rcvConfigValueRef {
	if strings.HasPrefix(config, "!") {
		return rcvConfigValueRef{isCommand: true, config: config}
	}
	return rcvConfigValueRef{isCommand: false, parts: rcvParseTemplate(config)}
}

func rcvEnvMap(envs []map[string]string) map[string]string {
	if len(envs) > 0 {
		return envs[0]
	}
	return nil
}

func rcvResolveEnv(name string, env map[string]string) *string {
	if env != nil {
		if v := env[name]; v != "" {
			return &v
		}
	}
	if v := os.Getenv(name); v != "" {
		return &v
	}
	return nil
}

func rcvTemplateEnvVarNames(parts []rcvTemplatePart) []string {
	names := []string{}
	seen := make(map[string]struct{})
	for _, part := range parts {
		if !part.isEnv {
			continue
		}
		if _, ok := seen[part.value]; ok {
			continue
		}
		seen[part.value] = struct{}{}
		names = append(names, part.value)
	}
	return names
}

func rcvResolveTemplate(parts []rcvTemplatePart, env map[string]string) *string {
	var b strings.Builder
	for _, part := range parts {
		if !part.isEnv {
			b.WriteString(part.value)
			continue
		}
		envVal := rcvResolveEnv(part.value, env)
		if envVal == nil {
			return nil
		}
		b.WriteString(*envVal)
	}
	res := b.String()
	return &res
}

// GetConfigValueEnvVarName returns the referenced env var name when config consists solely of "$VAR" or "${VAR}".
func GetConfigValueEnvVarName(config string) *string {
	ref := rcvParseReference(config)
	if ref.isCommand {
		return nil
	}
	if len(ref.parts) == 1 && ref.parts[0].isEnv {
		name := ref.parts[0].value
		return &name
	}
	return nil
}

// GetConfigValueEnvVarNames returns all unique env var names referenced in config (or empty for "!" commands).
func GetConfigValueEnvVarNames(config string) []string {
	ref := rcvParseReference(config)
	if ref.isCommand {
		return []string{}
	}
	return rcvTemplateEnvVarNames(ref.parts)
}

// GetMissingConfigValueEnvVarNames returns referenced env var names that are unset or empty.
func GetMissingConfigValueEnvVarNames(config string, env ...map[string]string) []string {
	envMap := rcvEnvMap(env)
	missing := []string{}
	for _, name := range GetConfigValueEnvVarNames(config) {
		if rcvResolveEnv(name, envMap) == nil {
			missing = append(missing, name)
		}
	}
	return missing
}

// IsCommandConfigValue reports whether config is a shell command value starting with "!".
func IsCommandConfigValue(config string) bool {
	return rcvParseReference(config).isCommand
}

// IsConfigValueConfigured reports whether all referenced environment variables in config resolve cleanly.
func IsConfigValueConfigured(config string, env ...map[string]string) bool {
	return len(GetMissingConfigValueEnvVarNames(config, env...)) == 0
}

func rcvExecuteWithConfiguredShell(command string) (executed bool, value *string) {
	cfg, err := utils.GetShellConfig()
	if err != nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), rcvCommandTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if cfg.CommandTransport == "stdin" {
		cmd = utils.SpawnProcessIn(ctx, "", nil, cfg.Shell, cfg.Args)
		cmd.Stdin = strings.NewReader(command)
	} else {
		args := append(append([]string(nil), cfg.Args...), command)
		cmd = utils.SpawnProcessIn(ctx, "", nil, cfg.Shell, args)
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "executable file not found") {
			return false, nil
		}
		return true, nil
	}
	trimmed := strings.TrimSpace(stdout.String())
	if trimmed == "" {
		return true, nil
	}
	return true, &trimmed
}

func rcvExecuteWithDefaultShell(command string) *string {
	ctx, cancel := context.WithTimeout(context.Background(), rcvCommandTimeout)
	defer cancel()

	var shell string
	var args []string
	if runtime.GOOS == "windows" {
		shell = os.Getenv("ComSpec")
		if shell == "" {
			shell = "cmd.exe"
		}
		args = []string{"/d", "/s", "/c", command}
	} else {
		shell = "/bin/sh"
		args = []string{"-c", command}
	}
	cmd := utils.SpawnProcessIn(ctx, "", nil, shell, args)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil
	}
	trimmed := strings.TrimSpace(stdout.String())
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func rcvExecuteCommandUncached(commandConfig string) *string {
	command := commandConfig[1:]
	if runtime.GOOS == "windows" {
		if executed, value := rcvExecuteWithConfiguredShell(command); executed {
			return value
		}
	}
	return rcvExecuteWithDefaultShell(command)
}

func rcvExecuteCommand(commandConfig string) *string {
	rcvMu.Lock()
	cached, ok := rcvCommandResultCache[commandConfig]
	rcvMu.Unlock()
	if ok {
		if cached == nil {
			return nil
		}
		cp := *cached
		return &cp
	}

	result := rcvExecuteCommandUncached(commandConfig)
	rcvMu.Lock()
	var toStore *string
	if result != nil {
		cp := *result
		toStore = &cp
	}
	rcvCommandResultCache[commandConfig] = toStore
	rcvMu.Unlock()
	return result
}

// ResolveConfigValue resolves a config value (shell command with caching, env template, or literal).
func ResolveConfigValue(config string, env ...map[string]string) *string {
	ref := rcvParseReference(config)
	if ref.isCommand {
		return rcvExecuteCommand(ref.config)
	}
	return rcvResolveTemplate(ref.parts, rcvEnvMap(env))
}

// ResolveConfigValueUncached resolves a config value without using or populating the command cache.
func ResolveConfigValueUncached(config string, env ...map[string]string) *string {
	ref := rcvParseReference(config)
	if ref.isCommand {
		return rcvExecuteCommandUncached(ref.config)
	}
	return rcvResolveTemplate(ref.parts, rcvEnvMap(env))
}

// ResolveConfigValueOrThrow resolves a config value without command caching and returns a descriptive error on failure.
func ResolveConfigValueOrThrow(config string, description string, env ...map[string]string) (string, error) {
	resolved := ResolveConfigValueUncached(config, env...)
	if resolved != nil {
		return *resolved, nil
	}

	ref := rcvParseReference(config)
	if ref.isCommand {
		return "", fmt.Errorf("Failed to resolve %s from shell command: %s", description, ref.config[1:])
	}

	missingEnvVars := GetMissingConfigValueEnvVarNames(config, env...)
	if len(missingEnvVars) == 1 {
		return "", fmt.Errorf("Failed to resolve %s from environment variable: %s", description, missingEnvVars[0])
	}
	if len(missingEnvVars) > 1 {
		return "", fmt.Errorf("Failed to resolve %s from environment variables: %s", description, strings.Join(missingEnvVars, ", "))
	}
	return "", fmt.Errorf("Failed to resolve %s", description)
}

// ResolveHeaders resolves every header value via ResolveConfigValue, omitting headers that resolve to nil or empty.
func ResolveHeaders(headers *omap.Map[string, string], env ...map[string]string) *omap.Map[string, string] {
	if headers == nil {
		return nil
	}
	resolved := omap.NewMap[string, string]()
	for key, val := range headers.All() {
		r := ResolveConfigValue(val, env...)
		if r != nil && *r != "" {
			resolved.Set(key, *r)
		}
	}
	if resolved.Len() == 0 {
		return nil
	}
	return resolved
}

// ResolveHeadersOrThrow resolves every header value via ResolveConfigValueOrThrow, returning an error if any fails.
func ResolveHeadersOrThrow(headers *omap.Map[string, string], description string, env ...map[string]string) (*omap.Map[string, string], error) {
	if headers == nil {
		return nil, nil
	}
	resolved := omap.NewMap[string, string]()
	for key, val := range headers.All() {
		r, err := ResolveConfigValueOrThrow(val, fmt.Sprintf("%s header %q", description, key), env...)
		if err != nil {
			return nil, err
		}
		resolved.Set(key, r)
	}
	if resolved.Len() == 0 {
		return nil, nil
	}
	return resolved, nil
}

// ClearConfigValueCache clears the cached shell command results.
func ClearConfigValueCache() {
	rcvMu.Lock()
	clear(rcvCommandResultCache)
	rcvMu.Unlock()
}
