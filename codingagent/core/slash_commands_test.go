package core

import "testing"

func TestBuiltinSlashCommands(t *testing.T) {
	if len(BuiltinSlashCommands) == 0 {
		t.Fatal("expected non-empty BuiltinSlashCommands")
	}
	seen := make(map[string]bool)
	for _, cmd := range BuiltinSlashCommands {
		if cmd.Name == "" {
			t.Fatal("builtin slash command has empty Name")
		}
		if cmd.Description == "" {
			t.Fatalf("builtin slash command %q has empty Description", cmd.Name)
		}
		if seen[cmd.Name] {
			t.Fatalf("duplicate builtin slash command %q", cmd.Name)
		}
		seen[cmd.Name] = true
	}
	for _, required := range []string{"settings", "model", "compact", "export", "tree", "reload", "quit"} {
		if !seen[required] {
			t.Fatalf("expected builtin slash command %q in BuiltinSlashCommands", required)
		}
	}
	if SlashCommandSourceExtension != "extension" || SlashCommandSourcePrompt != "prompt" || SlashCommandSourceSkill != "skill" {
		t.Fatalf("unexpected SlashCommandSource constants: ext=%q prompt=%q skill=%q", SlashCommandSourceExtension, SlashCommandSourcePrompt, SlashCommandSourceSkill)
	}
}
