// Ported from packages/coding-agent/src/migrations.ts (pi v1.5.2).

package codingagent

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"golang.org/x/term"
)

const (
	migGuideURL         = "https://github.com/earendil-works/pi/blob/main/packages/coding-agent/CHANGELOG.md#extensions-migration"
	migExtensionsDocURL = "https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md"
)

// MigrationResult summarizes startup auth migrations and extension deprecation warnings.
type MigrationResult struct {
	MigratedAuthProviders []string
	DeprecationWarnings   []string
}

func migExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// MigrateAuthToAuthJson migrates legacy oauth.json and settings.json apiKeys entries
// into auth.json when auth.json does not already exist.
func MigrateAuthToAuthJson() []string {
	agentDir := utils.GetAgentDir()
	authPath := utils.GetAuthPath()
	oauthPath := filepath.Join(agentDir, "oauth.json")
	settingsPath := utils.GetSettingsPath()

	if migExists(authPath) {
		return nil
	}

	migrated := jsonx.NewObject()
	var providers []string

	if rawOAuth, err := os.ReadFile(oauthPath); err == nil {
		var oauthObj *jsonx.Object
		if unmarshalErr := jsonx.Unmarshal([]byte(utils.StripBom(string(rawOAuth))), &oauthObj); unmarshalErr == nil && oauthObj != nil {
			for _, provider := range oauthObj.Keys() {
				val, _ := oauthObj.Get(provider)
				credObj := jsonx.NewObject()
				credObj.Set("type", "oauth")
				if objVal, ok := val.(*jsonx.Object); ok && objVal != nil {
					for _, k := range objVal.Keys() {
						v, _ := objVal.Get(k)
						credObj.Set(k, v)
					}
				}
				migrated.Set(provider, credObj)
				providers = append(providers, provider)
			}
			_ = os.Rename(oauthPath, oauthPath+".migrated")
		}
	}

	if rawSettings, err := os.ReadFile(settingsPath); err == nil {
		var settingsObj *jsonx.Object
		if unmarshalErr := jsonx.Unmarshal([]byte(utils.StripBom(string(rawSettings))), &settingsObj); unmarshalErr == nil && settingsObj != nil {
			if rawKeys, ok := settingsObj.Get("apiKeys"); ok {
				if keysObj, ok := rawKeys.(*jsonx.Object); ok && keysObj != nil {
					for _, provider := range keysObj.Keys() {
						v, _ := keysObj.Get(provider)
						if strKey, ok := v.(string); ok && !migrated.Has(provider) {
							entry := jsonx.NewObject()
							entry.Set("type", "api_key")
							entry.Set("key", strKey)
							migrated.Set(provider, entry)
							providers = append(providers, provider)
						}
					}
					settingsObj.Delete("apiKeys")
					if encoded, err := jsonx.MarshalIndent(settingsObj, "  "); err == nil {
						_ = os.WriteFile(settingsPath, encoded, 0o644)
					}
				}
			}
		}
	}

	if migrated.Len() > 0 {
		_ = os.MkdirAll(filepath.Dir(authPath), 0o755)
		if encoded, err := jsonx.MarshalIndent(migrated, "  "); err == nil {
			_ = os.WriteFile(authPath, encoded, 0o600)
		}
	}

	return providers
}

// MigrateSessionsFromAgentRoot moves legacy ~/.pi/agent/*.jsonl session files
// into ~/.pi/agent/sessions/<encoded-cwd>/ based on their session header cwd.
func MigrateSessionsFromAgentRoot() {
	agentDir := utils.GetAgentDir()
	entries, err := os.ReadDir(agentDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		filePath := filepath.Join(agentDir, entry.Name())
		f, err := os.Open(filePath)
		if err != nil {
			continue
		}
		reader := bufio.NewReader(f)
		firstLine, _ := reader.ReadString('\n')
		_ = f.Close()

		firstLine = strings.TrimSpace(firstLine)
		if firstLine == "" {
			continue
		}

		var header struct {
			Type string `json:"type"`
			Cwd  string `json:"cwd"`
		}
		if err := jsonx.Unmarshal([]byte(firstLine), &header); err != nil {
			continue
		}
		if header.Type != "session" || header.Cwd == "" {
			continue
		}

		correctDir := core.GetDefaultSessionDir(header.Cwd, agentDir)
		newPath := filepath.Join(correctDir, entry.Name())
		if migExists(newPath) {
			continue
		}
		_ = os.Rename(filePath, newPath)
	}
}

func migCommandsToPrompts(baseDir, label string, out io.Writer) bool {
	commandsDir := filepath.Join(baseDir, "commands")
	promptsDir := filepath.Join(baseDir, "prompts")
	if migExists(commandsDir) && !migExists(promptsDir) {
		if err := os.Rename(commandsDir, promptsDir); err != nil {
			_, _ = fmt.Fprintf(out, "\x1b[33mWarning: Could not migrate %s commands/ to prompts/: %s\x1b[39m\n", label, err.Error())
			return false
		}
		_, _ = fmt.Fprintf(out, "\x1b[32mMigrated %s commands/ → prompts/\x1b[39m\n", label)
		return true
	}
	return false
}

func migKeybindingsConfigFile() {
	configPath := filepath.Join(utils.GetAgentDir(), "keybindings.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	var parsed *jsonx.Object
	if err := jsonx.Unmarshal([]byte(utils.StripBom(string(raw))), &parsed); err != nil || parsed == nil {
		return
	}
	migratedObj, migrated := core.MigrateKeybindingsConfig(parsed)
	if !migrated {
		return
	}
	encoded, err := jsonx.MarshalIndent(migratedObj, "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(configPath, append(encoded, '\n'), 0o644)
}

func migToolsToBin(out io.Writer) {
	agentDir := utils.GetAgentDir()
	toolsDir := filepath.Join(agentDir, "tools")
	binDir := utils.GetBinDir()
	if !migExists(toolsDir) {
		return
	}

	binaries := []string{"fd", "rg", "fd.exe", "rg.exe"}
	movedAny := false
	for _, bin := range binaries {
		oldPath := filepath.Join(toolsDir, bin)
		newPath := filepath.Join(binDir, bin)
		if !migExists(oldPath) {
			continue
		}
		if !migExists(binDir) {
			_ = os.MkdirAll(binDir, 0o755)
		}
		if !migExists(newPath) {
			if err := os.Rename(oldPath, newPath); err == nil {
				movedAny = true
			}
		} else {
			_ = os.RemoveAll(oldPath)
		}
	}
	if movedAny {
		_, _ = fmt.Fprintln(out, "\x1b[32mMigrated managed binaries tools/ → bin/\x1b[39m")
	}
}

func migCheckDeprecatedExtensionDirs(baseDir, label string) []string {
	hooksDir := filepath.Join(baseDir, "hooks")
	toolsDir := filepath.Join(baseDir, "tools")
	var warnings []string

	if migExists(hooksDir) {
		warnings = append(warnings, fmt.Sprintf("%s hooks/ directory found. Hooks have been renamed to extensions.", label))
	}
	if migExists(toolsDir) {
		if entries, err := os.ReadDir(toolsDir); err == nil {
			hasCustom := false
			for _, e := range entries {
				name := e.Name()
				lower := strings.ToLower(name)
				if lower == "fd" || lower == "rg" || lower == "fd.exe" || lower == "rg.exe" || strings.HasPrefix(name, ".") {
					continue
				}
				hasCustom = true
				break
			}
			if hasCustom {
				warnings = append(warnings, fmt.Sprintf("%s tools/ directory contains custom tools. Custom tools have been merged into extensions.", label))
			}
		}
	}
	return warnings
}

func migExtensionSystem(cwd string, out io.Writer) []string {
	agentDir := utils.GetAgentDir()
	projectDir := filepath.Join(cwd, utils.ConfigDirName)

	migCommandsToPrompts(agentDir, "Global", out)
	migCommandsToPrompts(projectDir, "Project", out)

	warnings := append([]string{}, migCheckDeprecatedExtensionDirs(agentDir, "Global")...)
	warnings = append(warnings, migCheckDeprecatedExtensionDirs(projectDir, "Project")...)
	return warnings
}

// ShowDeprecationWarnings prints deprecation warnings to stdout and waits for a keypress.
func ShowDeprecationWarnings(warnings []string) {
	ShowDeprecationWarningsWithIO(warnings, os.Stdin, os.Stdout)
}

// ShowDeprecationWarningsWithIO prints deprecation warnings to out and waits for one byte from in.
func ShowDeprecationWarningsWithIO(warnings []string, in *os.File, out io.Writer) {
	if len(warnings) == 0 {
		return
	}
	if out == nil {
		out = os.Stdout
	}
	for _, w := range warnings {
		_, _ = fmt.Fprintf(out, "\x1b[33mWarning: %s\x1b[39m\n", w)
	}
	_, _ = fmt.Fprintf(out, "\x1b[33m\nMove your extensions to the extensions/ directory.\x1b[39m\n")
	_, _ = fmt.Fprintf(out, "\x1b[33mMigration guide: %s\x1b[39m\n", migGuideURL)
	_, _ = fmt.Fprintf(out, "\x1b[33mDocumentation: %s\x1b[39m\n", migExtensionsDocURL)
	_, _ = fmt.Fprintf(out, "\x1b[2m\nPress any key to continue...\x1b[39m\n")

	if in != nil {
		fd := int(in.Fd())
		if term.IsTerminal(fd) {
			if oldState, err := term.MakeRaw(fd); err == nil {
				defer func() { _ = term.Restore(fd, oldState) }()
			}
		}
		var buf [1]byte
		_, _ = in.Read(buf[:])
	}
	_, _ = fmt.Fprintln(out)
}

// RunMigrations runs all one-time startup migrations against cwd and utils.GetAgentDir().
func RunMigrations(cwd string) MigrationResult {
	return RunMigrationsWithOutput(cwd, os.Stdout)
}

// RunMigrationsWithOutput runs startup migrations, directing status messages to out.
func RunMigrationsWithOutput(cwd string, out io.Writer) MigrationResult {
	if out == nil {
		out = os.Stdout
	}
	migratedAuthProviders := MigrateAuthToAuthJson()
	MigrateSessionsFromAgentRoot()
	migToolsToBin(out)
	migKeybindingsConfigFile()
	deprecationWarnings := migExtensionSystem(cwd, out)
	return MigrationResult{
		MigratedAuthProviders: migratedAuthProviders,
		DeprecationWarnings:   deprecationWarnings,
	}
}
