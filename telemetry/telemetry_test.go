// Ported from packages/telemetry/test/telemetry.test.ts (pi v1.5.2).

package telemetry_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/telemetry"
)

func TestTelemetry_SchemasPreservesSerializableDefinitions(t *testing.T) {
	def := &telemetry.TelemetrySchemaDefinition{
		Version: 1,
		Spans: thlpMap[telemetry.TelemetrySpanDefinition](
			"operation", telemetry.TelemetrySpanDefinition{
				Description: "Test operation",
				Parents:     &telemetry.TelemetryParentAny{Kind: telemetry.TelemetryParentKindAny},
				StartAttributes: thlpMap[telemetry.TelemetryStartAttributeDefinition](
					"kind", telemetry.TelemetryStartAttributeDefinition{
						Type:        telemetry.TelemetryAttributeTypeString,
						Required:    true,
						Values:      []string{"read", "write"},
						Description: "Kind",
					},
				),
				EndAttributes: thlpMap[telemetry.TelemetryAttributeDefinition](),
				Events: thlpMap[telemetry.TelemetryEventDefinition](
					"result", telemetry.TelemetryEventDefinition{
						Description: "Result",
						Attributes: thlpMap[telemetry.TelemetryEventAttributeDefinition](
							"outcome", telemetry.TelemetryEventAttributeDefinition{
								Type:        telemetry.TelemetryAttributeTypeString,
								Required:    true,
								Values:      []string{"ok", "error"},
								Description: "Outcome",
							},
						),
					},
				),
				Status: telemetry.TelemetryStatusRule{Default: "ok", ErrorWhen: "The operation fails"},
			},
		),
	}

	schema := telemetry.DefineTelemetrySchema(def)
	if schema != def {
		t.Fatalf("expected DefineTelemetrySchema to return the exact definition pointer")
	}
	encoded, err := jsonx.Stringify(schema)
	if err != nil {
		t.Fatalf("expected JSON stringify to succeed: %v", err)
	}
	if encoded == "" {
		t.Fatalf("expected non-empty JSON encoding")
	}
	var decoded telemetry.TelemetrySchemaDefinition
	if err := jsonx.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("expected JSON unmarshal to succeed: %v", err)
	}
	if decoded.Version != 1 {
		t.Fatalf("expected decoded version 1, got %v", decoded.Version)
	}
}

func TestTelemetry_CombinesSchemaVocabulariesAndBindsChildStarters(t *testing.T) {
	operationSchema := telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{
		Version: 1,
		Spans: thlpMap[telemetry.TelemetrySpanDefinition](
			"operation", telemetry.TelemetrySpanDefinition{
				Description: "Operation",
				Parents:     &telemetry.TelemetryParentRootOrExternal{Kind: telemetry.TelemetryParentKindRootOrExternal},
				StartAttributes: thlpMap[telemetry.TelemetryStartAttributeDefinition](
					"kind", telemetry.TelemetryStartAttributeDefinition{
						Type:        telemetry.TelemetryAttributeTypeString,
						Required:    true,
						Values:      []string{"read", "write"},
						Description: "Kind",
					},
				),
				EndAttributes: thlpMap[telemetry.TelemetryAttributeDefinition](),
				Status:        telemetry.TelemetryStatusRule{Default: "ok", ErrorWhen: "The operation fails"},
			},
		),
	})
	requestSchema := telemetry.DefineTelemetrySchema(&telemetry.TelemetrySchemaDefinition{
		Version: 3,
		Spans: thlpMap[telemetry.TelemetrySpanDefinition](
			"request", telemetry.TelemetrySpanDefinition{
				Description: "Request",
				Parents: &telemetry.TelemetryParentSpans{
					Kind:  telemetry.TelemetryParentKindSpans,
					Spans: []string{"operation"},
				},
				StartAttributes: thlpMap[telemetry.TelemetryStartAttributeDefinition](
					"provider", telemetry.TelemetryStartAttributeDefinition{
						Type:        telemetry.TelemetryAttributeTypeString,
						Required:    true,
						Description: "Provider",
					},
				),
				EndAttributes: thlpMap[telemetry.TelemetryAttributeDefinition](
					"response", telemetry.TelemetryAttributeDefinition{
						Type:        telemetry.TelemetryAttributeTypeString,
						Description: "Response kind",
					},
				),
				Status: telemetry.TelemetryStatusRule{Default: "ok", ErrorWhen: "The request fails"},
			},
		),
	})

	telemetryContext := telemetry.NewInMemoryTelemetryContext()
	startSpan := telemetry.CreateTypedSpanStarter(telemetryContext, []*telemetry.TelemetrySchemaDefinition{operationSchema, requestSchema})

	result, err := startSpan("operation", thlpAttrs("kind", "read"), func(_ telemetry.TelemetrySpan, startChildSpan telemetry.TypedSpanStarter) (any, error) {
		return startChildSpan("request", thlpAttrs("provider", "example"), func(requestSpan telemetry.TelemetrySpan, _ telemetry.TypedSpanStarter) (any, error) {
			requestSpan.SetAttributes(thlpAttrs("response", "cached"))
			return 42, nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != 42 {
		t.Fatalf("expected 42, got %#v", result)
	}

	spans := telemetryContext.GetSpans()
	operationSpan := thlpFindSpan(t, spans, "operation")
	requestSpan := thlpFindSpan(t, spans, "request")
	if !operationSpan.ParentId.IsNull() {
		t.Fatalf("expected null operation parentId, got %v", operationSpan.ParentId)
	}
	reqParentId, ok := requestSpan.ParentId.Get()
	if !ok || reqParentId != operationSpan.Id {
		t.Fatalf("expected request parentId %d, got %v", operationSpan.Id, requestSpan.ParentId)
	}
	if diff := cmp.Diff(thlpAttrs("provider", "example", "response", "cached"), requestSpan.Attributes, thlpCmpOpts()...); diff != "" {
		t.Fatalf("request attributes mismatch (-want +got):\n%s", diff)
	}

	syncError := &thlpKindError{Kind: "sync"}
	_, gotSyncErr := startSpan("operation", thlpAttrs("kind", "write"), func(telemetry.TelemetrySpan, telemetry.TypedSpanStarter) (any, error) {
		return nil, syncError
	})
	if gotSyncErr != syncError {
		t.Fatalf("expected exact error %v, got %v", syncError, gotSyncErr)
	}

	asyncError := &thlpKindError{Kind: "async"}
	_, gotAsyncErr := startSpan("request", thlpAttrs("provider", "example"), func(telemetry.TelemetrySpan, telemetry.TypedSpanStarter) (any, error) {
		ch := make(chan error, 1)
		go func() { ch <- asyncError }()
		return nil, <-ch
	})
	if gotAsyncErr != asyncError {
		t.Fatalf("expected exact error %v, got %v", asyncError, gotAsyncErr)
	}
}

func TestTelemetry_NoopContextAdmitsSynchronouslyAndReusesOneInertSpan(t *testing.T) {
	admitted := false
	var firstSpan telemetry.TelemetrySpan
	result, err := telemetry.NoopTelemetryContext.StartSpan(&telemetry.SpanOptions{Name: "first"}, func(span telemetry.TelemetrySpan) (any, error) {
		admitted = true
		firstSpan = span
		child, childErr := span.StartSpan(&telemetry.SpanOptions{Name: "child"}, func(childSpan telemetry.TelemetrySpan) (any, error) {
			return childSpan, nil
		})
		if childErr != nil {
			return nil, childErr
		}
		if child != span {
			t.Fatalf("expected noop child span to equal parent span")
		}
		return 42, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !admitted {
		t.Fatalf("expected callback to be admitted")
	}
	if result != 42 {
		t.Fatalf("expected 42, got %#v", result)
	}
	if telemetry.NoopTelemetryContext.Metrics() != telemetry.NoopTelemetryMetrics {
		t.Fatalf("expected NoopTelemetryContext.Metrics() == NoopTelemetryMetrics")
	}
	if telemetry.NoopTelemetryContext.Propagation() != telemetry.NoopTelemetryPropagation {
		t.Fatalf("expected NoopTelemetryContext.Propagation() == NoopTelemetryPropagation")
	}
	if firstSpan == nil {
		t.Fatalf("expected non-nil span")
	}
	if firstSpan.Metrics() != telemetry.NoopTelemetryMetrics {
		t.Fatalf("expected span.Metrics() == NoopTelemetryMetrics")
	}
	if firstSpan.Propagation() != telemetry.NoopTelemetryPropagation {
		t.Fatalf("expected span.Propagation() == NoopTelemetryPropagation")
	}
}

func TestTelemetry_NoopContextPreservesRejectionValues(t *testing.T) {
	syncError := &thlpKindError{Kind: "sync"}
	_, gotSyncErr := telemetry.NoopTelemetryContext.StartSpan(&telemetry.SpanOptions{Name: "sync"}, func(telemetry.TelemetrySpan) (any, error) {
		return nil, syncError
	})
	if gotSyncErr != syncError {
		t.Fatalf("expected %v, got %v", syncError, gotSyncErr)
	}

	asyncError := &thlpKindError{Kind: "async"}
	_, gotAsyncErr := telemetry.NoopTelemetryContext.StartSpan(&telemetry.SpanOptions{Name: "async"}, func(telemetry.TelemetrySpan) (any, error) {
		ch := make(chan error, 1)
		go func() { ch <- asyncError }()
		return nil, <-ch
	})
	if gotAsyncErr != asyncError {
		t.Fatalf("expected %v, got %v", asyncError, gotAsyncErr)
	}
}

func TestTelemetry_NoopContextIgnoresTypedRecordingPayloads(t *testing.T) {
	result, err := telemetry.NoopTelemetryContext.StartSpan(&telemetry.SpanOptions{
		Name:       "operation",
		Attributes: thlpAttrs("kind", "test"),
	}, func(span telemetry.TelemetrySpan) (any, error) {
		span.AddEvent("event", thlpAttrs("count", 1))
		span.SetAttributes(thlpAttrs("complete", true))
		span.SetStatus(&telemetry.SpanStatusError{
			Status: telemetry.SpanStatusKindError,
			Error:  &telemetry.SpanError{Name: "Expected", Message: "test"},
		})
		span.AddLink(telemetry.SpanLink{Context: span})
		return 42, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != 42 {
		t.Fatalf("expected 42, got %#v", result)
	}
}
