// Ported from packages/coding-agent/src/core/model-resolver.ts (pi v1.5.2).

package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/agent"
	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/keejkrej/pi-go/internal/minimatch"
)

// DefaultModelPerProvider maps built-in provider ids to their preferred default chat model id.
var DefaultModelPerProvider = map[ai.KnownProvider]string{
	"amazon-bedrock":                "us.anthropic.claude-opus-5-5",
	"ant-ling":                      "Ring-2.6-1T",
	"anthropic":                     "claude-opus-5-5",
	"openai":                        "gpt-6.1-sol",
	"azure":                         "gpt-6.1-sol",
	"openai-codex":                  "gpt-6.1-sol",
	"radius":                        "balanced",
	"nvidia":                        "nvidia/nemotron-3.5-super-vl-120b-a12b",
	"deepseek":                      "deepseek-v4-pro",
	"google":                        "gemini-4-argon",
	"google-vertex":                 "gemini-4-argon",
	"github-copilot":                "gpt-6.1-sol",
	"openrouter":                    "moonshotai/kimi-k2.7-code",
	"vercel-ai-gateway":             "zai/glm-5.3",
	"xai":                           "grok-4.7",
	"groq":                          "openai/gpt-oss-120b",
	"cerebras":                      "gpt-oss-120b",
	"zai":                           "glm-5.3",
	"zai-coding-cn":                 "glm-5.3",
	"mistral":                       "devstral-medium-latest",
	"minimax":                       "MiniMax-M3",
	"minimax-cn":                    "MiniMax-M3",
	"moonshotai":                    "kimi-k2.7-code",
	"moonshotai-cn":                 "kimi-k2.7-code",
	"huggingface":                   "moonshotai/Kimi-K2.7-Code",
	"fireworks":                     "accounts/fireworks/models/kimi-k3",
	"together":                      "moonshotai/Kimi-K3",
	"baseten":                       "zai-org/GLM-5.3",
	"opencode":                      "kimi-k2.7-code",
	"opencode-go":                   "kimi-k3.1-pro",
	"kimi-coding":                   "kimi-for-coding",
	"meta":                          "muse-spark-1.3",
	"cloudflare-workers-ai":         "@cf/moonshotai/kimi-k2.7-code",
	"cloudflare-ai-gateway":         "workers-ai/@cf/moonshotai/kimi-k2.7-code",
	"qwen-token-plan":               "qwen3.8-max",
	"qwen-token-plan-cn":            "qwen3.8-max",
	"qwen-token-plan-individual":    "qwen3.8-max",
	"qwen-token-plan-individual-cn": "qwen3.8-max",
	"xiaomi":                        "mimo-v2.6-pro",
	"xiaomi-token-plan-cn":          "mimo-v2.6-pro",
	"xiaomi-token-plan-ams":         "mimo-v2.6-pro",
	"xiaomi-token-plan-sgp":         "mimo-v2.6-pro",
}

var mreDefaultProviderOrder = []ai.KnownProvider{
	"amazon-bedrock",
	"ant-ling",
	"anthropic",
	"openai",
	"azure",
	"openai-codex",
	"radius",
	"nvidia",
	"deepseek",
	"google",
	"google-vertex",
	"github-copilot",
	"openrouter",
	"vercel-ai-gateway",
	"xai",
	"groq",
	"cerebras",
	"zai",
	"zai-coding-cn",
	"mistral",
	"minimax",
	"minimax-cn",
	"moonshotai",
	"moonshotai-cn",
	"huggingface",
	"fireworks",
	"together",
	"baseten",
	"opencode",
	"opencode-go",
	"kimi-coding",
	"meta",
	"cloudflare-workers-ai",
	"cloudflare-ai-gateway",
	"qwen-token-plan",
	"qwen-token-plan-cn",
	"qwen-token-plan-individual",
	"qwen-token-plan-individual-cn",
	"xiaomi",
	"xiaomi-token-plan-cn",
	"xiaomi-token-plan-ams",
	"xiaomi-token-plan-sgp",
}

// ScopedModel pairs a resolved model with an optional thinking level override.
type ScopedModel struct {
	Model         *ai.Model            `json:"model"`
	ThinkingLevel *agent.ThinkingLevel `json:"thinkingLevel,omitzero"`
}

// ThinkingSuffixMode controls how resolveModelReference interprets trailing :<thinking> suffixes.
type ThinkingSuffixMode string

const (
	ThinkingSuffixNone    ThinkingSuffixMode = "none"
	ThinkingSuffixStrict  ThinkingSuffixMode = "strict"
	ThinkingSuffixLenient ThinkingSuffixMode = "lenient"
)

// ModelReferenceRequest specifies inputs for ResolveModelReference.
type ModelReferenceRequest struct {
	Text           string
	Provider       *string
	ThinkingSuffix ThinkingSuffixMode
	Fuzzy          bool
}

// ModelReferenceStatus indicates the outcome of ResolveModelReference.
type ModelReferenceStatus string

const (
	ModelReferenceResolved  ModelReferenceStatus = "resolved"
	ModelReferenceAmbiguous ModelReferenceStatus = "ambiguous"
	ModelReferenceNotFound  ModelReferenceStatus = "not-found"
)

// ModelReferenceTarget carries fallback metadata when a reference names a valid provider with an unknown model id.
type ModelReferenceTarget struct {
	Provider       string
	ProviderModels []*ai.Model
	ModelText      string
	ModelId        string
	ThinkingLevel  *agent.ThinkingLevel
}

// ModelReferenceResult is the union outcome of ResolveModelReference.
type ModelReferenceResult struct {
	Status        ModelReferenceStatus
	Model         *ai.Model
	Candidates    []*ai.Model
	UsableCount   int
	ThinkingLevel *agent.ThinkingLevel
	Warning       *string
	Target        *ModelReferenceTarget
}

var mreDatedSuffixRe = jsre.MustCompile(`-\d{8}$`, "")

func mreIsValidThinkingLevel(level string) bool {
	switch agent.ThinkingLevel(level) {
	case ai.ModelThinkingLevelOff,
		ai.ModelThinkingLevelMinimal,
		ai.ModelThinkingLevelLow,
		ai.ModelThinkingLevelMedium,
		ai.ModelThinkingLevelHigh,
		ai.ModelThinkingLevelXhigh,
		ai.ModelThinkingLevelMax:
		return true
	default:
		return false
	}
}

func mreSplitThinkingSuffix(text string) (base string, thinking agent.ThinkingLevel, ok bool) {
	colonIdx := strings.LastIndex(text, ":")
	if colonIdx == -1 {
		return "", "", false
	}
	suffix := text[colonIdx+1:]
	if !mreIsValidThinkingLevel(suffix) {
		return "", "", false
	}
	return text[:colonIdx], agent.ThinkingLevel(suffix), true
}

func mreSplitProviderPrefix(text string, models []*ai.Model) (provider string, modelText string, ok bool) {
	lowerText := strings.ToLower(text)
	var bestLen int
	for _, m := range models {
		if m == nil {
			continue
		}
		prov := string(m.Provider)
		prefix := strings.ToLower(strings.TrimSpace(prov))
		if prefix == "" || (ok && bestLen >= len(prov)) {
			continue
		}
		if len(lowerText) < len(prefix) {
			continue
		}
		slashRel := strings.Index(lowerText[len(prefix):], "/")
		if slashRel == -1 {
			continue
		}
		slashIdx := len(prefix) + slashRel
		if strings.TrimSpace(lowerText[:slashIdx]) != prefix {
			continue
		}
		rest := strings.TrimSpace(text[slashIdx+1:])
		if rest != "" {
			provider = prov
			modelText = rest
			bestLen = len(prov)
			ok = true
		}
	}
	return provider, modelText, ok
}

// ResolveModelReference resolves a model reference without letting catalog order break ties arbitrarily.
func ResolveModelReference(
	request ModelReferenceRequest,
	models []*ai.Model,
	usable func(provider string) bool,
) ModelReferenceResult {
	if usable == nil {
		usable = func(string) bool { return true }
	}
	text := strings.TrimSpace(request.Text)
	if text == "" {
		return ModelReferenceResult{Status: ModelReferenceNotFound}
	}

	pool := models
	if request.Provider != nil && *request.Provider != "" {
		constraint := strings.ToLower(*request.Provider)
		filtered := make([]*ai.Model, 0, len(models))
		for _, m := range models {
			if m != nil && strings.ToLower(string(m.Provider)) == constraint {
				filtered = append(filtered, m)
			}
		}
		pool = filtered
	}

	res := mreResolveText(text, request, pool, usable)
	if res.Status != ModelReferenceNotFound {
		return res
	}

	splitProv, splitModelText, hasSplit := mreSplitProviderPrefix(text, pool)
	var anchor *ai.Model
	if request.Provider != nil && *request.Provider != "" {
		if len(pool) > 0 {
			anchor = pool[0]
		}
	} else if hasSplit {
		for _, m := range pool {
			if m != nil && string(m.Provider) == splitProv {
				anchor = m
				break
			}
		}
	}
	if anchor == nil {
		return res
	}

	modelText := text
	if hasSplit {
		modelText = splitModelText
	}
	var suffixBase string
	var suffixLvl *agent.ThinkingLevel
	if request.ThinkingSuffix != ThinkingSuffixNone {
		if base, lvl, ok := mreSplitThinkingSuffix(modelText); ok {
			suffixBase = base
			l := lvl
			suffixLvl = &l
		}
	}
	modelId := modelText
	if suffixLvl != nil {
		modelId = suffixBase
	}

	providerModels := []*ai.Model{anchor}
	for _, m := range pool {
		if m != nil && m.Provider == anchor.Provider && m != anchor {
			providerModels = append(providerModels, m)
		}
	}
	return ModelReferenceResult{
		Status: ModelReferenceNotFound,
		Target: &ModelReferenceTarget{
			Provider:       string(anchor.Provider),
			ProviderModels: providerModels,
			ModelText:      modelText,
			ModelId:        modelId,
			ThinkingLevel:  suffixLvl,
		},
	}
}

// FindExactModelReferenceMatch performs exact provider/id or bare id lookup without thinking suffixes or fuzzy matching.
func FindExactModelReferenceMatch(modelReference string, models []*ai.Model) *ai.Model {
	res := ResolveModelReference(
		ModelReferenceRequest{
			Text:           modelReference,
			ThinkingSuffix: ThinkingSuffixNone,
			Fuzzy:          false,
		},
		models,
		func(string) bool { return true },
	)
	if res.Status == ModelReferenceResolved {
		return res.Model
	}
	return nil
}

func mreResolveText(
	text string,
	request ModelReferenceRequest,
	pool []*ai.Model,
	usable func(provider string) bool,
) ModelReferenceResult {
	if ref, ok := mreSelectReference(text, pool, request.Fuzzy, usable); ok {
		return ref
	}
	if request.Fuzzy {
		if fz, ok := mreSelectFuzzy(mreFuzzyMatches(text, pool), usable); ok {
			return fz
		}
	}
	if request.ThinkingSuffix == ThinkingSuffixNone {
		return ModelReferenceResult{Status: ModelReferenceNotFound}
	}
	if base, lvl, ok := mreSplitThinkingSuffix(text); ok {
		inner := mreResolveText(base, request, pool, usable)
		if inner.Status == ModelReferenceNotFound {
			return inner
		}
		if inner.Warning == nil {
			l := lvl
			inner.ThinkingLevel = &l
		}
		return inner
	}
	if request.ThinkingSuffix == ThinkingSuffixStrict {
		return ModelReferenceResult{Status: ModelReferenceNotFound}
	}
	colonIdx := strings.LastIndex(text, ":")
	if colonIdx == -1 {
		return ModelReferenceResult{Status: ModelReferenceNotFound}
	}
	suffix := text[colonIdx+1:]
	inner := mreResolveText(text[:colonIdx], request, pool, usable)
	if inner.Status == ModelReferenceNotFound {
		return inner
	}
	warn := fmt.Sprintf(`Invalid thinking level %q in pattern %q. Using default instead.`, suffix, text)
	inner.ThinkingLevel = nil
	inner.Warning = &warn
	return inner
}

func mreUniqueProviders(models []*ai.Model) int {
	seen := make(map[string]struct{}, len(models))
	for _, m := range models {
		if m != nil {
			seen[string(m.Provider)] = struct{}{}
		}
	}
	return len(seen)
}

func mreSelectReference(
	text string,
	pool []*ai.Model,
	fuzzy bool,
	usable func(provider string) bool,
) (ModelReferenceResult, bool) {
	lowerText := strings.ToLower(text)
	var raw []*ai.Model
	for _, m := range pool {
		if m != nil && strings.ToLower(m.Id) == lowerText {
			raw = append(raw, m)
		}
	}

	splitProv, splitModelText, hasSplit := mreSplitProviderPrefix(text, pool)
	var named *ai.Model
	if hasSplit {
		var providerModels []*ai.Model
		for _, m := range pool {
			if m != nil && string(m.Provider) == splitProv {
				providerModels = append(providerModels, m)
			}
		}
		lowerModelText := strings.ToLower(splitModelText)
		for _, m := range providerModels {
			if strings.ToLower(m.Id) == lowerModelText {
				named = m
				break
			}
		}
		if named == nil {
			for _, m := range raw {
				if string(m.Provider) == splitProv {
					named = m
					break
				}
			}
		}
		if named == nil && fuzzy {
			named = mreBestFuzzyMatch(mreFuzzyMatches(splitModelText, providerModels))
		}
	}

	others := make([]*ai.Model, 0, len(raw))
	for _, m := range raw {
		if m != named {
			others = append(others, m)
		}
	}
	candidates := others
	if named != nil {
		candidates = append([]*ai.Model{named}, others...)
	}
	if len(candidates) == 0 {
		return ModelReferenceResult{}, false
	}
	if mreUniqueProviders(candidates) == 1 {
		return ModelReferenceResult{Status: ModelReferenceResolved, Model: candidates[0]}, true
	}
	if named != nil && usable(string(named.Provider)) {
		return ModelReferenceResult{Status: ModelReferenceResolved, Model: named}, true
	}
	var usableOthers []*ai.Model
	for _, m := range others {
		if usable(string(m.Provider)) {
			usableOthers = append(usableOthers, m)
		}
	}
	if len(usableOthers) == 1 {
		return ModelReferenceResult{Status: ModelReferenceResolved, Model: usableOthers[0]}, true
	}
	if len(usableOthers) == 0 && named != nil {
		return ModelReferenceResult{Status: ModelReferenceResolved, Model: named}, true
	}
	return ModelReferenceResult{
		Status:      ModelReferenceAmbiguous,
		Candidates:  candidates,
		UsableCount: len(usableOthers),
	}, true
}

func mreFuzzyMatches(text string, models []*ai.Model) []*ai.Model {
	lowerText := strings.ToLower(text)
	var out []*ai.Model
	for _, m := range models {
		if m == nil {
			continue
		}
		if strings.Contains(strings.ToLower(m.Id), lowerText) ||
			(m.Name != "" && strings.Contains(strings.ToLower(m.Name), lowerText)) {
			out = append(out, m)
		}
	}
	return out
}

func mreSelectFuzzy(matches []*ai.Model, usable func(provider string) bool) (ModelReferenceResult, bool) {
	if len(matches) == 0 {
		return ModelReferenceResult{}, false
	}
	multipleProviders := mreUniqueProviders(matches) > 1
	var usableMatches []*ai.Model
	if multipleProviders {
		for _, m := range matches {
			if usable(string(m.Provider)) {
				usableMatches = append(usableMatches, m)
			}
		}
	}
	pool := matches
	if len(usableMatches) > 0 {
		pool = usableMatches
	}
	best := mreBestFuzzyMatch(pool)
	if best == nil {
		return ModelReferenceResult{}, false
	}
	var tied []*ai.Model
	for _, m := range pool {
		if m.Id == best.Id {
			tied = append(tied, m)
		}
	}
	if mreUniqueProviders(tied) == 1 {
		return ModelReferenceResult{Status: ModelReferenceResolved, Model: best}, true
	}
	usableCount := 0
	if len(usableMatches) > 0 {
		usableCount = len(tied)
	}
	return ModelReferenceResult{
		Status:      ModelReferenceAmbiguous,
		Candidates:  tied,
		UsableCount: usableCount,
	}, true
}

func mreBestFuzzyMatch(matches []*ai.Model) *ai.Model {
	if len(matches) == 0 {
		return nil
	}
	var aliases []*ai.Model
	for _, m := range matches {
		if mreIsAlias(m.Id) {
			aliases = append(aliases, m)
		}
	}
	candidates := matches
	if len(aliases) > 0 {
		candidates = aliases
	}
	sorted := append([]*ai.Model(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool {
		return mreCompareLocaleStrings(sorted[j].Id, sorted[i].Id) < 0
	})
	return sorted[0]
}

func mreIsAlias(id string) bool {
	return strings.HasSuffix(id, "-latest") || !mreDatedSuffixRe.Test(id)
}

func mreCompareLocaleStrings(a, b string) int {
	i, j := 0, 0
	caseDiff := 0
	for i < len(a) && j < len(b) {
		ra, wa := utf8.DecodeRuneInString(a[i:])
		rb, wb := utf8.DecodeRuneInString(b[j:])
		la, lb := unicode.ToLower(ra), unicode.ToLower(rb)
		if la != lb {
			if la < lb {
				return -1
			}
			return 1
		}
		if caseDiff == 0 && ra != rb {
			if ra > rb {
				caseDiff = -1
			} else {
				caseDiff = 1
			}
		}
		i += wa
		j += wb
	}
	if i < len(a) {
		return 1
	}
	if j < len(b) {
		return -1
	}
	return caseDiff
}

func mreBuildFallbackModel(providerModels []*ai.Model, modelId string) *ai.Model {
	base := providerModels[0]
	if defaultId, ok := DefaultModelPerProvider[base.Provider]; ok && defaultId != "" {
		for _, m := range providerModels {
			if m != nil && m.Id == defaultId {
				base = m
				break
			}
		}
	}
	cp := *base
	cp.Id = modelId
	cp.Name = modelId
	return &cp
}

// ModelScopeDiagnosticCode categorizes warnings emitted by ResolveModelScopeFromModels.
type ModelScopeDiagnosticCode string

const (
	ModelScopeNoMatch              ModelScopeDiagnosticCode = "no-match"
	ModelScopeInvalidThinkingLevel ModelScopeDiagnosticCode = "invalid-thinking-level"
	ModelScopeAmbiguous            ModelScopeDiagnosticCode = "ambiguous"
)

// ModelScopeDiagnostic describes a warning generated while resolving model scope patterns.
type ModelScopeDiagnostic struct {
	Type    string                   `json:"type"`
	Code    ModelScopeDiagnosticCode `json:"code"`
	Message string                   `json:"message"`
	Pattern string                   `json:"pattern"`
}

// ResolveModelScopeResult is returned by ResolveModelScopeFromModels and ResolveModelScopeWithDiagnostics.
type ResolveModelScopeResult struct {
	ScopedModels []ScopedModel          `json:"scopedModels"`
	Diagnostics  []ModelScopeDiagnostic `json:"diagnostics"`
}

// ResolveModelScopeFromModels resolves scope patterns (--models, enabledModels) against models.
func ResolveModelScopeFromModels(patterns []string, models []*ai.Model) ResolveModelScopeResult {
	scopedModels := make([]ScopedModel, 0)
	diagnostics := make([]ModelScopeDiagnostic, 0)

	add := func(model *ai.Model, thinkingLevel *agent.ThinkingLevel) {
		for _, existing := range scopedModels {
			if ai.ModelsAreEqual(existing.Model, model) {
				return
			}
		}
		scopedModels = append(scopedModels, ScopedModel{
			Model:         model,
			ThinkingLevel: thinkingLevel,
		})
	}
	noMatch := func(pattern string) {
		diagnostics = append(diagnostics, ModelScopeDiagnostic{
			Type:    "warning",
			Code:    ModelScopeNoMatch,
			Message: fmt.Sprintf(`No models match pattern %q`, pattern),
			Pattern: pattern,
		})
	}
	ambiguous := func(pattern string, candidates []*ai.Model) {
		provSet := make(map[string]struct{}, len(candidates))
		for _, c := range candidates {
			if c != nil {
				provSet[string(c.Provider)] = struct{}{}
			}
		}
		providers := make([]string, 0, len(provSet))
		for p := range provSet {
			providers = append(providers, p)
		}
		sort.Slice(providers, func(i, j int) bool {
			return mreCompareLocaleStrings(providers[i], providers[j]) < 0
		})
		diagnostics = append(diagnostics, ModelScopeDiagnostic{
			Type:    "warning",
			Code:    ModelScopeAmbiguous,
			Message: fmt.Sprintf(`Pattern %q matches several providers: %s. Use provider/id to choose one.`, pattern, strings.Join(providers, ", ")),
			Pattern: pattern,
		})
	}

	for _, pattern := range patterns {
		if strings.ContainsAny(pattern, "*?[") {
			globPattern := pattern
			var thinkingLevel *agent.ThinkingLevel
			if base, lvl, ok := mreSplitThinkingSuffix(pattern); ok {
				globPattern = base
				l := lvl
				thinkingLevel = &l
			}

			exact := ResolveModelReference(
				ModelReferenceRequest{
					Text:           globPattern,
					ThinkingSuffix: ThinkingSuffixNone,
					Fuzzy:          false,
				},
				models,
				func(string) bool { return true },
			)
			var matches []*ai.Model
			switch exact.Status {
			case ModelReferenceResolved:
				matches = []*ai.Model{exact.Model}
			case ModelReferenceAmbiguous:
				matches = exact.Candidates
				ambiguous(pattern, matches)
			default:
				for _, m := range models {
					if m == nil {
						continue
					}
					full := string(m.Provider) + "/" + m.Id
					if minimatch.Match(full, globPattern, minimatch.Options{NoCase: true}) ||
						minimatch.Match(m.Id, globPattern, minimatch.Options{NoCase: true}) {
						matches = append(matches, m)
					}
				}
			}
			if len(matches) == 0 {
				noMatch(pattern)
			}
			for _, m := range matches {
				add(m, thinkingLevel)
			}
			continue
		}

		res := ResolveModelReference(
			ModelReferenceRequest{
				Text:           pattern,
				ThinkingSuffix: ThinkingSuffixLenient,
				Fuzzy:          true,
			},
			models,
			func(string) bool { return true },
		)
		if res.Status == ModelReferenceNotFound {
			noMatch(pattern)
			continue
		}
		if res.Warning != nil {
			diagnostics = append(diagnostics, ModelScopeDiagnostic{
				Type:    "warning",
				Code:    ModelScopeInvalidThinkingLevel,
				Message: *res.Warning,
				Pattern: pattern,
			})
		}
		var matches []*ai.Model
		if res.Status == ModelReferenceResolved {
			matches = []*ai.Model{res.Model}
		} else {
			ambiguous(pattern, res.Candidates)
			matches = res.Candidates
		}
		for _, m := range matches {
			add(m, res.ThinkingLevel)
		}
	}

	return ResolveModelScopeResult{
		ScopedModels: scopedModels,
		Diagnostics:  diagnostics,
	}
}

// ResolveModelScopeWithDiagnostics resolves patterns against all available models in modelRuntime.
func ResolveModelScopeWithDiagnostics(
	ctx context.Context,
	patterns []string,
	modelRuntime *ModelRuntime,
) (ResolveModelScopeResult, error) {
	avail, err := modelRuntime.GetAvailable(ctx, nil, nil)
	if err != nil {
		return ResolveModelScopeResult{}, err
	}
	return ResolveModelScopeFromModels(patterns, avail), nil
}

// ResolveCliModelOptions specifies inputs for ResolveCliModel.
type ResolveCliModelOptions struct {
	CliProvider  *string
	CliModel     *string
	CliThinking  *agent.ThinkingLevel
	InlineApiKey bool
	ModelRuntime *ModelRuntime
}

// ResolveCliModelResult describes the resolved CLI model, optional thinking level, warning, or error.
type ResolveCliModelResult struct {
	Model         *ai.Model            `json:"model,omitzero"`
	ThinkingLevel *agent.ThinkingLevel `json:"thinkingLevel,omitzero"`
	Warning       *string              `json:"warning,omitzero"`
	Error         *string              `json:"error,omitzero"`
}

// ResolveCliModel resolves a single model from --provider and --model CLI flags.
func ResolveCliModel(options ResolveCliModelOptions) ResolveCliModelResult {
	if options.CliModel == nil || *options.CliModel == "" {
		return ResolveCliModelResult{}
	}
	cliModel := *options.CliModel
	models := options.ModelRuntime.GetModels(nil)
	if len(models) == 0 {
		errMsg := "No models available. Check your installation or add models to models.json."
		return ResolveCliModelResult{Error: &errMsg}
	}

	var provider *string
	if options.CliProvider != nil && *options.CliProvider != "" {
		cliProvLower := strings.ToLower(*options.CliProvider)
		for _, m := range models {
			if m != nil && strings.ToLower(string(m.Provider)) == cliProvLower {
				p := string(m.Provider)
				provider = &p
				break
			}
		}
		if provider == nil {
			errMsg := fmt.Sprintf(`Unknown provider %q. Use --list-models to see available providers/models.`, *options.CliProvider)
			return ResolveCliModelResult{Error: &errMsg}
		}
	}

	usable := func(providerId string) bool {
		return options.ModelRuntime.HasConfiguredAuth(providerId)
	}
	if options.InlineApiKey {
		usable = func(string) bool { return false }
	}

	res := ResolveModelReference(
		ModelReferenceRequest{
			Text:           cliModel,
			Provider:       provider,
			ThinkingSuffix: ThinkingSuffixStrict,
			Fuzzy:          true,
		},
		models,
		usable,
	)
	if res.Status == ModelReferenceResolved {
		return ResolveCliModelResult{
			Model:         res.Model,
			ThinkingLevel: res.ThinkingLevel,
			Warning:       res.Warning,
		}
	}
	if res.Status == ModelReferenceAmbiguous {
		matches := make([]string, 0, len(res.Candidates))
		for _, m := range res.Candidates {
			if m != nil {
				matches = append(matches, string(m.Provider)+"/"+m.Id)
			}
		}
		sort.Slice(matches, func(i, j int) bool {
			return mreCompareLocaleStrings(matches[i], matches[j]) < 0
		})
		var authHint string
		if options.InlineApiKey {
			authHint = "--api-key does not choose a provider."
		} else if res.UsableCount == 0 {
			authHint = "No matching provider is authenticated."
		} else {
			authHint = "More than one matching provider is authenticated."
		}
		errMsg := fmt.Sprintf(`Model %q is ambiguous across providers: %s. %s Use --provider or provider/model.`, cliModel, strings.Join(matches, ", "), authHint)
		return ResolveCliModelResult{Error: &errMsg}
	}

	if res.Target == nil {
		errMsg := fmt.Sprintf(`Model %q not found. Use --list-models to see available models.`, cliModel)
		return ResolveCliModelResult{Error: &errMsg}
	}

	modelId := res.Target.ModelId
	fallbackThinking := res.Target.ThinkingLevel
	if options.CliThinking != nil {
		modelId = res.Target.ModelText
		fallbackThinking = nil
	}
	fallbackModel := mreBuildFallbackModel(res.Target.ProviderModels, modelId)
	requestedThinking := fallbackThinking
	if options.CliThinking != nil {
		requestedThinking = options.CliThinking
	}
	if requestedThinking != nil && *requestedThinking != ai.ModelThinkingLevelOff {
		cp := *fallbackModel
		cp.Reasoning = true
		fallbackModel = &cp
	}
	warnMsg := fmt.Sprintf(`Model %q not found for provider %q. Using custom model id.`, modelId, res.Target.Provider)
	return ResolveCliModelResult{
		Model:         fallbackModel,
		ThinkingLevel: fallbackThinking,
		Warning:       &warnMsg,
	}
}

// FindInitialModelOptions configures FindInitialModel.
type FindInitialModelOptions struct {
	ScopedModels         []ScopedModel
	IsContinuing         bool
	DefaultProvider      *string
	DefaultModelId       *string
	DefaultThinkingLevel *agent.ThinkingLevel
	ModelThinkingLevels  map[string]agent.ThinkingLevel
	ModelRuntime         *ModelRuntime
}

// InitialModelResult is returned by FindInitialModel.
type InitialModelResult struct {
	Model           *ai.Model           `json:"model,omitzero"`
	ThinkingLevel   agent.ThinkingLevel `json:"thinkingLevel"`
	FallbackMessage *string             `json:"fallbackMessage,omitzero"`
}

func mrePickDefaultAvailable(availableModels []*ai.Model) *ai.Model {
	if len(availableModels) == 0 {
		return nil
	}
	for _, prov := range mreDefaultProviderOrder {
		defaultId := DefaultModelPerProvider[prov]
		for _, m := range availableModels {
			if m != nil && m.Provider == prov && m.Id == defaultId {
				return m
			}
		}
	}
	return availableModels[0]
}

// FindInitialModel selects the initial model from scoped models, saved defaults, or first available model.
func FindInitialModel(_ context.Context, options FindInitialModelOptions) (InitialModelResult, error) {
	if len(options.ScopedModels) > 0 && !options.IsContinuing {
		scoped := options.ScopedModels[0]
		tl := DefaultThinkingLevel
		if scoped.ThinkingLevel != nil {
			tl = *scoped.ThinkingLevel
		} else if options.ModelThinkingLevels != nil && scoped.Model != nil {
			if perModel, ok := options.ModelThinkingLevels[string(scoped.Model.Provider)+"/"+scoped.Model.Id]; ok {
				tl = perModel
			} else if options.DefaultThinkingLevel != nil {
				tl = *options.DefaultThinkingLevel
			}
		} else if options.DefaultThinkingLevel != nil {
			tl = *options.DefaultThinkingLevel
		}
		return InitialModelResult{
			Model:         scoped.Model,
			ThinkingLevel: tl,
		}, nil
	}

	if options.DefaultProvider != nil && *options.DefaultProvider != "" && options.DefaultModelId != nil && *options.DefaultModelId != "" {
		found := options.ModelRuntime.GetModel(*options.DefaultProvider, *options.DefaultModelId)
		if found != nil && options.ModelRuntime.HasConfiguredAuth(string(found.Provider)) {
			tl := DefaultThinkingLevel
			if options.ModelThinkingLevels != nil {
				if perModel, ok := options.ModelThinkingLevels[*options.DefaultProvider+"/"+*options.DefaultModelId]; ok {
					tl = perModel
				} else if options.DefaultThinkingLevel != nil {
					tl = *options.DefaultThinkingLevel
				}
			} else if options.DefaultThinkingLevel != nil {
				tl = *options.DefaultThinkingLevel
			}
			return InitialModelResult{
				Model:         found,
				ThinkingLevel: tl,
			}, nil
		}
	}

	available := options.ModelRuntime.GetAvailableSnapshot()
	if best := mrePickDefaultAvailable(available); best != nil {
		return InitialModelResult{
			Model:         best,
			ThinkingLevel: DefaultThinkingLevel,
		}, nil
	}

	return InitialModelResult{
		Model:         nil,
		ThinkingLevel: DefaultThinkingLevel,
	}, nil
}

// RestoreModelResult is returned by RestoreModelFromSession.
type RestoreModelResult struct {
	Model           *ai.Model `json:"model,omitzero"`
	FallbackMessage *string   `json:"fallbackMessage,omitzero"`
}

// RestoreModelFromSession restores a saved session model or falls back to an available model.
func RestoreModelFromSession(
	savedProvider, savedModelId string,
	currentModel *ai.Model,
	shouldPrintMessages bool,
	modelRuntime *ModelRuntime,
	writers ...io.Writer,
) RestoreModelResult {
	var stdout, stderr io.Writer = os.Stdout, os.Stderr
	if len(writers) > 0 && writers[0] != nil {
		stdout = writers[0]
	}
	if len(writers) > 1 && writers[1] != nil {
		stderr = writers[1]
	}

	restored := modelRuntime.GetModel(savedProvider, savedModelId)
	hasAuth := restored != nil && modelRuntime.HasConfiguredAuth(string(restored.Provider))
	if restored != nil && hasAuth {
		if shouldPrintMessages {
			fmt.Fprintf(stdout, "Restored model: %s/%s\n", savedProvider, savedModelId)
		}
		return RestoreModelResult{Model: restored}
	}

	reason := "no auth configured"
	if restored == nil {
		reason = "model no longer exists"
	}
	if shouldPrintMessages {
		fmt.Fprintf(stderr, "Warning: Could not restore model %s/%s (%s).\n", savedProvider, savedModelId, reason)
	}

	if currentModel != nil {
		if shouldPrintMessages {
			fmt.Fprintf(stdout, "Falling back to: %s/%s\n", currentModel.Provider, currentModel.Id)
		}
		msg := fmt.Sprintf("Could not restore model %s/%s (%s). Using %s/%s.", savedProvider, savedModelId, reason, currentModel.Provider, currentModel.Id)
		return RestoreModelResult{
			Model:           currentModel,
			FallbackMessage: &msg,
		}
	}

	available := modelRuntime.GetAvailableSnapshot()
	if fallbackModel := mrePickDefaultAvailable(available); fallbackModel != nil {
		if shouldPrintMessages {
			fmt.Fprintf(stdout, "Falling back to: %s/%s\n", fallbackModel.Provider, fallbackModel.Id)
		}
		msg := fmt.Sprintf("Could not restore model %s/%s (%s). Using %s/%s.", savedProvider, savedModelId, reason, fallbackModel.Provider, fallbackModel.Id)
		return RestoreModelResult{
			Model:           fallbackModel,
			FallbackMessage: &msg,
		}
	}
	return RestoreModelResult{}
}
