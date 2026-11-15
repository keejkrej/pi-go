// Ported from packages/ai/src/telemetry.ts (pi v1.5.2).

package ai

import (
	"reflect"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/telemetry"
)

func telProviderAttr() telemetry.TelemetryStartAttributeDefinition {
	return telemetry.TelemetryStartAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeString,
		Required:    true,
		Description: "Provider identifier.",
	}
}

func telModelAttr() telemetry.TelemetryStartAttributeDefinition {
	return telemetry.TelemetryStartAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeString,
		Required:    true,
		Description: "Requested model identifier.",
	}
}

func telStopValues() []string {
	return []string{"pending", "stop", "length", "toolUse", "error", "aborted", "deferred"}
}

func telBuildRequestSpanDefinition(description string) telemetry.TelemetrySpanDefinition {
	providerAttr := telProviderAttr()
	modelAttr := telModelAttr()
	startAttrs := omap.NewMap[string, telemetry.TelemetryStartAttributeDefinition]()
	startAttrs.Set("provider", providerAttr)
	startAttrs.Set("api", telemetry.TelemetryStartAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeString,
		Required:    true,
		Description: "API identifier.",
	})
	startAttrs.Set("model", modelAttr)
	startAttrs.Set("reasoning", telemetry.TelemetryStartAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeString,
		Required:    false,
		Description: "Scalar requested reasoning level.",
	})

	highCard := telemetry.TelemetryCardinalityHigh
	endAttrs := omap.NewMap[string, telemetry.TelemetryAttributeDefinition]()
	endAttrs.Set("stop", telemetry.TelemetryAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeString,
		Description: "Normalized stop reason.",
		Values:      telStopValues(),
	})
	endAttrs.Set("usage.input", telemetry.TelemetryAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeNumber,
		Description: "Uncached input tokens.",
	})
	endAttrs.Set("usage.output", telemetry.TelemetryAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeNumber,
		Description: "Output tokens.",
	})
	endAttrs.Set("usage.cache_read", telemetry.TelemetryAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeNumber,
		Description: "Cache-read tokens.",
	})
	endAttrs.Set("usage.cache_write", telemetry.TelemetryAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeNumber,
		Description: "Cache-write tokens.",
	})
	endAttrs.Set("cost.total", telemetry.TelemetryAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeNumber,
		Description: "Total request cost in USD.",
	})
	endAttrs.Set("response.id", telemetry.TelemetryAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeString,
		Description: "Provider response identifier.",
		Cardinality: &highCard,
	})

	firstChunkAttrs := omap.NewMap[string, telemetry.TelemetryEventAttributeDefinition]()
	blockAttrs := omap.NewMap[string, telemetry.TelemetryEventAttributeDefinition]()
	blockAttrs.Set("type", telemetry.TelemetryEventAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeString,
		Required:    true,
		Values:      []string{"text", "thinking", "tool_call"},
		Description: "Block kind.",
	})
	blockAttrs.Set("index", telemetry.TelemetryEventAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeNumber,
		Required:    true,
		Description: "Content block index.",
	})
	blockAttrs.Set("chars", telemetry.TelemetryEventAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeNumber,
		Required:    true,
		Description: "UTF-16 character count.",
	})
	blockAttrs.Set("tool", telemetry.TelemetryEventAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeString,
		Required:    false,
		Description: "Tool name, for tool-call blocks only.",
	})

	events := omap.NewMap[string, telemetry.TelemetryEventDefinition]()
	events.Set("pi.ai.first_chunk", telemetry.TelemetryEventDefinition{
		Description: "First normalized stream event delivered.",
		Attributes:  firstChunkAttrs,
	})
	events.Set("pi.ai.block", telemetry.TelemetryEventDefinition{
		Description: "A completed content block; content is never recorded.",
		Attributes:  blockAttrs,
	})

	return telemetry.TelemetrySpanDefinition{
		Description:     description,
		Parents:         &telemetry.TelemetryParentAny{Kind: telemetry.TelemetryParentKindAny},
		StartAttributes: startAttrs,
		EndAttributes:   endAttrs,
		Events:          events,
		Status: telemetry.TelemetryStatusRule{
			Default:   "ok",
			ErrorWhen: "Stop reason is error: provider_error with at most 500 error-message characters. Aborted stays ok.",
		},
	}
}

func telMetricBaseAttrs() *omap.Map[string, telemetry.TelemetryMetricAttributeDefinition] {
	attrs := omap.NewMap[string, telemetry.TelemetryMetricAttributeDefinition]()
	attrs.Set("provider", telProviderAttr())
	attrs.Set("model", telModelAttr())
	return attrs
}

func telBuildTelemetrySchema() *telemetry.TelemetrySchemaDefinition {
	spans := omap.NewMap[string, telemetry.TelemetrySpanDefinition]()
	spans.Set("pi.ai.request", telBuildRequestSpanDefinition("One logical model request, including authentication and provider setup."))
	spans.Set("pi.ai.poll", telBuildRequestSpanDefinition("One deferred-response poll, not the deferred request's lifetime."))

	unitMs := "ms"
	unitToken := "token"
	unitUSD := "USD"

	reqDurAttrs := telMetricBaseAttrs()
	reqDurAttrs.Set("stop", telemetry.TelemetryMetricAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeString,
		Required:    true,
		Description: "Normalized stop reason.",
		Values:      telStopValues(),
	})

	tokensAttrs := telMetricBaseAttrs()
	tokensAttrs.Set("kind", telemetry.TelemetryMetricAttributeDefinition{
		Type:        telemetry.TelemetryAttributeTypeString,
		Required:    true,
		Description: "Token usage category.",
		Values:      []string{"input", "output", "cache_read", "cache_write"},
	})

	metrics := omap.NewMap[string, telemetry.TelemetryMetricDefinition]()
	metrics.Set("pi.ai.request.duration", telemetry.TelemetryMetricDefinition{
		Kind:        telemetry.TelemetryMetricKindHistogram,
		Description: "Logical request or poll duration.",
		Unit:        &unitMs,
		Attributes:  reqDurAttrs,
	})
	metrics.Set("pi.ai.first_chunk.duration", telemetry.TelemetryMetricDefinition{
		Kind:        telemetry.TelemetryMetricKindHistogram,
		Description: "Time to the first normalized stream event.",
		Unit:        &unitMs,
		Attributes:  telMetricBaseAttrs(),
	})
	metrics.Set("pi.ai.tokens", telemetry.TelemetryMetricDefinition{
		Kind:        telemetry.TelemetryMetricKindCounter,
		Description: "Reported model token usage; pending and deferred polls are excluded.",
		Unit:        &unitToken,
		Attributes:  tokensAttrs,
	})
	metrics.Set("pi.ai.cost", telemetry.TelemetryMetricDefinition{
		Kind:        telemetry.TelemetryMetricKindCounter,
		Description: "Reported model cost; pending and deferred polls are excluded.",
		Unit:        &unitUSD,
		Attributes:  telMetricBaseAttrs(),
	})

	return telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{
		Version: 1,
		Spans:   spans,
		Metrics: metrics,
	})
}

// AiTelemetrySchema defines Pi-owned request metadata, bounded error status, events, and low-cardinality metrics.
var AiTelemetrySchema = telBuildTelemetrySchema()

// TelemetryRequestOptions configures telemetry observation for one model request.
type TelemetryRequestOptions struct {
	TelemetryContext telemetry.TelemetryContext
	Reasoning        *ModelThinkingLevel
}

type telInstruments struct {
	requestDuration    telemetry.SchemaTelemetryHistogram
	firstChunkDuration telemetry.SchemaTelemetryHistogram
	tokens             telemetry.SchemaTelemetryCounter
	cost               telemetry.SchemaTelemetryCounter
}

var (
	telInstrumentCacheMu sync.Mutex
	telInstrumentCache   = map[telemetry.TelemetryMetrics]*telInstruments{}
)

func telCreateInstruments(adapter telemetry.TelemetryMetrics) *telInstruments {
	metrics := telemetry.CreateTypedMetrics(adapter, []*telemetry.TelemetrySchemaDefinition{AiTelemetrySchema})
	return &telInstruments{
		requestDuration:    metrics.Histogram("pi.ai.request.duration"),
		firstChunkDuration: metrics.Histogram("pi.ai.first_chunk.duration"),
		tokens:             metrics.Counter("pi.ai.tokens"),
		cost:               metrics.Counter("pi.ai.cost"),
	}
}

func telGetInstruments(adapter telemetry.TelemetryMetrics) *telInstruments {
	if adapter == nil {
		adapter = telemetry.NoopTelemetryMetrics
	}
	comparable := reflect.TypeOf(adapter).Comparable()
	if comparable {
		telInstrumentCacheMu.Lock()
		cached := telInstrumentCache[adapter]
		telInstrumentCacheMu.Unlock()
		if cached != nil {
			return cached
		}
	}
	created := telCreateInstruments(adapter)
	if comparable {
		telInstrumentCacheMu.Lock()
		if existing := telInstrumentCache[adapter]; existing != nil {
			telInstrumentCacheMu.Unlock()
			return existing
		}
		telInstrumentCache[adapter] = created
		telInstrumentCacheMu.Unlock()
	}
	return created
}

type telStreamObserver struct {
	spanName       string
	started        time.Time
	provider       string
	modelId        string
	poll           bool
	span           telemetry.TelemetrySpan
	instruments    *telInstruments
	doneCh         chan struct{}
	spanDone       chan struct{}
	doneOnce       sync.Once
	mu             sync.Mutex
	firstChunkDone bool
	toolChars      map[int]int
}

func (o *telStreamObserver) finish() {
	o.doneOnce.Do(func() {
		close(o.doneCh)
	})
	if o.spanDone != nil {
		<-o.spanDone
	}
}

func (o *telStreamObserver) baseAttrs() telemetry.SpanAttributes {
	attrs := omap.NewMap[string, telemetry.AttributeValue]()
	attrs.Set("provider", o.provider)
	attrs.Set("model", o.modelId)
	return attrs
}

func (o *telStreamObserver) OnEvent(event AssistantMessageEvent) {
	if o == nil || o.span == nil || event == nil {
		return
	}
	o.mu.Lock()
	first := !o.firstChunkDone
	if first {
		o.firstChunkDone = true
	}
	if delta, ok := event.(*AssistantMessageEventToolcallDelta); ok {
		o.toolChars[delta.ContentIndex] += js.Len(delta.Delta)
	}
	var toolCallEndChars int
	if tce, ok := event.(*AssistantMessageEventToolcallEnd); ok {
		toolCallEndChars = o.toolChars[tce.ContentIndex]
		delete(o.toolChars, tce.ContentIndex)
	}
	o.mu.Unlock()

	if first {
		o.span.AddEvent("pi.ai.first_chunk", omap.NewMap[string, telemetry.AttributeValue]())
		if o.instruments != nil {
			elapsedMs := float64(time.Since(o.started).Nanoseconds()) / 1e6
			o.instruments.firstChunkDuration.Record(elapsedMs, o.baseAttrs(), o.span)
		}
	}

	switch e := event.(type) {
	case *AssistantMessageEventTextEnd:
		attrs := omap.NewMap[string, telemetry.AttributeValue]()
		attrs.Set("type", "text")
		attrs.Set("index", e.ContentIndex)
		attrs.Set("chars", js.Len(e.Content))
		o.span.AddEvent("pi.ai.block", attrs)
	case *AssistantMessageEventThinkingEnd:
		attrs := omap.NewMap[string, telemetry.AttributeValue]()
		attrs.Set("type", "thinking")
		attrs.Set("index", e.ContentIndex)
		attrs.Set("chars", js.Len(e.Content))
		o.span.AddEvent("pi.ai.block", attrs)
	case *AssistantMessageEventToolcallEnd:
		attrs := omap.NewMap[string, telemetry.AttributeValue]()
		attrs.Set("type", "tool_call")
		attrs.Set("index", e.ContentIndex)
		attrs.Set("chars", toolCallEndChars)
		attrs.Set("tool", e.ToolCall.Name)
		o.span.AddEvent("pi.ai.block", attrs)
	}
}

func (o *telStreamObserver) OnResult(message *AssistantMessage) {
	if o == nil {
		return
	}
	defer o.finish()
	if o.span == nil {
		return
	}
	if message != nil {
		endAttrs := omap.NewMap[string, telemetry.AttributeValue]()
		endAttrs.Set("stop", string(message.StopReason))
		endAttrs.Set("usage.input", float64(message.Usage.Input))
		endAttrs.Set("usage.output", float64(message.Usage.Output))
		endAttrs.Set("usage.cache_read", float64(message.Usage.CacheRead))
		endAttrs.Set("usage.cache_write", float64(message.Usage.CacheWrite))
		endAttrs.Set("cost.total", message.Usage.Cost.Total)
		if message.ResponseId != nil {
			endAttrs.Set("response.id", *message.ResponseId)
		}
		o.span.SetAttributes(endAttrs)

		if message.StopReason == StopReasonError {
			errMsg := ""
			if message.ErrorMessage != nil {
				errMsg = OwnedSlice(*message.ErrorMessage, 0, 500)
			}
			o.span.SetStatus(&telemetry.SpanStatusError{
				Status: telemetry.SpanStatusKindError,
				Error: &telemetry.SpanError{
					Name:    "provider_error",
					Message: errMsg,
				},
			})
		}

		if o.instruments != nil {
			elapsedMs := float64(time.Since(o.started).Nanoseconds()) / 1e6
			durAttrs := o.baseAttrs()
			durAttrs.Set("stop", string(message.StopReason))
			o.instruments.requestDuration.Record(elapsedMs, durAttrs, o.span)

			if !o.poll || (message.StopReason != StopReasonPending && message.StopReason != StopReasonDeferred) {
				for _, tc := range []struct {
					kind string
					val  int
				}{
					{"input", message.Usage.Input},
					{"output", message.Usage.Output},
					{"cache_read", message.Usage.CacheRead},
					{"cache_write", message.Usage.CacheWrite},
				} {
					tokAttrs := o.baseAttrs()
					tokAttrs.Set("kind", tc.kind)
					o.instruments.tokens.Add(float64(tc.val), tokAttrs)
				}
				o.instruments.cost.Add(message.Usage.Cost.Total, o.baseAttrs())
			}
		}
	}
}

// ObserveModelRequest creates a LazyStreamObserver for a model request or deferred poll.
// When options.TelemetryContext is nil or NoopTelemetryContext, returns nil.
func ObserveModelRequest(requestedModel *Model, options *TelemetryRequestOptions, poll bool) LazyStreamObserver {
	if requestedModel == nil || options == nil || options.TelemetryContext == nil || options.TelemetryContext == telemetry.NoopTelemetryContext {
		return nil
	}

	spanName := "pi.ai.request"
	if poll {
		spanName = "pi.ai.poll"
	}
	startAttrs := omap.NewMap[string, telemetry.AttributeValue]()
	startAttrs.Set("provider", string(requestedModel.Provider))
	startAttrs.Set("api", requestedModel.Api)
	startAttrs.Set("model", requestedModel.Id)
	if options.Reasoning != nil && *options.Reasoning != "" {
		startAttrs.Set("reasoning", string(*options.Reasoning))
	}

	spanReady := make(chan telemetry.TelemetrySpan, 1)
	spanDone := make(chan struct{})
	obs := &telStreamObserver{
		spanName:    spanName,
		started:     time.Now(),
		provider:    string(requestedModel.Provider),
		modelId:     requestedModel.Id,
		poll:        poll,
		instruments: telGetInstruments(options.TelemetryContext.Metrics()),
		doneCh:      make(chan struct{}),
		spanDone:    spanDone,
		toolChars:   make(map[int]int),
	}

	go func() {
		defer close(spanDone)
		_, _ = options.TelemetryContext.StartSpan(&telemetry.SpanOptions{
			Name:       spanName,
			Attributes: startAttrs,
		}, func(activeSpan telemetry.TelemetrySpan) (any, error) {
			spanReady <- activeSpan
			<-obs.doneCh
			return nil, nil
		})
	}()

	select {
	case sp := <-spanReady:
		obs.span = sp
	case <-spanDone:
		return nil
	}
	return obs
}
