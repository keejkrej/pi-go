// Ported from packages/coding-agent/src/package-manager-cli.ts (pi v1.5.2).

package codingagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/cli"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

// PackageCommand identifies a top-level package-management CLI subcommand.
type PackageCommand string

const (
	PackageCommandInstall PackageCommand = "install"
	PackageCommandRemove  PackageCommand = "remove"
	PackageCommandUpdate  PackageCommand = "update"
	PackageCommandList    PackageCommand = "list"
)

// PackageCommandOptions configures HandleConfigCommand and HandlePackageCommand for testing or embedding.
type PackageCommandOptions struct {
	Cwd              string
	AgentDir         string
	Stdout           io.Writer
	Stderr           io.Writer
	ConfigUiOptions  *cli.ConfigSelectorOptions
	StartupUiOptions *cli.StartupUiOptions
	ModelRuntime     *core.ModelRuntime
	AllowUi          *bool
	RefreshTimeout   time.Duration
}

type pmcPackageOptions struct {
	command            PackageCommand
	source             string
	modelsTarget       bool
	local              bool
	force              bool
	help               bool
	invalidOption      string
	invalidArgument    string
	missingOptionValue string
	conflictingOptions string
}

func pmcResolveIO(opts []*PackageCommandOptions) (PackageCommandOptions, io.Writer, io.Writer, string, string) {
	var cfg PackageCommandOptions
	if len(opts) > 0 && opts[0] != nil {
		cfg = *opts[0]
	}
	stdout := cfg.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	cwd := cfg.Cwd
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	agentDir := cfg.AgentDir
	if agentDir == "" {
		agentDir = utils.GetAgentDir()
	}
	return cfg, stdout, stderr, cwd, agentDir
}

func pmcConfigUsage() string {
	return fmt.Sprintf("%s config [-l] [--approve|--no-approve]", utils.AppName)
}

func pmcPackageUsage(cmd PackageCommand) string {
	switch cmd {
	case PackageCommandInstall:
		return fmt.Sprintf("%s install <source> [-l] [--approve|--no-approve]", utils.AppName)
	case PackageCommandRemove:
		return fmt.Sprintf("%s remove <source> [-l] [--approve|--no-approve]", utils.AppName)
	case PackageCommandUpdate:
		return fmt.Sprintf("%s update [source|self|pi] [--self|--extensions|--models|--all] [--extension <source>] [--approve|--no-approve] [--force]", utils.AppName)
	case PackageCommandList:
		return fmt.Sprintf("%s list [--approve|--no-approve]", utils.AppName)
	default:
		return utils.AppName
	}
}

func pmcPrintConfigHelp(out io.Writer) {
	_, _ = fmt.Fprintf(out, `Usage:
  %s

Open the resource configuration TUI to enable or disable package resources.
Without -l, starts in global settings (~/%s/agent/settings.json).
Press Tab in the TUI to switch between global and project-local modes.

Options:
  -l, --local       Edit project overrides (%s/settings.json)
  -a, --approve     Trust project-local files for this command with -l
  -na, --no-approve Ignore project-local files for this command with -l
`, pmcConfigUsage(), utils.ConfigDirName, utils.ConfigDirName)
}

func pmcPrintPackageHelp(out io.Writer, cmd PackageCommand) {
	switch cmd {
	case PackageCommandInstall:
		_, _ = fmt.Fprintf(out, `Usage:
  %s

Install a package and add it to settings.
Note: pi-go barebone build does not support npm/git package installation.

Options:
  -l, --local       Install project-locally (%s/settings.json)
  -a, --approve     Trust project-local files for this command
  -na, --no-approve Ignore project-local files for this command
`, pmcPackageUsage(PackageCommandInstall), utils.ConfigDirName)
	case PackageCommandRemove:
		_, _ = fmt.Fprintf(out, `Usage:
  %s

Remove a package and its source from settings.
Alias: %s uninstall <source> [-l]
Note: pi-go barebone build does not support npm/git package removal.

Options:
  -l, --local       Remove from project settings (%s/settings.json)
  -a, --approve     Trust project-local files for this command
  -na, --no-approve Ignore project-local files for this command
`, pmcPackageUsage(PackageCommandRemove), utils.AppName, utils.ConfigDirName)
	case PackageCommandUpdate:
		_, _ = fmt.Fprintf(out, `Usage:
  %s

Update pi, installed packages, or model catalogs.

Options:
  --self                  Update pi only (default when no target is given)
  --extensions            Update installed packages only
  --models                Refresh model catalogs only
  --all                   Update pi and installed packages
  --extension <source>    Update one package only
  -a, --approve           Trust project-local files for this command
  -na, --no-approve       Ignore project-local files for this command
  --force                 Reinstall pi even if the current version is latest
`, pmcPackageUsage(PackageCommandUpdate))
	case PackageCommandList:
		_, _ = fmt.Fprintf(out, `Usage:
  %s

List installed packages from user and project settings.

Options:
  -a, --approve      Trust project-local files for this command
  -na, --no-approve  Ignore project-local files for this command
`, pmcPackageUsage(PackageCommandList))
	}
}

func pmcReportSettingsErrors(sm *core.SettingsManager, label string, errOut io.Writer) {
	if sm == nil {
		return
	}
	for _, sErr := range sm.DrainErrors() {
		if sErr.Error != nil {
			_, _ = fmt.Fprintf(errOut, "Warning (%s, %s settings): %s\n", label, sErr.Scope, sErr.Error.Error())
		}
	}
}

// HandleConfigCommand handles the `pi config` CLI subcommand when args[0] == "config".
// It returns handled=true when `config` was dispatched along with the process exit code.
func HandleConfigCommand(ctx context.Context, args []string, opts ...*PackageCommandOptions) (bool, int) {
	if len(args) == 0 || args[0] != "config" {
		return false, 0
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, stdout, stderr, cwd, agentDir := pmcResolveIO(opts)

	rest := args[1:]
	for _, a := range rest {
		if a == "-h" || a == "--help" {
			pmcPrintConfigHelp(stdout)
			return true, 0
		}
	}

	local := false
	var trustOverride *bool
	for _, arg := range rest {
		switch {
		case arg == "-l" || arg == "--local":
			local = true
		case arg == "-a" || arg == "--approve":
			v := true
			trustOverride = &v
		case arg == "-na" || arg == "--no-approve":
			v := false
			trustOverride = &v
		case strings.HasPrefix(arg, "-"):
			_, _ = fmt.Fprintf(stderr, "Unknown option %s for \"config\".\n", arg)
			_, _ = fmt.Fprintf(stderr, "Use \"%s --help\" or \"%s\".\n", utils.AppName, pmcConfigUsage())
			return true, 1
		default:
			_, _ = fmt.Fprintf(stderr, "Unexpected argument %s.\n", arg)
			_, _ = fmt.Fprintf(stderr, "Usage: %s\n", pmcConfigUsage())
			return true, 1
		}
	}

	falseVal := false
	sm := core.SettingsManagerCreateWithOptions(cwd, agentDir, &core.SettingsManagerCreateOptions{
		ProjectTrusted: &falseVal,
	})

	hasUI := cli.IsInteractiveProcessTerminal()
	mode := core.ExtensionModePrint
	if hasUI {
		mode = core.ExtensionModeTui
	}
	if cfg.AllowUi != nil {
		hasUI = *cfg.AllowUi
		if hasUI {
			mode = core.ExtensionModeTui
		} else {
			mode = core.ExtensionModePrint
		}
	} else if cfg.ConfigUiOptions != nil && (cfg.ConfigUiOptions.UI != nil || cfg.ConfigUiOptions.Terminal != nil) {
		hasUI = true
		mode = core.ExtensionModeTui
	}

	trusted, trustErr := cli.ResolveStartupProjectTrust(ctx, cli.PromptProjectTrustOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: sm,
		TrustOverride:   trustOverride,
		Mode:            mode,
		HasUI:           hasUI,
		ErrOut:          stderr,
		UiOptions:       cfg.StartupUiOptions,
	})
	if trustErr != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", trustErr)
		return true, 1
	}
	sm.SetProjectTrusted(trusted)

	if local && !sm.IsProjectTrusted() {
		_, _ = fmt.Fprintln(stderr, "Project is not trusted. Use --approve to modify local resource config.")
		return true, 1
	}

	pmcReportSettingsErrors(sm, "config command", stderr)

	globalSm := core.SettingsManagerCreateWithOptions(cwd, agentDir, &core.SettingsManagerCreateOptions{
		ProjectTrusted: &falseVal,
	})
	globalPaths, _ := core.NewDefaultPackageManager(core.DefaultPackageManagerOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: globalSm,
	}).Resolve(ctx)

	projectPaths := globalPaths
	if sm.IsProjectTrusted() {
		if resolved, err := core.NewDefaultPackageManager(core.DefaultPackageManagerOptions{
			Cwd:             cwd,
			AgentDir:        agentDir,
			SettingsManager: sm,
		}).Resolve(ctx); err == nil {
			projectPaths = resolved
		}
	}

	writeScope := components.ConfigWriteScopeGlobal
	if local {
		writeScope = components.ConfigWriteScopeProject
	}

	if err := cli.SelectConfig(
		ctx,
		components.ScopedResolvedPaths{
			Global:  globalPaths,
			Project: projectPaths,
		},
		sm,
		cwd,
		agentDir,
		writeScope,
		sm.IsProjectTrusted(),
		cfg.ConfigUiOptions,
	); err != nil && !errors.Is(err, cli.ErrConfigSelectorExit) {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return true, 1
	}
	return true, 0
}

func pmcParsePackageCommand(args []string) *pmcPackageOptions {
	if len(args) == 0 {
		return nil
	}
	rawCmd := args[0]
	var cmd PackageCommand
	switch rawCmd {
	case "uninstall":
		cmd = PackageCommandRemove
	case "install":
		cmd = PackageCommandInstall
	case "remove":
		cmd = PackageCommandRemove
	case "update":
		cmd = PackageCommandUpdate
	case "list":
		cmd = PackageCommandList
	default:
		return nil
	}

	opts := &pmcPackageOptions{command: cmd}
	var selfFlag, extensionsFlag, modelsFlag, allFlag bool
	var extensionFlagSource string
	rest := args[1:]

	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		switch arg {
		case "-h", "--help":
			opts.help = true
		case "-l", "--local":
			if cmd == PackageCommandInstall || cmd == PackageCommandRemove {
				opts.local = true
			} else if opts.invalidOption == "" {
				opts.invalidOption = arg
			}
		case "--self":
			if cmd == PackageCommandUpdate {
				selfFlag = true
			} else if opts.invalidOption == "" {
				opts.invalidOption = arg
			}
		case "--extensions":
			if cmd == PackageCommandUpdate {
				extensionsFlag = true
			} else if opts.invalidOption == "" {
				opts.invalidOption = arg
			}
		case "--models":
			if cmd == PackageCommandUpdate {
				modelsFlag = true
			} else if opts.invalidOption == "" {
				opts.invalidOption = arg
			}
		case "--all":
			if cmd == PackageCommandUpdate {
				allFlag = true
			} else if opts.invalidOption == "" {
				opts.invalidOption = arg
			}
		case "--approve", "-a", "--no-approve", "-na":
		case "--force":
			if cmd == PackageCommandUpdate {
				opts.force = true
			} else if opts.invalidOption == "" {
				opts.invalidOption = arg
			}
		case "--extension":
			if cmd != PackageCommandUpdate {
				if opts.invalidOption == "" {
					opts.invalidOption = arg
				}
				continue
			}
			var next string
			if i+1 < len(rest) {
				next = rest[i+1]
			}
			if next == "" || strings.HasPrefix(next, "-") {
				if opts.missingOptionValue == "" {
					opts.missingOptionValue = arg
				}
			} else if extensionFlagSource != "" {
				if opts.conflictingOptions == "" {
					opts.conflictingOptions = "--extension can only be provided once"
				}
				i++
			} else {
				extensionFlagSource = next
				i++
			}
		default:
			if strings.HasPrefix(arg, "-") {
				if opts.invalidOption == "" {
					opts.invalidOption = arg
				}
			} else if opts.source == "" {
				opts.source = arg
			} else if opts.invalidArgument == "" {
				opts.invalidArgument = arg
			}
		}
	}

	if cmd == PackageCommandUpdate {
		if allFlag && (selfFlag || extensionsFlag || modelsFlag || extensionFlagSource != "") && opts.conflictingOptions == "" {
			opts.conflictingOptions = "--all cannot be combined with --self, --extensions, --models, or --extension"
		}
		if allFlag && opts.source != "" && opts.conflictingOptions == "" {
			opts.conflictingOptions = "--all cannot be combined with a positional source"
		}
		if modelsFlag {
			if (selfFlag || extensionsFlag || allFlag || extensionFlagSource != "") && opts.conflictingOptions == "" {
				opts.conflictingOptions = "--models cannot be combined with --self, --extensions, --all, or --extension"
			}
			if opts.source != "" && opts.conflictingOptions == "" {
				opts.conflictingOptions = "--models cannot be combined with a positional source"
			}
			opts.modelsTarget = true
		} else if extensionFlagSource != "" {
			if (selfFlag || extensionsFlag || allFlag) && opts.conflictingOptions == "" {
				opts.conflictingOptions = "--extension cannot be combined with --self, --extensions, or --all"
			}
			if opts.source != "" && opts.conflictingOptions == "" {
				opts.conflictingOptions = "--extension cannot be combined with a positional source"
			}
		} else if opts.source != "" {
			sourceIsSelf := opts.source == "self" || opts.source == "pi"
			if !sourceIsSelf && (extensionsFlag || selfFlag || allFlag) && opts.conflictingOptions == "" {
				opts.conflictingOptions = "positional update targets cannot be combined with --self, --extensions, or --all"
			}
		}
	}

	return opts
}

func pmcRefreshModelCatalogs(ctx context.Context, agentDir string, cfg PackageCommandOptions, stdout, stderr io.Writer) error {
	timeout := cfg.RefreshTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	refreshCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rt := cfg.ModelRuntime
	if rt == nil {
		authPath := filepath.Join(agentDir, "auth.json")
		modelsPath := filepath.Join(agentDir, "models.json")
		var err error
		rt, err = core.CreateModelRuntime(refreshCtx, &core.CreateModelRuntimeOptions{
			AuthPath:          &authPath,
			ModelsPath:        &modelsPath,
			AllowModelNetwork: false,
		})
		if err != nil {
			return err
		}
		defer rt.Close()
	}

	allowNetwork := true
	force := true
	result, err := rt.Refresh(refreshCtx, &ai.ModelsRefreshOptions{
		AllowNetwork: &allowNetwork,
		Force:        &force,
	})
	if err != nil {
		return err
	}
	if result.Aborted {
		return fmt.Errorf("Model catalog refresh timed out.")
	}

	var failures []string
	if result.Errors != nil {
		for _, provider := range result.Errors.Keys() {
			provErr, _ := result.Errors.Get(provider)
			if provErr == nil {
				continue
			}
			var modelsErr *ai.ModelsError
			if errors.As(provErr, &modelsErr) && modelsErr.Code == ai.ModelsErrorCodeOAuth {
				_, _ = fmt.Fprintf(stderr, "Warning: skipped model catalog for %s: %s\nLog in again with `%s auth login %s` to refresh it.\n", provider, modelsErr.Message, utils.AppName, provider)
			} else {
				failures = append(failures, fmt.Sprintf("%s: %s", provider, provErr.Error()))
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("Could not refresh model catalogs: %s", strings.Join(failures, "; "))
	}

	_, _ = fmt.Fprintln(stdout, "Model catalogs refreshed")
	return nil
}

// HandlePackageCommand handles `install`, `remove`, `uninstall`, `update`, and `list`.
// In barebone builds, `update --models` and `--help` are supported cleanly while external
// npm/git Pi package commands and self-update return a clear unsupported message and exit code 1.
func HandlePackageCommand(ctx context.Context, args []string, opts ...*PackageCommandOptions) (bool, int) {
	parsed := pmcParsePackageCommand(args)
	if parsed == nil {
		return false, 0
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, stdout, stderr, _, agentDir := pmcResolveIO(opts)

	if parsed.help {
		pmcPrintPackageHelp(stdout, parsed.command)
		return true, 0
	}
	if parsed.invalidOption != "" {
		_, _ = fmt.Fprintf(stderr, "Unknown option %s for %q.\n", parsed.invalidOption, parsed.command)
		_, _ = fmt.Fprintf(stderr, "Use \"%s --help\" or \"%s\".\n", utils.AppName, pmcPackageUsage(parsed.command))
		return true, 1
	}
	if parsed.missingOptionValue != "" {
		_, _ = fmt.Fprintf(stderr, "Missing value for %s.\n", parsed.missingOptionValue)
		_, _ = fmt.Fprintf(stderr, "Usage: %s\n", pmcPackageUsage(parsed.command))
		return true, 1
	}
	if parsed.invalidArgument != "" {
		_, _ = fmt.Fprintf(stderr, "Unexpected argument %s.\n", parsed.invalidArgument)
		_, _ = fmt.Fprintf(stderr, "Usage: %s\n", pmcPackageUsage(parsed.command))
		return true, 1
	}
	if parsed.conflictingOptions != "" {
		_, _ = fmt.Fprintln(stderr, parsed.conflictingOptions)
		_, _ = fmt.Fprintf(stderr, "Usage: %s\n", pmcPackageUsage(parsed.command))
		return true, 1
	}
	if (parsed.command == PackageCommandInstall || parsed.command == PackageCommandRemove) && parsed.source == "" {
		_, _ = fmt.Fprintf(stderr, "Missing %s source.\n", parsed.command)
		_, _ = fmt.Fprintf(stderr, "Usage: %s\n", pmcPackageUsage(parsed.command))
		return true, 1
	}

	if parsed.command == PackageCommandUpdate && parsed.modelsTarget {
		if err := pmcRefreshModelCatalogs(ctx, agentDir, cfg, stdout, stderr); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
			return true, 1
		}
		return true, 0
	}

	_, _ = fmt.Fprintf(stderr, "Error: %v (npm/git package installation, removal, listing, and self-update are not supported in pi-go barebone builds; use `%s config` to toggle local resources)\n", core.ErrPackagesUnsupported, utils.AppName)
	return true, 1
}
