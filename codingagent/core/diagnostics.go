// Ported from packages/coding-agent/src/core/diagnostics.ts (pi v1.5.2).

package core

// ResourceCollisionType identifies which resource namespace collided.
type ResourceCollisionType string

const (
	ResourceCollisionTypeExtension ResourceCollisionType = "extension"
	ResourceCollisionTypeSkill     ResourceCollisionType = "skill"
	ResourceCollisionTypePrompt    ResourceCollisionType = "prompt"
	ResourceCollisionTypeTheme     ResourceCollisionType = "theme"
)

// ResourceCollision describes a name collision between two discovered resources.
type ResourceCollision struct {
	ResourceType ResourceCollisionType `json:"resourceType"`
	Name         string                `json:"name"`
	WinnerPath   string                `json:"winnerPath"`
	LoserPath    string                `json:"loserPath"`
	WinnerSource *string               `json:"winnerSource,omitzero"`
	LoserSource  *string               `json:"loserSource,omitzero"`
}

// ResourceDiagnosticType classifies a resource loading diagnostic.
type ResourceDiagnosticType string

const (
	ResourceDiagnosticTypeWarning   ResourceDiagnosticType = "warning"
	ResourceDiagnosticTypeError     ResourceDiagnosticType = "error"
	ResourceDiagnosticTypeCollision ResourceDiagnosticType = "collision"
)

// ResourceDiagnostic records a warning, error, or collision encountered while loading resources.
type ResourceDiagnostic struct {
	Type      ResourceDiagnosticType `json:"type"`
	Message   string                 `json:"message"`
	Path      *string                `json:"path,omitzero"`
	Collision *ResourceCollision     `json:"collision,omitzero"`
}
