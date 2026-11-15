// Ported from packages/coding-agent/src/extensions/mcp/index.ts (pi v1.5.2).

package mcpext

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/mcp"
	mcpoauth "github.com/keejkrej/pi-go/mcp/oauth"
	mcpprotocol "github.com/keejkrej/pi-go/mcp/protocol"
	"github.com/keejkrej/pi-go/tui"
)

const (
	indexDefaultStartupWaitMs int64 = 10_000
	indexMaxServerDescChars         = 250
	indexCodemodeToolName           = "codemode"
	indexToolSearchToolName         = "tool_search"
	indexUsage                      = "Usage: /mcp, /mcp login [server], /mcp logout [server], /mcp reconnect [server]"
	McpServersSection               = "mcp_servers"
	MaxServersSectionChars          = 4096
)

var indexResourceToolNames = map[string]bool{
	ListMcpResourcesTool:         true,
	ListMcpResourceTemplatesTool: true,
	ReadMcpResourceTool:          true,
}

var indexExposureOrder = []McpExposure{
	core.McpExposureCodemode,
	core.McpExposureDeferred,
	core.McpExposureDirect,
}

var indexExposureDescriptions = map[McpExposure]string{
	core.McpExposureCodemode: "called from codemode scripts, which find them with searchTools()",
	core.McpExposureDeferred: "not declared until tool_search loads them, then called directly; no codemode needed",
	core.McpExposureDirect:   "declared to the model like built-in tools",
}

// McpExtensionOptions configures CreateMcpExtension.
type McpExtensionOptions struct {
	LoadConfig      func(ectx core.ExtensionContext) LoadedMcpConfig
	CreateTransport McpTransportFactory
	Credentials     *McpOAuthCredentialStore
	LogPath         string
	OpenUrl         func(url string)
	UpdateConfig    func(entry McpServerEntry, patch McpServerConfigPatch) error
	StartupWaitMs   *int64
	HTTPClient      *http.Client
}

// McpServerListing provides the entry and optional instructions needed by RenderServersSection.
type McpServerListing struct {
	Entry        McpServerEntry
	Instructions *string
}

type indexMcpServer struct {
	entry            McpServerEntry
	connection       *McpServerConnection
	registeredConfig string
	message          *string
	attempt          uint64
	ready            chan struct{}
	closing          chan struct{}
}

func indexIsEnabled(entry McpServerEntry) bool {
	return entry.Config.Enabled == nil || *entry.Config.Enabled
}

func indexExposureOf(entry McpServerEntry) McpExposure {
	if entry.Config.Exposure != nil {
		return *entry.Config.Exposure
	}
	return core.McpExposureCodemode
}

func indexConfiguredExposures(entry McpServerEntry) map[McpExposure]bool {
	out := map[McpExposure]bool{indexExposureOf(entry): true}
	if entry.Config.ToolExposure != nil {
		for _, exp := range entry.Config.ToolExposure.All() {
			out[exp] = true
		}
	}
	return out
}

func indexHasDirectTools(entry McpServerEntry) bool {
	return indexConfiguredExposures(entry)[core.McpExposureDirect]
}

func indexHasIndirectTools(entry McpServerEntry) bool {
	exposures := indexConfiguredExposures(entry)
	return exposures[core.McpExposureCodemode] || exposures[core.McpExposureDeferred]
}

func indexFirstLine(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx]
	}
	return text
}

func indexServersSectionIntro(reaches map[string]bool) string {
	intro := "MCP servers whose tools are not declared to you."
	if reaches["codemode"] {
		intro += " Call the tools of `codemode` servers from codemode scripts."
	}
	if reaches["tool_search"] {
		intro += " Load the tools of `tool_search` servers with `tool_search`."
	}
	return intro
}

func indexTruncate(text string, max int) string {
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	if max <= 1 {
		return ""
	}
	return strings.TrimRight(core.SliceHead(text, max-1), " \t\r\n") + "…"
}

func indexServerSummary(server McpServerListing) string {
	text := ""
	if server.Entry.Config.Description != nil {
		text = strings.TrimSpace(*server.Entry.Config.Description)
	}
	if text == "" && server.Instructions != nil {
		text = *server.Instructions
	}
	return strings.TrimSpace(indexFirstLine(text))
}

// RenderServersSection renders the `<mcp_servers>` system prompt section listing enabled servers
// whose tools are reached via codemode or tool_search, or nil when none exist.
func RenderServersSection(servers []McpServerListing) *string {
	var listed []McpServerListing
	for _, s := range servers {
		if indexIsEnabled(s.Entry) && indexHasIndirectTools(s.Entry) {
			listed = append(listed, s)
		}
	}
	if len(listed) == 0 {
		return nil
	}
	sort.Slice(listed, func(i, j int) bool {
		return listed[i].Entry.Name < listed[j].Entry.Name
	})

	reaches := make([]string, len(listed))
	reachesSet := make(map[string]bool)
	heads := make([]string, len(listed))
	for i, s := range listed {
		r := "tool_search"
		if indexConfiguredExposures(s.Entry)[core.McpExposureCodemode] {
			r = "codemode"
		}
		reaches[i] = r
		reachesSet[r] = true
		heads[i] = fmt.Sprintf("- %s (%s)", mcp.McpNamespace(s.Entry.Name), r)
	}
	intro := indexServersSectionIntro(reachesSet)
	omittedLines := func(count int) []string {
		if count <= 0 {
			return nil
		}
		plural := "s"
		if count == 1 {
			plural = ""
		}
		return []string{fmt.Sprintf("- … %d more server%s; find their tools with searchTools()", count, plural)}
	}
	sizeFor := func(kept int) int {
		lines := make([]string, 0, 1+kept+1)
		lines = append(lines, intro)
		lines = append(lines, heads[:kept]...)
		lines = append(lines, omittedLines(len(listed)-kept)...)
		return utf8.RuneCountInString(strings.Join(lines, "\n"))
	}

	kept := len(listed)
	for kept > 0 && sizeFor(kept) > MaxServersSectionChars {
		kept--
	}

	perServer := 0
	if kept > 0 {
		perServer = min(indexMaxServerDescChars, (MaxServersSectionChars-sizeFor(kept))/kept-2)
	}
	lines := make([]string, 0, 1+kept+1)
	lines = append(lines, intro)
	for i := 0; i < kept; i++ {
		summary := ""
		if perServer > 0 {
			summary = indexTruncate(indexServerSummary(listed[i]), perServer)
		}
		if summary != "" {
			lines = append(lines, heads[i]+": "+summary)
		} else {
			lines = append(lines, heads[i])
		}
	}
	lines = append(lines, omittedLines(len(listed)-kept)...)
	section := strings.Join(lines, "\n")
	return &section
}

func indexReachesServer(reach core.ToolReachesResult, server string) bool {
	if reach.All {
		return true
	}
	ns := mcp.McpNamespace(server)
	for _, word := range reach.Tools {
		if strings.Contains(word, ns) {
			return true
		}
	}
	return false
}

func indexDescribeState(server *indexMcpServer, withError bool) string {
	if !indexIsEnabled(server.entry) {
		return "disabled"
	}
	conn := server.connection
	if conn == nil {
		return "starting"
	}
	state := conn.State()
	switch state {
	case ServerStateNeedsAuth:
		return "needs sign-in"
	case ServerStateFailed:
		if !withError {
			return "failed"
		}
		errText := "unknown error"
		if e := conn.Error(); e != nil && *e != "" {
			errText = indexFirstLine(*e)
		}
		return "failed: " + errText
	case ServerStateConnected:
		tools := conn.Tools()
		plural := "s"
		if len(tools) == 1 {
			plural = ""
		}
		resSuffix := ""
		if conn.HasResources() {
			resSuffix = " · resources"
		}
		return fmt.Sprintf("connected · %d tool%s%s", len(tools), plural, resSuffix)
	case ServerStateConnecting:
		return "connecting…"
	default:
		return string(state)
	}
}

func indexAttentionRank(server *indexMcpServer) int {
	if !indexIsEnabled(server.entry) {
		return 5
	}
	if server.connection == nil {
		return 3
	}
	switch server.connection.State() {
	case ServerStateNeedsAuth:
		return 0
	case ServerStateFailed:
		return 1
	case ServerStateDisconnected:
		return 2
	case ServerStateConnected:
		return 4
	default:
		return 3
	}
}

func indexDescribeTransport(entry McpServerEntry) string {
	if entry.Config.IsHttp() && entry.Config.Url != nil {
		return *entry.Config.Url
	}
	var parts []string
	if entry.Config.Command != nil {
		parts = append(parts, *entry.Config.Command)
	}
	parts = append(parts, entry.Config.Args...)
	return strings.Join(parts, " ")
}

func indexCanonicalJson(v any) string {
	s, err := jsonx.Stringify(v)
	if err != nil {
		return ""
	}
	return s
}

func indexDefaultLoadConfig(ectx core.ExtensionContext) LoadedMcpConfig {
	return LoadMcpConfig(LoadMcpConfigOptions{
		AgentDir:       utils.GetAgentDir(),
		Cwd:            ectx.Cwd(),
		ProjectTrusted: ectx.IsProjectTrusted(),
	})
}

type indexExtState struct {
	pi      core.ExtensionAPI
	options McpExtensionOptions

	openUrl       func(url string)
	updateConfig  func(entry McpServerEntry, patch McpServerConfigPatch) error
	startupWaitMs int64
	httpClient    *http.Client

	mu sync.Mutex
	// regMu serializes all pi tool-registry calls (RegisterTool, GetAllTools,
	// GetActiveTools, SetActiveTools) across concurrent server connection
	// goroutines; lock order is always regMu -> mu, never mu -> regMu.
	regMu                 sync.Mutex
	servers               []*indexMcpServer
	configuredEntries     []McpServerEntry
	configErrors          []string
	projectConfig         *string
	overridden            []string
	autoEnableCodemode    bool
	warnedUnreachable     bool
	pendingDone           chan struct{}
	waitedForStartup      bool
	sessionCwd            string
	credentials           *McpOAuthCredentialStore
	modelRegistry         *core.ModelRegistry
	serverLog             *McpServerLog
	sessionCtx            context.Context
	sessionCancel         context.CancelFunc
	nextAttempt           uint64
	bgWg                  sync.WaitGroup
	nextListenerId        uint64
	listeners             map[uint64]func()
	toolOwners            map[string]string
	serverTools           map[string]map[string]bool
	definitions           map[string]core.ToolDefinition
	resourceToolsExposure *McpExposure
	tokensAtSignIn        map[*McpServerConnection]string
}

func (s *indexExtState) emitChange() {
	s.mu.Lock()
	cbs := make([]func(), 0, len(s.listeners))
	for _, cb := range s.listeners {
		cbs = append(cbs, cb)
	}
	s.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
}

func (s *indexExtState) subscribe(listener func()) func() {
	s.mu.Lock()
	s.nextListenerId++
	id := s.nextListenerId
	s.listeners[id] = listener
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.listeners, id)
		s.mu.Unlock()
	}
}

func (s *indexExtState) findServerLocked(name string) *indexMcpServer {
	for _, srv := range s.servers {
		if srv.entry.Name == name {
			return srv
		}
	}
	return nil
}

func (s *indexExtState) findServer(name string) *indexMcpServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.findServerLocked(name)
}

func (s *indexExtState) getCredentials(rt McpRuntimeModule) *McpOAuthCredentialStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.credentials == nil {
		s.credentials = rt.NewMcpOAuthCredentialStore()
	}
	return s.credentials
}

func (s *indexExtState) getServerLog(rt McpRuntimeModule) *McpServerLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.serverLog == nil {
		logPath := s.options.LogPath
		if logPath == "" {
			logPath = filepath.Join(utils.GetAgentDir(), "mcp.log")
		}
		s.serverLog = rt.NewMcpServerLog(logPath)
	}
	return s.serverLog
}

func (s *indexExtState) registeredServers(configured []McpServerEntry) (registered []*indexMcpServer, overridden []string) {
	for _, reg := range s.pi.GetMcpServers() {
		var match *McpServerEntry
		for i := range configured {
			if mcp.McpNamespace(configured[i].Name) == mcp.McpNamespace(reg.Name) {
				match = &configured[i]
				break
			}
		}
		if match != nil {
			overridden = append(overridden, fmt.Sprintf(
				`%q registered by %s is overridden by %q in %s`,
				reg.Name, reg.ExtensionPath, match.Name, match.Source,
			))
			continue
		}
		cfg := FromCoreMcpServerConfig(reg.Config)
		registered = append(registered, &indexMcpServer{
			entry: McpServerEntry{
				Name:   reg.Name,
				Config: cfg,
				Source: reg.ExtensionPath,
				Scope:  "extension",
			},
			registeredConfig: indexCanonicalJson(reg.Config),
		})
	}
	return registered, overridden
}

func (s *indexExtState) storedTokens(conn *McpServerConnection) string {
	urlPtr := conn.OAuthUrl()
	s.mu.Lock()
	creds := s.credentials
	s.mu.Unlock()
	if urlPtr == nil || creds == nil {
		return "null"
	}
	toks := creds.Tokens(conn.Name(), *urlPtr)
	if toks == nil {
		return "null"
	}
	encoded, err := jsonx.Stringify(toks)
	if err != nil {
		return "null"
	}
	return encoded
}

func (s *indexExtState) onConnectionChange(conn *McpServerConnection) {
	state := conn.State()
	if state == ServerStateNeedsAuth {
		snap := s.storedTokens(conn)
		s.mu.Lock()
		if _, exists := s.tokensAtSignIn[conn]; !exists {
			s.tokensAtSignIn[conn] = snap
		}
		s.mu.Unlock()
	} else {
		s.mu.Lock()
		delete(s.tokensAtSignIn, conn)
		s.mu.Unlock()
	}
	s.emitChange()
}

func (s *indexExtState) resourceServers() []McpResourceServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []McpResourceServer
	for _, srv := range s.servers {
		if srv.connection != nil && srv.connection.HasResources() && indexIsEnabled(srv.entry) && indexExposureOf(srv.entry) != core.McpExposureHidden {
			out = append(out, srv.connection)
		}
	}
	return out
}

func (s *indexExtState) syncResourceTools() {
	s.regMu.Lock()
	defer s.regMu.Unlock()
	s.syncResourceToolsLocked()
}

func (s *indexExtState) syncResourceToolsLocked() {
	s.mu.Lock()
	exposures := make(map[McpExposure]bool)
	for _, srv := range s.servers {
		if srv.connection != nil && srv.connection.HasResources() && indexIsEnabled(srv.entry) {
			exp := indexExposureOf(srv.entry)
			if exp != core.McpExposureHidden {
				exposures[exp] = true
			}
		}
	}
	next := core.McpExposureHidden
	for _, candidate := range []McpExposure{core.McpExposureDirect, core.McpExposureCodemode, core.McpExposureDeferred} {
		if exposures[candidate] {
			next = candidate
			break
		}
	}
	prev := s.resourceToolsExposure
	if (prev != nil && *prev == next) || (prev == nil && next == core.McpExposureHidden) {
		s.mu.Unlock()
		return
	}
	wasDirect := prev != nil && *prev == core.McpExposureDirect
	nextCopy := next
	s.resourceToolsExposure = &nextCopy
	s.mu.Unlock()

	defs := CreateMcpResourceToolDefinitions(CreateMcpResourceToolDefinitionsOptions{
		Exposure: next,
		Servers:  s.resourceServers,
	})
	hiddenNames := make(map[string]bool, len(defs))
	for _, def := range defs {
		hiddenNames[def.Name] = true
		s.pi.RegisterTool(def)
	}
	if wasDirect {
		active := s.pi.GetActiveTools()
		filtered := make([]string, 0, len(active))
		for _, name := range active {
			if !hiddenNames[name] {
				filtered = append(filtered, name)
			}
		}
		s.pi.SetActiveTools(filtered)
	}
}

func (s *indexExtState) registerTools(conn *McpServerConnection) {
	serverName := conn.Entry.Name
	tools := conn.Tools()

	s.regMu.Lock()
	defer s.regMu.Unlock()

	s.mu.Lock()
	entry := conn.Entry
	if srv := s.findServerLocked(serverName); srv != nil {
		entry = srv.entry
	}
	var desc *string
	if entry.Config.Description != nil {
		if trimmed := strings.TrimSpace(*entry.Config.Description); trimmed != "" {
			desc = &trimmed
		}
	}
	ns := core.ToolNamespace{
		Name:         mcp.McpNamespace(serverName),
		Aliases:      []string{serverName},
		Description:  desc,
		Instructions: conn.Instructions(),
	}

	previous := s.serverTools[serverName]
	current := make(map[string]bool)

	uniqueTools := make([]string, 0, len(tools))
	seenTool := make(map[string]bool, len(tools))
	for _, t := range tools {
		if !seenTool[t.Name] {
			seenTool[t.Name] = true
			uniqueTools = append(uniqueTools, t.Name)
		}
	}
	plainCounts := make(map[string]int, len(uniqueTools))
	for _, tName := range uniqueTools {
		plain := mcp.CreateMcpToolName(serverName, tName, nil)
		plainCounts[plain]++
	}

	assignName := func(toolName, owner string) string {
		assigned := mcp.CreateMcpToolName(serverName, toolName, func(candidate string) bool {
			if existing, exists := s.toolOwners[candidate]; exists && existing != owner {
				return true
			}
			if current[candidate] {
				return true
			}
			return plainCounts[candidate] > 1
		})
		s.toolOwners[assigned] = owner
		current[assigned] = true
		return assigned
	}

	defsToRegister := make([]core.ToolDefinition, 0, len(tools)+len(previous))
	for _, t := range tools {
		toolCopy := t
		qualifiedName := assignName(toolCopy.Name, serverName+"\x00"+toolCopy.Name)
		def := CreateMcpToolDefinition(CreateMcpToolDefinitionOptions{
			Server:    serverName,
			Tool:      toolCopy,
			Name:      qualifiedName,
			Exposure:  GetMcpToolExposure(entry.Config, toolCopy.Name),
			Namespace: ns,
			TimeoutMs: conn.TimeoutMs(),
			GetClient: func(_ context.Context) (McpToolCaller, error) {
				s.mu.Lock()
				curSrv := s.findServerLocked(serverName)
				var curEntry McpServerEntry
				var curConn *McpServerConnection
				if curSrv != nil {
					curEntry = curSrv.entry
					curConn = curSrv.connection
				}
				s.mu.Unlock()
				if curSrv == nil || !indexIsEnabled(curEntry) {
					return nil, fmt.Errorf("MCP server %q is disabled.", serverName)
				}
				if curConn == nil {
					return nil, fmt.Errorf("MCP server %q is still starting.", serverName)
				}
				if GetMcpToolExposure(curEntry.Config, toolCopy.Name) == core.McpExposureHidden {
					return nil, fmt.Errorf("MCP tool %q is no longer available.", serverName+"/"+toolCopy.Name)
				}
				if curConn.State() == ServerStateConnected {
					offered := false
					for _, ct := range curConn.Tools() {
						if ct.Name == toolCopy.Name {
							offered = true
							break
						}
					}
					if !offered {
						return nil, fmt.Errorf("MCP tool %q is no longer available.", serverName+"/"+toolCopy.Name)
					}
				}
				return curConn, nil
			},
			ReadableResources: func() bool {
				for _, rSrv := range s.resourceServers() {
					if rSrv.Name() == serverName {
						return true
					}
				}
				return false
			},
		})
		s.definitions[def.Name] = def
		defsToRegister = append(defsToRegister, def)
	}
	s.serverTools[serverName] = current
	for prevName := range previous {
		if !current[prevName] {
			if def, ok := s.definitions[prevName]; ok {
				hiddenDef := def
				hiddenDef.Exposure = core.ToolExposureHidden
				defsToRegister = append(defsToRegister, hiddenDef)
			}
		}
	}
	s.mu.Unlock()

	for _, def := range defsToRegister {
		s.pi.RegisterTool(def)
	}
	s.syncResourceToolsLocked()
}

func (s *indexExtState) hideTools(serverName string) {
	s.regMu.Lock()
	defer s.regMu.Unlock()

	s.mu.Lock()
	var toHide []core.ToolDefinition
	for name := range s.serverTools[serverName] {
		if def, ok := s.definitions[name]; ok {
			hidden := def
			hidden.Exposure = core.ToolExposureHidden
			toHide = append(toHide, hidden)
		}
	}
	s.serverTools[serverName] = make(map[string]bool)
	s.mu.Unlock()

	for _, hidden := range toHide {
		s.pi.RegisterTool(hidden)
	}
	s.syncResourceToolsLocked()
}

func (s *indexExtState) ensureDiscoveryActive(ectx core.ExtensionContext) {
	s.mu.Lock()
	exposures := make(map[McpExposure]bool)
	for _, srv := range s.servers {
		if indexIsEnabled(srv.entry) {
			for exp := range indexConfiguredExposures(srv.entry) {
				exposures[exp] = true
			}
		}
	}
	autoEnable := s.autoEnableCodemode
	warned := s.warnedUnreachable
	s.mu.Unlock()

	needsCodemode := exposures[core.McpExposureCodemode]
	needsToolSearch := exposures[core.McpExposureDeferred]
	if !needsCodemode && !needsToolSearch {
		return
	}

	s.regMu.Lock()
	allTools := s.pi.GetAllTools()
	hasReachingTool := func(name string) bool {
		for _, t := range allTools {
			if t.Name == name && t.ReachesTools != nil {
				return true
			}
		}
		return false
	}
	hasCodemode := hasReachingTool(indexCodemodeToolName)
	hasToolSearch := hasReachingTool(indexToolSearchToolName)
	active := s.pi.GetActiveTools()
	var activate []string
	if needsCodemode && hasCodemode && autoEnable && !slices.Contains(active, indexCodemodeToolName) {
		activate = append(activate, indexCodemodeToolName)
	}
	if needsToolSearch && hasToolSearch && !slices.Contains(active, indexToolSearchToolName) {
		activate = append(activate, indexToolSearchToolName)
	}
	if len(activate) > 0 {
		nextActive := append(append([]string(nil), active...), activate...)
		s.pi.SetActiveTools(nextActive)
		active = nextActive
	}
	s.regMu.Unlock()
	if (hasCodemode && slices.Contains(active, indexCodemodeToolName)) ||
		(hasToolSearch && slices.Contains(active, indexToolSearchToolName)) {
		return
	}
	if warned {
		return
	}
	s.mu.Lock()
	if s.warnedUnreachable {
		s.mu.Unlock()
		return
	}
	s.warnedUnreachable = true
	s.mu.Unlock()

	reason := ""
	if needsCodemode && hasCodemode && !autoEnable {
		reason = " (autoEnableCodemode is false)"
	}
	indexSafeNotify(
		ectx,
		fmt.Sprintf("MCP tools are only reachable from the codemode or tool_search tool, but neither is active%s; they cannot be called.", reason),
		"warning",
	)
}

func (s *indexExtState) createConnection(
	rt McpRuntimeModule,
	srv *indexMcpServer,
	attempt uint64,
	sessCtx context.Context,
) *McpServerConnection {
	s.mu.Lock()
	if !s.isCurrentAttemptLocked(srv, attempt, sessCtx) {
		s.mu.Unlock()
		return nil
	}
	entry := srv.entry
	cwd := s.sessionCwd
	reg := s.modelRegistry
	s.mu.Unlock()

	createTransport := s.options.CreateTransport
	if createTransport == nil {
		hc := s.httpClient
		createTransport = func(e McpServerEntry, c string, ap mcp.AuthProvider) (mcp.McpTransport, error) {
			return rt.CreateDefaultTransport(e, c, ap, hc)
		}
	}

	var conn *McpServerConnection
	conn = rt.NewMcpServerConnection(McpServerConnectionOptions{
		Entry:           entry,
		Cwd:             cwd,
		HTTPClient:      s.httpClient,
		CreateTransport: createTransport,
		Credentials:     s.getCredentials(rt),
		ProviderToken: func(ctx context.Context, provider string) (*string, error) {
			if reg == nil {
				return nil, nil
			}
			return reg.GetApiKeyForProvider(ctx, provider), nil
		},
		Log: s.getServerLog(rt),
		OnTools: func(c *McpServerConnection) {
			if s.isCurrentAttempt(srv, attempt, sessCtx) {
				s.registerTools(c)
			}
		},
		OnChange: func(c *McpServerConnection) {
			if s.isCurrentAttempt(srv, attempt, sessCtx) {
				s.onConnectionChange(c)
			} else {
				s.mu.Lock()
				delete(s.tokensAtSignIn, c)
				s.mu.Unlock()
			}
		},
	})

	s.mu.Lock()
	if !s.isCurrentAttemptLocked(srv, attempt, sessCtx) {
		s.mu.Unlock()
		_ = conn.Close()
		return nil
	}
	srv.connection = conn
	s.mu.Unlock()
	s.emitChange()
	return conn
}

func (s *indexExtState) isCurrentAttemptLocked(srv *indexMcpServer, attempt uint64, sessCtx context.Context) bool {
	if sessCtx == nil || sessCtx.Err() != nil {
		return false
	}
	return srv.attempt == attempt && indexIsEnabled(srv.entry) && slices.Contains(s.servers, srv)
}

func (s *indexExtState) isCurrentAttempt(srv *indexMcpServer, attempt uint64, sessCtx context.Context) bool {
	if sessCtx == nil || sessCtx.Err() != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isCurrentAttemptLocked(srv, attempt, sessCtx)
}

func (s *indexExtState) prepareStartConnectionLocked(srv *indexMcpServer, after chan struct{}) (attempt uint64, ready chan struct{}, sessCtx context.Context) {
	s.nextAttempt++
	attempt = s.nextAttempt
	srv.attempt = attempt
	ready = make(chan struct{})
	srv.ready = ready
	sessCtx = s.sessionCtx
	return attempt, ready, sessCtx
}

func (s *indexExtState) runStartConnection(srv *indexMcpServer, attempt uint64, ready, after chan struct{}, sessCtx context.Context) {
	defer close(ready)
	if after != nil {
		select {
		case <-after:
		case <-sessCtx.Done():
			return
		}
	}
	if !s.isCurrentAttempt(srv, attempt, sessCtx) {
		return
	}
	rt := LoadMcpRuntime()
	conn := s.createConnection(rt, srv, attempt, sessCtx)
	if conn == nil || !s.isCurrentAttempt(srv, attempt, sessCtx) {
		return
	}
	_, _ = conn.GetClient(sessCtx)
}

func (s *indexExtState) waitForServers(ctx context.Context, waiting []*indexMcpServer) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		s.mu.Lock()
		type waitTarget struct {
			srv   *indexMcpServer
			ready chan struct{}
		}
		var readyTargets []waitTarget
		for _, srv := range waiting {
			if indexIsEnabled(srv.entry) && slices.Contains(s.servers, srv) && srv.ready != nil {
				readyTargets = append(readyTargets, waitTarget{srv: srv, ready: srv.ready})
			}
		}
		s.mu.Unlock()

		if len(readyTargets) == 0 || ctx.Err() != nil {
			return
		}
		for _, tgt := range readyTargets {
			select {
			case <-tgt.ready:
			case <-ctx.Done():
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		allSettled := true
		for _, tgt := range readyTargets {
			if indexIsEnabled(tgt.srv.entry) && slices.Contains(s.servers, tgt.srv) && tgt.srv.ready != tgt.ready {
				allSettled = false
				break
			}
		}
		s.mu.Unlock()
		if allSettled {
			return
		}
	}
}

// indexSafeNotify sends a notification on ectx, ignoring stale-context panics after session invalidation/disposal.
func indexSafeNotify(ectx core.ExtensionContext, message string, level string) {
	if ectx == nil {
		return
	}
	var ui core.ExtensionUIContext
	stale := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok && strings.Contains(err.Error(), "stale") {
					stale = true
					return
				}
				panic(r)
			}
		}()
		ui = ectx.UI()
	}()
	if stale || ui == nil {
		return
	}
	ui.Notify(message, level)
}

func (s *indexExtState) reportProblems(ectx core.ExtensionContext, only []*indexMcpServer) {
	s.mu.Lock()
	var lines []string
	if only == nil {
		for _, errText := range s.configErrors {
			lines = append(lines, "config: "+errText)
		}
	}
	targets := only
	if targets == nil {
		targets = s.servers
	}
	for _, srv := range targets {
		if srv.connection == nil {
			continue
		}
		st := srv.connection.State()
		if st == ServerStateNeedsAuth || st == ServerStateFailed {
			lines = append(lines, fmt.Sprintf("%s: %s", srv.entry.Name, indexDescribeState(srv, true)))
		}
	}
	s.mu.Unlock()
	if len(lines) == 0 {
		return
	}
	indented := make([]string, len(lines))
	for i, line := range lines {
		indented[i] = "  " + line
	}
	indexSafeNotify(
		ectx,
		fmt.Sprintf("MCP servers need attention:\n%s\nRun /mcp to fix.", strings.Join(indented, "\n")),
		"warning",
	)
}

func (s *indexExtState) reconnectSignedIn(ctx context.Context, ectx core.ExtensionContext) {
	s.mu.Lock()
	var signedIn []*McpServerConnection
	for conn, prev := range s.tokensAtSignIn {
		s.mu.Unlock()
		cur := s.storedTokens(conn)
		s.mu.Lock()
		if cur != prev {
			signedIn = append(signedIn, conn)
			delete(s.tokensAtSignIn, conn)
		}
	}
	s.mu.Unlock()
	if len(signedIn) == 0 {
		return
	}
	var wg sync.WaitGroup
	for _, conn := range signedIn {
		wg.Add(1)
		go func(c *McpServerConnection) {
			defer wg.Done()
			_ = c.Reconnect(ctx)
		}(conn)
	}
	wg.Wait()
	s.ensureDiscoveryActive(ectx)
}

func (s *indexExtState) saveConfig(srv *indexMcpServer, patch McpServerConfigPatch, inProject bool) *string {
	s.mu.Lock()
	entry := srv.entry
	projCfg := s.projectConfig
	s.mu.Unlock()

	if inProject && projCfg != nil {
		entry.Override = projCfg
	}
	if entry.Scope != "extension" {
		if err := s.updateConfig(entry, patch); err != nil {
			target := entry.Source
			if entry.Override != nil {
				target = *entry.Override
			}
			msg := fmt.Sprintf("Could not update %s: %s", target, err.Error())
			return &msg
		}
	}

	s.mu.Lock()
	if patch.Enabled != nil {
		val := *patch.Enabled
		entry.Config.Enabled = &val
	}
	if patch.Exposure != nil {
		val := *patch.Exposure
		entry.Config.Exposure = &val
	}
	srv.entry = entry
	s.mu.Unlock()
	return nil
}

func (s *indexExtState) signIn(srv *indexMcpServer, prompt McpSignInPrompt, cancelCtx context.Context) *string {
	s.mu.Lock()
	conn := srv.connection
	sessCtx := s.sessionCtx
	s.mu.Unlock()
	if conn == nil {
		msg := fmt.Sprintf("MCP server %q does not use OAuth.", srv.entry.Name)
		return &msg
	}
	urlPtr := conn.OAuthUrl()
	if urlPtr == nil {
		msg := fmt.Sprintf("MCP server %q does not use OAuth.", srv.entry.Name)
		return &msg
	}

	settings, setErr := conn.OAuthSettings()
	if setErr != nil {
		msg := "Sign-in failed: " + setErr.Error()
		return &msg
	}

	mergedCtx := sessCtx
	if mergedCtx == nil {
		mergedCtx = context.Background()
	}
	var cancelMerged context.CancelFunc
	if cancelCtx != nil {
		mergedCtx, cancelMerged = context.WithCancel(mergedCtx)
		stopWatch := context.AfterFunc(cancelCtx, cancelMerged)
		defer func() {
			stopWatch()
			cancelMerged()
		}()
	}

	rt := LoadMcpRuntime()
	s.bgWg.Add(1)
	defer s.bgWg.Done()

	err := rt.SignInMcpServer(SignInMcpServerOptions{
		Context:    mergedCtx,
		ServerUrl:  *urlPtr,
		Store:      s.getCredentials(rt).ForServer(srv.entry.Name, *urlPtr),
		Settings:   settings,
		Challenge:  conn.Challenge(),
		Prompt:     prompt,
		HTTPClient: s.httpClient,
	})
	if err != nil {
		var cancelErr *mcpoauth.McpSignInCancelledError
		if errors.As(err, &cancelErr) {
			msg := "Sign-in cancelled."
			return &msg
		}
		msg := "Sign-in failed: " + err.Error()
		return &msg
	}
	conn.ClearChallenge()
	if err := conn.Reconnect(mergedCtx); err != nil {
		msg := "Signed in, but " + err.Error()
		return &msg
	}
	return nil
}

func (s *indexExtState) signOut(ctx context.Context, srv *indexMcpServer) bool {
	s.mu.Lock()
	conn := srv.connection
	s.mu.Unlock()
	if conn == nil {
		return false
	}
	urlPtr := conn.OAuthUrl()
	if urlPtr == nil {
		return false
	}
	rt := LoadMcpRuntime()
	removed := s.getCredentials(rt).Remove(srv.entry.Name, *urlPtr)
	_ = conn.SignOut(ctx)
	return removed
}

func (s *indexExtState) reconnect(ctx context.Context, srv *indexMcpServer) *string {
	s.mu.Lock()
	conn := srv.connection
	if conn == nil {
		name := srv.entry.Name
		s.mu.Unlock()
		msg := fmt.Sprintf("MCP server %q is disabled.", name)
		return &msg
	}
	prevReady := srv.ready
	ready := make(chan struct{})
	srv.ready = ready
	s.mu.Unlock()

	defer close(ready)
	if prevReady != nil {
		select {
		case <-prevReady:
		case <-ctx.Done():
			msg := ctx.Err().Error()
			return &msg
		}
	}
	if err := conn.Reconnect(ctx); err != nil {
		msg := err.Error()
		return &msg
	}
	return nil
}

func (s *indexExtState) setEnabled(srv *indexMcpServer, enabled bool, inProject bool) *string {
	if failed := s.saveConfig(srv, McpServerConfigPatch{Enabled: &enabled}, inProject); failed != nil {
		return failed
	}
	if !enabled {
		s.mu.Lock()
		conn := srv.connection
		prevReady := srv.ready
		srv.attempt = 0
		srv.connection = nil
		var closing chan struct{}
		if conn != nil {
			closing = make(chan struct{})
			srv.closing = closing
		} else {
			closing = srv.closing
		}
		serverName := srv.entry.Name
		s.mu.Unlock()

		s.hideTools(serverName)
		s.emitChange()

		if conn != nil {
			_ = conn.Close()
			if prevReady != nil {
				<-prevReady
			}
			close(closing)
			s.mu.Lock()
			if s.closingClearMatch(srv, closing) {
				srv.closing = nil
			}
			s.mu.Unlock()
		} else if closing != nil {
			<-closing
		}
		return nil
	}

	s.mu.Lock()
	after := srv.closing
	attempt, ready, sessCtx := s.prepareStartConnectionLocked(srv, after)
	s.bgWg.Add(1)
	s.mu.Unlock()

	var startWg sync.WaitGroup
	startWg.Add(1)
	go func() {
		defer s.bgWg.Done()
		defer startWg.Done()
		s.runStartConnection(srv, attempt, ready, after, sessCtx)
	}()
	startWg.Wait()
	return nil
}

func (s *indexExtState) closingClearMatch(srv *indexMcpServer, closing chan struct{}) bool {
	return srv.closing == closing
}

func (s *indexExtState) setExposure(srv *indexMcpServer, exposure McpExposure) *string {
	if failed := s.saveConfig(srv, McpServerConfigPatch{Exposure: &exposure}, false); failed != nil {
		return failed
	}
	s.mu.Lock()
	conn := srv.connection
	serverName := srv.entry.Name
	s.mu.Unlock()

	if conn != nil && conn.State() == ServerStateConnected {
		s.registerTools(conn)
	}
	s.syncResourceTools()

	s.regMu.Lock()
	indirect := make(map[string]bool)
	for _, t := range s.pi.GetAllTools() {
		if t.Exposure != core.ToolExposureDirect {
			indirect[t.Name] = true
		}
	}
	s.mu.Lock()
	owned := make(map[string]bool)
	for name := range s.serverTools[serverName] {
		owned[name] = true
	}
	s.mu.Unlock()

	active := s.pi.GetActiveTools()
	filtered := make([]string, 0, len(active))
	for _, name := range active {
		if !owned[name] || !indirect[name] {
			filtered = append(filtered, name)
		}
	}
	s.pi.SetActiveTools(filtered)
	s.regMu.Unlock()
	s.emitChange()
	return nil
}

func (s *indexExtState) noticesLocked() []string {
	var out []string
	for _, e := range s.configErrors {
		out = append(out, "config: "+e)
	}
	for _, line := range s.overridden {
		out = append(out, "overridden: "+line)
	}
	return out
}

func (s *indexExtState) serversMenu() McpMenu {
	s.mu.Lock()
	sorted := append([]*indexMcpServer(nil), s.servers...)
	notices := s.noticesLocked()
	s.mu.Unlock()

	sort.Slice(sorted, func(i, j int) bool {
		ri, rj := indexAttentionRank(sorted[i]), indexAttentionRank(sorted[j])
		if ri != rj {
			return ri < rj
		}
		return sorted[i].entry.Name < sorted[j].entry.Name
	})

	items := make([]tui.SelectItem, len(sorted))
	for i, srv := range sorted {
		scopeLabel := srv.entry.Scope
		if srv.entry.Override != nil {
			scopeLabel = "global, project override"
		} else if scopeLabel == "" {
			scopeLabel = srv.entry.Source
		}
		desc := fmt.Sprintf("%s · %s · %s", indexDescribeState(srv, true), indexExposureOf(srv.entry), scopeLabel)
		items[i] = tui.SelectItem{
			Value:       srv.entry.Name,
			Label:       srv.entry.Name,
			Description: &desc,
		}
	}

	var errPtr *string
	if len(notices) > 0 {
		joined := strings.Join(notices, "\n")
		errPtr = &joined
	}
	empty := fmt.Sprintf("No MCP servers configured. Add them to %s or .pi/mcp.json.", filepath.Join(utils.GetAgentDir(), "mcp.json"))
	return McpMenu{
		Title:        "MCP servers",
		Error:        errPtr,
		Items:        items,
		Empty:        &empty,
		ConfirmLabel: "manage",
		CancelLabel:  "close",
	}
}

func (s *indexExtState) serverMenu(name string) McpMenu {
	s.mu.Lock()
	srv := s.findServerLocked(name)
	var entry McpServerEntry
	var conn *McpServerConnection
	var msg *string
	projCfg := s.projectConfig
	if srv != nil {
		entry = srv.entry
		conn = srv.connection
		msg = srv.message
	}
	s.mu.Unlock()

	if srv == nil {
		empty := "This server is no longer configured."
		return McpMenu{
			Title:        name,
			Empty:        &empty,
			ConfirmLabel: "",
			CancelLabel:  "back",
		}
	}

	saved := "saved to mcp.json"
	switch {
	case entry.Scope == "extension":
		saved = "for this session"
	case entry.Override != nil:
		saved = "saved to the project mcp.json"
	case entry.Scope != "":
		saved = fmt.Sprintf("saved to the %s mcp.json", entry.Scope)
	}
	inProject := entry.Scope == "global" && entry.Override == nil && projCfg != nil
	inProjectSaved := "saved to the project mcp.json"

	var items []tui.SelectItem
	if !indexIsEnabled(entry) {
		items = append(items, tui.SelectItem{Value: "enable", Label: "Enable", Description: &saved})
		if inProject {
			items = append(items, tui.SelectItem{Value: "enable-project", Label: "Enable in this project", Description: &inProjectSaved})
		}
	} else {
		var st ServerState
		if conn != nil {
			st = conn.State()
		}
		if st == ServerStateNeedsAuth {
			d := "opens the browser"
			items = append(items, tui.SelectItem{Value: "signin", Label: "Sign in", Description: &d})
		}
		if st == ServerStateConnected && conn != nil {
			d := fmt.Sprintf("%d offered", len(conn.Tools()))
			items = append(items, tui.SelectItem{Value: "tools", Label: "Tools", Description: &d})
		}
		if st == ServerStateFailed || st == ServerStateDisconnected || st == ServerStateConnected || st == ServerStateNeedsAuth {
			items = append(items, tui.SelectItem{Value: "reconnect", Label: "Reconnect"})
		}
		if st == ServerStateConnected && conn != nil && conn.OAuthUrl() != nil {
			d := "deletes the stored credentials"
			items = append(items, tui.SelectItem{Value: "signout", Label: "Sign out", Description: &d})
		}
		expDesc := string(indexExposureOf(entry))
		items = append(items, tui.SelectItem{Value: "exposure", Label: "Exposure", Description: &expDesc})
		items = append(items, tui.SelectItem{Value: "disable", Label: "Disable", Description: &saved})
		if inProject {
			items = append(items, tui.SelectItem{Value: "disable-project", Label: "Disable in this project", Description: &inProjectSaved})
		}
	}

	scopeText := entry.Scope
	if scopeText == "" {
		scopeText = "config"
	}
	detailLines := []string{
		indexDescribeTransport(entry),
		fmt.Sprintf("%s: %s", scopeText, entry.Source),
	}
	if entry.Override != nil {
		detailLines = append(detailLines, "project override: "+*entry.Override)
	}
	detailLines = append(detailLines, "State: "+indexDescribeState(srv, false))
	details := strings.Join(detailLines, "\n")

	var errLines []string
	if msg != nil && *msg != "" {
		errLines = append(errLines, *msg)
	}
	if conn != nil && conn.State() != ServerStateConnected {
		if ce := conn.Error(); ce != nil && *ce != "" {
			errLines = append(errLines, *ce)
		}
	}
	var errPtr *string
	if len(errLines) > 0 {
		joined := strings.Join(errLines, "\n")
		errPtr = &joined
	}
	var selected *string
	if len(items) > 0 {
		v := items[0].Value
		selected = &v
	}
	return McpMenu{
		Title:        "MCP server " + name,
		Details:      &details,
		Error:        errPtr,
		Items:        items,
		Selected:     selected,
		ConfirmLabel: "select",
		CancelLabel:  "back",
	}
}

func (s *indexExtState) showTools(ctx context.Context, ui McpUi, srv *indexMcpServer) {
	s.mu.Lock()
	entry := srv.entry
	conn := srv.connection
	s.mu.Unlock()

	exposure := indexExposureOf(entry)
	expDesc := "unreachable"
	if d, ok := indexExposureDescriptions[exposure]; ok {
		expDesc = d
	}
	overridden := entry.Config.ToolExposure != nil && entry.Config.ToolExposure.Len() > 0
	details := fmt.Sprintf("Exposure %s: %s", exposure, expDesc)
	if overridden {
		details += "\nSome tools override it with toolExposure."
	}

	var tools []mcpprotocol.Tool
	if conn != nil {
		tools = conn.Tools()
	}
	items := make([]tui.SelectItem, len(tools))
	for i, t := range tools {
		toolExp := GetMcpToolExposure(entry.Config, t.Name)
		desc := ""
		if t.Description != nil {
			desc = indexFirstLine(*t.Description)
		}
		if toolExp != exposure {
			desc = fmt.Sprintf("[%s] %s", toolExp, desc)
		}
		d := desc
		items[i] = tui.SelectItem{
			Value:       t.Name,
			Label:       t.Name,
			Description: &d,
		}
	}
	empty := "The server offers no tools."
	_ = ui.Menu(ctx, func() McpMenu {
		return McpMenu{
			Title:        "Tools of " + entry.Name,
			Details:      &details,
			Items:        items,
			Empty:        &empty,
			ConfirmLabel: "back",
			CancelLabel:  "back",
		}
	}, nil)
}

func (s *indexExtState) chooseExposure(ctx context.Context, ui McpUi, srv *indexMcpServer) *string {
	s.mu.Lock()
	entry := srv.entry
	s.mu.Unlock()

	current := indexExposureOf(entry)
	details := fmt.Sprintf("Saved to %s.", entry.Source)
	if entry.Scope == "extension" {
		details = fmt.Sprintf("Applies to this session; the server is registered by %s.", entry.Source)
	} else if entry.Override != nil {
		details = fmt.Sprintf("Saved to %s.", *entry.Override)
	}

	items := make([]tui.SelectItem, len(indexExposureOrder))
	for i, exp := range indexExposureOrder {
		prefix := "  "
		if exp == current {
			prefix = "✓ "
		}
		desc := indexExposureDescriptions[exp]
		items[i] = tui.SelectItem{
			Value:       string(exp),
			Label:       prefix + string(exp),
			Description: &desc,
		}
	}
	curStr := string(current)
	choice := ui.Menu(ctx, func() McpMenu {
		return McpMenu{
			Title:        "Exposure of " + entry.Name,
			Details:      &details,
			Items:        items,
			Selected:     &curStr,
			ConfirmLabel: "save",
			CancelLabel:  "back",
		}
	}, nil)
	if choice == nil || McpExposure(*choice) == current {
		return nil
	}
	return s.setExposure(srv, McpExposure(*choice))
}

type indexUiPrompt struct {
	ui               McpUi
	title            string
	status           func(msg string)
	openUrl          func(url string)
	mu               sync.Mutex
	authorizationUrl string
}

func (p *indexUiPrompt) ShowAuthorizationUrl(u *url.URL) {
	urlStr := ""
	if u != nil {
		urlStr = u.String()
	}
	p.mu.Lock()
	p.authorizationUrl = urlStr
	p.mu.Unlock()
	if p.openUrl != nil {
		p.openUrl(urlStr)
	}
}

func (p *indexUiPrompt) PromptForRedirectUrl(ctx context.Context) (*string, error) {
	p.mu.Lock()
	authUrl := p.authorizationUrl
	p.mu.Unlock()
	val := p.ui.RedirectUrl(ctx, p.title, authUrl)
	p.status("Connecting…")
	return val, nil
}

func (s *indexExtState) signInWithUi(ctx context.Context, ui McpUi, srv *indexMcpServer) *string {
	title := "Sign in to " + srv.entry.Name
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	statusFn := func(msg string) {
		ui.Status(title, msg, cancel)
	}
	statusFn("Contacting the authorization server…")
	return s.signIn(srv, &indexUiPrompt{
		ui:      ui,
		title:   title,
		status:  statusFn,
		openUrl: s.openUrl,
	}, cancelCtx)
}

func (s *indexExtState) runInBackground(ectx core.ExtensionContext, srv *indexMcpServer, op func() *string) {
	s.mu.Lock()
	attempt := srv.attempt
	sessCtx := s.sessionCtx
	s.bgWg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.bgWg.Done()
		msg := op()
		s.mu.Lock()
		stale := sessCtx == nil || sessCtx.Err() != nil || srv.attempt != attempt || !slices.Contains(s.servers, srv)
		if !stale {
			srv.message = msg
		}
		s.mu.Unlock()
		if stale {
			return
		}
		s.ensureDiscoveryActive(ectx)
		s.emitChange()
	}()
}

func (s *indexExtState) runAction(ctx context.Context, ui McpUi, ectx core.ExtensionContext, srv *indexMcpServer, action string) {
	s.mu.Lock()
	sessCtx := s.sessionCtx
	s.mu.Unlock()

	var msg *string
	switch action {
	case "signin":
		msg = s.signInWithUi(ctx, ui, srv)
	case "reconnect":
		s.runInBackground(ectx, srv, func() *string {
			bgCtx := sessCtx
			if bgCtx == nil {
				bgCtx = context.Background()
			}
			_ = s.reconnect(bgCtx, srv)
			return nil
		})
	case "signout":
		_ = s.signOut(ctx, srv)
	case "tools":
		s.showTools(ctx, ui, srv)
	case "exposure":
		msg = s.chooseExposure(ctx, ui, srv)
	case "enable", "disable", "enable-project", "disable-project":
		enable := strings.HasPrefix(action, "enable")
		inProject := strings.HasSuffix(action, "-project")
		s.runInBackground(ectx, srv, func() *string {
			return s.setEnabled(srv, enable, inProject)
		})
	}
	if sessCtx == nil || sessCtx.Err() != nil {
		return
	}
	s.mu.Lock()
	srv.message = msg
	s.mu.Unlock()
	s.ensureDiscoveryActive(ectx)
	s.emitChange()
}

func (s *indexExtState) manage(ctx context.Context, ui McpUi, ectx core.ExtensionContext) {
	for {
		namePtr := ui.Menu(ctx, s.serversMenu, s.subscribe)
		if namePtr == nil {
			return
		}
		name := *namePtr
		for {
			actionPtr := ui.Menu(ctx, func() McpMenu { return s.serverMenu(name) }, s.subscribe)
			srv := s.findServer(name)
			if actionPtr == nil || srv == nil {
				break
			}
			s.runAction(ctx, ui, ectx, srv, *actionPtr)
		}
	}
}

func (s *indexExtState) formatStatus() string {
	s.mu.Lock()
	servers := append([]*indexMcpServer(nil), s.servers...)
	cfgErrs := append([]string(nil), s.configErrors...)
	overridden := append([]string(nil), s.overridden...)
	s.mu.Unlock()

	if len(servers) == 0 && len(cfgErrs) == 0 && len(overridden) == 0 {
		return fmt.Sprintf("No MCP servers configured. Add them to %s or .pi/mcp.json.", filepath.Join(utils.GetAgentDir(), "mcp.json"))
	}
	lines := make([]string, 0, len(servers)+len(cfgErrs)+len(overridden))
	for _, srv := range servers {
		name := srv.entry.Name
		exposure := indexExposureOf(srv.entry)
		conn := srv.connection
		if conn != nil && conn.State() == ServerStateNeedsAuth {
			lines = append(lines, fmt.Sprintf("%s: needs sign-in, run /mcp login %s (%s)", name, name, exposure))
			continue
		}
		toolsSuffix := ""
		if conn != nil && conn.State() == ServerStateConnected {
			toolsSuffix = fmt.Sprintf(", %d tools", len(conn.Tools()))
		}
		state := "starting"
		if !indexIsEnabled(srv.entry) {
			state = "disabled"
		} else if conn != nil {
			if conn.State() == ServerStateDisconnected {
				state = "disconnected, reconnects on next call"
			} else {
				state = string(conn.State())
			}
		}
		errSuffix := ""
		if conn != nil && conn.State() != ServerStateConnected {
			if ce := conn.Error(); ce != nil && *ce != "" {
				errSuffix = "\n    " + strings.ReplaceAll(*ce, "\n", "\n    ")
			}
		}
		lines = append(lines, fmt.Sprintf("%s: %s%s (%s)%s", name, state, toolsSuffix, exposure, errSuffix))
	}
	for _, e := range cfgErrs {
		lines = append(lines, "config error: "+e)
	}
	for _, line := range overridden {
		lines = append(lines, "overridden: "+line)
	}
	return strings.Join(lines, "\n")
}

func (s *indexExtState) pickServer(
	ctx context.Context,
	name *string,
	ectx core.ExtensionCommandContext,
	eligible func(*indexMcpServer) bool,
	preferred func(*indexMcpServer) bool,
	noneMsg string,
) *indexMcpServer {
	if name != nil && *name != "" {
		srv := s.findServer(*name)
		if srv == nil {
			ectx.UI().Notify(fmt.Sprintf(`No MCP server named %q.`, *name), "error")
			return nil
		}
		if !eligible(srv) {
			ectx.UI().Notify(noneMsg, "error")
			return nil
		}
		return srv
	}
	s.mu.Lock()
	var candidates []*indexMcpServer
	for _, srv := range s.servers {
		if eligible(srv) {
			candidates = append(candidates, srv)
		}
	}
	s.mu.Unlock()
	if len(candidates) == 0 {
		ectx.UI().Notify(noneMsg, "info")
		return nil
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	var pref []*indexMcpServer
	for _, c := range candidates {
		if preferred(c) {
			pref = append(pref, c)
		}
	}
	if len(pref) == 1 {
		return pref[0]
	}
	names := make([]string, len(candidates))
	for i, c := range candidates {
		names[i] = c.entry.Name
	}
	choice, err := ectx.UI().Select(ctx, "MCP server", names, &core.ExtensionUIDialogOptions{Ctx: ctx})
	if err != nil || choice == nil {
		return nil
	}
	for _, c := range candidates {
		if c.entry.Name == *choice {
			return c
		}
	}
	return nil
}

type indexCmdPrompt struct {
	name    string
	ectx    core.ExtensionCommandContext
	openUrl func(url string)
}

func (p *indexCmdPrompt) ShowAuthorizationUrl(u *url.URL) {
	urlStr := ""
	if u != nil {
		urlStr = u.String()
	}
	p.ectx.UI().Notify(fmt.Sprintf("Sign in to MCP server %q in your browser:\n%s", p.name, urlStr), "info")
	if p.openUrl != nil {
		p.openUrl(urlStr)
	}
}

func (p *indexCmdPrompt) PromptForRedirectUrl(ctx context.Context) (*string, error) {
	ph := "http://127.0.0.1:.../callback?code=..."
	return p.ectx.UI().Input(
		ctx,
		fmt.Sprintf(`Waiting for sign-in to %q. If the browser cannot reach this machine, paste the URL it was redirected to.`, p.name),
		&ph,
		&core.ExtensionUIDialogOptions{Ctx: ctx},
	)
}

func (s *indexExtState) loginCommand(ctx context.Context, srv *indexMcpServer, ectx core.ExtensionCommandContext) {
	name := srv.entry.Name
	if !ectx.HasUI() {
		ectx.UI().Notify(fmt.Sprintf(`Signing in to MCP server %q requires interactive mode.`, name), "error")
		return
	}
	s.mu.Lock()
	sessCtx := s.sessionCtx
	s.mu.Unlock()

	var failure *string
	if ectx.Mode() == core.ExtensionModeTui {
		_ = ShowMcpManager(ctx, ectx, func(ui McpUi) error {
			failure = s.signInWithUi(ctx, ui, srv)
			return nil
		})
	} else {
		failure = s.signIn(srv, &indexCmdPrompt{
			name:    name,
			ectx:    ectx,
			openUrl: s.openUrl,
		}, nil)
	}
	if sessCtx == nil || sessCtx.Err() != nil {
		return
	}
	if failure != nil {
		level := "error"
		if *failure == "Sign-in cancelled." {
			level = "info"
		}
		ectx.UI().Notify(*failure, level)
		return
	}
	s.ensureDiscoveryActive(ectx)
	toolCount := 0
	if srv.connection != nil {
		toolCount = len(srv.connection.Tools())
	}
	ectx.UI().Notify(fmt.Sprintf(`Signed in to MCP server %q (%d tools).`, name, toolCount), "info")
}

func (s *indexExtState) waitForDirectServers(ctx context.Context, ectx core.ExtensionContext) {
	s.mu.Lock()
	if s.waitedForStartup {
		s.mu.Unlock()
		return
	}
	s.waitedForStartup = true
	var readys []chan struct{}
	for _, srv := range s.servers {
		if indexIsEnabled(srv.entry) && indexHasDirectTools(srv.entry) && srv.ready != nil {
			readys = append(readys, srv.ready)
		}
	}
	waitMs := s.startupWaitMs
	s.mu.Unlock()

	if len(readys) == 0 {
		return
	}

	allReady := make(chan struct{})
	go func() {
		for _, ch := range readys {
			<-ch
		}
		close(allReady)
	}()

	select {
	case <-allReady:
		return
	default:
	}

	if waitMs <= 0 {
		indexSafeNotify(ectx, "MCP servers are still connecting; their tools become available once connected.", "info")
		return
	}

	timer := time.NewTimer(time.Duration(waitMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-allReady:
	case <-timer.C:
		indexSafeNotify(ectx, "MCP servers are still connecting; their tools become available once connected.", "info")
	case <-ctx.Done():
	}
}

// CreateMcpExtension constructs the built-in MCP extension factory.
func CreateMcpExtension(options ...McpExtensionOptions) core.ExtensionFactory {
	var opts McpExtensionOptions
	if len(options) > 0 {
		opts = options[0]
	}

	return func(pi core.ExtensionAPI) error {
		openUrl := opts.OpenUrl
		if openUrl == nil {
			openUrl = utils.OpenBrowser
		}
		updateConfig := opts.UpdateConfig
		if updateConfig == nil {
			updateConfig = func(entry McpServerEntry, patch McpServerConfigPatch) error {
				target := entry.Source
				if entry.Override != nil {
					target = *entry.Override
				}
				return UpdateMcpServerConfig(target, entry.Name, patch, UpdateMcpConfigOptions{
					Override: entry.Override != nil,
				})
			}
		}
		waitMs := indexDefaultStartupWaitMs
		if opts.StartupWaitMs != nil {
			waitMs = *opts.StartupWaitMs
		}
		httpClient := opts.HTTPClient
		if httpClient == nil {
			httpClient = ai.NodeHttpProxyDefaultClient()
		}

		abortedCtx, abortedCancel := context.WithCancel(context.Background())
		abortedCancel()

		cwd, _ := os.Getwd()

		s := &indexExtState{
			pi:                 pi,
			options:            opts,
			openUrl:            openUrl,
			updateConfig:       updateConfig,
			startupWaitMs:      waitMs,
			httpClient:         httpClient,
			credentials:        opts.Credentials,
			autoEnableCodemode: true,
			sessionCwd:         cwd,
			sessionCtx:         abortedCtx,
			sessionCancel:      abortedCancel,
			listeners:          make(map[uint64]func()),
			toolOwners:         make(map[string]string),
			serverTools:        make(map[string]map[string]bool),
			definitions:        make(map[string]core.ToolDefinition),
			tokensAtSignIn:     make(map[*McpServerConnection]string),
		}

		pi.RegisterToolRenderer(func(toolName string, next func() *core.ToolRenderers) *core.ToolRenderers {
			if existing := next(); existing != nil {
				return existing
			}
			if !strings.HasPrefix(toolName, "mcp__") {
				return nil
			}
			rest := toolName[len("mcp__"):]
			sep := strings.Index(rest, "__")
			if sep <= 0 || sep+2 >= len(rest) {
				return nil
			}
			r := CreateMcpToolRenderers(rest[:sep] + "/" + rest[sep+2:])
			return &r
		})

		pi.OnSessionStart(func(_ context.Context, _ *core.SessionStartEvent, ectx core.ExtensionContext) (*core.NoResult, error) {
			loadCfg := opts.LoadConfig
			if loadCfg == nil {
				loadCfg = indexDefaultLoadConfig
			}
			loaded := loadCfg(ectx)

			sessCtx, sessCancel := context.WithCancel(context.Background())
			registered, overridden := s.registeredServers(loaded.Servers)

			servers := make([]*indexMcpServer, 0, len(loaded.Servers)+len(registered))
			for _, entry := range loaded.Servers {
				servers = append(servers, &indexMcpServer{entry: entry})
			}
			servers = append(servers, registered...)

			autoEnable := true
			if loaded.AutoEnableCodemode != nil {
				autoEnable = *loaded.AutoEnableCodemode
			}

			pendingDone := make(chan struct{})

			type startJob struct {
				srv     *indexMcpServer
				attempt uint64
				ready   chan struct{}
			}
			var jobs []startJob

			s.mu.Lock()
			prevCancel := s.sessionCancel
			var prevClosing []*McpServerConnection
			for _, oldSrv := range s.servers {
				if oldSrv.connection != nil {
					prevClosing = append(prevClosing, oldSrv.connection)
				}
			}
			s.configErrors = loaded.Errors
			s.projectConfig = loaded.ProjectConfig
			s.autoEnableCodemode = autoEnable
			s.warnedUnreachable = false
			s.waitedForStartup = false
			s.sessionCwd = ectx.Cwd()
			s.modelRegistry = ectx.ModelRegistry()
			s.sessionCtx = sessCtx
			s.sessionCancel = sessCancel
			s.configuredEntries = loaded.Servers
			s.overridden = overridden
			s.servers = servers
			s.pendingDone = pendingDone
			for _, srv := range servers {
				if indexIsEnabled(srv.entry) {
					attempt, ready, _ := s.prepareStartConnectionLocked(srv, nil)
					jobs = append(jobs, startJob{srv: srv, attempt: attempt, ready: ready})
				}
			}
			s.mu.Unlock()
			if prevCancel != nil {
				prevCancel()
			}
			for _, oldConn := range prevClosing {
				go func(c *McpServerConnection) {
					_ = c.Close()
				}(oldConn)
			}

			s.emitChange()
			s.ensureDiscoveryActive(ectx)

			if len(jobs) == 0 {
				close(pendingDone)
				s.reportProblems(ectx, nil)
				return nil, nil
			}

			s.mu.Lock()
			s.bgWg.Add(1)
			s.mu.Unlock()
			go func() {
				defer close(pendingDone)
				defer s.bgWg.Done()
				var wg sync.WaitGroup
				for _, job := range jobs {
					wg.Add(1)
					go func(j startJob) {
						defer wg.Done()
						s.runStartConnection(j.srv, j.attempt, j.ready, nil, sessCtx)
					}(job)
				}
				wg.Wait()
				if sessCtx.Err() == nil {
					s.reportProblems(ectx, nil)
				}
			}()
			return nil, nil
		})

		pi.OnBeforeAgentStart(func(ctx context.Context, event *core.BeforeAgentStartEvent, ectx core.ExtensionContext) (*core.BeforeAgentStartEventResult, error) {
			s.waitForDirectServers(ctx, ectx)
			s.mu.Lock()
			listings := make([]McpServerListing, len(s.servers))
			for i, srv := range s.servers {
				var inst *string
				if srv.connection != nil {
					inst = srv.connection.Instructions()
				}
				listings[i] = McpServerListing{Entry: srv.entry, Instructions: inst}
			}
			s.mu.Unlock()

			section := RenderServersSection(listings)
			if event != nil {
				if event.SystemPromptOptions.Sections == nil {
					event.SystemPromptOptions.Sections = omap.NewMap[string, string]()
				}
				if section != nil {
					event.SystemPromptOptions.Sections.Set(McpServersSection, *section)
				} else {
					event.SystemPromptOptions.Sections.Delete(McpServersSection)
				}
			}
			return nil, nil
		})

		pi.OnToolCall(func(_ context.Context, event *core.ToolCallEvent, ectx core.ExtensionContext) (*core.ToolCallEventResult, error) {
			if event == nil {
				return nil, nil
			}
			s.regMu.Lock()
			allTools := pi.GetAllTools()
			s.regMu.Unlock()
			var found *core.ToolInfo
			for _, t := range allTools {
				if t.Name == event.ToolName {
					tc := t
					found = &tc
					break
				}
			}
			if found == nil {
				return nil, nil
			}

			s.mu.Lock()
			var readyServers []*indexMcpServer
			for _, srv := range s.servers {
				if indexIsEnabled(srv.entry) && srv.ready != nil {
					readyServers = append(readyServers, srv)
				}
			}
			owner := ""
			if fullOwner, ok := s.toolOwners[event.ToolName]; ok {
				if sep := strings.IndexByte(fullOwner, '\x00'); sep >= 0 {
					owner = fullOwner[:sep]
				} else {
					owner = fullOwner
				}
			}
			s.mu.Unlock()

			if len(readyServers) == 0 {
				return nil, nil
			}

			var waiting []*indexMcpServer
			if found.ReachesTools != nil {
				reach := found.ReachesTools(event.Input)
				for _, srv := range readyServers {
					if indexReachesServer(reach, srv.entry.Name) {
						waiting = append(waiting, srv)
					}
				}
			} else if indexResourceToolNames[found.Name] {
				waiting = readyServers
			} else {
				for _, srv := range readyServers {
					if srv.entry.Name == owner {
						waiting = append(waiting, srv)
					}
				}
			}
			s.waitForServers(ectx.Signal(), waiting)
			return nil, nil
		})

		pi.OnTurnStart(func(ctx context.Context, _ *core.TurnStartEvent, ectx core.ExtensionContext) (*core.NoResult, error) {
			s.mu.Lock()
			hasPendingSignIn := len(s.tokensAtSignIn) > 0
			s.mu.Unlock()
			if hasPendingSignIn {
				s.reconnectSignedIn(ctx, ectx)
			}
			return nil, nil
		})

		pi.OnMcpServersChange(func(ctx context.Context, _ *core.McpServersChangeEvent, ectx core.ExtensionContext) (*core.NoResult, error) {
			s.mu.Lock()
			sessCtx := s.sessionCtx
			configured := append([]McpServerEntry(nil), s.configuredEntries...)
			s.mu.Unlock()
			if sessCtx == nil || sessCtx.Err() != nil {
				return nil, nil
			}

			registered, overridden := s.registeredServers(configured)
			nextMap := make(map[string]*indexMcpServer, len(registered))
			for _, r := range registered {
				nextMap[r.entry.Name] = r
			}

			s.mu.Lock()
			var kept []*indexMcpServer
			var removed []*indexMcpServer
			for _, srv := range s.servers {
				if srv.entry.Scope == "extension" {
					nr := nextMap[srv.entry.Name]
					if nr == nil || nr.registeredConfig != srv.registeredConfig {
						removed = append(removed, srv)
						continue
					}
				}
				kept = append(kept, srv)
			}
			var added []*indexMcpServer
			for _, nr := range registered {
				exists := false
				for _, k := range kept {
					if k.entry.Name == nr.entry.Name {
						exists = true
						break
					}
				}
				if !exists {
					added = append(added, nr)
				}
			}
			s.servers = append(kept, added...)
			s.overridden = overridden

			type startJob struct {
				srv     *indexMcpServer
				attempt uint64
				ready   chan struct{}
			}
			var connecting []*indexMcpServer
			var jobs []startJob
			for _, a := range added {
				if indexIsEnabled(a.entry) {
					connecting = append(connecting, a)
					attempt, ready, _ := s.prepareStartConnectionLocked(a, nil)
					jobs = append(jobs, startJob{srv: a, attempt: attempt, ready: ready})
				}
			}
			s.mu.Unlock()

			for _, r := range removed {
				s.hideTools(r.entry.Name)
			}
			s.emitChange()
			s.ensureDiscoveryActive(ectx)

			var rmWg sync.WaitGroup
			for _, r := range removed {
				if r.connection != nil {
					rmWg.Add(1)
					go func(c *McpServerConnection) {
						defer rmWg.Done()
						_ = c.Close()
					}(r.connection)
				}
			}
			rmWg.Wait()

			if sessCtx.Err() != nil || len(jobs) == 0 {
				return nil, nil
			}

			var addWg sync.WaitGroup
			for _, job := range jobs {
				addWg.Add(1)
				go func(j startJob) {
					defer addWg.Done()
					s.runStartConnection(j.srv, j.attempt, j.ready, nil, sessCtx)
				}(job)
			}
			addWg.Wait()

			if sessCtx.Err() != nil {
				for _, srv := range connecting {
					if srv.connection != nil {
						_ = srv.connection.Close()
					}
				}
				return nil, nil
			}
			s.reportProblems(ectx, connecting)
			return nil, nil
		})

		pi.OnSessionShutdown(func(_ context.Context, _ *core.SessionShutdownEvent, _ core.ExtensionContext) (*core.NoResult, error) {
			s.mu.Lock()
			cancel := s.sessionCancel
			var closing []*McpServerConnection
			for _, srv := range s.servers {
				if srv.connection != nil {
					closing = append(closing, srv.connection)
				}
			}
			s.servers = nil
			s.mu.Unlock()

			if cancel != nil {
				cancel()
			}
			s.emitChange()

			var closeWg sync.WaitGroup
			for _, conn := range closing {
				closeWg.Add(1)
				go func(c *McpServerConnection) {
					defer closeWg.Done()
					_ = c.Close()
				}(conn)
			}
			closeWg.Wait()
			s.bgWg.Wait()
			return nil, nil
		})

		desc := "Manage MCP servers: sign in, reconnect, enable or disable, and change exposure"
		pi.RegisterCommand("mcp", core.RegisteredCommand{
			Description: &desc,
			GetArgumentCompletions: func(_ context.Context, prefix string) ([]tui.AutocompleteItem, error) {
				trimmed := strings.TrimLeft(prefix, " \t\r\n")
				parts := strings.Fields(trimmed)
				if len(parts) > 2 {
					return nil, nil
				}
				action := ""
				if len(parts) >= 1 {
					action = parts[0]
				}
				hasServerSpace := len(parts) >= 2 || (len(parts) == 1 && len(trimmed) < len(prefix) && len(parts) > 1) || (len(parts) == 1 && (strings.HasSuffix(prefix, " ") || strings.HasSuffix(prefix, "\t")))
				if !hasServerSpace {
					var items []tui.AutocompleteItem
					for _, item := range []string{"login", "logout", "reconnect"} {
						if strings.HasPrefix(item, action) {
							items = append(items, tui.AutocompleteItem{
								Value: item + " ",
								Label: item,
							})
						}
					}
					if len(items) == 0 {
						return nil, nil
					}
					return items, nil
				}
				if action != "login" && action != "logout" && action != "reconnect" {
					return nil, nil
				}
				serverPrefix := ""
				if len(parts) >= 2 {
					serverPrefix = parts[1]
				}
				s.mu.Lock()
				servers := append([]*indexMcpServer(nil), s.servers...)
				s.mu.Unlock()

				var items []tui.AutocompleteItem
				for _, cand := range servers {
					eligible := cand.connection != nil && cand.connection.OAuthUrl() != nil
					if action == "reconnect" {
						eligible = cand.connection != nil
					}
					if !eligible || !strings.HasPrefix(cand.entry.Name, serverPrefix) {
						continue
					}
					d := indexDescribeState(cand, true)
					items = append(items, tui.AutocompleteItem{
						Value:       action + " " + cand.entry.Name,
						Label:       cand.entry.Name,
						Description: &d,
					})
				}
				if len(items) == 0 {
					return nil, nil
				}
				return items, nil
			},
			Handler: func(ctx context.Context, args string, ectx core.ExtensionCommandContext) error {
				fields := strings.Fields(strings.TrimSpace(args))
				if len(fields) == 0 {
					if ectx.Mode() == core.ExtensionModeTui {
						return ShowMcpManager(ctx, ectx, func(ui McpUi) error {
							s.manage(ctx, ui, ectx)
							return nil
						})
					}
					s.mu.Lock()
					p := s.pendingDone
					s.mu.Unlock()
					if p != nil {
						select {
						case <-p:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					ectx.UI().Notify(s.formatStatus(), "info")
					return nil
				}
				if len(fields) > 2 {
					ectx.UI().Notify(indexUsage, "warning")
					return nil
				}
				s.mu.Lock()
				p := s.pendingDone
				s.mu.Unlock()
				if p != nil {
					select {
					case <-p:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				action := fields[0]
				var namePtr *string
				if len(fields) >= 2 {
					n := fields[1]
					namePtr = &n
				}
				usesOAuth := func(srv *indexMcpServer) bool {
					return srv.connection != nil && srv.connection.OAuthUrl() != nil
				}
				oauthPreferred := func(srv *indexMcpServer) bool {
					return srv.connection != nil && srv.connection.State() == ServerStateNeedsAuth
				}
				oauthNone := "No enabled MCP server uses OAuth. Only HTTP servers without an Authorization header do."

				switch action {
				case "login":
					srv := s.pickServer(ctx, namePtr, ectx, usesOAuth, oauthPreferred, oauthNone)
					if srv != nil {
						s.loginCommand(ctx, srv, ectx)
					}
				case "logout":
					srv := s.pickServer(ctx, namePtr, ectx, usesOAuth, oauthPreferred, oauthNone)
					if srv == nil {
						return nil
					}
					removed := s.signOut(ctx, srv)
					if removed {
						ectx.UI().Notify(fmt.Sprintf(`Signed out of MCP server %q.`, srv.entry.Name), "info")
					} else {
						ectx.UI().Notify(fmt.Sprintf(`No stored credentials for MCP server %q.`, srv.entry.Name), "info")
					}
				case "reconnect":
					srv := s.pickServer(
						ctx,
						namePtr,
						ectx,
						func(c *indexMcpServer) bool { return c.connection != nil },
						func(c *indexMcpServer) bool {
							if c.connection == nil {
								return false
							}
							st := c.connection.State()
							return st == ServerStateFailed || st == ServerStateDisconnected
						},
						"No enabled MCP server to reconnect.",
					)
					if srv == nil {
						return nil
					}
					if failure := s.reconnect(ctx, srv); failure != nil {
						ectx.UI().Notify(*failure, "error")
					} else {
						s.ensureDiscoveryActive(ectx)
						ectx.UI().Notify(fmt.Sprintf(`Reconnected to MCP server %q (%s).`, srv.entry.Name, indexDescribeState(srv, true)), "info")
					}
				default:
					ectx.UI().Notify(indexUsage, "warning")
				}
				return nil
			},
		})
		return nil
	}
}

// Extension registers the built-in MCP extension on pi.
func Extension(pi core.ExtensionAPI) error {
	return CreateMcpExtension()(pi)
}
