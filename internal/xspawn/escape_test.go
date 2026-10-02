package xspawn

import (
	"encoding/json"
	"os"
	"testing"
)

func TestEscapeVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/escape.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Escape []struct {
			In             string `json:"in"`
			Command        string `json:"command"`
			Argument       string `json:"argument"`
			ArgumentDouble string `json:"argumentDouble"`
		} `json:"escape"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Escape) == 0 {
		t.Fatal("no escape vectors")
	}
	for _, row := range file.Escape {
		if got := escapeCommand(row.In); got != row.Command {
			t.Errorf("command %q = %q, want %q", row.In, got, row.Command)
		}
		if got := escapeArgument(row.In, false); got != row.Argument {
			t.Errorf("argument %q = %q, want %q", row.In, got, row.Argument)
		}
		if got := escapeArgument(row.In, true); got != row.ArgumentDouble {
			t.Errorf("argument double %q = %q, want %q", row.In, got, row.ArgumentDouble)
		}
	}
}
