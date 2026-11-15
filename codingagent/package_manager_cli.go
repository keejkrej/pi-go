// Ported from packages/coding-agent/src/package-manager-cli.ts (pi v1.5.2).

package codingagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/cli"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/components"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/xspawn"
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
	HTTPClient       *http.Client
}

type pmcUpdateTargetType string

const (
	pmcUpdateSelf       pmcUpdateTargetType = "self"
	pmcUpdateExtensions pmcUpdateTargetType = "extensions"
	pmcUpdateModels     pmcUpdateTargetType = "models"
	pmcUpdateAll        pmcUpdateTargetType = "all"
)

type pmcUpdateTarget struct {
	kind   pmcUpdateTargetType
	source string
}

type pmcPackageOptions struct {
	command                   PackageCommand
	source                    string
	updateTarget              *pmcUpdateTarget
	showExtensionsSkippedNote bool
	modelsTarget              bool
	local                     bool
	force                     bool
	trustOverride             *bool
	help                      bool
	invalidOption             string
	invalidArgument           string
	missingOptionValue        string
	conflictingOptions        string
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

Options:
  -l, --local       Install project-locally (%s/settings.json)
  -a, --approve     Trust project-local files for this command
  -na, --no-approve Ignore project-local files for this command

Examples:
  %s install npm:@foo/bar
  %s install git:github.com/user/repo
  %s install git:git@github.com:user/repo
  %s install https://github.com/user/repo
  %s install /path/to/local/package
  %s install npm:@foo/bar -l
`, pmcPackageUsage(PackageCommandInstall), utils.ConfigDirName, utils.AppName, utils.AppName, utils.AppName, utils.AppName, utils.AppName, utils.AppName)
	case PackageCommandRemove:
		_, _ = fmt.Fprintf(out, `Usage:
  %s

Remove a package and its source from settings.
Alias: %s uninstall <source> [-l]

Options:
  -l, --local       Remove from project settings (%s/settings.json)
  -a, --approve     Trust project-local files for this command
  -na, --no-approve Ignore project-local files for this command

Examples:
  %s remove npm:@foo/bar
  %s remove npm:@foo/bar -l
  %s uninstall @foo/bar
`, pmcPackageUsage(PackageCommandRemove), utils.AppName, utils.ConfigDirName, utils.AppName, utils.AppName, utils.AppName)
	case PackageCommandUpdate:
		_, _ = fmt.Fprintf(out, `Usage:
  %s

Update %s, installed packages, or model catalogs.

Options:
  --self                  Update %s only (default when no target is given)
  --extensions            Update installed packages only
  --models                Refresh model catalogs only
  --all                   Update %s and installed packages
  --extension <source>    Update one package only
  -a, --approve           Trust project-local files for this command
  -na, --no-approve       Ignore project-local files for this command
  --force                 Reinstall %s even if the current version is latest
`, pmcPackageUsage(PackageCommandUpdate), utils.AppName, utils.AppName, utils.AppName, utils.AppName)
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
		HTTPClient:      cfg.HTTPClient,
	}).Resolve(ctx)

	projectPaths := globalPaths
	if sm.IsProjectTrusted() {
		if resolved, err := core.NewDefaultPackageManager(core.DefaultPackageManagerOptions{
			Cwd:             cwd,
			AgentDir:        agentDir,
			SettingsManager: sm,
			HTTPClient:      cfg.HTTPClient,
		}).Resolve(ctx); err == nil {
			projectPaths = resolved
		}
	}

	pmcReportInstallFailureWarnings(stderr, globalPaths.InstallFailures, projectPaths.InstallFailures)

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
		case "--approve", "-a":
			v := true
			opts.trustOverride = &v
		case "--no-approve", "-na":
			v := false
			opts.trustOverride = &v
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
			opts.updateTarget = &pmcUpdateTarget{kind: pmcUpdateModels}
		} else if extensionFlagSource != "" {
			if (selfFlag || extensionsFlag || allFlag) && opts.conflictingOptions == "" {
				opts.conflictingOptions = "--extension cannot be combined with --self, --extensions, or --all"
			}
			if opts.source != "" && opts.conflictingOptions == "" {
				opts.conflictingOptions = "--extension cannot be combined with a positional source"
			}
			opts.updateTarget = &pmcUpdateTarget{kind: pmcUpdateExtensions, source: extensionFlagSource}
		} else if opts.source != "" {
			sourceIsSelf := opts.source == "self" || opts.source == "pi"
			if sourceIsSelf {
				if extensionsFlag {
					opts.updateTarget = &pmcUpdateTarget{kind: pmcUpdateAll}
				} else {
					opts.updateTarget = &pmcUpdateTarget{kind: pmcUpdateSelf}
				}
			} else {
				if (extensionsFlag || selfFlag || allFlag) && opts.conflictingOptions == "" {
					opts.conflictingOptions = "positional update targets cannot be combined with --self, --extensions, or --all"
				}
				opts.updateTarget = &pmcUpdateTarget{kind: pmcUpdateExtensions, source: opts.source}
			}
		} else if allFlag || (selfFlag && extensionsFlag) {
			opts.updateTarget = &pmcUpdateTarget{kind: pmcUpdateAll}
		} else if selfFlag {
			opts.updateTarget = &pmcUpdateTarget{kind: pmcUpdateSelf}
		} else if extensionsFlag {
			opts.updateTarget = &pmcUpdateTarget{kind: pmcUpdateExtensions}
		} else {
			opts.updateTarget = &pmcUpdateTarget{kind: pmcUpdateSelf}
			opts.showExtensionsSkippedNote = true
		}
	}

	return opts
}

func pmcReportInstallFailureWarnings(stderr io.Writer, failureSlices ...[]core.PackageInstallFailure) {
	seen := make(map[string]struct{})
	for _, failures := range failureSlices {
		for _, f := range failures {
			msg := f.Message
			if msg == "" {
				msg = f.Error
			}
			warning := fmt.Sprintf("Failed to install %s: %s", f.Source, msg)
			if _, ok := seen[warning]; ok {
				continue
			}
			seen[warning] = struct{}{}
			_, _ = fmt.Fprintf(stderr, "Warning: %s\n", warning)
		}
	}
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

type pmcSelfUpdatePlan struct {
	packageName string
	installSpec string
	version     string
	shouldRun   bool
	note        string
}

func pmcGetSelfUpdatePlan(ctx context.Context, force bool, stdout io.Writer) (*pmcSelfUpdatePlan, error) {
	latest, err := utils.GetLatestPiRelease(ctx, utils.VERSION, &utils.VersionCheckOptions{Retry: true})
	if err != nil {
		return nil, fmt.Errorf("Could not determine latest %s version: %s", utils.AppName, utils.FormatVersionCheckError(err))
	}
	if latest == nil {
		return nil, fmt.Errorf("Could not determine latest %s version.", utils.AppName)
	}
	packageName := latest.PackageName
	if packageName == "" {
		packageName = utils.PackageName
	}
	installSpec := fmt.Sprintf("%s@%s", packageName, latest.Version)
	if force || packageName != utils.PackageName || utils.IsNewerPackageVersion(latest.Version, utils.VERSION) {
		return &pmcSelfUpdatePlan{
			packageName: packageName,
			installSpec: installSpec,
			version:     latest.Version,
			shouldRun:   true,
			note:        latest.Note,
		}, nil
	}
	_, _ = fmt.Fprintf(stdout, "%s is already up to date (v%s)\n", utils.AppName, utils.VERSION)
	return &pmcSelfUpdatePlan{
		packageName: packageName,
		installSpec: installSpec,
		version:     latest.Version,
		shouldRun:   false,
	}, nil
}

func pmcRunSelfUpdateCommand(ctx context.Context, command *utils.SelfUpdateCommand, stdout, stderr io.Writer) error {
	_, _ = fmt.Fprintf(stdout, "Updating %s with %s...\n", utils.AppName, command.Display)
	steps := command.Steps
	if len(steps) == 0 {
		steps = []utils.SelfUpdateCommandStep{{
			Command: command.Command,
			Args:    command.Args,
			Display: command.Display,
		}}
	}
	for _, step := range steps {
		cmd := xspawn.Command(ctx, step.Command, step.Args)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := xspawn.VerifyENOENTSync(cmd, cmd.Run()); err != nil {
			return fmt.Errorf("%s failed: %w", step.Display, err)
		}
	}
	return nil
}

func pmcPrintSelfUpdateUnavailable(stderr io.Writer, npmCommand []string, target utils.SelfUpdatePackageTarget) {
	_, _ = fmt.Fprintf(stderr, "error: %s cannot self-update this installation.\n", utils.AppName)
	_, _ = fmt.Fprintln(stderr, utils.GetSelfUpdateUnavailableInstruction(utils.PackageName, npmCommand, target))
	_, _ = fmt.Fprintf(stderr, "To make `%s update` run your own update command, set PI_SELF_UPDATE_COMMAND or the selfUpdateCommand setting.\n", utils.AppName)
	if len(os.Args) > 0 && os.Args[0] != "" {
		_, _ = fmt.Fprintf(stderr, "\nLocation of %s executable: %s\n", utils.AppName, os.Args[0])
	}
}

func pmcHandleSelfUpdate(
	ctx context.Context,
	sm *core.SettingsManager,
	force bool,
	stdout, stderr io.Writer,
) int {
	configuredArgv := utils.GetConfiguredSelfUpdateArgv(sm.GetGlobalSettings().SelfUpdateCommand)
	plan, err := pmcGetSelfUpdatePlan(ctx, force, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	if !plan.shouldRun {
		return 0
	}

	target := utils.SelfUpdatePackageTarget{
		PackageName: plan.packageName,
		InstallSpec: plan.installSpec,
		Version:     plan.version,
	}

	if len(configuredArgv) > 0 {
		command := utils.MakeConfiguredSelfUpdateCommand(configuredArgv, target)
		if strings.TrimSpace(plan.note) != "" {
			_, _ = fmt.Fprintf(stdout, "\nUpdate note\n%s\n\n", strings.TrimSpace(plan.note))
		}
		if err := pmcRunSelfUpdateCommand(ctx, command, stdout, stderr); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "Updated %s from %s to %s\n", utils.AppName, utils.VERSION, plan.version)
		return 0
	}

	selfUpdateNpmCommand := sm.GetGlobalSettings().NpmCommand
	selfUpdateCmd := utils.GetSelfUpdateCommand(utils.PackageName, selfUpdateNpmCommand, target)
	if selfUpdateCmd == nil {
		pmcPrintSelfUpdateUnavailable(stderr, selfUpdateNpmCommand, target)
		return 1
	}
	if strings.TrimSpace(plan.note) != "" {
		_, _ = fmt.Fprintf(stdout, "\nUpdate note\n%s\n\n", strings.TrimSpace(plan.note))
	}
	if err := pmcRunSelfUpdateCommand(ctx, selfUpdateCmd, stdout, stderr); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		_, _ = fmt.Fprintf(stderr, "If this keeps failing, run this command yourself: %s\n", selfUpdateCmd.Display)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Updated %s from %s to %s\n", utils.AppName, utils.VERSION, plan.version)
	return 0
}

// HandlePackageCommand handles `install`, `remove`, `uninstall`, `update`, and `list`.
func HandlePackageCommand(ctx context.Context, args []string, opts ...*PackageCommandOptions) (bool, int) {
	parsed := pmcParsePackageCommand(args)
	if parsed == nil {
		return false, 0
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, stdout, stderr, cwd, agentDir := pmcResolveIO(opts)

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

	falseVal := false
	sm := core.SettingsManagerCreateWithOptions(cwd, agentDir, &core.SettingsManagerCreateOptions{
		ProjectTrusted: &falseVal,
	})
	if parsed.command == PackageCommandUpdate {
		trustStore := core.NewProjectTrustStore(agentDir)
		savedDecision, _ := trustStore.Get(cwd)
		savedTrusted := savedDecision != nil && *savedDecision
		if parsed.trustOverride != nil {
			sm.SetProjectTrusted(*parsed.trustOverride)
		} else {
			sm.SetProjectTrusted(savedTrusted)
		}
	} else {
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
		} else if cfg.StartupUiOptions != nil && (cfg.StartupUiOptions.UI != nil || cfg.StartupUiOptions.Terminal != nil) {
			hasUI = true
			mode = core.ExtensionModeTui
		}
		trusted, trustErr := cli.ResolveStartupProjectTrust(ctx, cli.PromptProjectTrustOptions{
			Cwd:             cwd,
			AgentDir:        agentDir,
			SettingsManager: sm,
			TrustOverride:   parsed.trustOverride,
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
	}

	writesProjectPackageConfig := (parsed.command == PackageCommandInstall || parsed.command == PackageCommandRemove) && parsed.local
	if !sm.IsProjectTrusted() && writesProjectPackageConfig {
		_, _ = fmt.Fprintln(stderr, "Project is not trusted. Use --approve to modify local package config.")
		return true, 1
	}

	pmcReportSettingsErrors(sm, "package command", stderr)

	pm := core.NewDefaultPackageManager(core.DefaultPackageManagerOptions{
		Cwd:             cwd,
		AgentDir:        agentDir,
		SettingsManager: sm,
		HTTPClient:      cfg.HTTPClient,
	})

	var progressMu sync.Mutex
	var updatedPackages []string
	var warningCount int
	lineCh := make(chan string)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for msg := range lineCh {
			_, _ = fmt.Fprintln(stdout, msg)
		}
	}()
	var closeWriterOnce sync.Once
	closeProgressWriter := func() {
		closeWriterOnce.Do(func() {
			pm.SetProgressCallback(nil)
			close(lineCh)
			<-writerDone
		})
	}
	defer closeProgressWriter()

	pm.SetProgressCallback(func(ev core.ProgressEvent) {
		switch {
		case ev.Type == "result" && ev.Action == "update" && ev.Message != "":
			progressMu.Lock()
			updatedPackages = append(updatedPackages, ev.Message)
			progressMu.Unlock()
		case (ev.Type == "start" || ev.Type == "progress") && ev.Message != "":
			if ev.Type == "progress" {
				progressMu.Lock()
				warningCount++
				progressMu.Unlock()
			}
			lineCh <- ev.Message
		}
	})

	switch parsed.command {
	case PackageCommandInstall:
		err := pm.InstallAndPersist(ctx, parsed.source, &core.PackageInstallOptions{Local: parsed.local})
		closeProgressWriter()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
			return true, 1
		}
		_, _ = fmt.Fprintf(stdout, "Installed %s\n", parsed.source)
		return true, 0

	case PackageCommandRemove:
		removed, err := pm.RemoveAndPersist(ctx, parsed.source, &core.PackageRemoveOptions{Local: parsed.local})
		closeProgressWriter()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
			return true, 1
		}
		if !removed {
			_, _ = fmt.Fprintf(stderr, "No matching package found for %s\n", parsed.source)
			return true, 1
		}
		_, _ = fmt.Fprintf(stdout, "Removed %s\n", parsed.source)
		return true, 0

	case PackageCommandList:
		closeProgressWriter()
		configured := pm.ListConfiguredPackages()
		if len(configured) == 0 {
			_, _ = fmt.Fprintln(stdout, "No packages installed.")
			return true, 0
		}
		var userPkgs, projectPkgs []core.ConfiguredPackage
		for _, pkg := range configured {
			if pkg.Scope == utils.SourceScopeProject {
				projectPkgs = append(projectPkgs, pkg)
			} else {
				userPkgs = append(userPkgs, pkg)
			}
		}
		formatPkg := func(pkg core.ConfiguredPackage) {
			display := pkg.Source
			if pkg.Filtered {
				display += " (filtered)"
			}
			_, _ = fmt.Fprintf(stdout, "  %s\n", display)
			if pkg.InstalledPath != "" {
				_, _ = fmt.Fprintf(stdout, "    %s\n", pkg.InstalledPath)
			}
		}
		if len(userPkgs) > 0 {
			_, _ = fmt.Fprintln(stdout, "User packages:")
			for _, pkg := range userPkgs {
				formatPkg(pkg)
			}
		}
		if len(projectPkgs) > 0 {
			if len(userPkgs) > 0 {
				_, _ = fmt.Fprintln(stdout)
			}
			_, _ = fmt.Fprintln(stdout, "Project packages:")
			for _, pkg := range projectPkgs {
				formatPkg(pkg)
			}
		}
		return true, 0

	case PackageCommandUpdate:
		target := parsed.updateTarget
		if target == nil {
			target = &pmcUpdateTarget{kind: pmcUpdateSelf}
		}
		if target.kind != pmcUpdateAll && target.kind != pmcUpdateExtensions {
			closeProgressWriter()
		}
		if parsed.showExtensionsSkippedNote {
			_, _ = fmt.Fprintf(stdout, "Extensions are skipped. Run %s update --extensions to update extensions.\n", utils.AppName)
		}
		if target.kind == pmcUpdateAll || target.kind == pmcUpdateExtensions {
			var err error
			if target.source != "" {
				err = pm.Update(ctx, target.source)
			} else {
				err = pm.Update(ctx)
			}
			closeProgressWriter()
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
				return true, 1
			}
			progressMu.Lock()
			updatedSnapshot := slices.Clone(updatedPackages)
			warnSnapshot := warningCount
			progressMu.Unlock()
			switch {
			case core.IsOfflineModeEnabled():
				_, _ = fmt.Fprintln(stdout, "Skipped package updates (offline mode)")
			case len(updatedSnapshot) == 0 && warnSnapshot > 0:
				if target.source != "" {
					_, _ = fmt.Fprintf(stdout, "%s was not updated, see the warnings above\n", target.source)
				} else {
					_, _ = fmt.Fprintln(stdout, "No packages were updated, see the warnings above")
				}
			case len(updatedSnapshot) == 0:
				if target.source != "" {
					_, _ = fmt.Fprintf(stdout, "%s is already up to date\n", target.source)
				} else {
					_, _ = fmt.Fprintln(stdout, "Packages are already up to date")
				}
			default:
				plural := "s"
				if len(updatedSnapshot) == 1 {
					plural = ""
				}
				_, _ = fmt.Fprintf(stdout, "Updated %d package%s:\n", len(updatedSnapshot), plural)
				for _, msg := range updatedSnapshot {
					_, _ = fmt.Fprintf(stdout, "  %s\n", msg)
				}
			}
		}
		if target.kind == pmcUpdateAll || target.kind == pmcUpdateSelf {
			if code := pmcHandleSelfUpdate(ctx, sm, parsed.force, stdout, stderr); code != 0 {
				return true, code
			}
		}
		return true, 0
	}

	return true, 0
}
