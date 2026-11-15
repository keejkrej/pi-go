// Ported from packages/coding-agent/src/core/slash-commands.ts (pi v1.5.2).

package core

import "github.com/keejkrej/pi-go/codingagent/utils"

// SlashCommandSource identifies the origin of a discovered slash command.
type SlashCommandSource = string

const (
	SlashCommandSourceExtension SlashCommandSource = "extension"
	SlashCommandSourcePrompt    SlashCommandSource = "prompt"
	SlashCommandSourceSkill     SlashCommandSource = "skill"
)

// SlashCommandInfo describes a slash command exposed by an extension, prompt template, or skill.
type SlashCommandInfo struct {
	Name        string             `json:"name"`
	Description *string            `json:"description,omitzero"`
	Source      SlashCommandSource `json:"source"`
	SourceInfo  utils.SourceInfo   `json:"sourceInfo"`
}

// BuiltinSlashCommand describes a built-in interactive slash command.
type BuiltinSlashCommand struct {
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	ArgumentHint *string `json:"argumentHint,omitzero"`
}

func scoStrPtr(s string) *string {
	return &s
}

// BuiltinSlashCommands lists the built-in interactive slash commands.
var BuiltinSlashCommands = []BuiltinSlashCommand{
	{Name: "settings", Description: "Open settings menu"},
	{Name: "model", Description: "Select model", ArgumentHint: scoStrPtr("[provider/model]")},
	{Name: "thinking", Description: "Select reasoning effort", ArgumentHint: scoStrPtr("[level]")},
	{Name: "scoped-models", Description: "Enable/disable models for Ctrl+P cycling"},
	{Name: "export", Description: "Export session to HTML file", ArgumentHint: scoStrPtr("[path]")},
	{Name: "import", Description: "Import a JSONL session", ArgumentHint: scoStrPtr("<path>")},
	{Name: "share", Description: "Share session as a secret GitHub gist"},
	{Name: "copy", Description: "Copy last agent message to clipboard"},
	{Name: "name", Description: "Set session display name", ArgumentHint: scoStrPtr("[name]")},
	{Name: "session", Description: "Show session info and stats"},
	{Name: "changelog", Description: "Show changelog entries"},
	{Name: "hotkeys", Description: "Show all keyboard shortcuts"},
	{Name: "fork", Description: "Create a new fork from a previous message"},
	{Name: "clone", Description: "Duplicate the current session at its current position"},
	{Name: "tree", Description: "Navigate session tree (switch branches)"},
	{Name: "login", Description: "Login with OAuth provider", ArgumentHint: scoStrPtr("[provider]")},
	{Name: "logout", Description: "Logout from OAuth provider"},
	{Name: "new", Description: "Start a new session"},
	{Name: "compact", Description: "Manually compact the session context", ArgumentHint: scoStrPtr("[instructions]")},
	{Name: "resume", Description: "Resume a different session"},
	{Name: "reload", Description: "Reload keybindings, extensions, skills, prompts, and themes"},
	{Name: "trust", Description: "Save a project trust decision for future " + utils.AppName + " processes"},
	{Name: "bug", Description: "Prepare a private bug report for developers", ArgumentHint: scoStrPtr("[description]")},
	{Name: "quit", Description: "Quit " + utils.AppName},
}
