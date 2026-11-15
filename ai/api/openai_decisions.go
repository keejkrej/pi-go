// Ported from packages/ai/src/api/openai-decisions.ts (pi v1.5.2).

package api

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

const (
	odLabel     = "OpenAI Decisions"
	odMaxImages = 128
)

var odNoRetryStatuses = []int{504}

func odPredicateInstructions(question *ai.ClassifierBoolQuestion) string {
	var meanings []string
	if question.Criteria.True != "" {
		meanings = append(meanings, "True means: "+question.Criteria.True)
	}
	if question.Criteria.False != "" {
		meanings = append(meanings, "False means: "+question.Criteria.False)
	}
	if len(meanings) > 0 {
		return question.Instructions + "\n\n" + strings.Join(meanings, "\n")
	}
	return question.Instructions
}

func odWireQuestion(name string, question ai.ClassifierQuestion) (*jsonx.Object, error) {
	if question == nil {
		return nil, fmt.Errorf("%s received nil question for %s", odLabel, name)
	}
	switch q := question.(type) {
	case *ai.ClassifierChoiceQuestion:
		choices := make([]*jsonx.Object, 0)
		if q.Criteria != nil {
			for value, description := range q.Criteria.All() {
				item := jsonx.ObjectOf("value", value)
				if description != "" {
					item.Set("description", description)
				}
				choices = append(choices, item)
			}
		}
		return jsonx.ObjectOf(
			"type", "choice",
			"name", name,
			"instructions", q.Instructions,
			"choices", choices,
		), nil
	case *ai.ClassifierScoreQuestion:
		levels := make([]*jsonx.Object, 0, len(q.Criteria))
		for _, label := range q.Criteria {
			levels = append(levels, jsonx.ObjectOf("label", label))
		}
		return jsonx.ObjectOf(
			"type", "score",
			"name", name,
			"instructions", q.Instructions,
			"levels", levels,
		), nil
	case *ai.ClassifierBoolQuestion:
		return jsonx.ObjectOf(
			"type", "predicate",
			"name", name,
			"instructions", odPredicateInstructions(q),
		), nil
	default:
		return nil, fmt.Errorf("%s received unsupported question type for %s", odLabel, name)
	}
}

func odWireInput(c *ai.ClassifierContext) (any, error) {
	stateObj := c.State
	if stateObj == nil {
		stateObj = jsonx.NewObject()
	}
	stateBytes, err := jsonx.Marshal(stateObj)
	if err != nil {
		return nil, err
	}
	state := string(stateBytes)
	if len(c.Images) == 0 {
		return state, nil
	}
	if len(c.Images) > odMaxImages {
		return nil, fmt.Errorf("%s accepts at most %d images, got %d", odLabel, odMaxImages, len(c.Images))
	}
	content := make([]*jsonx.Object, 0, 1+len(c.Images))
	content = append(content, jsonx.ObjectOf("type", "input_text", "text", state))
	for _, image := range c.Images {
		if image == nil {
			return nil, fmt.Errorf("%s received nil image", odLabel)
		}
		content = append(content, jsonx.ObjectOf(
			"type", "input_image",
			"image_url", fmt.Sprintf("data:%s;base64,%s", image.MimeType, image.Data),
		))
	}
	return []*jsonx.Object{
		jsonx.ObjectOf(
			"role", "user",
			"content", content,
		),
	}, nil
}

func odGetField(record any, key string) (any, bool) {
	switch r := record.(type) {
	case *jsonx.Object:
		if r == nil {
			return nil, false
		}
		return r.Get(key)
	case map[string]any:
		if r == nil {
			return nil, false
		}
		v, ok := r[key]
		return v, ok
	default:
		return nil, false
	}
}

func odGetString(record any, key string) (string, bool) {
	v, ok := odGetField(record, key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func odAsSlice(value any) ([]any, bool) {
	switch s := value.(type) {
	case []any:
		if s == nil {
			return nil, false
		}
		return s, true
	case []*jsonx.Object:
		if s == nil {
			return nil, false
		}
		out := make([]any, len(s))
		for i, item := range s {
			out[i] = item
		}
		return out, true
	default:
		return nil, false
	}
}

func odChoiceProbabilities(value any, id string) (*omap.Map[string, float64], error) {
	entries, ok := odAsSlice(value)
	if !ok {
		return nil, fmt.Errorf("%s returned invalid probabilities for %s", odLabel, id)
	}
	out := omap.NewMap[string, float64]()
	for _, entry := range entries {
		if !IsRecord(entry) {
			return nil, fmt.Errorf("%s returned invalid probabilities for %s", odLabel, id)
		}
		entryValue, ok := odGetString(entry, "value")
		if !ok {
			return nil, fmt.Errorf("%s returned invalid probabilities for %s", odLabel, id)
		}
		rawProb, _ := odGetField(entry, "probability")
		prob, err := RequiredNumber(odLabel, rawProb, fmt.Sprintf("probability for %s.%s", id, entryValue))
		if err != nil {
			return nil, err
		}
		out.Set(entryValue, prob)
	}
	return out, nil
}

func odParseAnswer(id string, question ai.ClassifierQuestion, answer any) (ai.ClassifierAnswer, error) {
	answerType, _ := odGetString(answer, "type")
	if answerType == "refusal" {
		return nil, fmt.Errorf("%s refused to answer %s", odLabel, id)
	}
	switch question.(type) {
	case *ai.ClassifierChoiceQuestion:
		choice, hasChoice := odGetString(answer, "choice")
		if answerType != "choice" || !hasChoice {
			return nil, fmt.Errorf("%s did not return a choice answer for %s", odLabel, id)
		}
		rawProbs, _ := odGetField(answer, "probabilities")
		probs, err := odChoiceProbabilities(rawProbs, id)
		if err != nil {
			return nil, err
		}
		rawConf, _ := odGetField(answer, "confidence")
		confidence, err := RequiredNumber(odLabel, rawConf, "confidence for "+id)
		if err != nil {
			return nil, err
		}
		return &ai.ClassifierChoiceAnswer{
			Type:          "choice",
			Choice:        choice,
			Probabilities: probs,
			Confidence:    confidence,
		}, nil
	case *ai.ClassifierScoreQuestion:
		if answerType != "score" {
			return nil, fmt.Errorf("%s did not return a score answer for %s", odLabel, id)
		}
		rawScore, _ := odGetField(answer, "score")
		score, err := RequiredNumber(odLabel, rawScore, "score for "+id)
		if err != nil {
			return nil, err
		}
		rawConf, _ := odGetField(answer, "confidence")
		confidence, err := RequiredNumber(odLabel, rawConf, "confidence for "+id)
		if err != nil {
			return nil, err
		}
		return &ai.ClassifierScoreAnswer{
			Type:       "score",
			Score:      score,
			Confidence: confidence,
		}, nil
	case *ai.ClassifierBoolQuestion:
		if answerType != "predicate" {
			return nil, fmt.Errorf("%s did not return a predicate answer for %s", odLabel, id)
		}
		rawProb, _ := odGetField(answer, "probability")
		probability, err := RequiredNumber(odLabel, rawProb, "probability for "+id)
		if err != nil {
			return nil, err
		}
		return &ai.ClassifierBoolAnswer{
			Type:        "bool",
			Probability: probability,
		}, nil
	default:
		return nil, fmt.Errorf("%s received unsupported question type for %s", odLabel, id)
	}
}

func odParseAnswers(value any, c *ai.ClassifierContext) (*omap.Map[string, ai.ClassifierAnswer], error) {
	items, ok := odAsSlice(value)
	if !ok {
		return nil, fmt.Errorf("%s returned an unexpected response", odLabel)
	}
	byName := make(map[string]any, len(items))
	for _, answer := range items {
		if IsRecord(answer) {
			if name, ok := odGetString(answer, "name"); ok {
				byName[name] = answer
			}
		}
	}
	out := omap.NewMap[string, ai.ClassifierAnswer]()
	if c.Questions == nil {
		return out, nil
	}
	for id, question := range c.Questions.All() {
		answer, ok := byName[id]
		if !ok {
			return nil, fmt.Errorf("%s did not return an answer for %s", odLabel, id)
		}
		parsed, err := odParseAnswer(id, question, answer)
		if err != nil {
			return nil, err
		}
		out.Set(id, parsed)
	}
	return out, nil
}

func odErrorMessage(err error) string {
	// Cloudflare in front of api.openai.com answers 504 with an HTML page when a request runs longer
	// than about five seconds; map 504 to a concise actionable message instead of raw HTML.
	norm := ai.NormalizeProviderError(err)
	if norm.Status != nil && *norm.Status == 504 {
		return fmt.Sprintf("%s error (504): the request timed out at the gateway. Very large inputs (above roughly 600K tokens) currently exceed its time limit.", odLabel)
	}
	return ai.FormatProviderError(norm, odLabel+" error")
}

func odDecisionUrl(baseUrl string) (string, error) {
	baseParsed, err := url.Parse(strings.TrimRight(baseUrl, "/") + "/")
	if err != nil {
		return "", err
	}
	rel, err := url.Parse("decisions")
	if err != nil {
		return "", err
	}
	return baseParsed.ResolveReference(rel).String(), nil
}

// ClassifyOpenaiDecisions runs classification through OpenAI's Decisions API.
func ClassifyOpenaiDecisions(
	ctx context.Context,
	model *ai.ClassifierModel,
	c *ai.ClassifierContext,
	options *ai.ClassifierOptions,
) (*ai.ClassifierResult, error) {
	sig := ai.OperationSignal(ctx)
	if model == nil {
		return ai.ClassifierErrorResult(&ai.ClassifierModel{}, errors.New(odLabel+": model is required"), sig.Err() != nil), nil
	}
	output := &ai.ClassifierResult{
		Api:        ai.ClassifierApi(model.Api),
		Provider:   model.Provider,
		Model:      model.Id,
		Answers:    omap.NewMap[string, ai.ClassifierAnswer](),
		StopReason: ai.ClassifierStopReasonStop,
		Timestamp:  js.DateNow(),
	}
	fail := func(err error) (*ai.ClassifierResult, error) {
		output.Answers = omap.NewMap[string, ai.ClassifierAnswer]()
		if sig.Err() != nil {
			output.StopReason = ai.ClassifierStopReasonAborted
		} else {
			output.StopReason = ai.ClassifierStopReasonError
		}
		msg := odErrorMessage(err)
		output.ErrorMessage = &msg
		return output, nil
	}

	if model.Api != string(ai.KnownClassifierApiOpenaiDecisions) {
		return fail(fmt.Errorf("Unsupported classifier API: %s", model.Api))
	}
	if c == nil {
		return fail(errors.New(odLabel + ": context is required"))
	}
	input, err := odWireInput(c)
	if err != nil {
		return fail(err)
	}
	questions := make([]*jsonx.Object, 0)
	if c.Questions != nil {
		for id, question := range c.Questions.All() {
			q, err := odWireQuestion(id, question)
			if err != nil {
				return fail(err)
			}
			questions = append(questions, q)
		}
	}
	rawUrl, err := odDecisionUrl(model.BaseUrl)
	if err != nil {
		return fail(err)
	}
	reqBody := jsonx.ObjectOf(
		"model", model.Id,
		"input", input,
		"questions", questions,
	)
	body, err := PostClassifierRequest(sig, odLabel, rawUrl, model, reqBody, options, odNoRetryStatuses)
	if err != nil {
		return fail(err)
	}
	if !IsRecord(body) {
		return fail(fmt.Errorf("%s returned an unexpected response", odLabel))
	}
	// Set before parsing answers: a request with malformed or refused answers was still billed.
	rawUsage, _ := odGetField(body, "usage")
	if usage := ParseClassifierUsage(rawUsage, model); usage != nil {
		output.Usage = usage
	}
	rawAnswers, _ := odGetField(body, "answers")
	answers, err := odParseAnswers(rawAnswers, c)
	if err != nil {
		return fail(err)
	}
	output.Answers = answers
	return output, nil
}
