// Ported from packages/telemetry/src/memory.ts (pi v1.0.0).

package telemetry

import (
	"sync"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// RecordedTelemetryEvent is a detached event snapshot.
type RecordedTelemetryEvent struct {
	Name       string         `json:"name"`
	Attributes SpanAttributes `json:"attributes"`
}

// RecordedTelemetrySpan is a detached span snapshot in span-start order.
// ParentId is null when the span has no parent.
// EndSequence is absent until the span settles.
// Attributes and Events are empty, not nil, when unset so they marshal as {} and [].
type RecordedTelemetrySpan struct {
	Id          int                      `json:"id"`
	ParentId    jsonx.Opt[int]           `json:"parentId"`
	Name        string                   `json:"name"`
	Attributes  SpanAttributes           `json:"attributes"`
	Events      []RecordedTelemetryEvent `json:"events"`
	Status      SpanStatus               `json:"status"`
	Settled     bool                     `json:"settled"`
	EndSequence *int                     `json:"endSequence,omitzero"`
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
			spans:           []*memoryMutableRecordedTelemetrySpan{},
			nextSpanId:      1,
			nextEndSequence: 1,
		},
	}
}

func (c *InMemoryTelemetryContext) StartSpan(options *SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error) {
	panic("unported: StartSpan")
}

// GetSpans returns detached snapshots in span-start order.
func (c *InMemoryTelemetryContext) GetSpans() []RecordedTelemetrySpan {
	panic("unported: GetSpans")
}

var _ TelemetryContext = (*InMemoryTelemetryContext)(nil)

type memoryMutableRecordedTelemetryEvent struct {
	name       string
	attributes SpanAttributes
}

type memoryMutableRecordedTelemetrySpan struct {
	id             int
	parentId       *int
	name           string
	attributes     SpanAttributes
	events         []memoryMutableRecordedTelemetryEvent
	status         SpanStatus
	explicitStatus bool
	settled        bool
	endSequence    *int
}

type memoryInMemoryTelemetryState struct {
	spans           []*memoryMutableRecordedTelemetrySpan
	nextSpanId      int
	nextEndSequence int
}

// memorySpan is the TelemetrySpan closed over one recorded span.
type memorySpan struct {
	ctx      *InMemoryTelemetryContext
	recorded *memoryMutableRecordedTelemetrySpan
}

func (s *memorySpan) StartSpan(options *SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error) {
	panic("unported: StartSpan")
}

func (s *memorySpan) AddEvent(name string, attributes SpanAttributes) {
	panic("unported: AddEvent")
}

func (s *memorySpan) SetAttributes(attributes SpanAttributes) {
	panic("unported: SetAttributes")
}

func (s *memorySpan) SetStatus(status SpanStatus) {
	panic("unported: SetStatus")
}

var _ TelemetrySpan = (*memorySpan)(nil)

func memoryCopyAttributeValue(value AttributeValue) AttributeValue {
	panic("unported: memoryCopyAttributeValue")
}

func memoryCopyAttributes(attributes SpanAttributes) SpanAttributes {
	panic("unported: memoryCopyAttributes")
}

func memoryMergeAttributes(current SpanAttributes, attributes SpanAttributes) SpanAttributes {
	panic("unported: memoryMergeAttributes")
}

func memoryCopyStatus(status SpanStatus) SpanStatus {
	panic("unported: memoryCopyStatus")
}

func memoryAutomaticErrorStatus(err any) SpanStatus {
	panic("unported: memoryAutomaticErrorStatus")
}

func memorySettleSpan(state *memoryInMemoryTelemetryState, span *memoryMutableRecordedTelemetrySpan, failed bool, err any) {
	panic("unported: memorySettleSpan")
}

func memoryCreateSpan(state *memoryInMemoryTelemetryState, parent *memoryMutableRecordedTelemetrySpan, options *SpanOptions) *memoryMutableRecordedTelemetrySpan {
	panic("unported: memoryCreateSpan")
}

func memoryStartInMemorySpan(state *memoryInMemoryTelemetryState, parent *memoryMutableRecordedTelemetrySpan, options *SpanOptions, callback func(span TelemetrySpan) (any, error)) (any, error) {
	panic("unported: memoryStartInMemorySpan")
}
