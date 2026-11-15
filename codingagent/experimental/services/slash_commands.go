// Ported from packages/coding-agent/src/experimental/services/slash-commands.ts (pi v1.0.0).

package services

import (
	"fmt"

	"github.com/keejkrej/pi-go/chord"
)

// SlashCommandCompletion describes one autocomplete candidate for a slash command argument.
type SlashCommandCompletion struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitzero"`
}

// SlashCommandRunResult is returned by SlashCommandContribution.Run (AgentOperationResponse, AgentQueueResponse, or nil).
type SlashCommandRunResult interface {
	isSlashCommandRunResult()
}

// SlashCommandContribution defines a single presentation slash command.
type SlashCommandContribution struct {
	Name                   string
	Description            string
	ArgumentHint           string
	GetArgumentCompletions func(argumentPrefix string) ([]SlashCommandCompletion, error)
	Run                    func(args string, ctx chord.Context) (SlashCommandRunResult, error)
}

// SlashCommands manages process-local slash command contributions.
type SlashCommands interface {
	Register(command SlashCommandContribution) (func(), error)
	// Replace stages a same-name replacement while the previous facet generation retires.
	Replace(command SlashCommandContribution) (func(), error)
	List() []SlashCommandContribution
	Subscribe(listener func(commands []SlashCommandContribution)) func()
}

// SlashCommandsService is the process-local Chord service token for SlashCommands.
var SlashCommandsService = connDefineService[SlashCommands]("pi.local.slash-commands", true)

// AsSlashCommands unwraps a Chord handle or concrete implementation to SlashCommands.
func AsSlashCommands(raw any) SlashCommands {
	if raw == nil {
		return nil
	}
	if impl, ok := raw.(SlashCommands); ok {
		return impl
	}
	if view, ok := raw.(connServiceView); ok {
		return &scClient{view: view}
	}
	panic(fmt.Sprintf("services: unsupported SlashCommands handle %T", raw))
}

type scClient struct {
	view connServiceView
}

func (c *scClient) Register(command SlashCommandContribution) (func(), error) {
	raw, err := c.view.Call("Register", command)
	if err != nil {
		return nil, err
	}
	unsub, _ := raw.(func())
	return unsub, nil
}

func (c *scClient) Replace(command SlashCommandContribution) (func(), error) {
	raw, err := c.view.Call("Replace", command)
	if err != nil {
		return nil, err
	}
	unsub, _ := raw.(func())
	return unsub, nil
}

func (c *scClient) List() []SlashCommandContribution {
	raw, err := c.view.Call("List")
	if err != nil {
		return nil
	}
	list, _ := raw.([]SlashCommandContribution)
	return list
}

func (c *scClient) Subscribe(listener func(commands []SlashCommandContribution)) func() {
	raw, err := c.view.Call("Subscribe", listener)
	if err != nil {
		return func() {}
	}
	unsub, _ := raw.(func())
	if unsub == nil {
		return func() {}
	}
	return unsub
}
