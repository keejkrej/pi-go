// Ported from packages/telemetry/src/memory.ts (pi v1.0.0).

package telemetry

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// RecordedTelemetryEvent is a detached event snapshot.
type RecordedTelemetryEvent struct {
	Name       string         `json:"name"`
	Attributes SpanAttributes `json:"attributes"`
}

// RecordedTelemetryLink is a detached link snapshot.
type RecordedTelemetryLink struct {
	Context    TelemetryCarrier `json:"context"`
	Attributes SpanAttributes   `json:"attributes"`
}

// RecordedTelemetrySpan is a detached span snapshot in span-start order.
// ParentId and ParentContext are null when the span has no parent.
// EndSequence is absent until the span settles.
// Attributes, Links, and Events are empty, not nil, when unset so they marshal as {} and [].
type RecordedTelemetrySpan struct {
	Id            int                         `json:"id"`
	ParentId      jsonx.Opt[int]              `json:"parentId"`
	Context       TelemetryCarrier            `json:"context"`
	ParentContext jsonx.Opt[TelemetryCarrier] `json:"parentContext"`
	Links         []RecordedTelemetryLink     `json:"links"`
	Name          string                      `json:"name"`
	Attributes    SpanAttributes              `json:"attributes"`
	Events        []RecordedTelemetryEvent    `json:"events"`
	Status        SpanStatus                  `json:"status"`
	Settled       bool                        `json:"settled"`
	EndSequence   *int                        `json:"endSequence,omitzero"`
}

// UnmarshalJSON decodes a RecordedTelemetrySpan JSON object, preserving polymorphic SpanStatus.
func (s *RecordedTelemetrySpan) UnmarshalJSON(data []byte) error {
	v, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	type indexRecordedSpanAlias struct {
		Id            int                         `json:"id"`
		ParentId      jsonx.Opt[int]              `json:"parentId"`
		Context       TelemetryCarrier            `json:"context"`
		ParentContext jsonx.Opt[TelemetryCarrier] `json:"parentContext"`
		Links         []RecordedTelemetryLink     `json:"links"`
		Name          string                      `json:"name"`
		Attributes    SpanAttributes              `json:"attributes"`
		Events        []RecordedTelemetryEvent    `json:"events"`
		Status        any                         `json:"status"`
		Settled       bool                        `json:"settled"`
		EndSequence   *int                        `json:"endSequence,omitzero"`
	}
	var raw indexRecordedSpanAlias
	if err := jsonx.Decode(v, &raw); err != nil {
		return err
	}
	var status SpanStatus
	if raw.Status != nil {
		status, err = DecodeSpanStatus(raw.Status)
		if err != nil {
			return err
		}
	}
	*s = RecordedTelemetrySpan{
		Id:            raw.Id,
		ParentId:      raw.ParentId,
		Context:       raw.Context,
		ParentContext: raw.ParentContext,
		Links:         raw.Links,
		Name:          raw.Name,
		Attributes:    raw.Attributes,
		Events:        raw.Events,
		Status:        status,
		Settled:       raw.Settled,
		EndSequence:   raw.EndSequence,
	}
	return nil
}

// RecordedTelemetryMeasurement is a detached measurement snapshot in recording order.
type RecordedTelemetryMeasurement struct {
	Kind        TelemetryMetricKind         `json:"kind"`
	Name        string                      `json:"name"`
	Description *string                     `json:"description,omitzero"`
	Unit        *string                     `json:"unit,omitzero"`
	Boundaries  []float64                   `json:"boundaries,omitzero"`
	Value       float64                     `json:"value"`
	Attributes  SpanAttributes              `json:"attributes"`
	Context     jsonx.Opt[TelemetryCarrier] `json:"context"`
}

// How far the anchored clock may part from Date.now() before it is anchored anew:
// more than Date.now()'s own truncation to whole milliseconds, less than any step
// worth following (a wall clock set, a sleep the monotonic clock did not count).
const memoryToleranceMs = 10

var (
	memoryStartTime = time.Now()
	memoryDateNow   = func() float64 { return float64(time.Now().UnixMilli()) }
	memoryPerfNow   = func() float64 { return float64(time.Since(memoryStartTime).Nanoseconds()) / 1e6 }
	memoryClockMu   sync.Mutex
	memoryAnchor    = float64(memoryStartTime.UnixNano()) / 1e6
)

// TelemetryNow returns wall-clock milliseconds since the epoch, rounded to the microsecond.
// While the monotonic and wall clocks agree within 10 ms, times in one process keep their
// order and spacing to the microsecond; past that the clock follows Date.now() again.
func TelemetryNow() float64 {
	memoryClockMu.Lock()
	clock := memoryPerfNow()
	wall := memoryDateNow()
	if math.Abs(memoryAnchor+clock-wall) > memoryToleranceMs {
		memoryAnchor = wall - clock
	}
	now := memoryAnchor + clock
	memoryClockMu.Unlock()
	return memoryMicroseconds(now)
}

func memoryCurrentDateNow() float64 {
	memoryClockMu.Lock()
	wall := memoryDateNow()
	memoryClockMu.Unlock()
	return wall
}

func memoryCurrentPerfNow() float64 {
	memoryClockMu.Lock()
	clock := memoryPerfNow()
	memoryClockMu.Unlock()
	return clock
}

// MemoryOverrideClock replaces the package's wall and monotonic clock sources for tests
// and returns a restore function. Either callback may be nil to leave that clock unchanged.
func MemoryOverrideClock(dateNow func() float64, perfNow func() float64) func() {
	memoryClockMu.Lock()
	prevDate := memoryDateNow
	prevPerf := memoryPerfNow
	prevAnchor := memoryAnchor
	if dateNow != nil {
		memoryDateNow = dateNow
	}
	if perfNow != nil {
		memoryPerfNow = perfNow
	}
	memoryClockMu.Unlock()
	return func() {
		memoryClockMu.Lock()
		memoryDateNow = prevDate
		memoryPerfNow = prevPerf
		memoryAnchor = prevAnchor
		memoryClockMu.Unlock()
	}
}

// memoryMicroseconds returns ms rounded to the microsecond, as span times are.
func memoryMicroseconds(ms float64) float64 {
	return js.Round(ms*1000) / 1000
}

type memoryCarrierProvider interface {
	memoryCarrier() TelemetryCarrier
}

// InMemoryTelemetryContext is a backend-neutral reference implementation that records spans in process memory.
// Create a fresh instance to isolate tests or independent recording scopes.
type InMemoryTelemetryContext struct {
	mu    sync.Mutex
	state memoryInMemoryTelemetryState
}

// NewInMemoryTelemetryContext returns an empty recording context.
func NewInMemoryTelemetryContext() *InMemoryTelemetryContext {
	return &InMemoryTelemetryContext{
		state: memoryInMemoryTelemetryState{
			scope:           uuid.NewString(),
			spans:           []*memoryMutableRecordedTelemetrySpan{},
			measurements:    []memoryIntervalMeasurement{},
			nextSpanId:      1,
			nextEndSequence: 1,
		},
	}
}

func (c *InMemoryTelemetryContext) Metrics() TelemetryMetrics {
	return c
}

func (c *InMemoryTelemetryContext) Propagation() TelemetryPropagation {
	return c
}

func (c *InMemoryTelemetryContext) StartSpan(options *SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error) {
	return memoryStartInMemorySpan(c, nil, options, callback)
}

// Inject returns a detached carrier for a known span or extracted parent, including settled spans.
func (c *InMemoryTelemetryContext) Inject(context TelemetryContext) TelemetryCarrier {
	if context == nil {
		return nil
	}
	cp, ok := context.(memoryCarrierProvider)
	if !ok {
		return nil
	}
	carrier := cp.memoryCarrier()
	if carrier == nil {
		return nil
	}
	return memoryCopyCarrier(carrier)
}

// Extract returns a receiver-owned parent context for carrier.
// Missing or non-string carrier identities fall back to this recorder's root context.
func (c *InMemoryTelemetryContext) Extract(carrier TelemetryCarrier) TelemetryContext {
	if carrier == nil {
		return c
	}
	scope, okScope := carrier.Get("pi-memory-scope")
	span, okSpan := carrier.Get("pi-memory-span")
	traceId, okTrace := carrier.Get("pi-memory-trace")
	if !okScope || !okSpan || !okTrace {
		return c
	}
	copied := omap.NewMap[string, string]()
	copied.Set("pi-memory-scope", scope)
	copied.Set("pi-memory-span", span)
	copied.Set("pi-memory-trace", traceId)
	var localId *int
	if scope == c.state.scope {
		if parsed, err := strconv.Atoi(span); err == nil {
			localId = &parsed
		}
	}
	return &memoryExtractedContext{
		ctx: c,
		parent: &memoryInMemoryParent{
			context: copied,
			localId: localId,
		},
	}
}

func (c *InMemoryTelemetryContext) Counter(name string, options *InstrumentOptions) TelemetryCounter {
	var histOpts *HistogramOptions
	if options != nil {
		histOpts = &HistogramOptions{InstrumentOptions: *options}
	}
	return &memoryCounterRecorder{record: memoryCreateMeasurementRecorder(c, TelemetryMetricKindCounter, name, histOpts)}
}

func (c *InMemoryTelemetryContext) UpDownCounter(name string, options *InstrumentOptions) TelemetryUpDownCounter {
	var histOpts *HistogramOptions
	if options != nil {
		histOpts = &HistogramOptions{InstrumentOptions: *options}
	}
	return &memoryCounterRecorder{record: memoryCreateMeasurementRecorder(c, TelemetryMetricKindUpDownCounter, name, histOpts)}
}

func (c *InMemoryTelemetryContext) Histogram(name string, options *HistogramOptions) TelemetryHistogram {
	return &memoryHistogramRecorder{record: memoryCreateMeasurementRecorder(c, TelemetryMetricKindHistogram, name, options)}
}

func (c *InMemoryTelemetryContext) storedSpansLocked() []StoredSpan {
	out := make([]StoredSpan, 0, len(c.state.spans))
	for _, span := range c.state.spans {
		if !span.settled {
			continue
		}
		traceId, _ := span.context.Get("pi-memory-trace")
		var parentId *string
		if span.parentContext != nil {
			pid := memoryCarrierId(span.parentContext)
			parentId = &pid
		}
		events := make([]StoredSpanEvent, len(span.events))
		for i, ev := range span.events {
			events[i] = StoredSpanEvent{
				Name:       ev.name,
				TimeMs:     ev.timeMs,
				Attributes: memoryCopyAttributes(ev.attributes),
			}
		}
		links := make([]StoredSpanLink, len(span.links))
		for i, link := range span.links {
			linkTraceId, _ := link.Context.Get("pi-memory-trace")
			links[i] = StoredSpanLink{
				TraceId:    linkTraceId,
				SpanId:     memoryCarrierId(link.Context),
				Attributes: memoryCopyAttributes(link.Attributes),
			}
		}
		out = append(out, StoredSpan{
			Id:         memoryCarrierId(span.context),
			TraceId:    traceId,
			ParentId:   parentId,
			Name:       span.name,
			StartedMs:  span.startedMs,
			EndedMs:    *span.endedMs,
			Attributes: memoryCopyAttributes(span.attributes),
			Events:     events,
			Links:      links,
			Status:     memoryCopyStatus(span.status),
		})
	}
	slices.SortStableFunc(out, func(a, b StoredSpan) int {
		if c := cmp.Compare(a.StartedMs, b.StartedMs); c != 0 {
			return c
		}
		return cmp.Compare(a.Id, b.Id)
	})
	return out
}

// GetTrace returns settled spans belonging to traceId, or nil when absent.
func (c *InMemoryTelemetryContext) GetTrace(traceId string) (*StoredTrace, error) {
	c.mu.Lock()
	all := c.storedSpansLocked()
	c.mu.Unlock()

	var spans []StoredSpan
	for _, span := range all {
		if span.TraceId == traceId {
			spans = append(spans, span)
		}
	}
	if len(spans) == 0 {
		return nil, nil
	}
	return &StoredTrace{TraceId: traceId, Spans: spans}, nil
}

// FindTraces returns complete locally stored traces matching query, newest first.
func (c *InMemoryTelemetryContext) FindTraces(query *TraceQuery) (FindTracesResult, error) {
	if query == nil {
		query = &TraceQuery{}
	}
	limit := 20
	if query.Limit != nil {
		limit = *query.Limit
	}
	var (
		hasAfter     bool
		afterStarted float64
		afterTraceId string
	)
	if query.Cursor != nil {
		parsed, err := jsonx.Parse([]byte(*query.Cursor))
		if err != nil {
			return FindTracesResult{}, err
		}
		arr, ok := parsed.([]any)
		if !ok || len(arr) != 2 {
			return FindTracesResult{}, fmt.Errorf("telemetry: invalid trace cursor")
		}
		started, okNum := arr[0].(float64)
		tid, okStr := arr[1].(string)
		if !okNum || !okStr {
			return FindTracesResult{}, fmt.Errorf("telemetry: invalid trace cursor")
		}
		hasAfter = true
		afterStarted = started
		afterTraceId = tid
	}

	c.mu.Lock()
	all := c.storedSpansLocked()
	c.mu.Unlock()

	grouped := make(map[string]*StoredTrace)
	var traceOrder []*StoredTrace
	for _, span := range all {
		trace := grouped[span.TraceId]
		if trace == nil {
			trace = &StoredTrace{TraceId: span.TraceId, Spans: []StoredSpan{}}
			grouped[span.TraceId] = trace
			traceOrder = append(traceOrder, trace)
		}
		trace.Spans = append(trace.Spans, span)
	}

	matches := make([]StoredTrace, 0, len(traceOrder))
	for _, trace := range traceOrder {
		first := trace.Spans[0]
		if query.Name != nil && first.Name != *query.Name {
			continue
		}
		if query.StartMin != nil && first.StartedMs < *query.StartMin {
			continue
		}
		if query.StartMax != nil && first.StartedMs > *query.StartMax {
			continue
		}
		if hasAfter && !(first.StartedMs < afterStarted || (first.StartedMs == afterStarted && trace.TraceId > afterTraceId)) {
			continue
		}
		if query.Errors && !slices.ContainsFunc(trace.Spans, func(s StoredSpan) bool {
			_, isErr := s.Status.(*SpanStatusError)
			return isErr
		}) {
			continue
		}
		if query.Attributes != nil && !slices.ContainsFunc(trace.Spans, func(s StoredSpan) bool {
			return memoryAttributesMatch(s.Attributes, query.Attributes)
		}) {
			continue
		}
		matches = append(matches, *trace)
	}

	slices.SortStableFunc(matches, func(a, b StoredTrace) int {
		if c := cmp.Compare(b.Spans[0].StartedMs, a.Spans[0].StartedMs); c != 0 {
			return c
		}
		return cmp.Compare(a.TraceId, b.TraceId)
	})

	if limit < 0 {
		limit = 0
	}
	traces := matches
	var cursor *string
	if len(matches) > limit {
		traces = matches[:limit]
		if len(traces) > 0 {
			last := traces[len(traces)-1]
			encoded, err := jsonx.Stringify([]any{last.Spans[0].StartedMs, last.TraceId})
			if err != nil {
				return FindTracesResult{}, err
			}
			cursor = &encoded
		}
	}
	return FindTracesResult{
		Traces: traces,
		Cursor: cursor,
	}, nil
}

// FindMetrics reads raw measurements as count-one rows, without changing GetMetrics() snapshots.
func (c *InMemoryTelemetryContext) FindMetrics(query MetricQuery) ([]StoredMetric, error) {
	c.mu.Lock()
	measurements := make([]memoryIntervalMeasurement, len(c.state.measurements))
	copy(measurements, c.state.measurements)
	c.mu.Unlock()

	out := []StoredMetric{}
	for _, item := range measurements {
		m := item.measurement
		if m.Name != query.Name {
			continue
		}
		if query.StartMin != nil && item.intervalMs < *query.StartMin {
			continue
		}
		if query.StartMax != nil && item.intervalMs > *query.StartMax {
			continue
		}
		if query.Attributes != nil && !memoryAttributesMatch(m.Attributes, query.Attributes) {
			continue
		}
		out = append(out, StoredMetric{
			IntervalMs: item.intervalMs,
			Name:       m.Name,
			Kind:       m.Kind,
			Unit:       memoryCopyStrPtr(m.Unit),
			Attributes: memoryCopyAttributes(m.Attributes),
			Count:      1,
			Sum:        m.Value,
			Min:        m.Value,
			Max:        m.Value,
		})
	}
	return out, nil
}

// GetMetrics returns detached measurements in recording order, without aggregation.
func (c *InMemoryTelemetryContext) GetMetrics() []RecordedTelemetryMeasurement {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]RecordedTelemetryMeasurement, len(c.state.measurements))
	for i, item := range c.state.measurements {
		m := item.measurement
		var boundaries []float64
		if m.Boundaries != nil {
			boundaries = slices.Clone(m.Boundaries)
			if boundaries == nil {
				boundaries = []float64{}
			}
		}
		ctx := jsonx.Null[TelemetryCarrier]()
		if carrier, ok := m.Context.Get(); ok && carrier != nil {
			ctx = jsonx.Some(memoryCopyCarrier(carrier))
		}
		out[i] = RecordedTelemetryMeasurement{
			Kind:        m.Kind,
			Name:        m.Name,
			Description: memoryCopyStrPtr(m.Description),
			Unit:        memoryCopyStrPtr(m.Unit),
			Boundaries:  boundaries,
			Value:       m.Value,
			Attributes:  memoryCopyAttributes(m.Attributes),
			Context:     ctx,
		}
	}
	return out
}

// GetSpans returns detached snapshots in span-start order.
func (c *InMemoryTelemetryContext) GetSpans() []RecordedTelemetrySpan {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]RecordedTelemetrySpan, len(c.state.spans))
	for i, span := range c.state.spans {
		parentId := jsonx.Null[int]()
		if span.parentId != nil {
			parentId = jsonx.Some(*span.parentId)
		}
		parentContext := jsonx.Null[TelemetryCarrier]()
		if span.parentContext != nil {
			parentContext = jsonx.Some(memoryCopyCarrier(span.parentContext))
		}
		links := make([]RecordedTelemetryLink, len(span.links))
		for j, link := range span.links {
			links[j] = RecordedTelemetryLink{
				Context:    memoryCopyCarrier(link.Context),
				Attributes: memoryCopyAttributes(link.Attributes),
			}
		}
		events := make([]RecordedTelemetryEvent, len(span.events))
		for j, ev := range span.events {
			events[j] = RecordedTelemetryEvent{
				Name:       ev.name,
				Attributes: memoryCopyAttributes(ev.attributes),
			}
		}
		var endSeq *int
		if span.endSequence != nil {
			seq := *span.endSequence
			endSeq = &seq
		}
		out[i] = RecordedTelemetrySpan{
			Id:            span.id,
			ParentId:      parentId,
			Context:       memoryCopyCarrier(span.context),
			ParentContext: parentContext,
			Links:         links,
			Name:          span.name,
			Attributes:    memoryCopyAttributes(span.attributes),
			Events:        events,
			Status:        memoryCopyStatus(span.status),
			Settled:       span.settled,
			EndSequence:   endSeq,
		}
	}
	return out
}

var (
	_ TelemetryContext     = (*InMemoryTelemetryContext)(nil)
	_ TelemetryPropagation = (*InMemoryTelemetryContext)(nil)
	_ TelemetryMetrics     = (*InMemoryTelemetryContext)(nil)
	_ TelemetryStore       = (*InMemoryTelemetryContext)(nil)
)

type memoryMutableRecordedTelemetryEvent struct {
	timeMs     float64
	name       string
	attributes SpanAttributes
}

type memoryMutableRecordedTelemetrySpan struct {
	startedMs      float64
	clock          float64
	endedMs        *float64
	id             int
	parentId       *int
	context        TelemetryCarrier
	parentContext  TelemetryCarrier
	links          []RecordedTelemetryLink
	name           string
	attributes     SpanAttributes
	events         []memoryMutableRecordedTelemetryEvent
	status         SpanStatus
	explicitStatus bool
	settled        bool
	endSequence    *int
}

type memoryInMemoryParent struct {
	context TelemetryCarrier
	localId *int
	span    *memoryMutableRecordedTelemetrySpan
}

type memoryIntervalMeasurement struct {
	intervalMs  int64
	measurement RecordedTelemetryMeasurement
}

type memoryInMemoryTelemetryState struct {
	scope           string
	spans           []*memoryMutableRecordedTelemetrySpan
	measurements    []memoryIntervalMeasurement
	nextSpanId      int
	nextEndSequence int
}

type memoryExtractedContext struct {
	ctx    *InMemoryTelemetryContext
	parent *memoryInMemoryParent
}

func (e *memoryExtractedContext) Metrics() TelemetryMetrics {
	return e.ctx
}

func (e *memoryExtractedContext) Propagation() TelemetryPropagation {
	return e.ctx
}

func (e *memoryExtractedContext) StartSpan(options *SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error) {
	return memoryStartInMemorySpan(e.ctx, e.parent, options, callback)
}

func (e *memoryExtractedContext) memoryCarrier() TelemetryCarrier {
	return e.parent.context
}

var (
	_ TelemetryContext      = (*memoryExtractedContext)(nil)
	_ memoryCarrierProvider = (*memoryExtractedContext)(nil)
)

type memoryInertSpan struct {
	ctx *InMemoryTelemetryContext
}

func (s *memoryInertSpan) Metrics() TelemetryMetrics {
	return s.ctx
}

func (s *memoryInertSpan) Propagation() TelemetryPropagation {
	return s.ctx
}

func (s *memoryInertSpan) StartSpan(options *SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error) {
	return memoryStartInertSpan(s.ctx, options, callback)
}

func (*memoryInertSpan) AddLink(link SpanLink) {}

func (*memoryInertSpan) AddEvent(name string, attributes SpanAttributes) {}

func (*memoryInertSpan) SetAttributes(attributes SpanAttributes) {}

func (*memoryInertSpan) SetStatus(status SpanStatus) {}

var _ TelemetrySpan = (*memoryInertSpan)(nil)

// memorySpan is the TelemetrySpan closed over one recorded span.
type memorySpan struct {
	ctx      *InMemoryTelemetryContext
	recorded *memoryMutableRecordedTelemetrySpan
}

func (s *memorySpan) Metrics() TelemetryMetrics {
	return s.ctx
}

func (s *memorySpan) Propagation() TelemetryPropagation {
	return s.ctx
}

func (s *memorySpan) StartSpan(options *SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error) {
	var localId int
	localId = s.recorded.id
	return memoryStartInMemorySpan(
		s.ctx,
		&memoryInMemoryParent{
			context: s.recorded.context,
			localId: &localId,
			span:    s.recorded,
		},
		options,
		callback,
	)
}

func (s *memorySpan) AddLink(link SpanLink) {
	copy, ok := memoryCopyLink(link)
	s.ctx.mu.Lock()
	defer s.ctx.mu.Unlock()
	if s.recorded.settled || !ok {
		return
	}
	s.recorded.links = append(s.recorded.links, copy)
}

func (s *memorySpan) AddEvent(name string, attributes SpanAttributes) {
	attrs := memoryCopyAttributes(attributes)
	clock := memoryCurrentPerfNow()
	s.ctx.mu.Lock()
	defer s.ctx.mu.Unlock()
	if s.recorded.settled {
		return
	}
	s.recorded.events = append(s.recorded.events, memoryMutableRecordedTelemetryEvent{
		name:       name,
		timeMs:     memoryMicroseconds(clock - s.recorded.clock),
		attributes: attrs,
	})
}

func (s *memorySpan) SetAttributes(attributes SpanAttributes) {
	s.ctx.mu.Lock()
	defer s.ctx.mu.Unlock()
	if s.recorded.settled {
		return
	}
	s.recorded.attributes = memoryMergeAttributes(s.recorded.attributes, attributes)
}

func (s *memorySpan) SetStatus(status SpanStatus) {
	if status == nil {
		return
	}
	copied := memoryCopyStatus(status)
	s.ctx.mu.Lock()
	defer s.ctx.mu.Unlock()
	if s.recorded.settled {
		return
	}
	s.recorded.status = copied
	s.recorded.explicitStatus = true
}

func (s *memorySpan) memoryCarrier() TelemetryCarrier {
	return s.recorded.context
}

var (
	_ TelemetrySpan         = (*memorySpan)(nil)
	_ memoryCarrierProvider = (*memorySpan)(nil)
)

type memoryCounterRecorder struct {
	record func(value float64, attributes SpanAttributes, context TelemetryContext)
}

func (r *memoryCounterRecorder) Add(value float64, attributes SpanAttributes) {
	r.record(value, attributes, nil)
}

type memoryHistogramRecorder struct {
	record func(value float64, attributes SpanAttributes, context TelemetryContext)
}

func (r *memoryHistogramRecorder) Record(value float64, attributes SpanAttributes, context TelemetryContext) {
	r.record(value, attributes, context)
}

func memoryCreateMeasurementRecorder(
	ctx *InMemoryTelemetryContext,
	kind TelemetryMetricKind,
	name string,
	options *HistogramOptions,
) func(value float64, attributes SpanAttributes, context TelemetryContext) {
	var description *string
	var unit *string
	var boundaries []float64
	if options != nil {
		description = memoryCopyStrPtr(options.Description)
		unit = memoryCopyStrPtr(options.Unit)
		if kind == TelemetryMetricKindHistogram && options.Boundaries != nil {
			boundaries = make([]float64, len(options.Boundaries))
			copy(boundaries, options.Boundaries)
		}
	}
	return func(value float64, attributes SpanAttributes, context TelemetryContext) {
		var carrier TelemetryCarrier
		if context != nil {
			if cp, ok := context.(memoryCarrierProvider); ok {
				carrier = cp.memoryCarrier()
			}
		}
		ctxCarrier := jsonx.Null[TelemetryCarrier]()
		if carrier != nil {
			ctxCarrier = jsonx.Some(memoryCopyCarrier(carrier))
		}
		intervalMs := int64(math.Floor(memoryCurrentDateNow()/10_000) * 10_000)
		measurement := RecordedTelemetryMeasurement{
			Kind:        kind,
			Name:        name,
			Description: memoryCopyStrPtr(description),
			Unit:        memoryCopyStrPtr(unit),
			Boundaries:  boundaries,
			Value:       value,
			Attributes:  memoryCopyAttributes(attributes),
			Context:     ctxCarrier,
		}
		ctx.mu.Lock()
		ctx.state.measurements = append(ctx.state.measurements, memoryIntervalMeasurement{
			intervalMs:  intervalMs,
			measurement: measurement,
		})
		ctx.mu.Unlock()
	}
}

func memoryCarrierId(carrier TelemetryCarrier) string {
	scope, _ := carrier.Get("pi-memory-scope")
	span, _ := carrier.Get("pi-memory-span")
	return scope + ":" + span
}

func memoryCopyStrPtr(p *string) *string {
	if p == nil {
		return nil
	}
	s := *p
	return &s
}

func memoryCopyCarrier(carrier TelemetryCarrier) TelemetryCarrier {
	if carrier == nil {
		return nil
	}
	out := omap.NewMap[string, string]()
	for k, v := range carrier.All() {
		out.Set(k, v)
	}
	return out
}

func memoryCopyAttributeValue(value AttributeValue) AttributeValue {
	switch v := value.(type) {
	case []string:
		out := make([]string, len(v))
		copy(out, v)
		return out
	case []float64:
		out := make([]float64, len(v))
		copy(out, v)
		return out
	case []int:
		out := make([]int, len(v))
		copy(out, v)
		return out
	case []bool:
		out := make([]bool, len(v))
		copy(out, v)
		return out
	case []any:
		out := make([]any, len(v))
		copy(out, v)
		return out
	default:
		return value
	}
}

func memoryCopyAttributes(attributes SpanAttributes) SpanAttributes {
	out := omap.NewMap[string, AttributeValue]()
	if attributes == nil {
		return out
	}
	for k, v := range attributes.All() {
		if v == nil {
			continue
		}
		out.Set(k, memoryCopyAttributeValue(v))
	}
	return out
}

func memoryMergeAttributes(current SpanAttributes, attributes SpanAttributes) SpanAttributes {
	out := memoryCopyAttributes(current)
	if attributes == nil {
		return out
	}
	for k, v := range attributes.All() {
		if v == nil {
			continue
		}
		out.Set(k, memoryCopyAttributeValue(v))
	}
	return out
}

func memoryCopyLink(link SpanLink) (RecordedTelemetryLink, bool) {
	if link.Context == nil {
		return RecordedTelemetryLink{}, false
	}
	cp, ok := link.Context.(memoryCarrierProvider)
	if !ok {
		return RecordedTelemetryLink{}, false
	}
	carrier := cp.memoryCarrier()
	if carrier == nil {
		return RecordedTelemetryLink{}, false
	}
	return RecordedTelemetryLink{
		Context:    memoryCopyCarrier(carrier),
		Attributes: memoryCopyAttributes(link.Attributes),
	}, true
}

func memoryCopyStatus(status SpanStatus) SpanStatus {
	switch s := status.(type) {
	case *SpanStatusOk:
		return &SpanStatusOk{Status: SpanStatusKindOk}
	case *SpanStatusError:
		if s == nil {
			return &SpanStatusError{Status: SpanStatusKindError}
		}
		var errCopy *SpanError
		if s.Error != nil {
			errCopy = &SpanError{Name: s.Error.Name, Message: s.Error.Message}
		}
		return &SpanStatusError{Status: SpanStatusKindError, Error: errCopy}
	case *UnknownSpanStatus:
		if s == nil || s.Raw == nil {
			return &UnknownSpanStatus{}
		}
		return &UnknownSpanStatus{Raw: s.Raw.Clone()}
	default:
		return &SpanStatusOk{Status: SpanStatusKindOk}
	}
}

func memoryAutomaticErrorStatus(err error) SpanStatus {
	name := "Error"
	if n, ok := err.(interface{ Name() string }); ok {
		name = n.Name()
	}
	return &SpanStatusError{
		Status: SpanStatusKindError,
		Error: &SpanError{
			Name:    name,
			Message: err.Error(),
		},
	}
}

func memoryAttributesMatch(spanAttrs SpanAttributes, queryAttrs *omap.Map[string, AttributeValue]) bool {
	if queryAttrs == nil || queryAttrs.Len() == 0 {
		return true
	}
	if spanAttrs == nil {
		return false
	}
	for k, want := range queryAttrs.All() {
		got, ok := spanAttrs.Get(k)
		if !ok || !memoryAttrScalarEqual(got, want) {
			return false
		}
	}
	return true
}

func memoryAttrScalarEqual(a, b AttributeValue) bool {
	if na, okA := memoryAsFloat64(a); okA {
		nb, okB := memoryAsFloat64(b)
		return okB && na == nb
	}
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && x == y
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	default:
		return a == b
	}
}

func memoryAsFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
}

func memorySettleSpan(state *memoryInMemoryTelemetryState, span *memoryMutableRecordedTelemetrySpan, failed bool, err error, panicVal any) {
	if failed && !span.explicitStatus {
		if err != nil {
			span.status = memoryAutomaticErrorStatus(err)
		} else if pErr, ok := panicVal.(error); ok {
			span.status = memoryAutomaticErrorStatus(pErr)
		} else {
			span.status = &SpanStatusError{Status: SpanStatusKindError}
		}
	}
	span.settled = true
	endedMs := memoryMicroseconds(span.startedMs + memoryCurrentPerfNow() - span.clock)
	span.endedMs = &endedMs
	seq := state.nextEndSequence
	state.nextEndSequence++
	span.endSequence = &seq
}

func memoryCreateSpan(state *memoryInMemoryTelemetryState, parent *memoryInMemoryParent, options *SpanOptions) *memoryMutableRecordedTelemetrySpan {
	if options == nil {
		options = &SpanOptions{}
	}
	attributes := memoryCopyAttributes(options.Attributes)
	links := make([]RecordedTelemetryLink, 0, len(options.Links))
	for _, link := range options.Links {
		if copied, ok := memoryCopyLink(link); ok {
			links = append(links, copied)
		}
	}
	id := state.nextSpanId
	state.nextSpanId++

	var parentId *int
	var parentContext TelemetryCarrier
	traceId := ""
	if parent != nil {
		if parent.localId != nil {
			pid := *parent.localId
			parentId = &pid
		}
		if parent.context != nil {
			parentContext = memoryCopyCarrier(parent.context)
			traceId, _ = parent.context.Get("pi-memory-trace")
		}
	}
	if traceId == "" {
		traceId = state.scope + ":" + strconv.Itoa(id)
	}
	ctxCarrier := omap.NewMap[string, string]()
	ctxCarrier.Set("pi-memory-scope", state.scope)
	ctxCarrier.Set("pi-memory-span", strconv.Itoa(id))
	ctxCarrier.Set("pi-memory-trace", traceId)

	startedMs := TelemetryNow()
	clock := memoryCurrentPerfNow()
	return &memoryMutableRecordedTelemetrySpan{
		startedMs:      startedMs,
		clock:          clock,
		id:             id,
		parentId:       parentId,
		context:        ctxCarrier,
		parentContext:  parentContext,
		links:          links,
		name:           options.Name,
		attributes:     attributes,
		events:         []memoryMutableRecordedTelemetryEvent{},
		status:         &SpanStatusOk{Status: SpanStatusKindOk},
		explicitStatus: false,
		settled:        false,
	}
}

func memoryStartInertSpan(ctx *InMemoryTelemetryContext, options *SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error) {
	return NoopTelemetryContext.StartSpan(options, func(_ TelemetrySpan) (any, error) {
		return callback(&memoryInertSpan{ctx: ctx})
	})
}

func memoryStartInMemorySpan(ctx *InMemoryTelemetryContext, parent *memoryInMemoryParent, options *SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error) {
	ctx.mu.Lock()
	if parent != nil && parent.span != nil && parent.span.settled {
		ctx.mu.Unlock()
		return memoryStartInertSpan(ctx, options, callback)
	}
	recordedSpan := memoryCreateSpan(&ctx.state, parent, options)
	ctx.state.spans = append(ctx.state.spans, recordedSpan)
	ctx.mu.Unlock()

	span := &memorySpan{ctx: ctx, recorded: recordedSpan}

	var (
		res       any
		err       error
		completed bool
	)
	defer func() {
		var r any
		if !completed {
			r = recover()
		}
		ctx.mu.Lock()
		memorySettleSpan(&ctx.state, recordedSpan, !completed || err != nil, err, r)
		ctx.mu.Unlock()
		if !completed {
			panic(r)
		}
	}()

	res, err = callback(span)
	completed = true
	return res, err
}
