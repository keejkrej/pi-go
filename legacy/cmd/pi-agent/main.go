// Command pi-agent is a minimal example host that wires the agentloop engine to
// the OpenAI Chat Completions adapter (wire/openai) and the local file tools.
//
// It reads a single instruction from os.Args (or stdin), runs one agent loop,
// and prints a readable trace of the events to stdout.
//
// Environment:
//
//	OPENAI_API_KEY  (required) API key for the chat-completions endpoint.
//	OPENAI_BASE_URL (optional) defaults to https://api.openai.com/v1.
//	PI_MODEL        (optional) model id, defaults to gpt-4o-mini.
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	ag "github.com/keejkrej/pi-go/legacy/agentloop"
	"github.com/keejkrej/pi-go/legacy/tools"
	"github.com/keejkrej/pi-go/legacy/wire/openai"
)

const systemPrompt = "You are a concise coding assistant. " +
	"Use the provided file tools (read, write, edit, grep, find) to inspect and modify the " +
	"working directory when needed. Prefer small, verifiable steps and explain what you do."

// identity is the ConvertToLlm passthrough: every AgentMessage in this host is
// already an LLM-visible Message, so we forward them and drop anything else.
func identity(messages []ag.AgentMessage) ([]ag.Message, error) {
	out := make([]ag.Message, 0, len(messages))
	for _, m := range messages {
		if msg, ok := m.(ag.Message); ok {
			out = append(out, msg)
		}
	}
	return out, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// readPrompt takes the instruction from the command-line args, falling back to
// reading all of stdin.
func readPrompt() string {
	if len(os.Args) > 1 {
		return strings.TrimSpace(strings.Join(os.Args[1:], " "))
	}
	data, _ := io.ReadAll(os.Stdin)
	return strings.TrimSpace(string(data))
}

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "error: OPENAI_API_KEY is not set")
		os.Exit(1)
	}

	prompt := readPrompt()
	if prompt == "" {
		fmt.Fprintln(os.Stderr, "usage: pi-agent <instruction>   (or pipe one on stdin)")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	model := &ag.Model{
		ID:        env("PI_MODEL", "gpt-4o-mini"),
		Name:      env("PI_MODEL", "gpt-4o-mini"),
		Api:       "openai-completions",
		Provider:  "openai",
		BaseURL:   env("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		Input:     []string{"text"},
		MaxTokens: 4096,
	}

	agentCtx := &ag.AgentContext{
		SystemPrompt: systemPrompt,
		Tools: []ag.AgentTool{
			tools.NewReadTool("."),
			tools.NewWriteTool("."),
			tools.NewEditTool("."),
			tools.NewGrepTool("."),
			tools.NewFindTool("."),
		},
	}

	config := &ag.AgentLoopConfig{
		Model:        model,
		MaxTokens:    4096,
		APIKey:       apiKey,
		ConvertToLlm: identity,
	}

	prompts := []ag.AgentMessage{ag.NewUserText(prompt)}
	stream := ag.AgentLoop(ctx, prompts, agentCtx, config, openai.StreamSimple)

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()

	for ev := range stream.Events() {
		printEvent(w, ev)
		w.Flush()
	}

	fmt.Fprintln(w)
}

// printEvent renders a single AgentEvent as a human-readable trace line.
// Events arrive as pointers to the concrete event structs.
func printEvent(w io.Writer, ev ag.AgentEvent) {
	switch e := ev.(type) {
	case *ag.AgentStartEvent:
		fmt.Fprintln(w, "== agent start ==")
	case *ag.TurnStartEvent:
		fmt.Fprintln(w, "-- turn --")
	case *ag.MessageStartEvent:
		if _, ok := e.Message.(*ag.AssistantMessage); ok {
			fmt.Fprint(w, "assistant: ")
		}
	case *ag.MessageUpdateEvent:
		switch u := e.AssistantMessageEvent.(type) {
		case *ag.TextDeltaEvent:
			fmt.Fprint(w, u.Delta)
		case *ag.ThinkingDeltaEvent:
			fmt.Fprint(w, u.Delta)
		case *ag.ToolCallEndEvent:
			if u.ToolCall != nil {
				fmt.Fprintf(w, "[tool call: %s]", u.ToolCall.Name)
			}
		}
	case *ag.MessageEndEvent:
		if _, ok := e.Message.(*ag.AssistantMessage); ok {
			fmt.Fprintln(w)
		}
	case *ag.ToolExecutionStartEvent:
		fmt.Fprintf(w, "  tool %s(%s)\n", e.ToolName, snippet(fmt.Sprintf("%v", e.Args), 120))
	case *ag.ToolExecutionEndEvent:
		status := "ok"
		if e.IsError {
			status = "error"
		}
		fmt.Fprintf(w, "  -> %s: %s\n", status, snippet(resultText(e.Result), 200))
	case *ag.AgentEndEvent:
		fmt.Fprintf(w, "== agent end (%d messages) ==\n", len(e.Messages))
	}
}

// resultText extracts a short textual summary from a tool ToolResult value.
func resultText(result any) string {
	tr, ok := result.(ag.ToolResult)
	if !ok {
		return fmt.Sprintf("%v", result)
	}
	var b strings.Builder
	for _, c := range tr.Content {
		if t, ok := c.(*ag.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// snippet collapses whitespace and truncates s to n runes.
func snippet(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
