// Ported from packages/coding-agent/src/core/bug-report.ts (pi v1.5.2).

package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// BugReportCustomEntryType identifies persisted bug-report metadata entries in a session JSONL file.
const BugReportCustomEntryType = "pi.bug-report"

const (
	brSchemaVersion = 1
	brRedacted      = "<redacted>"
)

var (
	brSensitiveKeyRegex = regexp.MustCompile(`(?i)(?:^|[-_])(api[-_]?key|secret|token|password|passwd|credential|authorization|cookie)(?:$|[-_])`)
	brCamelSplitRegex   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	brNestedUrlRegex    = regexp.MustCompile(`(?i)^([a-z][a-z0-9+.-]*:)([a-z][a-z0-9+.-]*://.*)$`)
)

func brIsSensitiveKey(key string) bool {
	snake := brCamelSplitRegex.ReplaceAllString(key, "${1}_${2}")
	return brSensitiveKeyRegex.MatchString(snake)
}

// RedactUrl strips credentials and secret-looking query parameters from a URL string.
func RedactUrl(value string) string {
	if nested := brNestedUrlRegex.FindStringSubmatch(value); nested != nil {
		return nested[1] + RedactUrl(nested[2])
	}
	if !strings.Contains(value, ":") {
		return value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return value
	}
	// Reject Windows-style drive letters or plain "host:port" strings that aren't valid URLs with schemes.
	if len(parsed.Scheme) == 1 && !strings.Contains(value, "://") {
		return value
	}

	changed := false
	if parsed.User != nil {
		parsed.User = nil
		changed = true
	}
	if parsed.RawQuery != "" {
		q := parsed.Query()
		for k := range q {
			if brIsSensitiveKey(k) {
				q.Set(k, brRedacted)
				changed = true
			}
		}
		if changed {
			parsed.RawQuery = q.Encode()
		}
	}
	if !changed {
		return value
	}
	return parsed.String()
}

func brRedactAny(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return RedactUrl(v)
	case *jsonx.Object:
		if v == nil {
			return nil
		}
		out := jsonx.NewObject()
		for k, child := range v.All() {
			if child != nil && brIsSensitiveKey(k) {
				out.Set(k, brRedacted)
			} else {
				out.Set(k, brRedactAny(child))
			}
		}
		return out
	case jsonx.Object:
		out := jsonx.NewObject()
		for k, child := range v.All() {
			if child != nil && brIsSensitiveKey(k) {
				out.Set(k, brRedacted)
			} else {
				out.Set(k, brRedactAny(child))
			}
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := jsonx.NewObject()
		for _, k := range keys {
			child := v[k]
			if child != nil && brIsSensitiveKey(k) {
				out.Set(k, brRedacted)
			} else {
				out.Set(k, brRedactAny(child))
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = brRedactAny(item)
		}
		return out
	default:
		return v
	}
}

// RedactJsonValue returns a deep copy of value with sensitive keys masked and URLs sanitized.
func RedactJsonValue(value any) any {
	if value == nil {
		return nil
	}
	raw, err := jsonx.Marshal(value)
	if err != nil {
		return nil
	}
	var decoded any
	if err := jsonx.Unmarshal(raw, &decoded); err != nil {
		return nil
	}
	return brRedactAny(decoded)
}

func brRedactSettings(settings Settings) Settings {
	raw, err := jsonx.Marshal(settings)
	if err != nil {
		return settings
	}
	var obj jsonx.Object
	if err := jsonx.Unmarshal(raw, &obj); err != nil {
		return settings
	}
	obj.Delete("trackingId")
	obj.Delete("deviceId")
	redacted := brRedactAny(&obj)
	redactedRaw, err := jsonx.Marshal(redacted)
	if err != nil {
		return settings
	}
	var out Settings
	if err := jsonx.Unmarshal(redactedRaw, &out); err != nil {
		return settings
	}
	return out
}

// BugReportTerminalEnvironment captures non-secret terminal metadata.
type BugReportTerminalEnvironment struct {
	Term           *string `json:"term"`
	Program        *string `json:"program"`
	ProgramVersion *string `json:"programVersion"`
	Colorterm      *string `json:"colorterm"`
	Tmux           bool    `json:"tmux"`
	Ssh            bool    `json:"ssh"`
	Ci             bool    `json:"ci"`
}

// BugReportEnvironment captures platform and PI_* variable names (never values).
type BugReportEnvironment struct {
	Version                string                       `json:"version"`
	UserAgent              string                       `json:"userAgent"`
	Runtime                string                       `json:"runtime"`
	Platform               string                       `json:"platform"`
	Arch                   string                       `json:"arch"`
	OsRelease              string                       `json:"osRelease"`
	OsVersion              string                       `json:"osVersion"`
	Shell                  *string                      `json:"shell"`
	Terminal               BugReportTerminalEnvironment `json:"terminal"`
	PiEnvironmentVariables []string                     `json:"piEnvironmentVariables"`
}

// BugReportSessionInfo records non-content session details for metadata export.
type BugReportSessionInfo struct {
	Id              string  `json:"id"`
	Included        bool    `json:"included"`
	SummaryIncluded bool    `json:"summaryIncluded"`
	MessageCount    int     `json:"messageCount"`
	Cwd             *string `json:"cwd,omitzero"`
}

// BugReportModelDescription summarizes the active model without leaking credentials.
type BugReportModelDescription struct {
	Provider         ai.ProviderId        `json:"provider"`
	Id               string               `json:"id"`
	Name             string               `json:"name"`
	Api              string               `json:"api"`
	BaseUrl          string               `json:"baseUrl"`
	Reasoning        bool                 `json:"reasoning"`
	Input            []ai.ModelModality   `json:"input"`
	ContextWindow    int                  `json:"contextWindow"`
	MaxTokens        int                  `json:"maxTokens"`
	SamplingParams   any                  `json:"samplingParams"`
	Compat           any                  `json:"compat"`
	ThinkingLevelMap *ai.ThinkingLevelMap `json:"thinkingLevelMap"`
	HeaderNames      []string             `json:"headerNames"`
}

// BugReportProviderDescription summarizes provider metadata and auth status without secrets.
type BugReportProviderDescription struct {
	Id                    string     `json:"id"`
	Name                  string     `json:"name"`
	BaseUrl               *string    `json:"baseUrl"`
	HeaderNames           []string   `json:"headerNames"`
	AuthTypes             []string   `json:"authTypes"`
	AuthStatus            AuthStatus `json:"authStatus"`
	UsingOAuth            bool       `json:"usingOAuth"`
	RegisteredByExtension bool       `json:"registeredByExtension"`
}

// BugReportExtensionDescription summarizes a loaded extension with redacted source URL.
type BugReportExtensionDescription struct {
	Path   string             `json:"path"`
	Source string             `json:"source"`
	Scope  utils.SourceScope  `json:"scope"`
	Origin utils.SourceOrigin `json:"origin"`
	Hidden bool               `json:"hidden"`
}

// BugReportExtensionError pairs an extension path with its load error message.
type BugReportExtensionError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// BugReportSettingsSnapshot stores redacted global and project settings.
type BugReportSettingsSnapshot struct {
	Global  Settings `json:"global"`
	Project Settings `json:"project"`
}

// BugReportMetadata is serialized as report.json inside a bug report bundle.
type BugReportMetadata struct {
	SchemaVersion   int                             `json:"schemaVersion"`
	Id              string                          `json:"id"`
	CreatedAt       string                          `json:"createdAt"`
	Hint            *string                         `json:"hint"`
	Environment     BugReportEnvironment            `json:"environment"`
	Session         BugReportSessionInfo            `json:"session"`
	Model           *BugReportModelDescription      `json:"model"`
	Provider        *BugReportProviderDescription   `json:"provider"`
	ThinkingLevel   agent.ThinkingLevel             `json:"thinkingLevel"`
	Extensions      []BugReportExtensionDescription `json:"extensions"`
	ExtensionErrors []BugReportExtensionError       `json:"extensionErrors"`
	Settings        BugReportSettingsSnapshot       `json:"settings"`
}

// BugReportAssistantDiagnostic captures failed/errored/diagnostic assistant turns without message text.
type BugReportAssistantDiagnostic struct {
	EntryId       string                          `json:"entryId"`
	Timestamp     string                          `json:"timestamp"`
	Provider      ai.ProviderId                   `json:"provider"`
	Model         string                          `json:"model"`
	Api           string                          `json:"api"`
	StopReason    ai.StopReason                   `json:"stopReason"`
	RawStopReason *string                         `json:"rawStopReason,omitzero"`
	ErrorMessage  *string                         `json:"errorMessage,omitzero"`
	Diagnostics   []ai.AssistantMessageDiagnostic `json:"diagnostics"`
}

// BugReportDiagnostics is serialized as diagnostics.json inside a bug report bundle.
type BugReportDiagnostics struct {
	SchemaVersion         int                            `json:"schemaVersion"`
	SessionId             string                         `json:"sessionId"`
	EntryCount            int                            `json:"entryCount"`
	AssistantMessageCount int                            `json:"assistantMessageCount"`
	Assistant             []BugReportAssistantDiagnostic `json:"assistant"`
	Crashes               []CrashRecord                  `json:"crashes"`
}

// BugReportBundle holds all files packaged into a bug report archive or upload.
type BugReportBundle struct {
	Metadata     BugReportMetadata    `json:"metadata"`
	Diagnostics  BugReportDiagnostics `json:"diagnostics"`
	SessionJsonl *string              `json:"sessionJsonl,omitzero"`
	Summary      *string              `json:"summary,omitzero"`
}

// BugReportSessionEntryData stores bug report delivery history inside a session custom entry.
type BugReportSessionEntryData struct {
	Id              string  `json:"id"`
	CreatedAt       string  `json:"createdAt"`
	Hint            *string `json:"hint"`
	SessionIncluded bool    `json:"sessionIncluded"`
	SummaryIncluded bool    `json:"summaryIncluded"`
	Delivery        string  `json:"delivery"`
	Path            *string `json:"path,omitzero"`
}

// BugReportFile represents a single named payload shared by zip archive and HTTP upload.
type BugReportFile struct {
	Name        string
	ContentType string
	Data        string
}

// CollectBugReportMetadataOptions configures CollectBugReportMetadata.
type CollectBugReportMetadataOptions struct {
	Id              *string
	Hint            *string
	SessionId       string
	Cwd             string
	IncludeSession  bool
	IncludeSummary  bool
	MessageCount    int
	Model           *ai.Model
	ModelRuntime    *ModelRuntime
	ThinkingLevel   agent.ThinkingLevel
	Extensions      []Extension
	ExtensionErrors []BugReportExtensionError
	GlobalSettings  Settings
	ProjectSettings Settings
}

// GenerateBugReportSummaryOptions configures GenerateBugReportSummary.
type GenerateBugReportSummaryOptions struct {
	Messages      []agent.AgentMessage
	Hint          *string
	Model         *ai.Model
	ApiKey        *string
	Headers       map[string]string
	Env           map[string]string
	ThinkingLevel *agent.ThinkingLevel
	StreamFn      agent.StreamFn
	Retry         *ai.RetryPolicy
	SessionId     *string
}

func brEnvOpt(name string) *string {
	v := os.Getenv(name)
	if v == "" {
		return nil
	}
	return &v
}

func brShellBasename() *string {
	raw := os.Getenv("SHELL")
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return nil
	}
	last := parts[len(parts)-1]
	if last == "" {
		return nil
	}
	return &last
}

func brCollectEnvironment() BugReportEnvironment {
	var piVars []string
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(name, "PI_") {
			piVars = append(piVars, name)
		}
	}
	sort.Strings(piVars)
	if piVars == nil {
		piVars = []string{}
	}

	return BugReportEnvironment{
		Version:   utils.VERSION,
		UserAgent: utils.GetPiUserAgent(utils.VERSION),
		Runtime:   runtime.Version(),
		Platform:  runtime.GOOS,
		Arch:      runtime.GOARCH,
		OsRelease: runtime.GOOS,
		OsVersion: runtime.Version(),
		Shell:     brShellBasename(),
		Terminal: BugReportTerminalEnvironment{
			Term:           brEnvOpt("TERM"),
			Program:        brEnvOpt("TERM_PROGRAM"),
			ProgramVersion: brEnvOpt("TERM_PROGRAM_VERSION"),
			Colorterm:      brEnvOpt("COLORTERM"),
			Tmux:           os.Getenv("TMUX") != "",
			Ssh:            os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "",
			Ci:             os.Getenv("CI") != "",
		},
		PiEnvironmentVariables: piVars,
	}
}

func brDescribeModel(model *ai.Model) *BugReportModelDescription {
	if model == nil {
		return nil
	}
	headerNames := []string{}
	if model.Headers != nil {
		for k := range model.Headers.All() {
			headerNames = append(headerNames, k)
		}
		sort.Strings(headerNames)
	}
	var samplingParams any
	if model.SamplingParams != nil {
		samplingParams = RedactJsonValue(model.SamplingParams)
	}
	var compat any
	if !model.Compat.IsZero() {
		compat = RedactJsonValue(model.Compat.V)
	}
	return &BugReportModelDescription{
		Provider:         model.Provider,
		Id:               model.Id,
		Name:             model.Name,
		Api:              model.Api,
		BaseUrl:          RedactUrl(model.BaseUrl),
		Reasoning:        model.Reasoning,
		Input:            model.Input,
		ContextWindow:    model.ContextWindow,
		MaxTokens:        model.MaxTokens,
		SamplingParams:   samplingParams,
		Compat:           compat,
		ThinkingLevelMap: model.ThinkingLevelMap,
		HeaderNames:      headerNames,
	}
}

func brDescribeProvider(modelRuntime *ModelRuntime, provider *ai.Provider) *BugReportProviderDescription {
	if provider == nil {
		return nil
	}
	authTypes := []string{}
	if provider.Auth.ApiKey != nil {
		authTypes = append(authTypes, "api_key")
	}
	if provider.Auth.OAuth != nil {
		authTypes = append(authTypes, "oauth")
	}
	headerNames := []string{}
	if provider.Headers != nil {
		for k := range provider.Headers.All() {
			headerNames = append(headerNames, k)
		}
		sort.Strings(headerNames)
	}
	var baseUrl *string
	if provider.BaseUrl != nil && *provider.BaseUrl != "" {
		redacted := RedactUrl(*provider.BaseUrl)
		baseUrl = &redacted
	}
	var authStatus AuthStatus
	var usingOAuth bool
	var registeredByExtension bool
	if modelRuntime != nil {
		authStatus = modelRuntime.GetProviderAuthStatus(provider.Id)
		usingOAuth = modelRuntime.IsUsingOAuth(provider.Id)
		for _, id := range modelRuntime.GetRegisteredProviderIds() {
			if id == provider.Id {
				registeredByExtension = true
				break
			}
		}
	}
	return &BugReportProviderDescription{
		Id:                    provider.Id,
		Name:                  provider.Name,
		BaseUrl:               baseUrl,
		HeaderNames:           headerNames,
		AuthTypes:             authTypes,
		AuthStatus:            authStatus,
		UsingOAuth:            usingOAuth,
		RegisteredByExtension: registeredByExtension,
	}
}

func brDescribeExtension(extension Extension) BugReportExtensionDescription {
	return BugReportExtensionDescription{
		Path:   extension.Path,
		Source: RedactUrl(extension.SourceInfo.Source),
		Scope:  extension.SourceInfo.Scope,
		Origin: extension.SourceInfo.Origin,
		Hidden: extension.Hidden,
	}
}

// CollectBugReportMetadata collects redacted runtime, environment, model, provider, and settings metadata.
func CollectBugReportMetadata(options CollectBugReportMetadataOptions) BugReportMetadata {
	var hint *string
	if options.Hint != nil {
		trimmed := strings.TrimSpace(*options.Hint)
		if trimmed != "" {
			hint = &trimmed
		}
	}
	id := ai.Uuidv7()
	if options.Id != nil && *options.Id != "" {
		id = *options.Id
	}

	var provider *ai.Provider
	if options.Model != nil && options.ModelRuntime != nil {
		provider = options.ModelRuntime.GetProvider(string(options.Model.Provider))
	}

	var cwdOpt *string
	if options.IncludeSession {
		c := options.Cwd
		cwdOpt = &c
	}

	extensions := make([]BugReportExtensionDescription, len(options.Extensions))
	for i, ext := range options.Extensions {
		extensions[i] = brDescribeExtension(ext)
	}
	extErrors := make([]BugReportExtensionError, len(options.ExtensionErrors))
	copy(extErrors, options.ExtensionErrors)

	return BugReportMetadata{
		SchemaVersion: brSchemaVersion,
		Id:            id,
		CreatedAt:     time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Hint:          hint,
		Environment:   brCollectEnvironment(),
		Session: BugReportSessionInfo{
			Id:              options.SessionId,
			Included:        options.IncludeSession,
			SummaryIncluded: options.IncludeSummary,
			MessageCount:    options.MessageCount,
			Cwd:             cwdOpt,
		},
		Model:           brDescribeModel(options.Model),
		Provider:        brDescribeProvider(options.ModelRuntime, provider),
		ThinkingLevel:   options.ThinkingLevel,
		Extensions:      extensions,
		ExtensionErrors: extErrors,
		Settings: BugReportSettingsSnapshot{
			Global:  brRedactSettings(options.GlobalSettings),
			Project: brRedactSettings(options.ProjectSettings),
		},
	}
}

// CollectBugReportDiagnostics collects failed/diagnostic assistant turns and recent crash records without content.
func CollectBugReportDiagnostics(sessionManager ReadonlySessionManager, crashes ...CrashRecord) BugReportDiagnostics {
	var entries []SessionEntry
	var sessionId string
	if sessionManager != nil {
		entries = sessionManager.GetEntries()
		sessionId = sessionManager.GetSessionId()
	}

	assistant := []BugReportAssistantDiagnostic{}
	assistantMessageCount := 0
	for _, entry := range entries {
		sm, ok := entry.(*SessionMessageEntry)
		if !ok || sm == nil || sm.Message == nil {
			continue
		}
		msg, ok := sm.Message.(*ai.AssistantMessage)
		if !ok || msg == nil {
			continue
		}
		assistantMessageCount++
		diags := msg.Diagnostics
		if len(diags) == 0 &&
			msg.StopReason != ai.StopReasonError &&
			msg.StopReason != ai.StopReasonAborted &&
			(msg.ErrorMessage == nil || *msg.ErrorMessage == "") {
			continue
		}
		if diags == nil {
			diags = []ai.AssistantMessageDiagnostic{}
		}
		assistant = append(assistant, BugReportAssistantDiagnostic{
			EntryId:       sm.Id,
			Timestamp:     sm.Timestamp,
			Provider:      msg.Provider,
			Model:         msg.Model,
			Api:           string(msg.Api),
			StopReason:    msg.StopReason,
			RawStopReason: msg.RawStopReason,
			ErrorMessage:  msg.ErrorMessage,
			Diagnostics:   diags,
		})
	}

	cleanCrashes := make([]CrashRecord, len(crashes))
	for i, c := range crashes {
		c.Notified = nil
		cleanCrashes[i] = c
	}

	return BugReportDiagnostics{
		SchemaVersion:         brSchemaVersion,
		SessionId:             sessionId,
		EntryCount:            len(entries),
		AssistantMessageCount: assistantMessageCount,
		Assistant:             assistant,
		Crashes:               cleanCrashes,
	}
}

// BugReportFiles produces the files packaged in both ZIP export and HTTP bug report upload.
func BugReportFiles(bundle BugReportBundle) []BugReportFile {
	metaBytes, _ := jsonx.MarshalIndent(bundle.Metadata, "  ")
	diagBytes, _ := jsonx.MarshalIndent(bundle.Diagnostics, "  ")
	files := []BugReportFile{
		{Name: "report.json", ContentType: "application/json", Data: string(metaBytes) + "\n"},
		{Name: "diagnostics.json", ContentType: "application/json", Data: string(diagBytes) + "\n"},
	}
	if bundle.SessionJsonl != nil {
		files = append(files, BugReportFile{
			Name:        "session.jsonl",
			ContentType: "application/x-ndjson",
			Data:        *bundle.SessionJsonl,
		})
	}
	if bundle.Summary != nil {
		s := *bundle.Summary
		if !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		files = append(files, BugReportFile{
			Name:        "summary.md",
			ContentType: "text/markdown",
			Data:        s,
		})
	}
	return files
}

// WriteBugReportArchive writes bundle to a ZIP archive at filePath.
func WriteBugReportArchive(bundle BugReportBundle, filePath string) error {
	files := BugReportFiles(bundle)
	entries := make([]utils.ZipEntry, len(files))
	for i, f := range files {
		entries[i] = utils.ZipEntry{Name: f.Name, Data: []byte(f.Data)}
	}
	archiveBytes, err := utils.CreateZip(entries)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filePath, archiveBytes, 0o644)
}

// BugReportArchiveFileName returns the canonical ZIP filename for report id.
func BugReportArchiveFileName(id string) string {
	return fmt.Sprintf("pi-bug-report-%s.zip", id)
}

const brBugSummarySystemPrompt = `You are helping a user file a bug report about pi, the coding agent they are talking to. You will be shown the conversation transcript. Write a report for the pi developers describing what the user was doing and what went wrong.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the report.`

const brBugSummaryInstructions = `Write the bug report in Markdown with these sections:

## What the user was doing
One short paragraph.

## What went wrong
Concrete description of the failure: wrong output, errors, hangs, tool failures, unexpected behavior. Quote error messages and tool output verbatim where they exist.

## Steps to reproduce
Numbered list, as specific as the transcript allows.

## Relevant details
Tool calls involved, files touched, model behavior, anything else that helps a developer reproduce or locate the problem.

Do not include file contents, secrets, or credentials from the transcript; refer to files by path only. Keep the report factual and concise.`

func brSelectMessages(messages []agent.AgentMessage, tokenBudget int) []agent.AgentMessage {
	var selected []agent.AgentMessage
	tokens := 0
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		next := EstimateTokens(msg)
		if len(selected) > 0 && tokens+next > tokenBudget {
			break
		}
		selected = append(selected, msg)
		tokens += next
	}
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	return selected
}

// GenerateBugReportSummary asks the session model to summarize what went wrong when the user omits the transcript.
func GenerateBugReportSummary(ctx context.Context, options GenerateBugReportSummaryOptions) (string, error) {
	if options.Model == nil {
		return "", errors.New("Bug report summary requires a model")
	}
	contextWindow := options.Model.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	selected := brSelectMessages(options.Messages, int(math.Floor(float64(contextWindow)*0.6)))

	var parts []string
	if len(selected) < len(options.Messages) {
		parts = append(parts, fmt.Sprintf("Note: only the last %d of %d messages are shown.", len(selected), len(options.Messages)))
	}
	parts = append(parts, fmt.Sprintf("<conversation>\n%s\n</conversation>", SerializeConversation(ConvertToLlm(selected))))
	if options.Hint != nil {
		if trimmed := strings.TrimSpace(*options.Hint); trimmed != "" {
			parts = append(parts, fmt.Sprintf("<user-report>\n%s\n</user-report>", trimmed))
		}
	}
	parts = append(parts, brBugSummaryInstructions)
	prompt := strings.Join(parts, "\n\n")

	maxTokens := 4096
	if options.Model.MaxTokens > 0 && options.Model.MaxTokens < maxTokens {
		maxTokens = options.Model.MaxTokens
	}
	requestOptions := ccCreateSummarizationOptions(
		options.Model,
		options.ApiKey,
		options.Headers,
		options.Env,
		options.ThinkingLevel,
		options.SessionId,
	)
	requestOptions.MaxTokens = &maxTokens

	sys := brBugSummarySystemPrompt
	c := &ai.Context{
		SystemPrompt: &sys,
		Messages: ai.MessageList{
			&ai.UserMessage{
				Role: "user",
				Content: ai.UserMessageContent{
					Blocks: ai.ContentList{&ai.TextContent{Type: "text", Text: prompt}},
				},
				Timestamp: time.Now().UnixMilli(),
			},
		},
	}

	response, err := CompleteSummarization(
		ctx,
		options.Model,
		c,
		requestOptions,
		options.StreamFn,
		options.Retry,
		nil,
	)
	if err != nil {
		return "", err
	}
	if response.StopReason == ai.StopReasonAborted {
		return "", errors.New("Bug report summary was cancelled")
	}
	if failure := GetSummarizationFailure(response, "Bug report summary"); failure != nil {
		return "", errors.New(*failure)
	}
	if ccHasToolCallBlock(response.Content) {
		return "", errors.New("Bug report summary attempted to call a tool")
	}
	text := strings.TrimSpace(ai.ContentText(response.Content))
	if text == "" {
		return "", errors.New("Bug report summary was empty")
	}
	return text, nil
}
