// Ported from packages/evals/src/plan.ts (pi v1.0.0).

package evals

import (
	"fmt"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// DocumentationVariant identifies the experimental condition for documentation evals.
type DocumentationVariant string

const (
	DocumentationVariantWithoutDocs DocumentationVariant = "without_docs"
	DocumentationVariantWithDocs    DocumentationVariant = "with_docs"
)

// DocumentationVariants lists the ordered documentation variants.
var DocumentationVariants = []DocumentationVariant{
	DocumentationVariantWithoutDocs,
	DocumentationVariantWithDocs,
}

// DiscoveredTestEntry represents a raw entry returned by test discovery before identity parsing.
type DiscoveredTestEntry struct {
	Name string `json:"name"`
	File string `json:"file"`
}

// DiscoveredEvalCase represents a validated discovered evaluation case.
type DiscoveredEvalCase struct {
	File     string `json:"file"`
	FullName string `json:"fullName"`
	EvalSet  string `json:"evalSet"`
	CaseId   string `json:"caseId"`
}

// EvalTask schedules a single isolated execution of a DiscoveredEvalCase.
type EvalTask struct {
	File      string               `json:"file"`
	FullName  string               `json:"fullName"`
	EvalSet   string               `json:"evalSet"`
	CaseId    string               `json:"caseId"`
	Variant   DocumentationVariant `json:"variant"`
	Model     string               `json:"model"`
	RunNumber int                  `json:"runNumber"`
}

func planExtractEntry(item any) (name string, file string, ok bool) {
	switch v := item.(type) {
	case DiscoveredTestEntry:
		return v.Name, v.File, true
	case *DiscoveredTestEntry:
		if v == nil {
			return "", "", false
		}
		return v.Name, v.File, true
	case *jsonx.Object:
		if v == nil {
			return "", "", false
		}
		rawName, hasName := v.Get("name")
		rawFile, hasFile := v.Get("file")
		if !hasName || !hasFile {
			return "", "", false
		}
		n, nOk := rawName.(string)
		f, fOk := rawFile.(string)
		return n, f, nOk && fOk
	case map[string]any:
		if v == nil {
			return "", "", false
		}
		n, nOk := v["name"].(string)
		f, fOk := v["file"].(string)
		return n, f, nOk && fOk
	default:
		return "", "", false
	}
}

func planNormalizeItems(value any) ([]any, bool) {
	switch v := value.(type) {
	case []DiscoveredTestEntry:
		if v == nil {
			return nil, false
		}
		out := make([]any, len(v))
		for i := range v {
			out[i] = v[i]
		}
		return out, true
	case []any:
		if v == nil {
			return nil, false
		}
		return v, true
	case []*jsonx.Object:
		if v == nil {
			return nil, false
		}
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out, true
	case []map[string]any:
		if v == nil {
			return nil, false
		}
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out, true
	default:
		return nil, false
	}
}

// ParseDiscoveredCases validates raw discovered test entries and derives stable eval identities.
func ParseDiscoveredCases(value any) ([]DiscoveredEvalCase, error) {
	items, isArray := planNormalizeItems(value)
	if !isArray {
		return nil, fmt.Errorf("Discovered eval cases must be an array.")
	}
	identities := make(map[string]struct{}, len(items))
	cases := make([]DiscoveredEvalCase, 0, len(items))
	for _, item := range items {
		name, file, ok := planExtractEntry(item)
		if !ok {
			return nil, fmt.Errorf("Discovered eval case is invalid.")
		}
		parts := strings.Split(name, " > ")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf(`Documentation eval must use "<eval set> > <case>": %s`, name)
		}
		evalSet := parts[0]
		caseId := parts[1]
		identity := jsonx.MustStringify([]string{evalSet, caseId})
		if _, dup := identities[identity]; dup {
			return nil, fmt.Errorf("Duplicate eval case identity: %s", name)
		}
		identities[identity] = struct{}{}
		cases = append(cases, DiscoveredEvalCase{
			File:     file,
			FullName: name,
			EvalSet:  evalSet,
			CaseId:   caseId,
		})
	}
	return cases, nil
}

// CreateTaskPlan produces alternating variant tasks across runsPerVariant repetitions.
func CreateTaskPlan(cases []DiscoveredEvalCase, model string, runsPerVariant int) ([]EvalTask, error) {
	if !strings.Contains(model, "/") || strings.HasPrefix(model, "/") || strings.HasSuffix(model, "/") {
		return nil, fmt.Errorf("Model identity must contain a provider and model.")
	}
	if runsPerVariant < 1 {
		return nil, fmt.Errorf("Runs per variant must be a positive integer.")
	}
	tasks := make([]EvalTask, 0, len(cases)*runsPerVariant*len(DocumentationVariants))
	for _, evalCase := range cases {
		for runNumber := 1; runNumber <= runsPerVariant; runNumber++ {
			variants := []DocumentationVariant{DocumentationVariantWithoutDocs, DocumentationVariantWithDocs}
			if runNumber%2 == 0 {
				variants = []DocumentationVariant{DocumentationVariantWithDocs, DocumentationVariantWithoutDocs}
			}
			for _, variant := range variants {
				tasks = append(tasks, EvalTask{
					File:      evalCase.File,
					FullName:  evalCase.FullName,
					EvalSet:   evalCase.EvalSet,
					CaseId:    evalCase.CaseId,
					Variant:   variant,
					Model:     model,
					RunNumber: runNumber,
				})
			}
		}
	}
	return tasks, nil
}
