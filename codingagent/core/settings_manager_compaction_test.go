package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

func TestSettingsManagerCompactionSettingsAndModelOverrides(t *testing.T) {
	model := &ai.Model{
		Provider: "anthropic",
		Id:       "claude-sonnet-4-5",
	}

	t.Run("defaults", func(t *testing.T) {
		sm := SettingsManagerInMemory()
		cfg, err := sm.GetCompactionSettings(model)
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.Enabled || cfg.ReserveTokens != SettingsDefaults.Compaction.ReserveTokens || cfg.KeepRecentTokens != SettingsDefaults.Compaction.KeepRecentTokens {
			t.Fatalf("unexpected defaults: %+v", cfg)
		}
	})

	t.Run("modelOverrides take precedence and zero is valid", func(t *testing.T) {
		cwd := t.TempDir()
		agentDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(cwd, utils.ConfigDirName), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{
			"compaction": {
				"reserveTokens": 12000,
				"keepRecentTokens": 6000,
				"modelOverrides": {
					"anthropic/claude-sonnet-4-5": {
						"reserveTokens": 0,
						"keepRecentTokens": 4000
					}
				}
			}
		}`), 0o644); err != nil {
			t.Fatal(err)
		}

		sm := SettingsManagerCreate(cwd, agentDir)
		cfg, err := sm.GetCompactionSettings(model)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ReserveTokens != 0 || cfg.KeepRecentTokens != 4000 {
			t.Fatalf("override cfg = %+v", cfg)
		}

		otherModel := &ai.Model{Provider: "openai", Id: "gpt-5"}
		otherCfg, err := sm.GetCompactionSettings(otherModel)
		if err != nil {
			t.Fatal(err)
		}
		if otherCfg.ReserveTokens != 12000 || otherCfg.KeepRecentTokens != 6000 {
			t.Fatalf("otherCfg = %+v", otherCfg)
		}
	})

	t.Run("invalid compaction values return descriptive errors", func(t *testing.T) {
		for _, jsonStr := range []string{
			`{"compaction":{"reserveTokens":-1}}`,
			`{"compaction":{"keepRecentTokens":1.5}}`,
			`{"compaction":{"reserveTokens":"bad"}}`,
			`{"compaction":{"modelOverrides":{"anthropic/claude-sonnet-4-5":"invalid"}}}`,
			`{"compaction":{"modelOverrides":{"anthropic/claude-sonnet-4-5":{"keepRecentTokens":-5}}}}`,
		} {
			cwd := t.TempDir()
			agentDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(jsonStr), 0o644); err != nil {
				t.Fatal(err)
			}
			sm := SettingsManagerCreate(cwd, agentDir)
			if _, err := sm.GetCompactionSettings(model); err == nil {
				t.Fatalf("expected error for %s", jsonStr)
			}
		}
	})
}
