// Ported from packages/coding-agent/src/experimental/client.ts (pi v1.0.0).

package experimental

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
	"github.com/keejkrej/pi-go/protocol"
)

// ClientResultKind identifies which action RunClient performed.
type ClientResultKind string

const (
	ClientResultKindList     ClientResultKind = "list"
	ClientResultKindAttached ClientResultKind = "attached"
	ClientResultKindPrompted ClientResultKind = "prompted"
)

// ClientResult returns the outcome of RunClient for non-interactive CLI invocations.
type ClientResult struct {
	Kind      ClientResultKind          `json:"kind"`
	Sessions  []services.SessionAddress `json:"sessions,omitzero"`
	ServerId  protocol.ServerId         `json:"serverId,omitzero"`
	SessionId string                    `json:"sessionId,omitzero"`
	Text      string                    `json:"text,omitzero"`
}

// RunClientOptions configures RunClient.
type RunClientOptions struct {
	Directory string
}

// RunClient discovers servers, then lists sessions, attaches to one, or creates one for a non-interactive prompt.
func RunClient(command experimentalcli.ClientCommand, options ...RunClientOptions) (ClientResult, error) {
	var openOpts OpenClientRuntimeOptions
	if len(options) > 0 {
		openOpts.Directory = options[0].Directory
	}
	runtime, err := OpenClientRuntime(command, openOpts)
	if err != nil {
		return ClientResult{}, err
	}
	defer func() { _ = runtime.Dispose() }()

	discovered := make([]*ActivatedClientRuntimeServer, 0, len(runtime.Servers))
	for _, srv := range runtime.Servers {
		activated, err := ActivateBuiltinClientServices(srv)
		if err != nil {
			return ClientResult{}, err
		}
		discovered = append(discovered, activated)
	}

	var sessionId *string
	if command.SessionId != nil {
		s := *command.SessionId
		sessionId = &s
	}

	if sessionId == nil && command.Prompt == nil {
		var sessions []services.SessionAddress
		for _, d := range discovered {
			dirState, ok := d.Directory.State().Value()
			if !ok {
				continue
			}
			for _, s := range dirState.Sessions {
				sessions = append(sessions, services.SessionAddress{
					ServerId:  d.Route.ServerId,
					SessionId: s.SessionId,
				})
			}
		}
		sort.Slice(sessions, func(i, j int) bool {
			if sessions[i].ServerId != sessions[j].ServerId {
				return sessions[i].ServerId < sessions[j].ServerId
			}
			return sessions[i].SessionId < sessions[j].SessionId
		})
		return ClientResult{
			Kind:     ClientResultKindList,
			Sessions: sessions,
		}, nil
	}

	var match *ActivatedClientRuntimeServer
	var resolvedSessionId string
	if sessionId == nil {
		if len(discovered) != 1 {
			return ClientResult{}, errors.New("Client prompt requires exactly one discovered server to create a Session")
		}
		match = discovered[0]
		summary, err := match.Management.Create(services.SessionCreateOptions{}, chord.BackgroundContext)
		if err != nil {
			return ClientResult{}, err
		}
		resolvedSessionId = summary.SessionId
	} else {
		selectedId := *sessionId
		var matches []*ActivatedClientRuntimeServer
		for _, cand := range discovered {
			dirState, ok := cand.Directory.State().Value()
			if !ok {
				continue
			}
			for _, s := range dirState.Sessions {
				if s.SessionId == selectedId {
					matches = append(matches, cand)
					break
				}
			}
		}
		if len(matches) > 1 {
			return ClientResult{}, fmt.Errorf("Session %s is available from more than one server", selectedId)
		}
		if len(matches) == 1 {
			match = matches[0]
		} else {
			isRadius := command.Connect != nil && command.Connect.Transport == experimentalcli.TransportTypeRadius
			if isRadius || command.Prompt == nil || len(discovered) != 1 {
				return ClientResult{}, fmt.Errorf("No discovered server contains session %s", selectedId)
			}
			match = discovered[0]
			if _, err := match.Management.Create(services.SessionCreateOptions{Id: &selectedId}, chord.BackgroundContext); err != nil {
				return ClientResult{}, err
			}
		}
		resolvedSessionId = selectedId
	}

	var resolvedPkgPaths []string
	if command.PluginPackages != nil {
		resolvedPkgPaths = make([]string, 0, len(command.PluginPackages))
		for _, pkgPath := range command.PluginPackages {
			abs, err := filepath.Abs(pkgPath)
			if err != nil {
				return ClientResult{}, err
			}
			resolvedPkgPaths = append(resolvedPkgPaths, abs)
		}
	}
	if _, err := match.Plugins.PrepareSession(services.PrepareSessionPluginsRequest{
		SessionId:    resolvedSessionId,
		PackagePaths: resolvedPkgPaths,
	}, chord.BackgroundContext); err != nil {
		return ClientResult{}, err
	}
	if err := match.Management.Attach(resolvedSessionId, chord.BackgroundContext); err != nil {
		return ClientResult{}, err
	}

	if command.Prompt == nil {
		return ClientResult{
			Kind:      ClientResultKindAttached,
			ServerId:  match.Route.ServerId,
			SessionId: resolvedSessionId,
		}, nil
	}

	resp, err := match.Agent.Prompt(services.AgentPromptRequest{
		Message: *command.Prompt,
	}, chord.BackgroundContext)
	if err != nil {
		return ClientResult{}, err
	}
	if !resp.Accepted || resp.OperationId == nil {
		msg := "Prompt rejected"
		if resp.Error != nil && resp.Error.Message != "" {
			msg = resp.Error.Message
		}
		return ClientResult{}, errors.New(msg)
	}
	result, err := match.Agent.WaitForPrompt(*resp.OperationId, chord.BackgroundContext)
	if err != nil {
		return ClientResult{}, err
	}
	if result.Status == "unanswered" {
		reason := "unknown"
		if result.Reason != nil {
			reason = *result.Reason
		}
		return ClientResult{}, fmt.Errorf("Prompt was not answered: %s", reason)
	}
	text := ""
	if result.Text != nil {
		text = *result.Text
	}
	return ClientResult{
		Kind:      ClientResultKindPrompted,
		ServerId:  match.Route.ServerId,
		SessionId: resolvedSessionId,
		Text:      text,
	}, nil
}
