package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	ag "github.com/keejkrej/pi-go/agentloop"
)

// chunk mirrors the relevant fields of an OpenAI streaming chunk.
type chunk struct {
	ID      string        `json:"id"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *rawUsage     `json:"usage"`
}

type chunkChoice struct {
	Delta        chunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

type chunkDelta struct {
	Content          *string         `json:"content"`
	ReasoningContent *string         `json:"reasoning_content"`
	Reasoning        *string         `json:"reasoning"`
	ReasoningText    *string         `json:"reasoning_text"`
	ToolCalls        []chunkToolCall `json:"tool_calls"`
}

type chunkToolCall struct {
	Index    int               `json:"index"`
	ID       string            `json:"id"`
	Function *chunkToolCallFun `json:"function"`
}

type chunkToolCallFun struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type rawUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptCacheHitToken int `json:"prompt_cache_hit_tokens"`
	PromptTokensDetails *struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// mapStopReason maps an OpenAI finish_reason to an agentloop StopReason and an
// optional error message.
func mapStopReason(reason string) (ag.StopReason, string) {
	switch reason {
	case "", "stop", "end":
		return ag.StopReasonStop, ""
	case "length":
		return ag.StopReasonLength, ""
	case "function_call", "tool_calls":
		return ag.StopReasonToolUse, ""
	case "content_filter":
		return ag.StopReasonError, "Provider finish_reason: content_filter"
	default:
		return ag.StopReasonError, "Unexpected finish_reason: " + reason
	}
}

// parseChunkUsage converts a raw usage object to an agentloop Usage, including
// computed cost from the model price table.
func parseChunkUsage(u *rawUsage, model *ag.Model) ag.Usage {
	if u == nil {
		return ag.Usage{}
	}
	prompt := u.PromptTokens
	cacheRead := u.PromptCacheHitToken
	cacheWrite := 0
	reasoning := 0
	if u.PromptTokensDetails != nil {
		if u.PromptTokensDetails.CachedTokens != 0 {
			cacheRead = u.PromptTokensDetails.CachedTokens
		}
		cacheWrite = u.PromptTokensDetails.CacheWriteTokens
	}
	if u.CompletionTokensDetails != nil {
		reasoning = u.CompletionTokensDetails.ReasoningTokens
	}
	input := prompt - cacheRead - cacheWrite
	if input < 0 {
		input = 0
	}
	output := u.CompletionTokens

	usage := ag.Usage{
		Input:       input,
		Output:      output,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		Reasoning:   reasoning,
		TotalTokens: input + output + cacheRead + cacheWrite,
	}
	usage.Cost = ag.UsageCost{
		Input:      float64(input) / 1e6 * model.Cost.Input,
		Output:     float64(output) / 1e6 * model.Cost.Output,
		CacheRead:  float64(cacheRead) / 1e6 * model.Cost.CacheRead,
		CacheWrite: float64(cacheWrite) / 1e6 * model.Cost.CacheWrite,
	}
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
	return usage
}

// parseStreamingJson best-effort parses a possibly-incomplete JSON object,
// returning an empty map on failure so partial deltas never crash the stream.
func parseStreamingJson(s string) map[string]any {
	s = strings.TrimSpace(s)
	if s == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err == nil && m != nil {
		return m
	}
	if repaired, ok := repairJSON(s); ok {
		var rm map[string]any
		if err := json.Unmarshal([]byte(repaired), &rm); err == nil && rm != nil {
			return rm
		}
	}
	return map[string]any{}
}

// repairJSON attempts to close an unterminated JSON object/array/string so a
// partial streaming fragment can be parsed. Returns (repaired, true) when it
// produced a candidate.
func repairJSON(s string) (string, bool) {
	var stack []byte
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	var b strings.Builder
	b.WriteString(s)
	if inString {
		b.WriteByte('"')
	}
	for i := len(stack) - 1; i >= 0; i-- {
		b.WriteByte(stack[i])
	}
	out := b.String()
	if out == s {
		return out, false
	}
	return out, true
}

// streamState tracks the in-progress blocks during SSE parsing.
type streamState struct {
	out       *ag.AssistantMessageEventStream
	output    *ag.AssistantMessage
	model     *ag.Model
	textBlock *ag.TextContent
	thinkBlk  *ag.ThinkingContent
	tcByIndex map[int]*tcEntry
	hasFinish bool
}

type tcEntry struct {
	block       *ag.ToolCall
	partialArgs string
}

func newStreamState(out *ag.AssistantMessageEventStream, output *ag.AssistantMessage, model *ag.Model) *streamState {
	return &streamState{
		out:       out,
		output:    output,
		model:     model,
		tcByIndex: map[int]*tcEntry{},
	}
}

func (s *streamState) contentIndex(block ag.Content) int {
	for i, b := range s.output.Content {
		if b == block {
			return i
		}
	}
	return -1
}

func (s *streamState) ensureTextBlock() *ag.TextContent {
	if s.textBlock == nil {
		s.textBlock = &ag.TextContent{}
		s.output.Content = append(s.output.Content, s.textBlock)
		s.out.Push(&ag.TextStartEvent{ContentIndex: s.contentIndex(s.textBlock), Partial: s.output})
	}
	return s.textBlock
}

func (s *streamState) ensureThinkingBlock(signature string) *ag.ThinkingContent {
	if s.thinkBlk == nil {
		s.thinkBlk = &ag.ThinkingContent{ThinkingSignature: signature}
		s.output.Content = append(s.output.Content, s.thinkBlk)
		s.out.Push(&ag.ThinkingStartEvent{ContentIndex: s.contentIndex(s.thinkBlk), Partial: s.output})
	}
	return s.thinkBlk
}

func (s *streamState) ensureToolCallBlock(index int, id string) *tcEntry {
	entry := s.tcByIndex[index]
	if entry == nil {
		entry = &tcEntry{block: &ag.ToolCall{Arguments: map[string]any{}}}
		s.tcByIndex[index] = entry
		s.output.Content = append(s.output.Content, entry.block)
		s.out.Push(&ag.ToolCallStartEvent{ContentIndex: s.contentIndex(entry.block), Partial: s.output})
	}
	if id != "" && entry.block.ID == "" {
		entry.block.ID = id
	}
	return entry
}

// process consumes one parsed chunk, emitting delta events.
func (s *streamState) process(ch *chunk) {
	if s.output.ResponseID == "" {
		s.output.ResponseID = ch.ID
	}
	if ch.Model != "" && ch.Model != s.model.ID && s.output.ResponseModel == "" {
		s.output.ResponseModel = ch.Model
	}
	if ch.Usage != nil {
		s.output.Usage = parseChunkUsage(ch.Usage, s.model)
	}
	if len(ch.Choices) == 0 {
		return
	}
	choice := ch.Choices[0]
	if choice.FinishReason != nil {
		reason, errMsg := mapStopReason(*choice.FinishReason)
		s.output.StopReason = reason
		if errMsg != "" {
			s.output.ErrorMessage = errMsg
		}
		s.hasFinish = true
	}

	delta := choice.Delta
	if delta.Content != nil && *delta.Content != "" {
		block := s.ensureTextBlock()
		block.Text += *delta.Content
		s.out.Push(&ag.TextDeltaEvent{ContentIndex: s.contentIndex(block), Delta: *delta.Content, Partial: s.output})
	}

	if reasoning, sig := firstReasoning(delta); reasoning != "" {
		block := s.ensureThinkingBlock(sig)
		block.Thinking += reasoning
		s.out.Push(&ag.ThinkingDeltaEvent{ContentIndex: s.contentIndex(block), Delta: reasoning, Partial: s.output})
	}

	for _, tc := range delta.ToolCalls {
		entry := s.ensureToolCallBlock(tc.Index, tc.ID)
		var fragment string
		if tc.Function != nil {
			if tc.Function.Name != "" && entry.block.Name == "" {
				entry.block.Name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				fragment = tc.Function.Arguments
				entry.partialArgs += tc.Function.Arguments
				entry.block.Arguments = parseStreamingJson(entry.partialArgs)
			}
		}
		s.out.Push(&ag.ToolCallDeltaEvent{ContentIndex: s.contentIndex(entry.block), Delta: fragment, Partial: s.output})
	}
}

// firstReasoning returns the first non-empty reasoning field and its signature.
func firstReasoning(delta chunkDelta) (string, string) {
	if delta.ReasoningContent != nil && *delta.ReasoningContent != "" {
		return *delta.ReasoningContent, "reasoning_content"
	}
	if delta.Reasoning != nil && *delta.Reasoning != "" {
		return *delta.Reasoning, "reasoning"
	}
	if delta.ReasoningText != nil && *delta.ReasoningText != "" {
		return *delta.ReasoningText, "reasoning_text"
	}
	return "", ""
}

// finishBlocks emits the end event for each accumulated block in order.
func (s *streamState) finishBlocks() {
	for idx, block := range s.output.Content {
		switch b := block.(type) {
		case *ag.TextContent:
			s.out.Push(&ag.TextEndEvent{ContentIndex: idx, Content: b.Text, Partial: s.output})
		case *ag.ThinkingContent:
			s.out.Push(&ag.ThinkingEndEvent{ContentIndex: idx, Content: b.Thinking, Partial: s.output})
		case *ag.ToolCall:
			// Finalize arguments from the accumulated buffer.
			for _, entry := range s.tcByIndex {
				if entry.block == b {
					b.Arguments = parseStreamingJson(entry.partialArgs)
					break
				}
			}
			s.out.Push(&ag.ToolCallEndEvent{ContentIndex: idx, ToolCall: b, Partial: s.output})
		}
	}
}

// readStream parses the SSE body, emitting events into the stream state.
func (s *streamState) readStream(ctx context.Context, body io.Reader) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			return
		}
		var ch chunk
		if err := json.Unmarshal([]byte(payload), &ch); err != nil {
			continue
		}
		s.process(&ch)
	}
}
