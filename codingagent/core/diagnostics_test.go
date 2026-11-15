package core

import (
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestResourceDiagnosticJSON(t *testing.T) {
	path := "/tmp/ext.go"
	winnerSrc := "user"
	loserSrc := "project"

	diag := ResourceDiagnostic{
		Type:    ResourceDiagnosticTypeCollision,
		Message: "duplicate extension",
		Path:    &path,
		Collision: &ResourceCollision{
			ResourceType: ResourceCollisionTypeExtension,
			Name:         "demo",
			WinnerPath:   "/a/demo",
			LoserPath:    "/b/demo",
			WinnerSource: &winnerSrc,
			LoserSource:  &loserSrc,
		},
	}

	encoded, err := jsonx.Stringify(diag)
	if err != nil {
		t.Fatalf("Stringify failed: %v", err)
	}
	want := `{"type":"collision","message":"duplicate extension","path":"/tmp/ext.go","collision":{"resourceType":"extension","name":"demo","winnerPath":"/a/demo","loserPath":"/b/demo","winnerSource":"user","loserSource":"project"}}`
	if encoded != want {
		t.Fatalf("encoded = %s, want %s", encoded, want)
	}

	var decoded ResourceDiagnostic
	if err := jsonx.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.Type != ResourceDiagnosticTypeCollision || decoded.Collision == nil || decoded.Collision.Name != "demo" {
		t.Fatalf("unexpected decoded diagnostic: %+v", decoded)
	}

	minimal := ResourceDiagnostic{
		Type:    ResourceDiagnosticTypeWarning,
		Message: "heads up",
	}
	minEncoded, err := jsonx.Stringify(minimal)
	if err != nil {
		t.Fatalf("Stringify minimal failed: %v", err)
	}
	if minEncoded != `{"type":"warning","message":"heads up"}` {
		t.Fatalf("minimal encoded = %s", minEncoded)
	}
}
