// Ported from packages/coding-agent/src/main.ts (pi v1.5.2).

package codingagent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/cli"
	"github.com/keejkrej/pi-go/codingagent/core"
	_ "github.com/keejkrej/pi-go/codingagent/extensions"
	"github.com/keejkrej/pi-go/codingagent/extensions/mcpext"
	"github.com/keejkrej/pi-go/codingagent/modes"
	"github.com/keejkrej/pi-go/codingagent/modes/gui"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/modes/rpc"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/tui"
	"golang.org/x/term"
)

// MainOptions configures codingagent.Main for alternative entrypoints (such as pi-gui)
// and in-process tests.
type MainOptions struct {
	// DefaultGuiMode defaults to GUI mode instead of terminal TUI unless overridden
	// by explicit CLI flags (-p/--print, --mode rpc|json|text) or non-empty piped stdin.
	DefaultGuiMode bool

	// Optional overrides for embedding and testing.
	Stdin                   io.Reader
	Stdout                  io.Writer
	Stderr                  io.Writer
	StdinIsTTY              *bool
	StdoutIsTTY             *bool
	Cwd                     string
	AgentDir                string
	AuthPath                *string
	ModelsPath              *string
	SessionDir              *string
	ExtensionFactories      []core.InlineExtension
	StartupUiOptions        *cli.StartupUiOptions
	SessionPickerOptions    *cli.SessionPickerOptions
	LoginTerminal           cli.LoginTerminal
	GetDeviceId             func() string
	ShowDeprecationWarnings func(warnings []string, out io.Writer)
	RunInteractiveMode      func(ctx context.Context, runtime *core.AgentSessionRuntime, opts *interactive.InteractiveModeOptions) error
	RunGuiMode              func(ctx context.Context, runtime *core.AgentSessionRuntime, opts *gui.GuiModeOptions) error
	RunPrintMode            func(ctx context.Context, runtime *core.AgentSessionRuntime, opts *modes.PrintModeOptions) int
	RunRpcMode              func(ctx context.Context, runtime *core.AgentSessionRuntime, opts *rpc.RpcModeOptions) error
}

type camAppMode string

const (
	camAppModeInteractive camAppMode = "interactive"
	camAppModeGui         camAppMode = "gui"
	camAppModePrint       camAppMode = "print"
	camAppModeJson        camAppMode = "json"
	camAppModeRpc         camAppMode = "rpc"
)

type camAuthCheckJsonOutput struct {
	Status      cli.AuthCheckStatus  `json:"status"`
	Provider    string               `json:"provider"`
	Reason      *cli.AuthCheckReason `json:"reason,omitzero"`
	AuthType    *ai.AuthType         `json:"authType,omitzero"`
	Credentials *string              `json:"credentials,omitzero"`
}

type camAuthWriterOutput struct {
	out    io.Writer
	errOut io.Writer
}

func (o *camAuthWriterOutput) Write(line string) {
	if o.out != nil {
		_, _ = fmt.Fprintln(o.out, line)
	}
}

func (o *camAuthWriterOutput) Error(line string) {
	if o.errOut != nil {
		_, _ = fmt.Fprintln(o.errOut, line)
	}
}

type camSessionKind string

const (
	camSessionPath     camSessionKind = "path"
	camSessionLocal    camSessionKind = "local"
	camSessionGlobal   camSessionKind = "global"
	camSessionNotFound camSessionKind = "not_found"
)

type camResolvedSession struct {
	kind camSessionKind
	path string
	cwd  string
	arg  string
}

type camBuildSessionOptionsResult struct {
	model                *ai.Model
	thinkingLevel        agent.ThinkingLevel
	scopedModels         []core.ScopedModel
	toolNames            []string
	excludeTools         []string
	noTools              core.NoToolsOption
	cliThinkingFromModel bool
	diagnostics          []core.AgentSessionRuntimeDiagnostic
}

type camStartupIO struct {
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	stdinIsTTY  bool
	stdoutIsTTY bool
}

func camResolveStartupIO(opt *MainOptions) camStartupIO {
	var in io.Reader = os.Stdin
	if opt != nil && opt.Stdin != nil {
		in = opt.Stdin
	}
	var out io.Writer = os.Stdout
	if opt != nil && opt.Stdout != nil {
		out = opt.Stdout
	}
	var errOut io.Writer = os.Stderr
	if opt != nil && opt.Stderr != nil {
		errOut = opt.Stderr
	}

	stdinIsTTY := camFileIsTTY(in)
	if opt != nil && opt.StdinIsTTY != nil {
		stdinIsTTY = *opt.StdinIsTTY
	}
	stdoutIsTTY := camFileIsTTY(out)
	if opt != nil && opt.StdoutIsTTY != nil {
		stdoutIsTTY = *opt.StdoutIsTTY
	}

	return camStartupIO{
		stdin:       in,
		stdout:      out,
		stderr:      errOut,
		stdinIsTTY:  stdinIsTTY,
		stdoutIsTTY: stdoutIsTTY,
	}
}

func camFileIsTTY(v any) bool {
	f, ok := v.(*os.File)
	if !ok || f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func camIsTruthyEnvFlag(value string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	return trimmed == "1" || trimmed == "true" || trimmed == "yes"
}

func camExtensionLoadFailureHint() string {
	return fmt.Sprintf(`Hint: Start without extensions using "%s -ne".`, utils.AppName)
}

func camResolveAppMode(parsed core.Args, defaultGuiMode, stdinIsTTY, stdoutIsTTY bool) camAppMode {
	if parsed.Mode != nil {
		switch *parsed.Mode {
		case core.ModeRpc:
			return camAppModeRpc
		case core.ModeJson:
			return camAppModeJson
		case core.ModeGui:
			return camAppModeGui
		case core.ModeText:
			return camAppModePrint
		}
	}
	if parsed.Gui {
		return camAppModeGui
	}
	if parsed.Print {
		return camAppModePrint
	}
	if defaultGuiMode {
		return camAppModeGui
	}
	if !stdinIsTTY || !stdoutIsTTY {
		return camAppModePrint
	}
	return camAppModeInteractive
}

func camHasExplicitGuiFlag(parsed core.Args) bool {
	if parsed.Gui {
		return true
	}
	return parsed.Mode != nil && *parsed.Mode == core.ModeGui
}

func camToPrintOutputMode(appMode camAppMode) modes.PrintModeOutput {
	if appMode == camAppModeJson {
		return modes.PrintModeOutputJson
	}
	return modes.PrintModeOutputText
}

func camIsPlainRuntimeMetadataCommand(parsed core.Args) bool {
	return !parsed.Print && !parsed.Gui && parsed.Mode == nil && (parsed.Help || parsed.ListModels != nil)
}

func camToTrustExtensionMode(appMode camAppMode) core.ExtensionMode {
	switch appMode {
	case camAppModeRpc:
		return core.ExtensionModeRpc
	case camAppModeJson:
		return core.ExtensionModeJson
	case camAppModePrint:
		return core.ExtensionModePrint
	default:
		return core.ExtensionModeTui
	}
}

func camReportDiagnostics(errOut io.Writer, diagnostics []core.AgentSessionRuntimeDiagnostic) {
	if errOut == nil {
		errOut = os.Stderr
	}
	for _, d := range diagnostics {
		prefix := ""
		switch d.Type {
		case core.AgentSessionRuntimeDiagnosticError:
			prefix = "Error: "
		case core.AgentSessionRuntimeDiagnosticWarning:
			prefix = "Warning: "
		}
		_, _ = fmt.Fprintf(errOut, "%s%s\n", prefix, d.Message)
	}
}

func camReadPipedStdin(in io.Reader, stdinIsTTY bool) *string {
	if stdinIsTTY || in == nil {
		return nil
	}
	data, err := io.ReadAll(in)
	if err != nil {
		return nil
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func camModelRuntimeOptions(opt *MainOptions, agentDir string, readOnly bool, allowNetwork bool) *core.CreateModelRuntimeOptions {
	mrOpts := &core.CreateModelRuntimeOptions{
		AllowModelNetwork: allowNetwork,
	}
	if opt != nil && opt.AuthPath != nil {
		mrOpts.AuthPath = opt.AuthPath
	} else if agentDir != "" {
		p := filepath.Join(agentDir, "auth.json")
		mrOpts.AuthPath = &p
	}
	if readOnly {
		if mrOpts.AuthPath != nil {
			mrOpts.Credentials = core.NewReadOnlyAuthStorage(*mrOpts.AuthPath)
		} else {
			mrOpts.Credentials = core.NewReadOnlyAuthStorage()
		}
	}
	if opt != nil && opt.ModelsPath != nil {
		mrOpts.ModelsPath = opt.ModelsPath
	} else if agentDir != "" {
		p := filepath.Join(agentDir, "models.json")
		mrOpts.ModelsPath = &p
	}
	return mrOpts
}

func camResolveCredentialStore(opt *MainOptions, agentDir string, readOnly bool) ai.CredentialStore {
	if opt != nil && opt.AuthPath != nil {
		if readOnly {
			return core.NewReadOnlyAuthStorage(*opt.AuthPath)
		}
		return core.AuthStorageCreate(*opt.AuthPath)
	}
	if agentDir != "" {
		p := filepath.Join(agentDir, "auth.json")
		if readOnly {
			return core.NewReadOnlyAuthStorage(p)
		}
		return core.AuthStorageCreate(p)
	}
	if readOnly {
		return core.NewReadOnlyAuthStorage()
	}
	return core.AuthStorageCreate()
}

func camResolveWritableAuthStorage(opt *MainOptions, agentDir string) *core.AuthStorage {
	if opt != nil && opt.AuthPath != nil {
		return core.AuthStorageCreate(*opt.AuthPath)
	}
	if agentDir != "" {
		return core.AuthStorageCreate(filepath.Join(agentDir, "auth.json"))
	}
	return core.AuthStorageCreate()
}

func camFilterOfflineFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--offline" {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func camRunAuthLoginCommand(ctx context.Context, args []string, sio camStartupIO, opt *MainOptions) int {
	cwd := ""
	if opt != nil && strings.TrimSpace(opt.Cwd) != "" {
		cwd = opt.Cwd
	} else if wd, err := os.Getwd(); err == nil {
		cwd = wd
	}
	agentDir := utils.GetAgentDir()
	if opt != nil && strings.TrimSpace(opt.AgentDir) != "" {
		agentDir = opt.AgentDir
	}

	settingsManager := core.SettingsManagerCreateWithOptions(cwd, agentDir, &core.SettingsManagerCreateOptions{
		ProjectTrusted: camBoolPtr(false),
	})
	core.ApplyHttpProxySettings(settingsManager.GetGlobalSettings().HttpProxy)
	if timeoutMs, err := settingsManager.GetHttpIdleTimeoutMs(); err == nil {
		_ = core.ConfigureHttpDispatcher(timeoutMs)
	}

	mrCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	modelRuntime, err := core.CreateModelRuntime(mrCtx, camModelRuntimeOptions(opt, agentDir, false, false))
	if err != nil {
		_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
		return 1
	}
	defer modelRuntime.Close()

	termCtx, termCancel := context.WithCancel(ctx)
	defer termCancel()

	loginTerm := cli.LoginTerminal(nil)
	if opt != nil && opt.LoginTerminal != nil {
		loginTerm = opt.LoginTerminal
	} else {
		loginTerm = cli.NewProcessLoginTerminal(termCancel)
	}

	getDeviceId := func() string {
		return settingsManager.GetOrCreateDeviceId()
	}
	if opt != nil && opt.GetDeviceId != nil {
		getDeviceId = opt.GetDeviceId
	}

	return cli.RunAuthLogin(termCtx, args, cli.AuthLoginOptions{
		ModelRuntime: modelRuntime,
		Terminal:     loginTerm,
		AgentDir:     agentDir,
		Cwd:          cwd,
		GetDeviceId:  getDeviceId,
	})
}

func camRunAuthLogoutCommand(ctx context.Context, args []string, sio camStartupIO, opt *MainOptions) int {
	agentDir := ""
	if opt != nil {
		agentDir = opt.AgentDir
	}
	mrCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	modelRuntime, err := core.CreateModelRuntime(mrCtx, camModelRuntimeOptions(opt, agentDir, false, false))
	if err != nil {
		_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
		return 1
	}
	defer modelRuntime.Close()

	return cli.RunAuthLogout(mrCtx, args, cli.AuthLogoutOptions{
		ModelRuntime: modelRuntime,
		Terminal:     &camAuthWriterOutput{out: sio.stdout, errOut: sio.stderr},
	})
}

func camRunAuthCommand(ctx context.Context, args []string, sio camStartupIO, opt *MainOptions) (handled bool, code int) {
	if len(args) == 0 || args[0] != "auth" {
		return false, 0
	}
	if cli.IsAuthCommandHelp(args) {
		cli.PrintAuthCommandHelpTo(sio.stdout)
		return true, 0
	}
	if len(args) > 1 && args[1] == "login" {
		return true, camRunAuthLoginCommand(ctx, camFilterOfflineFlag(args[2:]), sio, opt)
	}
	if len(args) > 1 && args[1] == "logout" {
		return true, camRunAuthLogoutCommand(ctx, camFilterOfflineFlag(args[2:]), sio, opt)
	}

	command, err := cli.ParseAuthCommand(args)
	if err != nil {
		_, _ = fmt.Fprintf(sio.stderr, "Error: %s\n", err.Error())
		return true, 1
	}
	if command == nil {
		return false, 0
	}

	parsed := core.ParseArgs(command.Args)
	if parsed.UnknownFlags != nil && parsed.UnknownFlags.Len() > 0 {
		option := parsed.UnknownFlags.Keys()[0]
		_, _ = fmt.Fprintf(sio.stderr, "Unknown option --%s for %q.\n", option, cli.GetAuthCommandName(command.Kind))
		_, _ = fmt.Fprintf(sio.stderr, "Use \"%s --help\" or \"%s\".\n", utils.AppName, cli.GetAuthCommandUsage(command.Kind))
		return true, 1
	}

	if len(parsed.Diagnostics) > 0 {
		msgs := make([]string, 0, len(parsed.Diagnostics))
		for _, d := range parsed.Diagnostics {
			msgs = append(msgs, d.Message)
		}
		_, _ = fmt.Fprintf(sio.stderr, "Error: %s\n", strings.Join(msgs, "\n"))
		if command.Kind == cli.AuthCommandKindCheck {
			return true, 2
		}
		return true, 1
	}

	agentDir := ""
	if opt != nil {
		agentDir = opt.AgentDir
	}

	if command.Kind != cli.AuthCommandKindCheck {
		mrCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		modelRuntime, mrErr := core.CreateModelRuntime(mrCtx, camModelRuntimeOptions(opt, agentDir, false, false))
		if mrErr != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %s\n", mrErr.Error())
			return true, 1
		}
		defer modelRuntime.Close()

		credential, credErr := cli.ResolveCredentialForPrint(mrCtx, parsed, modelRuntime, command.Kind, command.MinExpiryMs)
		if credErr != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %s\n", credErr.Error())
			return true, 1
		}
		_, _ = fmt.Fprintln(sio.stdout, credential)
		return true, 0
	}

	cliProvider, cliModel, valErr := cli.ValidateAuthCommandArgs(parsed, command.Kind)
	if valErr != nil {
		_, _ = fmt.Fprintf(sio.stderr, "Error: %s\n", valErr.Error())
		return true, 2
	}

	requestedProvider := ""
	if cliProvider != nil {
		requestedProvider = *cliProvider
	} else if cliModel != nil {
		requestedProvider = *cliModel
	}

	var result cli.AuthCheckResult
	var credential *string
	invalidReason := cli.AuthCheckReasonInvalidState

	mrCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	credentials := camResolveCredentialStore(opt, agentDir, command.NoRefresh)

	var modelRuntime *core.ModelRuntime
	var mrErr error
	if opt != nil && (opt.AuthPath != nil || opt.ModelsPath != nil || opt.AgentDir != "") {
		mrOpts := camModelRuntimeOptions(opt, agentDir, command.NoRefresh, false)
		mrOpts.Credentials = credentials
		mrOpts.ModelsStore = core.NewInMemoryCodingAgentModelsStore()
		refreshOnCreate := false
		mrOpts.RefreshOnCreate = &refreshOnCreate
		modelRuntime, mrErr = core.CreateModelRuntime(mrCtx, mrOpts)
	} else {
		modelRuntime, mrErr = cli.CreateAuthCheckModelRuntime(mrCtx, credentials)
	}

	if mrErr != nil {
		result = cli.AuthCheckResult{
			Status:   cli.AuthCheckStatusInvalid,
			Provider: requestedProvider,
			Reason:   &invalidReason,
		}
	} else {
		defer modelRuntime.Close()
		checked, checkErr := cli.CheckProviderAuth(mrCtx, parsed, modelRuntime, &cli.AuthCheckOptions{
			Refresh: !command.NoRefresh,
		})
		if checkErr != nil {
			var authCmdErr *cli.AuthCommandError
			if errors.As(checkErr, &authCmdErr) {
				_, _ = fmt.Fprintf(sio.stderr, "Error: %s\n", authCmdErr.Error())
				return true, 2
			}
			result = cli.AuthCheckResult{
				Status:   cli.AuthCheckStatusInvalid,
				Provider: requestedProvider,
				Reason:   &invalidReason,
			}
		} else {
			result = checked
			if command.Credentials && result.Status == cli.AuthCheckStatusReady {
				cred, getCredErr := cli.GetProviderCredential(mrCtx, result.Provider, modelRuntime, credentials, cli.AuthCheckOptions{
					Refresh: !command.NoRefresh,
				})
				if getCredErr != nil {
					result = cli.AuthCheckResult{
						Status:   cli.AuthCheckStatusInvalid,
						Provider: result.Provider,
						Reason:   &invalidReason,
					}
				} else if cred == nil || *cred == "" {
					unavail := cli.AuthCheckReasonCredentialNotAvailable
					result = cli.AuthCheckResult{
						Status:   cli.AuthCheckStatusNotReady,
						Provider: result.Provider,
						Reason:   &unavail,
					}
				} else {
					credential = cred
				}
			}
		}
	}

	if command.Json {
		payload := camAuthCheckJsonOutput{
			Status:      result.Status,
			Provider:    result.Provider,
			Reason:      result.Reason,
			AuthType:    result.AuthType,
			Credentials: credential,
		}
		encoded, marshalErr := jsonx.Marshal(payload)
		if marshalErr != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", marshalErr)
			return true, 2
		}
		_, _ = fmt.Fprintln(sio.stdout, string(encoded))
	} else if credential != nil {
		_, _ = fmt.Fprintln(sio.stdout, *credential)
	} else {
		_, _ = fmt.Fprintln(sio.stdout, string(result.Status))
	}

	switch result.Status {
	case cli.AuthCheckStatusReady:
		return true, 0
	case cli.AuthCheckStatusNotReady:
		return true, 1
	default:
		return true, 2
	}
}

func camPrepareInitialMessage(parsed *core.Args, stdinContent *string, cwd string) (cli.InitialMessageResult, error) {
	if len(parsed.FileArgs) == 0 {
		return cli.BuildInitialMessage(cli.InitialMessageInput{
			Parsed:       parsed,
			StdinContent: stdinContent,
		}), nil
	}
	autoResize := false
	processed, err := cli.ProcessFileArguments(parsed.FileArgs, &cli.ProcessFileOptions{
		AutoResizeImages: &autoResize,
		Cwd:              cwd,
	})
	if err != nil {
		return cli.InitialMessageResult{}, err
	}
	fileText := processed.Text
	return cli.BuildInitialMessage(cli.InitialMessageInput{
		Parsed:       parsed,
		FileText:     &fileText,
		FileImages:   processed.Images,
		StdinContent: stdinContent,
	}), nil
}

func camResolveSessionPath(ctx context.Context, sessionArg string, cwd string, sessionDir string) (camResolvedSession, error) {
	if strings.Contains(sessionArg, "/") || strings.Contains(sessionArg, `\`) || strings.HasSuffix(sessionArg, ".jsonl") {
		resolved, err := utils.ResolvePath(sessionArg, cwd, nil)
		if err != nil {
			resolved = sessionArg
		}
		return camResolvedSession{kind: camSessionPath, path: resolved}, nil
	}

	if exact := core.SessionManagerFindById(cwd, sessionArg, sessionDir); exact != nil && *exact != "" {
		return camResolvedSession{kind: camSessionLocal, path: *exact}, nil
	}

	localSessions, err := core.SessionManagerList(ctx, cwd, sessionDir)
	if err != nil {
		return camResolvedSession{}, err
	}
	for _, s := range localSessions {
		if strings.HasPrefix(s.Id, sessionArg) {
			return camResolvedSession{kind: camSessionLocal, path: s.Path}, nil
		}
	}

	allSessions, err := core.SessionManagerListAll(ctx, sessionDir)
	if err != nil {
		return camResolvedSession{}, err
	}
	for _, s := range allSessions {
		if s.Id == sessionArg {
			return camResolvedSession{kind: camSessionGlobal, path: s.Path, cwd: s.Cwd}, nil
		}
	}
	for _, s := range allSessions {
		if strings.HasPrefix(s.Id, sessionArg) {
			return camResolvedSession{kind: camSessionGlobal, path: s.Path, cwd: s.Cwd}, nil
		}
	}

	return camResolvedSession{kind: camSessionNotFound, arg: sessionArg}, nil
}

func camPromptConfirm(in io.Reader, out io.Writer, message string) bool {
	if out != nil {
		_, _ = fmt.Fprintf(out, "%s [y/N] ", message)
	}
	if in == nil {
		return false
	}
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	ans := strings.TrimSpace(strings.ToLower(line))
	return ans == "y" || ans == "yes"
}

func camValidateForkFlags(parsed core.Args) error {
	if parsed.Fork == nil || *parsed.Fork == "" {
		return nil
	}
	var conflicting []string
	if parsed.Session != nil {
		conflicting = append(conflicting, "--session")
	}
	if parsed.Continue {
		conflicting = append(conflicting, "--continue")
	}
	if parsed.Resume {
		conflicting = append(conflicting, "--resume")
	}
	if parsed.NoSession {
		conflicting = append(conflicting, "--no-session")
	}
	if len(conflicting) > 0 {
		return fmt.Errorf("--fork cannot be combined with %s", strings.Join(conflicting, ", "))
	}
	return nil
}

func camValidateSessionIdFlags(parsed core.Args) error {
	if parsed.SessionId == nil {
		return nil
	}
	var conflicting []string
	if parsed.Session != nil {
		conflicting = append(conflicting, "--session")
	}
	if parsed.Continue {
		conflicting = append(conflicting, "--continue")
	}
	if parsed.Resume {
		conflicting = append(conflicting, "--resume")
	}
	if len(conflicting) > 0 {
		return fmt.Errorf("--session-id cannot be combined with %s", strings.Join(conflicting, ", "))
	}
	return core.AssertValidSessionId(*parsed.SessionId)
}

func camCreateSessionManager(
	ctx context.Context,
	parsed core.Args,
	cwd string,
	sessionDir string,
	agentDir string,
	sio camStartupIO,
	pickerOpts *cli.SessionPickerOptions,
) (sm *core.SessionManager, exitCode int, handled bool) {
	if parsed.NoSession || parsed.Help || parsed.ListModels != nil {
		var nsOpts *core.NewSessionOptions
		if parsed.SessionId != nil {
			nsOpts = &core.NewSessionOptions{Id: parsed.SessionId}
		}
		created, err := core.SessionManagerInMemoryWith(cwd, nsOpts, nil)
		if err != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
			return nil, 1, true
		}
		return created, 0, false
	}

	if parsed.Fork != nil && *parsed.Fork != "" {
		if parsed.SessionId != nil {
			if existing := core.SessionManagerFindById(cwd, *parsed.SessionId, sessionDir); existing != nil && *existing != "" {
				_, _ = fmt.Fprintf(sio.stderr, "Session already exists with id '%s'\n", *parsed.SessionId)
				return nil, 1, true
			}
		}
		resolved, err := camResolveSessionPath(ctx, *parsed.Fork, cwd, sessionDir)
		if err != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
			return nil, 1, true
		}
		if resolved.kind == camSessionNotFound {
			_, _ = fmt.Fprintf(sio.stderr, "No session found matching '%s'\n", resolved.arg)
			return nil, 1, true
		}
		var nsOpts *core.NewSessionOptions
		if parsed.SessionId != nil {
			nsOpts = &core.NewSessionOptions{Id: parsed.SessionId}
		}
		forked, err := core.SessionManagerForkFrom(resolved.path, cwd, sessionDir, nsOpts)
		if err != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
			return nil, 1, true
		}
		return forked, 0, false
	}

	if parsed.Session != nil && *parsed.Session != "" {
		resolved, err := camResolveSessionPath(ctx, *parsed.Session, cwd, sessionDir)
		if err != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
			return nil, 1, true
		}
		switch resolved.kind {
		case camSessionPath, camSessionLocal:
			opened, openErr := core.SessionManagerOpen(resolved.path, core.SessionOpenOptions{SessionDir: sessionDir})
			if openErr != nil {
				_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", openErr)
				return nil, 1, true
			}
			return opened, 0, false
		case camSessionGlobal:
			_, _ = fmt.Fprintf(sio.stdout, "Session found in different project: %s\n", resolved.cwd)
			if !camPromptConfirm(sio.stdin, sio.stdout, "Fork this session into current directory?") {
				_, _ = fmt.Fprintln(sio.stdout, "Aborted.")
				return nil, 0, true
			}
			forked, forkErr := core.SessionManagerForkFrom(resolved.path, cwd, sessionDir)
			if forkErr != nil {
				_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", forkErr)
				return nil, 1, true
			}
			return forked, 0, false
		case camSessionNotFound:
			_, _ = fmt.Fprintf(sio.stderr, "No session found matching '%s'\n", resolved.arg)
			return nil, 1, true
		}
	}

	if parsed.Resume {
		defer theme.StopThemeWatcher()
		currentLoader := func(onProgress core.SessionListProgress, lctx context.Context) ([]core.SessionInfo, error) {
			return core.SessionManagerList(lctx, cwd, sessionDir, onProgress)
		}
		allLoader := func(onProgress core.SessionListProgress, lctx context.Context) ([]core.SessionInfo, error) {
			return core.SessionManagerListAll(lctx, sessionDir, onProgress)
		}
		var resolvedPicker cli.SessionPickerOptions
		if pickerOpts != nil {
			resolvedPicker = *pickerOpts
		}
		if resolvedPicker.AgentDir == "" {
			resolvedPicker.AgentDir = agentDir
		}
		if resolvedPicker.RenameSession == nil {
			resolvedPicker.RenameSession = func(_ context.Context, sessionPath string, nextName *string) error {
				opened, openErr := core.SessionManagerOpen(sessionPath, core.SessionOpenOptions{SessionDir: sessionDir})
				if openErr != nil {
					return openErr
				}
				name := ""
				if nextName != nil {
					name = *nextName
				}
				_, renameErr := opened.AppendSessionInfo(name)
				return renameErr
			}
		}
		selectedPath, pickErr := cli.SelectSession(ctx, currentLoader, allLoader, &resolvedPicker)
		if pickErr != nil {
			if errors.Is(pickErr, cli.ErrSessionPickerExit) {
				_, _ = fmt.Fprintln(sio.stdout, "No session selected")
				return nil, 0, true
			}
			_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", pickErr)
			return nil, 1, true
		}
		if selectedPath == nil || *selectedPath == "" {
			_, _ = fmt.Fprintln(sio.stdout, "No session selected")
			return nil, 0, true
		}
		opened, openErr := core.SessionManagerOpen(*selectedPath, core.SessionOpenOptions{SessionDir: sessionDir})
		if openErr != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", openErr)
			return nil, 1, true
		}
		return opened, 0, false
	}

	if parsed.Continue {
		continued, err := core.SessionManagerContinueRecent(cwd, sessionDir)
		if err != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
			return nil, 1, true
		}
		return continued, 0, false
	}

	if parsed.SessionId != nil {
		if existing := core.SessionManagerFindById(cwd, *parsed.SessionId, sessionDir); existing != nil && *existing != "" {
			opened, openErr := core.SessionManagerOpen(*existing, core.SessionOpenOptions{SessionDir: sessionDir})
			if openErr != nil {
				_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", openErr)
				return nil, 1, true
			}
			return opened, 0, false
		}
		_, _ = fmt.Fprintf(sio.stderr, "Warning: No project session found with id '%s'; creating a new session with that id.\n", *parsed.SessionId)
	}

	var nsOpts *core.NewSessionOptions
	if parsed.SessionId != nil {
		nsOpts = &core.NewSessionOptions{Id: parsed.SessionId}
	}
	created, err := core.SessionManagerCreate(cwd, sessionDir, nsOpts)
	if err != nil {
		_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
		return nil, 1, true
	}
	return created, 0, false
}

func camBuildSessionOptions(
	parsed core.Args,
	scopedModels []core.ScopedModel,
	hasExistingSession bool,
	modelRuntime *core.ModelRuntime,
	settingsManager *core.SettingsManager,
) camBuildSessionOptionsResult {
	var res camBuildSessionOptionsResult

	if parsed.Provider != nil && *parsed.Provider != "" && (parsed.Model == nil || *parsed.Model == "") {
		res.diagnostics = append(res.diagnostics, core.AgentSessionRuntimeDiagnostic{
			Type:    core.AgentSessionRuntimeDiagnosticError,
			Message: fmt.Sprintf("--provider requires --model (for example: --provider %s --model <pattern>)", *parsed.Provider),
		})
	}

	if parsed.Model != nil && *parsed.Model != "" {
		resolved := core.ResolveCliModel(core.ResolveCliModelOptions{
			CliProvider:  parsed.Provider,
			CliModel:     parsed.Model,
			CliThinking:  parsed.Thinking,
			InlineApiKey: parsed.ApiKey != nil && *parsed.ApiKey != "",
			ModelRuntime: modelRuntime,
		})
		if resolved.Warning != nil && *resolved.Warning != "" {
			res.diagnostics = append(res.diagnostics, core.AgentSessionRuntimeDiagnostic{
				Type:    core.AgentSessionRuntimeDiagnosticWarning,
				Message: *resolved.Warning,
			})
		}
		if resolved.Error != nil && *resolved.Error != "" {
			res.diagnostics = append(res.diagnostics, core.AgentSessionRuntimeDiagnostic{
				Type:    core.AgentSessionRuntimeDiagnosticError,
				Message: *resolved.Error,
			})
		}
		if resolved.Model != nil {
			res.model = resolved.Model
			if parsed.Thinking == nil && resolved.ThinkingLevel != nil {
				res.thinkingLevel = *resolved.ThinkingLevel
				res.cliThinkingFromModel = true
			}
		}
	}

	if res.model == nil && len(scopedModels) > 0 && !hasExistingSession {
		savedProvider := settingsManager.GetDefaultProvider()
		savedModelId := settingsManager.GetDefaultModel()
		var savedModel *ai.Model
		if savedProvider != nil && *savedProvider != "" && savedModelId != nil && *savedModelId != "" {
			savedModel = modelRuntime.GetModel(*savedProvider, *savedModelId)
		}
		var savedInScope *core.ScopedModel
		if savedModel != nil {
			for i := range scopedModels {
				if ai.ModelsAreEqual(scopedModels[i].Model, savedModel) {
					savedInScope = &scopedModels[i]
					break
				}
			}
		}
		if savedInScope != nil {
			res.model = savedInScope.Model
			if parsed.Thinking == nil && savedInScope.ThinkingLevel != nil {
				res.thinkingLevel = *savedInScope.ThinkingLevel
			}
		} else {
			res.model = scopedModels[0].Model
			if parsed.Thinking == nil && scopedModels[0].ThinkingLevel != nil {
				res.thinkingLevel = *scopedModels[0].ThinkingLevel
			}
		}
	}

	if parsed.Thinking != nil {
		res.thinkingLevel = *parsed.Thinking
	}

	if len(scopedModels) > 0 {
		res.scopedModels = make([]core.ScopedModel, len(scopedModels))
		for i, sm := range scopedModels {
			res.scopedModels[i] = core.ScopedModel{
				Model:         sm.Model,
				ThinkingLevel: sm.ThinkingLevel,
			}
		}
	}

	if parsed.NoTools {
		res.noTools = core.NoToolsAll
	} else if parsed.NoBuiltinTools {
		res.noTools = core.NoToolsBuiltin
	}
	if len(parsed.Tools) > 0 {
		res.toolNames = slices.Clone(parsed.Tools)
	}
	if len(parsed.ExcludeTools) > 0 {
		res.excludeTools = slices.Clone(parsed.ExcludeTools)
	}

	return res
}

func camResolveCliPaths(cwd string, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, len(paths))
	for i, value := range paths {
		if utils.IsLocalPath(value) {
			if resolved, err := utils.ResolvePath(value, cwd, nil); err == nil {
				out[i] = resolved
				continue
			}
		}
		out[i] = value
	}
	return out
}

func camUnknownFlagsToMap(m *core.Args) map[string]any {
	if m == nil || m.UnknownFlags == nil || m.UnknownFlags.Len() == 0 {
		return nil
	}
	out := make(map[string]any, m.UnknownFlags.Len())
	for k, v := range m.UnknownFlags.All() {
		if v.IsString {
			out[k] = v.String
		} else {
			out[k] = v.Bool
		}
	}
	return out
}

func camCollectExtensionFlags(resourceLoader core.ResourceLoader) []core.ExtensionFlag {
	if resourceLoader == nil {
		return nil
	}
	exts := resourceLoader.GetExtensions()
	var flags []core.ExtensionFlag
	for _, ext := range exts.Extensions {
		if len(ext.Flags) == 0 {
			continue
		}
		names := make([]string, 0, len(ext.Flags))
		for name := range ext.Flags {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			flags = append(flags, ext.Flags[name])
		}
	}
	return flags
}

func camConvertImagesToValues(ptrs []*ai.ImageContent) []ai.ImageContent {
	if len(ptrs) == 0 {
		return nil
	}
	out := make([]ai.ImageContent, 0, len(ptrs))
	for _, img := range ptrs {
		if img != nil {
			out = append(out, *img)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func camBoolPtr(v bool) *bool {
	return &v
}

// Main runs the Pi coding agent CLI entrypoint and returns the process exit code.
// cmd/pi passes no options (or &MainOptions{}); cmd/pi-gui passes &MainOptions{DefaultGuiMode: true}.
func Main(ctx context.Context, args []string, opts ...*MainOptions) int {
	if ctx == nil {
		ctx = context.Background()
	}
	var opt *MainOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	sio := camResolveStartupIO(opt)

	core.ResetTimings()
	core.Time("start")
	theme.SetThemeJsonValidator(theme.ValidateThemeJson)

	cli.SetupCli()

	offlineMode := slices.Contains(args, "--offline") || core.IsOfflineModeEnabled()
	if offlineMode {
		_ = os.Setenv("PI_OFFLINE", "1")
		_ = os.Setenv("PI_SKIP_VERSION_CHECK", "1")
	}

	if handled, code := camRunAuthCommand(ctx, args, sio, opt); handled {
		return code
	}

	var cwd string
	if opt != nil && strings.TrimSpace(opt.Cwd) != "" {
		cwd = opt.Cwd
	} else {
		wd, err := os.Getwd()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				_, _ = fmt.Fprintln(sio.stderr, "Error: The current working directory no longer exists. Change to an existing directory and try again.")
				return 1
			}
			_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
			return 1
		}
		cwd = wd
	}

	agentDir := utils.GetAgentDir()
	if opt != nil && strings.TrimSpace(opt.AgentDir) != "" {
		agentDir = opt.AgentDir
	}

	bootstrapSettingsManager := core.SettingsManagerCreateWithOptions(cwd, agentDir, &core.SettingsManagerCreateOptions{
		ProjectTrusted: camBoolPtr(false),
	})
	core.ApplyHttpProxySettings(bootstrapSettingsManager.GetGlobalSettings().HttpProxy)
	if timeoutMs, err := bootstrapSettingsManager.GetHttpIdleTimeoutMs(); err == nil {
		_ = core.ConfigureHttpDispatcher(timeoutMs)
	} else {
		_ = core.ConfigureHttpDispatcher()
	}

	var extFactories []core.InlineExtension
	if opt != nil && len(opt.ExtensionFactories) > 0 {
		extFactories = slices.Clone(opt.ExtensionFactories)
	}

	pkgOpts := &PackageCommandOptions{
		Cwd:                cwd,
		AgentDir:           agentDir,
		Stdout:             sio.stdout,
		Stderr:             sio.stderr,
		ExtensionFactories: extFactories,
	}
	if opt != nil && opt.StartupUiOptions != nil {
		pkgOpts.StartupUiOptions = opt.StartupUiOptions
	}
	if handled, code := HandlePackageCommand(ctx, args, pkgOpts); handled {
		return code
	}
	if handled, code := HandleConfigCommand(ctx, args, pkgOpts); handled {
		return code
	}
	if len(args) > 0 && args[0] == "mcp" {
		interactive := sio.stdinIsTTY
		return mcpext.RunMcpCli(args[1:], mcpext.McpCommandOptions{
			Cwd:         cwd,
			AgentDir:    agentDir,
			Interactive: &interactive,
			Input:       sio.stdin,
			Context:     ctx,
			Log: func(line string) {
				_, _ = fmt.Fprintln(sio.stdout, line)
			},
			Error: func(line string) {
				_, _ = fmt.Fprintln(sio.stderr, line)
			},
		})
	}

	parsed := core.ParseArgs(args)
	if len(parsed.Diagnostics) > 0 {
		hasArgError := false
		for _, d := range parsed.Diagnostics {
			label := "Warning"
			if d.Type == "error" {
				label = "Error"
				hasArgError = true
			}
			_, _ = fmt.Fprintf(sio.stderr, "%s: %s\n", label, d.Message)
		}
		if hasArgError {
			return 1
		}
	}
	core.Time("parseArgs")

	if parsed.Version {
		_, _ = fmt.Fprintln(sio.stdout, utils.VERSION)
		return 0
	}

	if parsed.Export != nil {
		var exportOpts *core.ExportOptions
		if len(parsed.Messages) > 0 {
			outPath := parsed.Messages[0]
			exportOpts = &core.ExportOptions{OutputPath: &outPath}
		}
		exportedPath, err := core.ExportFromFile(*parsed.Export, exportOpts)
		if err != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %s\n", err.Error())
			return 1
		}
		_, _ = fmt.Fprintf(sio.stdout, "Exported to: %s\n", exportedPath)
		return 0
	}

	defaultGuiMode := opt != nil && opt.DefaultGuiMode
	explicitGuiFlag := camHasExplicitGuiFlag(parsed)
	appMode := camResolveAppMode(parsed, defaultGuiMode, sio.stdinIsTTY, sio.stdoutIsTTY)

	takenOverStdout := false
	takeOverIfNeeded := func() {
		if !takenOverStdout {
			core.TakeOverStdout()
			takenOverStdout = true
		}
	}
	defer func() {
		if takenOverStdout {
			core.RestoreStdout()
		}
	}()

	shouldTakeOverStdout := appMode != camAppModeInteractive && appMode != camAppModeGui && !camIsPlainRuntimeMetadataCommand(parsed)
	if shouldTakeOverStdout {
		takeOverIfNeeded()
	}

	if parsed.Mode != nil && *parsed.Mode == core.ModeRpc && len(parsed.FileArgs) > 0 {
		_, _ = fmt.Fprintln(sio.stderr, "Error: @file arguments are not supported in RPC mode")
		return 1
	}

	if err := camValidateForkFlags(parsed); err != nil {
		_, _ = fmt.Fprintf(sio.stderr, "Error: %s\n", err.Error())
		return 1
	}
	if err := camValidateSessionIdFlags(parsed); err != nil {
		_, _ = fmt.Fprintf(sio.stderr, "Error: %s\n", err.Error())
		return 1
	}

	migOut := sio.stdout
	if shouldTakeOverStdout {
		migOut = sio.stderr
	}
	migRes := RunMigrationsWithOutput(cwd, migOut)
	core.Time("runMigrations")

	startupSettingsManager := core.SettingsManagerCreate(cwd, agentDir)
	startupSettingsDiagnostics := core.CollectSettingsDiagnostics(startupSettingsManager)

	var startupUiOpts cli.StartupUiOptions
	if opt != nil && opt.StartupUiOptions != nil {
		startupUiOpts = *opt.StartupUiOptions
	}
	if startupUiOpts.AgentDir == "" {
		startupUiOpts.AgentDir = agentDir
	}

	canRunTerminalUI := (sio.stdinIsTTY && camFileIsTTY(sio.stdin) && camFileIsTTY(sio.stdout)) || startupUiOpts.Terminal != nil || startupUiOpts.UI != nil
	firstTimeInteractive := appMode == camAppModeInteractive && canRunTerminalUI
	if !parsed.Help && parsed.ListModels == nil && cli.ShouldRunFirstTimeSetup(startupSettingsManager, agentDir, parsed.UseTheme, firstTimeInteractive) {
		_, _ = cli.RunFirstTimeSetup(ctx, startupSettingsManager, &startupUiOpts)
		core.Time("firstTimeSetup")
	}

	if (appMode == camAppModeInteractive || appMode == camAppModeGui) && parsed.UseTheme != nil {
		startupSettingsManager.ApplyOverrides(&core.Settings{
			Theme: parsed.UseTheme,
		})
	}

	sessionDir := ""
	if opt != nil && opt.SessionDir != nil {
		sessionDir = *opt.SessionDir
	} else if parsed.SessionDir != nil && *parsed.SessionDir != "" {
		if norm, err := utils.NormalizePath(*parsed.SessionDir, nil); err == nil {
			sessionDir = norm
		} else {
			sessionDir = *parsed.SessionDir
		}
	} else if envSessionDir := os.Getenv(utils.EnvSessionDir); envSessionDir != "" {
		sessionDir = utils.ExpandTildePath(envSessionDir)
	} else if configuredDir := startupSettingsManager.GetSessionDir(); configuredDir != nil {
		sessionDir = *configuredDir
	}

	var pickerOpts *cli.SessionPickerOptions
	if opt != nil {
		pickerOpts = opt.SessionPickerOptions
	}
	sessionManager, code, handled := camCreateSessionManager(ctx, parsed, cwd, sessionDir, agentDir, sio, pickerOpts)
	if handled {
		return code
	}

	if issue := core.GetMissingSessionCwdIssue(sessionManager, cwd); issue != nil {
		if (appMode == camAppModeInteractive || appMode == camAppModeGui) && canRunTerminalUI {
			sessionFile := ""
			if issue.SessionFile != nil {
				sessionFile = *issue.SessionFile
			}
			optsList := core.GetMissingSessionCwdOptions(*issue)
			labels := make([]string, len(optsList))
			for i, o := range optsList {
				labels[i] = o.Label
			}
			selectedLabel, selErr := cli.ShowStartupSelect(ctx, core.FormatMissingSessionCwdPrompt(*issue), labels, nil, &startupUiOpts)
			if selErr != nil {
				_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", selErr)
				return 1
			}
			var choice *core.MissingSessionCwdChoice
			if selectedLabel != nil {
				for _, o := range optsList {
					if o.Label == *selectedLabel {
						choice = o.Value
						break
					}
				}
			}
			if choice == nil {
				return 0
			}
			switch *choice {
			case core.MissingSessionCwdChoiceContinue:
				reopened, openErr := core.SessionManagerOpen(sessionFile, core.SessionOpenOptions{
					SessionDir:  sessionDir,
					CwdOverride: &cwd,
				})
				if openErr != nil {
					_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", openErr)
					return 1
				}
				sessionManager = reopened
			case core.MissingSessionCwdChoiceFork:
				forked, forkErr := core.SessionManagerForkFrom(sessionFile, cwd, sessionDir)
				if forkErr != nil {
					_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", forkErr)
					return 1
				}
				sessionManager = forked
			}
		} else {
			_, _ = fmt.Fprintln(sio.stderr, core.FormatMissingSessionCwdError(*issue))
			return 1
		}
	}

	if parsed.Name != nil {
		normalized := core.NormalizeSessionName(*parsed.Name)
		if normalized == nil {
			_, _ = fmt.Fprintln(sio.stderr, "Error: --name requires a non-empty value")
			return 1
		}
		_, _ = sessionManager.AppendSessionInfo(*normalized)
	}
	core.Time("createSessionManager")

	trustStore := core.NewProjectTrustStore(agentDir)
	sessionCwd := sessionManager.GetCwd()
	var autoTrustOnReloadCwd *string
	if parsed.ProjectTrustOverride == nil && !core.HasTrustRequiringProjectResources(sessionCwd) {
		autoTrustOnReloadCwd = &sessionCwd
	}

	trustPromptMode := appMode
	if parsed.Help || parsed.ListModels != nil {
		trustPromptMode = camAppModePrint
	}
	projectTrustByCwd := make(map[string]bool)

	resolvedExtensionPaths := camResolveCliPaths(cwd, parsed.Extensions)
	resolvedSkillPaths := camResolveCliPaths(cwd, parsed.Skills)
	resolvedPromptTemplatePaths := camResolveCliPaths(cwd, parsed.PromptTemplates)
	resolvedThemePaths := camResolveCliPaths(cwd, parsed.Themes)
	flagValues := camUnknownFlagsToMap(&parsed)

	var disabledBuiltin []string
	if parsed.NoMcp {
		disabledBuiltin = []string{"mcp"}
	}

	createRuntime := func(rtCtx context.Context, rtArgs core.AgentSessionRuntimeCreateArgs) (*core.CreateAgentSessionRuntimeResult, error) {
		isInitialRuntime := rtArgs.SessionStartEvent == nil
		var projectTrustDiagnostics []core.AgentSessionRuntimeDiagnostic

		cachedProjectTrust, hasCached := projectTrustByCwd[rtArgs.Cwd]
		hasTrustResources := core.HasTrustRequiringProjectResources(rtArgs.Cwd)
		shouldResolveTrust := parsed.ProjectTrustOverride == nil && !hasCached && hasTrustResources

		var projectTrusted bool
		if hasCached {
			projectTrusted = cachedProjectTrust
		} else if parsed.ProjectTrustOverride != nil {
			projectTrusted = *parsed.ProjectTrustOverride
		} else if !hasTrustResources {
			projectTrusted = true
		} else if storedDecision, getErr := trustStore.Get(rtArgs.Cwd); getErr == nil && storedDecision != nil {
			projectTrusted = *storedDecision
		}

		runtimeSettingsManager := core.SettingsManagerCreateWithOptions(rtArgs.Cwd, rtArgs.AgentDir, &core.SettingsManagerCreateOptions{
			ProjectTrusted: &projectTrusted,
		})

		if shouldResolveTrust {
			activeTrustMode := appMode
			if isInitialRuntime {
				activeTrustMode = trustPromptMode
			}
			hasTrustUI := isInitialRuntime && (activeTrustMode == camAppModeInteractive || (activeTrustMode == camAppModeGui && sio.stdinIsTTY && sio.stdoutIsTTY))
			trustUiOpts := startupUiOpts
			trustUiOpts.AgentDir = rtArgs.AgentDir
			resolvedTrust, trustErr := cli.ResolveStartupProjectTrust(rtCtx, cli.PromptProjectTrustOptions{
				Cwd:             rtArgs.Cwd,
				AgentDir:        rtArgs.AgentDir,
				SettingsManager: runtimeSettingsManager,
				TrustOverride:   parsed.ProjectTrustOverride,
				Mode:            camToTrustExtensionMode(activeTrustMode),
				HasUI:           hasTrustUI,
				ErrOut:          sio.stderr,
				UiOptions:       &trustUiOpts,
				OnExtensionError: func(message string) {
					projectTrustDiagnostics = append(projectTrustDiagnostics, core.AgentSessionRuntimeDiagnostic{
						Type:    core.AgentSessionRuntimeDiagnosticWarning,
						Message: message,
					})
				},
			})
			if trustErr == nil {
				projectTrusted = resolvedTrust
				projectTrustByCwd[rtArgs.Cwd] = resolvedTrust
				runtimeSettingsManager.SetProjectTrusted(resolvedTrust)
			}
		}

		svcOpts := &core.CreateAgentSessionServicesOptions{
			Cwd:                           rtArgs.Cwd,
			AgentDir:                      rtArgs.AgentDir,
			SettingsManager:               runtimeSettingsManager,
			FlagValues:                    flagValues,
			AdditionalExtensionPaths:      resolvedExtensionPaths,
			AdditionalSkillPaths:          resolvedSkillPaths,
			AdditionalPromptTemplatePaths: resolvedPromptTemplatePaths,
			AdditionalThemePaths:          resolvedThemePaths,
			NoExtensions:                  parsed.NoExtensions,
			DisabledBuiltinExtensions:     disabledBuiltin,
			NoSkills:                      parsed.NoSkills,
			NoPromptTemplates:             parsed.NoPromptTemplates,
			NoThemes:                      parsed.NoThemes,
			NoContextFiles:                parsed.NoContextFiles,
			SystemPrompt:                  parsed.SystemPrompt,
			AppendSystemPrompt:            parsed.AppendSystemPrompt,
		}
		if len(extFactories) > 0 {
			svcOpts.ResourceLoaderOptions = &core.DefaultResourceLoaderOptions{
				ExtensionFactories: slices.Clone(extFactories),
			}
		}
		if opt != nil && (opt.AuthPath != nil || opt.ModelsPath != nil) {
			mrCtx, mrCancel := context.WithTimeout(rtCtx, 15*time.Second)
			mr, mrErr := core.CreateModelRuntime(mrCtx, camModelRuntimeOptions(opt, rtArgs.AgentDir, false, !offlineMode))
			mrCancel()
			if mrErr != nil {
				return nil, mrErr
			}
			svcOpts.ModelRuntime = mr
			svcOpts.AuthStorage = camResolveWritableAuthStorage(opt, rtArgs.AgentDir)
		}

		services, svcErr := core.CreateAgentSessionServices(rtCtx, svcOpts)
		if svcErr != nil {
			return nil, svcErr
		}

		var diagnostics []core.AgentSessionRuntimeDiagnostic
		diagnostics = append(diagnostics, projectTrustDiagnostics...)
		diagnostics = append(diagnostics, services.Diagnostics...)

		modelPatterns := parsed.Models
		if len(modelPatterns) == 0 {
			modelPatterns = services.SettingsManager.GetEnabledModels()
		}
		var scopedModels []core.ScopedModel
		if len(modelPatterns) > 0 {
			scopeCtx, scopeCancel := context.WithTimeout(rtCtx, 15*time.Second)
			scopeRes, scopeErr := core.ResolveModelScopeWithDiagnostics(scopeCtx, modelPatterns, services.ModelRuntime)
			scopeCancel()
			if scopeErr != nil {
				diagnostics = append(diagnostics, core.AgentSessionRuntimeDiagnostic{
					Type:    core.AgentSessionRuntimeDiagnosticWarning,
					Message: scopeErr.Error(),
				})
			} else {
				scopedModels = scopeRes.ScopedModels
				for _, sd := range scopeRes.Diagnostics {
					typ := core.AgentSessionRuntimeDiagnosticWarning
					if sd.Type == core.AgentSessionRuntimeDiagnosticError {
						typ = core.AgentSessionRuntimeDiagnosticError
					}
					diagnostics = append(diagnostics, core.AgentSessionRuntimeDiagnostic{
						Type:    typ,
						Message: sd.Message,
					})
				}
			}
		}

		hasExistingMessages := len(rtArgs.SessionManager.BuildSessionContext().Messages) > 0
		sessionOpts := camBuildSessionOptions(parsed, scopedModels, hasExistingMessages, services.ModelRuntime, services.SettingsManager)
		diagnostics = append(diagnostics, sessionOpts.diagnostics...)

		if parsed.ApiKey != nil && *parsed.ApiKey != "" {
			if sessionOpts.model == nil {
				diagnostics = append(diagnostics, core.AgentSessionRuntimeDiagnostic{
					Type:    core.AgentSessionRuntimeDiagnosticError,
					Message: "--api-key requires a model to be specified via --model, --provider/--model, or --models",
				})
			} else if keyErr := services.ModelRuntime.SetRuntimeApiKey(rtCtx, string(sessionOpts.model.Provider), *parsed.ApiKey); keyErr != nil {
				diagnostics = append(diagnostics, core.AgentSessionRuntimeDiagnostic{
					Type:    core.AgentSessionRuntimeDiagnosticError,
					Message: keyErr.Error(),
				})
			}
		}

		created, createErr := core.CreateAgentSessionFromServices(rtCtx, core.CreateAgentSessionFromServicesOptions{
			Services:          services,
			SessionManager:    rtArgs.SessionManager,
			SessionStartEvent: rtArgs.SessionStartEvent,
			Model:             sessionOpts.model,
			ThinkingLevel:     sessionOpts.thinkingLevel,
			ScopedModels:      sessionOpts.scopedModels,
			ToolNames:         sessionOpts.toolNames,
			ExcludeTools:      sessionOpts.excludeTools,
			NoTools:           sessionOpts.noTools,
			FlagValues:        flagValues,
		})
		if createErr != nil {
			return nil, createErr
		}

		// agent.NewAgent substitutes agentDefaultModel() (google/gemini-2.5-flash-lite-preview-06-17)
		// when InitialState.Model is nil; clear it when neither explicit CLI selection nor
		// available runtime models produced an initial model (TS parity: session.model == undefined).
		if sessionOpts.model == nil && created.Session != nil && created.Session.Agent != nil {
			if m := created.Session.Model(); m != nil && m.Provider == ai.KnownProviderGoogle && m.Id == "gemini-2.5-flash-lite-preview-06-17" &&
				len(services.ModelRuntime.GetAvailableSnapshot()) == 0 && !services.ModelRuntime.HasConfiguredAuth(string(m.Provider)) {
				created.Session.Agent.SetModel(nil)
			}
		}

		cliThinkingOverride := parsed.Thinking != nil || sessionOpts.cliThinkingFromModel
		if created.Session != nil && created.Session.Model() != nil && cliThinkingOverride {
			created.Session.SetThinkingLevel(created.Session.ThinkingLevel())
		}

		return &core.CreateAgentSessionRuntimeResult{
			CreateAgentSessionResult: *created,
			Services:                 services,
			Diagnostics:              diagnostics,
		}, nil
	}

	core.Time("createRuntime")
	runtime, rtErr := core.CreateAgentSessionRuntime(ctx, createRuntime, core.CreateAgentSessionRuntimeOptions{
		Cwd:            sessionManager.GetCwd(),
		AgentDir:       agentDir,
		SessionManager: sessionManager,
	})
	if rtErr != nil {
		_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", rtErr)
		return 1
	}
	defer func() {
		_ = runtime.Dispose(context.Background())
		theme.StopThemeWatcher()
	}()
	core.Time("createAgentSessionRuntime")

	services := runtime.Services
	settingsManager := services.SettingsManager
	modelRuntime := services.ModelRuntime
	resourceLoader := services.ResourceLoader

	capOverrides := settingsManager.GetTerminalCapabilityOverrides()
	tui.SetCapabilityOverrides(&capOverrides)
	core.ApplyHttpProxySettings(settingsManager.GetGlobalSettings().HttpProxy)
	if timeoutMs, err := settingsManager.GetHttpIdleTimeoutMs(); err == nil {
		_ = core.ConfigureHttpDispatcher(timeoutMs)
	}

	mergedDiags := make([]core.AgentSessionRuntimeDiagnostic, 0, len(startupSettingsDiagnostics)+len(runtime.Diagnostics))
	mergedDiags = append(mergedDiags, startupSettingsDiagnostics...)
	mergedDiags = append(mergedDiags, runtime.Diagnostics...)
	startupDiagnostics := core.DeduplicateDiagnostics(mergedDiags)

	hasRuntimeErrors := false
	for _, d := range runtime.Diagnostics {
		if d.Type == core.AgentSessionRuntimeDiagnosticError {
			hasRuntimeErrors = true
			break
		}
	}

	if parsed.Help {
		camReportDiagnostics(sio.stderr, startupDiagnostics)
		extFlags := camCollectExtensionFlags(resourceLoader)
		_, _ = fmt.Fprintln(sio.stdout, core.FormatHelp(extFlags...))
		return 0
	}

	if parsed.ListModels != nil {
		camReportDiagnostics(sio.stderr, startupDiagnostics)
		var searchPattern *string
		if parsed.ListModels.Search != "" {
			s := parsed.ListModels.Search
			searchPattern = &s
		}
		lmCtx, lmCancel := context.WithTimeout(ctx, 15*time.Second)
		defer lmCancel()
		if err := cli.ListModelsTo(lmCtx, sio.stdout, sio.stderr, modelRuntime, searchPattern); err != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
			return 1
		}
		return 0
	}

	var stdinContent *string
	if appMode != camAppModeRpc {
		stdinContent = camReadPipedStdin(sio.stdin, sio.stdinIsTTY)
		if stdinContent != nil {
			if appMode == camAppModeInteractive || (appMode == camAppModeGui && !explicitGuiFlag) {
				appMode = camAppModePrint
				if !camIsPlainRuntimeMetadataCommand(parsed) {
					takeOverIfNeeded()
				}
			}
		}
	}
	core.Time("readPipedStdin")

	initMsg, initErr := camPrepareInitialMessage(&parsed, stdinContent, cwd)
	if initErr != nil {
		_, _ = fmt.Fprintf(sio.stderr, "Error: %s\n", initErr.Error())
		return 1
	}
	core.Time("prepareInitialMessage")

	themeName := ""
	if t := settingsManager.GetTheme(); t != nil {
		themeName = *t
	}
	_, _ = theme.InitTheme(themeName, appMode == camAppModeInteractive)
	core.Time("initTheme")

	if len(migRes.DeprecationWarnings) > 0 {
		if appMode == camAppModeInteractive {
			if opt != nil && opt.ShowDeprecationWarnings != nil {
				opt.ShowDeprecationWarnings(migRes.DeprecationWarnings, sio.stdout)
			} else if inFile, ok := sio.stdin.(*os.File); ok {
				ShowDeprecationWarningsWithIO(migRes.DeprecationWarnings, inFile, sio.stdout)
			} else {
				ShowDeprecationWarningsWithIO(migRes.DeprecationWarnings, nil, sio.stdout)
			}
		} else if appMode == camAppModeGui {
			for _, w := range migRes.DeprecationWarnings {
				startupDiagnostics = append(startupDiagnostics, core.AgentSessionRuntimeDiagnostic{
					Type:    core.AgentSessionRuntimeDiagnosticWarning,
					Message: w,
				})
			}
			startupDiagnostics = core.DeduplicateDiagnostics(startupDiagnostics)
		}
	}

	hasInteractiveUI := appMode == camAppModeInteractive || appMode == camAppModeGui
	core.Time("resolveModelScope")
	if !hasInteractiveUI || hasRuntimeErrors {
		camReportDiagnostics(sio.stderr, startupDiagnostics)
	}
	if hasRuntimeErrors {
		for _, d := range runtime.Diagnostics {
			if strings.Contains(d.Message, "Failed to load extension") {
				_, _ = fmt.Fprintln(sio.stderr, camExtensionLoadFailureHint())
				break
			}
		}
		return 1
	}
	core.Time("createAgentSession")

	if !hasInteractiveUI && (runtime.Session == nil || runtime.Session.Model() == nil) {
		_, _ = fmt.Fprintln(sio.stderr, core.FormatNoModelsAvailableMessage())
		return 1
	}

	startupBenchmark := camIsTruthyEnvFlag(os.Getenv("PI_STARTUP_BENCHMARK"))
	if startupBenchmark && appMode != camAppModeInteractive {
		_, _ = fmt.Fprintln(sio.stderr, "Error: PI_STARTUP_BENCHMARK only supports interactive mode")
		return 1
	}

	if !offlineMode && appMode == camAppModeRpc {
		go func() {
			refreshCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			_, _ = modelRuntime.Refresh(refreshCtx, nil)
		}()
	}

	valueImages := camConvertImagesToValues(initMsg.InitialImages)

	switch appMode {
	case camAppModeRpc:
		core.PrintTimings()
		runRpc := func(c context.Context, rt *core.AgentSessionRuntime, ro *rpc.RpcModeOptions) error {
			return rpc.RunRpcMode(c, rt, ro)
		}
		if opt != nil && opt.RunRpcMode != nil {
			runRpc = opt.RunRpcMode
		}
		rpcOpts := &rpc.RpcModeOptions{
			Stdin:  sio.stdin,
			Stdout: sio.stdout,
			Stderr: sio.stderr,
		}
		if err := runRpc(ctx, runtime, rpcOpts); err != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
			return 1
		}
		return 0

	case camAppModeGui:
		core.PrintTimings()
		runGui := gui.RunGuiMode
		if opt != nil && opt.RunGuiMode != nil {
			runGui = opt.RunGuiMode
		}
		err := runGui(ctx, runtime, &gui.GuiModeOptions{
			MigratedProviders:    migRes.MigratedAuthProviders,
			StartupDiagnostics:   startupDiagnostics,
			ModelFallbackMessage: runtime.ModelFallbackMessage,
			AutoTrustOnReloadCwd: autoTrustOnReloadCwd,
			InitialMessage:       initMsg.InitialMessage,
			InitialImages:        valueImages,
			InitialMessages:      parsed.Messages,
			Verbose:              parsed.Verbose,
			InitialThemeSetting:  parsed.UseTheme,
		})
		if err != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
			return 1
		}
		return 0

	case camAppModeInteractive:
		imOpts := &interactive.InteractiveModeOptions{
			MigratedProviders:    migRes.MigratedAuthProviders,
			StartupDiagnostics:   startupDiagnostics,
			ModelFallbackMessage: runtime.ModelFallbackMessage,
			AutoTrustOnReloadCwd: autoTrustOnReloadCwd,
			InitialMessage:       initMsg.InitialMessage,
			InitialImages:        valueImages,
			InitialMessages:      parsed.Messages,
			Verbose:              parsed.Verbose,
			TuiMode:              parsed.TuiMode,
			InitialThemeSetting:  parsed.UseTheme,
		}
		if opt != nil {
			if opt.Stdin != nil {
				imOpts.Stdin = opt.Stdin
			}
			if opt.Stdout != nil {
				imOpts.Stdout = opt.Stdout
			}
			if opt.Stderr != nil {
				imOpts.Stderr = opt.Stderr
			}
		}

		if startupBenchmark {
			im := interactive.NewInteractiveMode(runtime, imOpts)
			if err := im.Init(ctx); err != nil {
				_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
				return 1
			}
			core.Time("interactiveMode.init")
			time.Sleep(150 * time.Millisecond)
			im.Stop()
			_ = runtime.Dispose(ctx)
			theme.StopThemeWatcher()
			core.PrintTimings()
			return 0
		}

		core.PrintTimings()
		runInteractive := interactive.RunInteractiveMode
		if opt != nil && opt.RunInteractiveMode != nil {
			runInteractive = opt.RunInteractiveMode
		}
		if err := runInteractive(ctx, runtime, imOpts); err != nil {
			_, _ = fmt.Fprintf(sio.stderr, "Error: %v\n", err)
			return 1
		}
		return 0

	default:
		core.PrintTimings()
		runPrint := modes.RunPrintMode
		if opt != nil && opt.RunPrintMode != nil {
			runPrint = opt.RunPrintMode
		}
		printOpts := &modes.PrintModeOptions{
			Mode:           camToPrintOutputMode(appMode),
			Messages:       parsed.Messages,
			InitialMessage: initMsg.InitialMessage,
			InitialImages:  initMsg.InitialImages,
			Stdout:         sio.stdout,
			Stderr:         sio.stderr,
		}
		return runPrint(ctx, runtime, printOpts)
	}
}
