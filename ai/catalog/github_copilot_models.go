// Ported from packages/ai/src/providers/github-copilot.models.ts (pi v1.0.0).

package catalog

import "github.com/keejkrej/pi-go/ai"

var gcmChat, gcmImage, gcmClassifier = mgLoadProviderCatalog(ai.KnownProviderGithubCopilot, "github-copilot.json")

// GithubCopilotModels is the chat model catalog for github-copilot.
var GithubCopilotModels ai.ChatModelCatalog = gcmChat

// GithubCopilotImageModels is the image model catalog for github-copilot.
var GithubCopilotImageModels ai.ImageModelCatalog = gcmImage

// GithubCopilotClassifierModels is the classifier model catalog for github-copilot.
var GithubCopilotClassifierModels ai.ClassifierModelCatalog = gcmClassifier
