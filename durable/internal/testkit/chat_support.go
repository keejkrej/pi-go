// Ported from packages/durable/test/chat-support.ts (pi v1.0.0).

package testkit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/env"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// ChatSetup holds Models, Registry, Faux provider handle, settings, and reports shared across reopen cycles.
type ChatSetup struct {
	mu       sync.Mutex
	Faux     *providers.FauxProviderHandle
	Models   ai.MutableModels
	Registry durable.Registry
	reports  []error
	settings durable.HarnessSettings
	now      func() int64
}

// NewChatSetup initializes a faux provider, models registry, and extension registry for chat tests.
func NewChatSetup(opts ...*providers.RegisterFauxProviderOptions) (*ChatSetup, error) {
	var opt *providers.RegisterFauxProviderOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	faux, err := providers.FauxProvider(opt)
	if err != nil {
		return nil, err
	}
	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)
	return &ChatSetup{
		Faux:     faux,
		Models:   models,
		Registry: durable.CreateRegistry(),
		now:      func() int64 { return time.Now().UnixMilli() },
	}, nil
}

// Settings returns a snapshot of the live HarnessSettings.
func (c *ChatSetup) Settings() durable.HarnessSettings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settings
}

// UpdateSettings mutates live HarnessSettings safely under c's mutex.
func (c *ChatSetup) UpdateSettings(fn func(s *durable.HarnessSettings)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(&c.settings)
}

// SetSettings replaces the live HarnessSettings safely under c's mutex.
func (c *ChatSetup) SetSettings(s durable.HarnessSettings) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settings = s
}

// Reports returns a snapshot copy of all OnReport errors observed so far.
func (c *ChatSetup) Reports() []error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.reports)
}

// ClearReports clears recorded OnReport errors.
func (c *ChatSetup) ClearReports() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports = nil
}

// Now calls the configured clock function.
func (c *ChatSetup) Now() int64 {
	c.mu.Lock()
	fn := c.now
	c.mu.Unlock()
	return fn()
}

// SetNow replaces the clock supplier used by OpenChat.
func (c *ChatSetup) SetNow(fn func() int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = fn
}

// OpenChatOptions configures OpenChat.
type OpenChatOptions struct {
	Env   env.ExecutionEnv
	EnvFn func(target durable.EnvTarget, ctx chord.Context) (env.ExecutionEnv, error)
}

// OpenChat opens a Harness over storage and configures its root conversation with the faux model.
func OpenChat(st durable.Storage, setup *ChatSetup, opts ...OpenChatOptions) (durable.Harness, durable.Conversation, error) {
	var opt OpenChatOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	var envFn func(target durable.EnvTarget, ctx chord.Context) (env.ExecutionEnv, error)
	if opt.EnvFn != nil {
		envFn = opt.EnvFn
	} else if opt.Env != nil {
		fixed := opt.Env
		envFn = func(_ durable.EnvTarget, _ chord.Context) (env.ExecutionEnv, error) {
			return fixed, nil
		}
	}
	harness, err := durable.HarnessOpen(st, durable.HarnessOptions{
		Models:   setup.Models,
		Registry: setup.Registry,
		Settings: setup.Settings,
		Env:      envFn,
		Now:      setup.Now,
		OnReport: func(err error) {
			setup.mu.Lock()
			setup.reports = append(setup.reports, err)
			setup.mu.Unlock()
		},
	}, TestContext)
	if err != nil {
		return nil, nil, err
	}
	root, err := harness.Root(TestContext, durable.RootOptions{
		Agent: &durable.AgentChange{
			Model: jsonx.Some(durable.ModelRef{Provider: "faux", ModelId: "faux-1"}),
		},
	})
	if err != nil {
		_ = harness.Close(TestContext)
		return nil, nil, err
	}
	return harness, root, nil
}

// AllEntries returns every active entry of conversation ordered oldest-first.
func AllEntries(conv durable.Conversation, callCtx ...chord.Context) ([]durable.EntryRecord, error) {
	ctx := TestContext
	if len(callCtx) > 0 && callCtx[0] != nil {
		ctx = callCtx[0]
	}
	page, err := conv.Entries(durable.ConversationEntryQuery{}, 1000, nil, ctx)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(page.Items)
	slices.Reverse(out)
	return out, nil
}

// TextOf returns the text of message's first text content, or ("", false) when absent.
func TextOf(message ai.Message) (string, bool) {
	if message == nil {
		return "", false
	}
	switch m := message.(type) {
	case *ai.SystemMessage:
		return "", false
	case *ai.UserMessage:
		if m.Content.Text != nil {
			return *m.Content.Text, true
		}
		for _, block := range m.Content.Blocks {
			if tc, ok := block.(*ai.TextContent); ok {
				return tc.Text, true
			}
		}
		return "", false
	case *ai.AssistantMessage:
		for _, block := range m.Content {
			if tc, ok := block.(*ai.TextContent); ok {
				return tc.Text, true
			}
		}
		return "", false
	case *ai.ToolResultMessage:
		for _, block := range m.Content {
			if tc, ok := block.(*ai.TextContent); ok {
				return tc.Text, true
			}
		}
		return "", false
	default:
		return "", false
	}
}

// WaitFor polls check in real time every 5ms until it returns true or timeoutMs elapses.
func WaitFor(check func() (bool, error), timeoutMs ...int64) error {
	timeout := int64(5000)
	if len(timeoutMs) > 0 && timeoutMs[0] > 0 {
		timeout = timeoutMs[0]
	}
	deadline := time.Now().Add(time.Duration(timeout) * time.Millisecond)
	for {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("Condition was not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Unanswered returns a FauxResponseFactory that blocks until cancelled and a channel closed when reached.
func Unanswered() (providers.FauxResponseFactory, <-chan struct{}) {
	reached := make(chan struct{})
	var once sync.Once
	step := func(ctx context.Context, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions, _ *providers.FauxProviderState, _ *ai.Model) (*ai.AssistantMessage, error) {
		once.Do(func() { close(reached) })
		<-ctx.Done()
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		return nil, ctx.Err()
	}
	return step, reached
}

// ToolsNamed resolves installed tools by name from setup.Registry in the given order.
func ToolsNamed(setup *ChatSetup, names ...string) ([]*durable.ToolRegistration, error) {
	installed := setup.Registry.Snapshot().Tools()
	out := make([]*durable.ToolRegistration, 0, len(names))
	for _, name := range names {
		var found *durable.ToolRegistration
		for _, rt := range installed {
			if rt.Tool != nil && rt.Tool.Name == name {
				found = rt.Tool
				break
			}
		}
		if found == nil {
			return nil, fmt.Errorf("Tool %s is not installed", name)
		}
		out = append(out, found)
	}
	return out, nil
}
