package ai_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/telemetry"
)

type telTestSpanRecord struct {
	Name            string
	StartAttributes telemetry.SpanAttributes
	Events          []telTestEventRecord
	EndAttributes   []telemetry.SpanAttributes
	Statuses        []telemetry.SpanStatus
	Ended           bool
}

type telTestEventRecord struct {
	Name  string
	Attrs telemetry.SpanAttributes
}

type telTestMetricRecord struct {
	Kind        string // "histogram" or "counter"
	Name        string
	Description *string
	Unit        *string
	Value       float64
	Attrs       telemetry.SpanAttributes
	Span        telemetry.TelemetryContext
}

type telTestTracingOnlyContext struct {
	mu    sync.Mutex
	wg    sync.WaitGroup
	spans []*telTestSpanRecord
}

func (c *telTestTracingOnlyContext) Metrics() telemetry.TelemetryMetrics { return nil }
func (c *telTestTracingOnlyContext) Propagation() telemetry.TelemetryPropagation {
	return telemetry.NoopTelemetryPropagation
}

func (c *telTestTracingOnlyContext) startSpanWithMetrics(metrics telemetry.TelemetryMetrics, options *telemetry.SpanOptions, callback func(span telemetry.TelemetrySpan) (any, error)) (any, error) {
	c.wg.Add(1)
	defer c.wg.Done()

	rec := &telTestSpanRecord{
		Name:            options.Name,
		StartAttributes: options.Attributes,
	}
	c.mu.Lock()
	c.spans = append(c.spans, rec)
	c.mu.Unlock()

	sp := &telTestSpanHandle{owner: c, metrics: metrics, rec: rec}
	res, err := callback(sp)

	c.mu.Lock()
	rec.Ended = true
	c.mu.Unlock()
	return res, err
}

func (c *telTestTracingOnlyContext) StartSpan(options *telemetry.SpanOptions, callback func(span telemetry.TelemetrySpan) (any, error)) (any, error) {
	return c.startSpanWithMetrics(nil, options, callback)
}

func (c *telTestTracingOnlyContext) Wait() {
	c.wg.Wait()
}

type telTestSpanHandle struct {
	owner   *telTestTracingOnlyContext
	metrics telemetry.TelemetryMetrics
	rec     *telTestSpanRecord
}

func (s *telTestSpanHandle) Metrics() telemetry.TelemetryMetrics { return s.metrics }
func (s *telTestSpanHandle) Propagation() telemetry.TelemetryPropagation {
	return telemetry.NoopTelemetryPropagation
}

func (s *telTestSpanHandle) StartSpan(options *telemetry.SpanOptions, callback func(span telemetry.TelemetrySpan) (any, error)) (any, error) {
	return s.owner.startSpanWithMetrics(s.metrics, options, callback)
}

func (s *telTestSpanHandle) AddLink(telemetry.SpanLink) {}

func (s *telTestSpanHandle) AddEvent(name string, attributes telemetry.SpanAttributes) {
	s.owner.mu.Lock()
	s.rec.Events = append(s.rec.Events, telTestEventRecord{Name: name, Attrs: attributes})
	s.owner.mu.Unlock()
}

func (s *telTestSpanHandle) SetAttributes(attributes telemetry.SpanAttributes) {
	s.owner.mu.Lock()
	s.rec.EndAttributes = append(s.rec.EndAttributes, attributes)
	s.owner.mu.Unlock()
}

func (s *telTestSpanHandle) SetStatus(status telemetry.SpanStatus) {
	s.owner.mu.Lock()
	s.rec.Statuses = append(s.rec.Statuses, status)
	s.owner.mu.Unlock()
}

type telTestFullMetricsContext struct {
	telTestTracingOnlyContext
	metrics            []telTestMetricRecord
	createdInstruments []string
}

func (c *telTestFullMetricsContext) Metrics() telemetry.TelemetryMetrics { return c }

func (c *telTestFullMetricsContext) StartSpan(options *telemetry.SpanOptions, callback func(span telemetry.TelemetrySpan) (any, error)) (any, error) {
	return c.startSpanWithMetrics(c, options, callback)
}

type telTestCounter struct {
	owner *telTestFullMetricsContext
	kind  string
	name  string
	opts  *telemetry.InstrumentOptions
}

func (tc *telTestCounter) Add(value float64, attributes telemetry.SpanAttributes) {
	var desc, unit *string
	if tc.opts != nil {
		desc = tc.opts.Description
		unit = tc.opts.Unit
	}
	tc.owner.mu.Lock()
	tc.owner.metrics = append(tc.owner.metrics, telTestMetricRecord{
		Kind:        tc.kind,
		Name:        tc.name,
		Description: desc,
		Unit:        unit,
		Value:       value,
		Attrs:       attributes,
	})
	tc.owner.mu.Unlock()
}

type telTestHistogram struct {
	owner *telTestFullMetricsContext
	name  string
	opts  *telemetry.HistogramOptions
}

func (th *telTestHistogram) Record(value float64, attributes telemetry.SpanAttributes, context telemetry.TelemetryContext) {
	var desc, unit *string
	if th.opts != nil {
		desc = th.opts.Description
		unit = th.opts.Unit
	}
	th.owner.mu.Lock()
	th.owner.metrics = append(th.owner.metrics, telTestMetricRecord{
		Kind:        "histogram",
		Name:        th.name,
		Description: desc,
		Unit:        unit,
		Value:       value,
		Attrs:       attributes,
		Span:        context,
	})
	th.owner.mu.Unlock()
}

func (c *telTestFullMetricsContext) Counter(name string, options *telemetry.InstrumentOptions) telemetry.TelemetryCounter {
	c.mu.Lock()
	c.createdInstruments = append(c.createdInstruments, "counter:"+name)
	c.mu.Unlock()
	return &telTestCounter{owner: c, kind: "counter", name: name, opts: options}
}

func (c *telTestFullMetricsContext) UpDownCounter(name string, options *telemetry.InstrumentOptions) telemetry.TelemetryUpDownCounter {
	c.mu.Lock()
	c.createdInstruments = append(c.createdInstruments, "upDownCounter:"+name)
	c.mu.Unlock()
	return &telTestCounter{owner: c, kind: "upDownCounter", name: name, opts: options}
}

func (c *telTestFullMetricsContext) Histogram(name string, options *telemetry.HistogramOptions) telemetry.TelemetryHistogram {
	c.mu.Lock()
	c.createdInstruments = append(c.createdInstruments, "histogram:"+name)
	c.mu.Unlock()
	return &telTestHistogram{owner: c, name: name, opts: options}
}

func telTestAttrString(m telemetry.SpanAttributes, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m.Get(key)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func telTestAttrNumber(m telemetry.SpanAttributes, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m.Get(key)
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	default:
		return 0, false
	}
}

func telTestMergedEndAttrs(rec *telTestSpanRecord) telemetry.SpanAttributes {
	merged := omap.NewMap[string, telemetry.AttributeValue]()
	for _, m := range rec.EndAttributes {
		if m == nil {
			continue
		}
		for k, v := range m.All() {
			merged.Set(k, v)
		}
	}
	return merged
}

func telTestCollectSerialized(t *testing.T, rec *telTestSpanRecord, metrics []telTestMetricRecord) string {
	t.Helper()
	var sb strings.Builder
	for k, v := range rec.StartAttributes.All() {
		fmt.Fprintf(&sb, "%s=%v\n", k, v)
	}
	for _, m := range rec.EndAttributes {
		for k, v := range m.All() {
			fmt.Fprintf(&sb, "%s=%v\n", k, v)
		}
	}
	for _, ev := range rec.Events {
		sb.WriteString(ev.Name)
		sb.WriteByte('\n')
		for k, v := range ev.Attrs.All() {
			fmt.Fprintf(&sb, "%s=%v\n", k, v)
		}
	}
	for _, st := range rec.Statuses {
		fmt.Fprintf(&sb, "%#v\n", st)
	}
	for _, met := range metrics {
		sb.WriteString(met.Name)
		sb.WriteByte('\n')
		if met.Attrs != nil {
			for k, v := range met.Attrs.All() {
				fmt.Fprintf(&sb, "%s=%v\n", k, v)
			}
		}
	}
	return sb.String()
}

func telTestModel(id string) *ai.Model {
	return &ai.Model{
		BaseModel: ai.BaseModel{
			Id:       id,
			Name:     id,
			Api:      "tel-mock-api",
			Provider: "tel-prov",
			BaseUrl:  "https://example.com",
			Input:    []ai.ModelModality{ai.ModelModalityText},
		},
		Reasoning:     true,
		ContextWindow: 8192,
		MaxTokens:     2048,
	}
}

func TestTelemetrySchemaAndNoopObservation(t *testing.T) {
	if ai.AiTelemetrySchema == nil || ai.AiTelemetrySchema.Version != 1 {
		t.Fatalf("expected AiTelemetrySchema v1, got %+v", ai.AiTelemetrySchema)
	}
	if _, ok := ai.AiTelemetrySchema.Spans.Get("pi.ai.request"); !ok {
		t.Fatal("expected pi.ai.request span in AiTelemetrySchema")
	}
	if _, ok := ai.AiTelemetrySchema.Spans.Get("pi.ai.poll"); !ok {
		t.Fatal("expected pi.ai.poll span in AiTelemetrySchema")
	}
	if ai.AiTelemetrySchema.Metrics == nil {
		t.Fatal("expected Metrics definitions in AiTelemetrySchema")
	}
	for _, metricName := range []string{"pi.ai.request.duration", "pi.ai.first_chunk.duration", "pi.ai.tokens", "pi.ai.cost"} {
		if _, ok := ai.AiTelemetrySchema.Metrics.Get(metricName); !ok {
			t.Fatalf("expected metric %q in AiTelemetrySchema.Metrics", metricName)
		}
	}

	m := telTestModel("noop-m")
	if obs := ai.ObserveModelRequest(m, nil, false); obs != nil {
		t.Fatal("expected nil observer for nil options")
	}
	if obs := ai.ObserveModelRequest(m, &ai.TelemetryRequestOptions{TelemetryContext: telemetry.NoopTelemetryContext}, false); obs != nil {
		t.Fatal("expected nil observer for NoopTelemetryContext")
	}
}

func TestTelemetryObservesStreamsAndBlocksWithoutLeakingSecrets(t *testing.T) {
	secretText := "TOP_SECRET_USER_ANSWER_😀"
	secretThinking := "SECRET_CHAIN_OF_THOUGHT"
	secretToolArg := `{"password":"super-secret-password"}`

	model := telTestModel("obs-model")
	respId := "resp-42"

	prov, err := ai.CreateProvider(&ai.CreateProviderOptions{
		Id:     "tel-prov",
		Auth:   ai.ProviderAuth{ApiKey: ai.EnvApiKeyAuth("Key", []string{"TEL_TEST_KEY"})},
		Models: []ai.AnyModel{model},
		Api: ai.ProviderStreams{
			Stream: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, _ ai.ProviderStreamOptions) *ai.AssistantMessageEventStream {
				s := ai.NewAssistantMessageEventStream()
				msg := &ai.AssistantMessage{
					Role:       "assistant",
					Api:        ai.Api(m.Api),
					Provider:   m.Provider,
					Model:      m.Id,
					ResponseId: &respId,
					StopReason: ai.StopReasonToolUse,
					Usage: ai.Usage{
						Input:      120,
						Output:     45,
						CacheRead:  30,
						CacheWrite: 15,
						Cost: ai.UsageCost{
							Total: 0.0125,
						},
					},
					Content: ai.ContentList{
						&ai.ThinkingContent{Type: "thinking", Thinking: secretThinking},
						&ai.TextContent{Type: "text", Text: secretText},
						&ai.ToolCall{Type: "toolCall", Id: "tc-1", Name: "bash", Arguments: jsonx.NewObject()},
					},
				}
				go func() {
					s.Push(&ai.AssistantMessageEventStart{Type: "start", Partial: msg})
					s.Push(&ai.AssistantMessageEventThinkingEnd{Type: "thinking_end", ContentIndex: 0, Content: secretThinking, Partial: msg})
					s.Push(&ai.AssistantMessageEventTextEnd{Type: "text_end", ContentIndex: 1, Content: secretText, Partial: msg})
					s.Push(&ai.AssistantMessageEventToolcallDelta{Type: "toolcall_delta", ContentIndex: 2, Delta: secretToolArg, Partial: msg})
					s.Push(&ai.AssistantMessageEventToolcallEnd{
						Type:         "toolcall_end",
						ContentIndex: 2,
						ToolCall:     &ai.ToolCall{Type: "toolCall", Id: "tc-1", Name: "bash", Arguments: jsonx.NewObject()},
						Partial:      msg,
					})
					s.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonToolUse, Message: msg})
					s.End(msg)
				}()
				return s
			},
			StreamSimple: func(_ context.Context, m *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
				s := ai.NewAssistantMessageEventStream()
				msg := &ai.AssistantMessage{
					Role:       "assistant",
					Api:        ai.Api(m.Api),
					Provider:   m.Provider,
					Model:      m.Id,
					StopReason: ai.StopReasonStop,
					Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "ok"}},
				}
				s.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonStop, Message: msg})
				s.End(msg)
				return s
			},
			FetchDeferred: func(_ context.Context, m *ai.Model, _ *ai.DeferredHandle, _ *ai.DeferredFetchOptions) *ai.AssistantMessageEventStream {
				s := ai.NewAssistantMessageEventStream()
				msg := &ai.AssistantMessage{
					Role:       "assistant",
					Api:        ai.Api(m.Api),
					Provider:   m.Provider,
					Model:      m.Id,
					StopReason: ai.StopReasonPending,
					Usage: ai.Usage{
						Input:  50,
						Output: 10,
						Cost:   ai.UsageCost{Total: 0.005},
					},
				}
				s.Push(&ai.AssistantMessageEventDone{Type: "done", Reason: ai.StopReasonPending, Message: msg})
				s.End(msg)
				return s
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}

	models := ai.CreateModels(nil)
	models.SetProvider(prov)

	t.Run("Stream records span, UTF-16 block lengths, metrics, and never leaks content", func(t *testing.T) {
		telCtx := &telTestFullMetricsContext{}
		apiKey := "sk-secret-key"
		prompt := "SECRET_PROMPT_DO_NOT_RECORD"
		res, err := models.Complete(context.Background(), model, &ai.Context{
			Messages: []ai.Message{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &prompt}}},
		}, &ai.ModelsApiStreamOptions{
			ProviderStreamOptions: &ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: &apiKey, TelemetryContext: telCtx},
			},
		})
		if err != nil {
			t.Fatalf("Complete failed: %v", err)
		}
		if res.StopReason != ai.StopReasonToolUse || len(res.Content) != 3 {
			t.Fatalf("expected passthrough result preserved, got %+v", res)
		}
		telCtx.Wait()

		if len(telCtx.spans) != 1 {
			t.Fatalf("expected 1 span, got %d", len(telCtx.spans))
		}
		sp := telCtx.spans[0]
		if !sp.Ended || sp.Name != "pi.ai.request" {
			t.Fatalf("unexpected span %+v", sp)
		}
		if v, _ := telTestAttrString(sp.StartAttributes, "provider"); v != "tel-prov" {
			t.Fatalf("expected provider=tel-prov, got %q", v)
		}
		if v, _ := telTestAttrString(sp.StartAttributes, "model"); v != "obs-model" {
			t.Fatalf("expected model=obs-model, got %q", v)
		}
		if _, hasReasoning := sp.StartAttributes.Get("reasoning"); hasReasoning {
			t.Fatal("expected reasoning omitted on plain Stream")
		}

		endAttrs := telTestMergedEndAttrs(sp)
		if v, _ := telTestAttrString(endAttrs, "stop"); v != "toolUse" {
			t.Fatalf("expected stop=toolUse, got %q", v)
		}
		if v, _ := telTestAttrString(endAttrs, "response.id"); v != "resp-42" {
			t.Fatalf("expected response.id=resp-42, got %q", v)
		}
		if v, _ := telTestAttrNumber(endAttrs, "usage.input"); v != 120 {
			t.Fatalf("expected usage.input=120, got %v", v)
		}
		if v, _ := telTestAttrNumber(endAttrs, "cost.total"); v != 0.0125 {
			t.Fatalf("expected cost.total=0.0125, got %v", v)
		}
		if len(sp.Statuses) != 0 {
			t.Fatalf("expected ok span with no error statuses, got %+v", sp.Statuses)
		}

		// Expect first_chunk once + 3 block events
		if len(sp.Events) != 4 || sp.Events[0].Name != "pi.ai.first_chunk" {
			t.Fatalf("expected first_chunk + 3 block events, got %+v", sp.Events)
		}
		// UTF-16 length: "TOP_SECRET_USER_ANSWER_" (23) + "😀" (2 UTF-16 code units) = 25
		if chars, _ := telTestAttrNumber(sp.Events[2].Attrs, "chars"); chars != 25 {
			t.Fatalf("expected text chars=25 (UTF-16), got %v", chars)
		}
		if toolName, _ := telTestAttrString(sp.Events[3].Attrs, "tool"); toolName != "bash" {
			t.Fatalf("expected tool=bash, got %q", toolName)
		}
		if toolChars, _ := telTestAttrNumber(sp.Events[3].Attrs, "chars"); toolChars != float64(len(secretToolArg)) {
			t.Fatalf("expected tool_call chars=%d, got %v", len(secretToolArg), toolChars)
		}

		// Verify NO secret text/key/prompt leaked anywhere
		dump := telTestCollectSerialized(t, sp, telCtx.metrics)
		for _, forbidden := range []string{secretText, secretThinking, secretToolArg, prompt, apiKey} {
			if strings.Contains(dump, forbidden) {
				t.Fatalf("telemetry leaked secret %q in:\n%s", forbidden, dump)
			}
		}

		// Verify metrics recorded: 2 histograms (first_chunk.duration, request.duration) + 5 counters (4 tokens + cost)
		histCount, counterCount := 0, 0
		seenTokenKinds := map[string]float64{}
		var seenCost float64
		for _, m := range telCtx.metrics {
			switch m.Name {
			case "pi.ai.first_chunk.duration", "pi.ai.request.duration":
				if m.Kind == "histogram" {
					histCount++
				}
			case "pi.ai.tokens":
				counterCount++
				if kind, ok := telTestAttrString(m.Attrs, "kind"); ok {
					seenTokenKinds[kind] = m.Value
				}
			case "pi.ai.cost":
				counterCount++
				seenCost = m.Value
			}
		}
		if histCount != 2 || counterCount != 5 {
			t.Fatalf("expected 2 histograms and 5 counters, got hist=%d counter=%d", histCount, counterCount)
		}
		if seenTokenKinds["input"] != 120 || seenTokenKinds["output"] != 45 ||
			seenTokenKinds["cache_read"] != 30 || seenTokenKinds["cache_write"] != 15 || seenCost != 0.0125 {
			t.Fatalf("unexpected token/cost counter values: tokens=%v cost=%v", seenTokenKinds, seenCost)
		}
	})

	t.Run("StreamSimple sets reasoning attribute and works with tracing-only context", func(t *testing.T) {
		tracingOnly := &telTestTracingOnlyContext{}
		apiKey := "k"
		high := ai.ThinkingLevelHigh
		hi := "hi"
		if _, err := models.CompleteSimple(context.Background(), model, &ai.Context{
			Messages: []ai.Message{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &hi}}},
		}, &ai.ModelsSimpleStreamOptions{
			SimpleStreamOptions: ai.SimpleStreamOptions{
				Reasoning: &high,
				StreamOptions: ai.StreamOptions{
					ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: &apiKey, TelemetryContext: tracingOnly},
				},
			},
		}); err != nil {
			t.Fatalf("CompleteSimple failed: %v", err)
		}
		tracingOnly.Wait()
		if len(tracingOnly.spans) != 1 {
			t.Fatalf("expected 1 span, got %d", len(tracingOnly.spans))
		}
		if r, _ := telTestAttrString(tracingOnly.spans[0].StartAttributes, "reasoning"); r != "high" {
			t.Fatalf("expected reasoning=high, got %q", r)
		}
	})

	t.Run("StreamDeferred uses pi.ai.poll and skips token/cost counters when pending", func(t *testing.T) {
		telCtx := &telTestFullMetricsContext{}
		apiKey := "k"
		handle := &ai.DeferredHandle{Provider: "tel-prov", ModelId: "obs-model", Api: "tel-mock-api", Id: "h1"}
		if _, err := models.FetchDeferred(context.Background(), model, handle, &ai.ModelsDeferredFetchOptions{
			DeferredFetchOptions: ai.DeferredFetchOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: &apiKey, TelemetryContext: telCtx},
			},
		}); err != nil {
			t.Fatalf("FetchDeferred failed: %v", err)
		}
		telCtx.Wait()
		if len(telCtx.spans) != 1 || telCtx.spans[0].Name != "pi.ai.poll" {
			t.Fatalf("expected pi.ai.poll span, got %+v", telCtx.spans)
		}
		for _, m := range telCtx.metrics {
			if m.Name == "pi.ai.tokens" || m.Name == "pi.ai.cost" {
				t.Fatalf("expected pending poll to skip %s counter", m.Name)
			}
		}
	})
}

func TestTelemetryErrorsAbortsInterleavingAndLazyStream(t *testing.T) {
	t.Run("provider error truncates message to 500 runes and aborted leaves status ok", func(t *testing.T) {
		longErr := strings.Repeat("x", 650)
		model := telTestModel("err-model")

		telErr := &telTestFullMetricsContext{}
		obsErr := ai.ObserveModelRequest(model, &ai.TelemetryRequestOptions{TelemetryContext: telErr}, false)
		obsErr.OnResult(&ai.AssistantMessage{
			Role:         "assistant",
			Api:          "tel-mock-api",
			Provider:     "tel-prov",
			Model:        "err-model",
			StopReason:   ai.StopReasonError,
			ErrorMessage: &longErr,
		})
		telErr.Wait()

		spErr := telErr.spans[0]
		if len(spErr.Statuses) != 1 {
			t.Fatalf("expected 1 error status, got %d", len(spErr.Statuses))
		}
		st, ok := spErr.Statuses[0].(*telemetry.SpanStatusError)
		if !ok || st.Error == nil || st.Error.Name != "provider_error" || len([]rune(st.Error.Message)) != 500 {
			t.Fatalf("expected 500-char provider_error status, got %#v", spErr.Statuses[0])
		}

		telAbort := &telTestFullMetricsContext{}
		obsAbort := ai.ObserveModelRequest(model, &ai.TelemetryRequestOptions{TelemetryContext: telAbort}, false)
		obsAbort.OnResult(&ai.AssistantMessage{
			Role:       "assistant",
			Api:        "tel-mock-api",
			Provider:   "tel-prov",
			Model:      "err-model",
			StopReason: ai.StopReasonAborted,
		})
		telAbort.Wait()
		if len(telAbort.spans[0].Statuses) != 0 {
			t.Fatalf("expected aborted request not to set error status, got %+v", telAbort.spans[0].Statuses)
		}
		if stop, _ := telTestAttrString(telTestMergedEndAttrs(telAbort.spans[0]), "stop"); stop != "aborted" {
			t.Fatalf("expected stop=aborted, got %q", stop)
		}
	})

	t.Run("interleaved toolcall_delta maintains independent per-index UTF-16 lengths", func(t *testing.T) {
		telCtx := &telTestFullMetricsContext{}
		model := telTestModel("interleave")
		obs := ai.ObserveModelRequest(model, &ai.TelemetryRequestOptions{TelemetryContext: telCtx}, false)

		obs.OnEvent(&ai.AssistantMessageEventToolcallDelta{ContentIndex: 0, Delta: `{"a":`})
		obs.OnEvent(&ai.AssistantMessageEventToolcallDelta{ContentIndex: 1, Delta: `"😀"`}) // 4 UTF-16 units
		obs.OnEvent(&ai.AssistantMessageEventToolcallDelta{ContentIndex: 0, Delta: `123}`})
		obs.OnEvent(&ai.AssistantMessageEventToolcallEnd{ContentIndex: 1, ToolCall: &ai.ToolCall{Name: "t1"}})
		obs.OnEvent(&ai.AssistantMessageEventToolcallEnd{ContentIndex: 0, ToolCall: &ai.ToolCall{Name: "t0"}})
		obs.OnResult(&ai.AssistantMessage{StopReason: ai.StopReasonToolUse})
		telCtx.Wait()

		ev1 := telCtx.spans[0].Events[1] // first_chunk is 0
		ev0 := telCtx.spans[0].Events[2]
		if c1, _ := telTestAttrNumber(ev1.Attrs, "chars"); c1 != 4 {
			t.Fatalf("expected index 1 chars=4, got %v", c1)
		}
		if c0, _ := telTestAttrNumber(ev0.Attrs, "chars"); c0 != 9 {
			t.Fatalf("expected index 0 chars=9, got %v", c0)
		}
	})

	t.Run("LazyStream observes setup failure and aborted setup cleanly without extra iteration", func(t *testing.T) {
		telCtx := &telTestFullMetricsContext{}
		model := telTestModel("lazy-fail")
		obs := ai.ObserveModelRequest(model, &ai.TelemetryRequestOptions{TelemetryContext: telCtx}, false)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		stream := ai.LazyStream(model, func() (*ai.AssistantMessageEventStream, error) {
			return nil, fmt.Errorf("auth setup failed")
		}, obs, ctx)

		msg, err := stream.Result(context.Background())
		if err != nil {
			t.Fatalf("expected error encoded on AssistantMessage, got %v", err)
		}
		if msg.StopReason != ai.StopReasonAborted {
			t.Fatalf("expected aborted stopReason on canceled ctx, got %q", msg.StopReason)
		}
		telCtx.Wait()
		if len(telCtx.spans) != 1 || !telCtx.spans[0].Ended {
			t.Fatalf("expected 1 completed span, got %+v", telCtx.spans)
		}
	})

	t.Run("Models.Stream setup error with unknown provider completes telemetry span", func(t *testing.T) {
		models := ai.CreateModels(nil)
		telCtx := &telTestFullMetricsContext{}
		unknownModel := telTestModel("missing")
		apiKey := "k"
		hi := "hi"
		stream := models.Stream(context.Background(), unknownModel, &ai.Context{
			Messages: []ai.Message{&ai.UserMessage{Role: "user", Content: ai.UserMessageContent{Text: &hi}}},
		}, &ai.ModelsApiStreamOptions{
			ProviderStreamOptions: &ai.StreamOptions{
				ProviderRequestOptions: ai.ProviderRequestOptions[ai.Model]{ApiKey: &apiKey, TelemetryContext: telCtx},
			},
		})
		res, err := stream.Result(context.Background())
		if err != nil {
			t.Fatalf("Result returned error: %v", err)
		}
		if res.StopReason != ai.StopReasonError || res.ErrorMessage == nil || !strings.Contains(*res.ErrorMessage, "Unknown provider") {
			t.Fatalf("expected Unknown provider error message, got %+v", res)
		}
		telCtx.Wait()
		if len(telCtx.spans) != 1 || !telCtx.spans[0].Ended || len(telCtx.spans[0].Statuses) != 1 {
			t.Fatalf("expected failed span recorded cleanly, got %+v", telCtx.spans)
		}
	})
}

func TestTelemetryInMemoryIntegrationAndInstrumentCaching(t *testing.T) {
	model := telTestModel("inmem-m")
	mem := telemetry.NewInMemoryTelemetryContext()

	_, err := mem.StartSpan(&telemetry.SpanOptions{Name: "caller"}, func(parent telemetry.TelemetrySpan) (any, error) {
		obs := ai.ObserveModelRequest(model, &ai.TelemetryRequestOptions{TelemetryContext: parent}, false)
		msg := &ai.AssistantMessage{
			Role:       "assistant",
			Api:        ai.Api(model.Api),
			Provider:   model.Provider,
			Model:      model.Id,
			StopReason: ai.StopReasonStop,
			Usage: ai.Usage{
				Input:      11,
				Output:     22,
				CacheRead:  33,
				CacheWrite: 44,
				Cost:       ai.UsageCost{Total: 1},
			},
		}
		obs.OnEvent(&ai.AssistantMessageEventStart{Type: "start", Partial: msg})
		obs.OnEvent(&ai.AssistantMessageEventTextEnd{Type: "text_end", ContentIndex: 0, Content: "hello", Partial: msg})
		obs.OnResult(msg)
		return nil, nil
	})
	if err != nil {
		t.Fatalf("StartSpan failed: %v", err)
	}

	spans := mem.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans (caller + pi.ai.request), got %d", len(spans))
	}
	if spans[1].Name != "pi.ai.request" || !spans[1].Settled {
		t.Fatalf("expected settled pi.ai.request span, got %+v", spans[1])
	}
	pid, ok := spans[1].ParentId.Get()
	if !ok || pid != spans[0].Id {
		t.Fatalf("expected child parentId=%d, got (%d, %v)", spans[0].Id, pid, ok)
	}

	measurements := mem.GetMetrics()
	if len(measurements) != 7 {
		t.Fatalf("expected 7 metric measurements, got %d", len(measurements))
	}
	for _, m := range measurements {
		if m.Unit == nil {
			t.Fatalf("expected non-nil unit on metric %s", m.Name)
		}
		switch m.Name {
		case "pi.ai.first_chunk.duration", "pi.ai.request.duration":
			if *m.Unit != "ms" || m.Context.IsNull() || m.Context.IsZero() {
				t.Fatalf("unexpected histogram measurement %+v", m)
			}
		case "pi.ai.tokens":
			if *m.Unit != "token" {
				t.Fatalf("unexpected tokens unit %q", *m.Unit)
			}
		case "pi.ai.cost":
			if *m.Unit != "USD" || m.Value != 1 {
				t.Fatalf("unexpected cost measurement %+v", m)
			}
		}
	}

	// Verify instruments are created once per adapter across multiple requests.
	cachedCtx := &telTestFullMetricsContext{}
	for range 3 {
		obs := ai.ObserveModelRequest(model, &ai.TelemetryRequestOptions{TelemetryContext: cachedCtx}, false)
		obs.OnEvent(&ai.AssistantMessageEventStart{Type: "start"})
		obs.OnResult(&ai.AssistantMessage{StopReason: ai.StopReasonStop})
	}
	cachedCtx.Wait()
	if len(cachedCtx.createdInstruments) != 4 {
		t.Fatalf("expected 4 instruments created once per adapter, got %v", cachedCtx.createdInstruments)
	}
	if len(cachedCtx.metrics) != 21 {
		t.Fatalf("expected 21 measurements across 3 calls, got %d", len(cachedCtx.metrics))
	}
}
