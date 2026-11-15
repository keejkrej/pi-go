// Ported from packages/telemetry/test/propagation.test.ts and packages/telemetry/test/propagation-worker.ts (pi v1.5.2).

package telemetry_test

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
	"github.com/keejkrej/pi-go/telemetry"
)

func TestMain(m *testing.M) {
	if os.Getenv("PI_TELEMETRY_PROPAGATION_WORKER") == "1" {
		os.Exit(tpropRunWorker(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func tpropRunWorker(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "expected carrier JSON argument")
		return 2
	}
	var carrier telemetry.TelemetryCarrier
	if err := jsonx.Unmarshal([]byte(args[0]), &carrier); err != nil {
		fmt.Fprintf(os.Stderr, "unmarshal carrier: %v\n", err)
		return 1
	}
	ctx := telemetry.NewInMemoryTelemetryContext()
	if _, err := ctx.Extract(carrier).StartSpan(&telemetry.SpanOptions{Name: "remote"}, func(telemetry.TelemetrySpan) (any, error) {
		return nil, nil
	}); err != nil {
		fmt.Fprintf(os.Stderr, "start span: %v\n", err)
		return 1
	}
	out, err := jsonx.Marshal(ctx.GetSpans())
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal spans: %v\n", err)
		return 1
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintf(os.Stderr, "write stdout: %v\n", err)
		return 1
	}
	return 0
}

func TestPropagation_ContinuesParentInAnotherProcessWithoutSharedRecorderState(t *testing.T) {
	adapter := telemetry.NewInMemoryTelemetryContext()
	var carrier telemetry.TelemetryCarrier
	if _, err := adapter.StartSpan(&telemetry.SpanOptions{Name: "caller"}, func(span telemetry.TelemetrySpan) (any, error) {
		carrier = adapter.Inject(span)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	carrierJSON, err := jsonx.Marshal(carrier)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, string(carrierJSON))
	cmd.Env = append(os.Environ(), "PI_TELEMETRY_PROPAGATION_WORKER=1")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("worker failed: %v", err)
	}

	var recorded []telemetry.RecordedTelemetrySpan
	if err := jsonx.Unmarshal(output, &recorded); err != nil {
		t.Fatalf("unmarshal worker spans (%s): %v", string(output), err)
	}
	if len(recorded) != 1 {
		t.Fatalf("expected 1 recorded span, got %d", len(recorded))
	}
	if diff := cmp.Diff(jsonx.Some(carrier), recorded[0].ParentContext, thlpCmpOpts()...); diff != "" {
		t.Fatalf("parentContext mismatch (-want +got):\n%s", diff)
	}
	if !recorded[0].ParentId.IsNull() {
		t.Fatalf("expected null parentId, got %v", recorded[0].ParentId)
	}
	if diff := cmp.Diff(carrier, recorded[0].Context, thlpCmpOpts()...); diff == "" {
		t.Fatalf("expected remote span context to differ from caller carrier")
	}
}

func TestPropagation_IsolatesCrossedConcurrentRemoteParentsAndLinksSharedRoots(t *testing.T) {
	caller := telemetry.NewInMemoryTelemetryContext()
	provider := telemetry.NewInMemoryTelemetryContext()

	var activation telemetry.TelemetrySpan
	firstCallerStarted := make(chan struct{})
	activationReady := make(chan struct{})
	activationDone := make(chan struct{})
	gate := make(chan struct{})

	var wg sync.WaitGroup
	errs := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := caller.StartSpan(&telemetry.SpanOptions{Name: "first"}, func(trigger telemetry.TelemetrySpan) (any, error) {
			close(firstCallerStarted)
			parent := provider.Extract(caller.Inject(trigger))
			return parent.StartSpan(&telemetry.SpanOptions{Name: "first.method"}, func(getter telemetry.TelemetrySpan) (any, error) {
				_, actErr := provider.StartSpan(&telemetry.SpanOptions{
					Name:  "activation",
					Links: []telemetry.SpanLink{{Context: getter}},
				}, func(span telemetry.TelemetrySpan) (any, error) {
					activation = span
					close(activationReady)
					<-gate
					return nil, nil
				})
				close(activationDone)
				return nil, actErr
			})
		})
		if err != nil {
			errs <- err
		}
	}()

	<-firstCallerStarted

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := caller.StartSpan(&telemetry.SpanOptions{Name: "later"}, func(trigger telemetry.TelemetrySpan) (any, error) {
			parent := provider.Extract(caller.Inject(trigger))
			return parent.StartSpan(&telemetry.SpanOptions{Name: "later.method"}, func(getter telemetry.TelemetrySpan) (any, error) {
				<-activationReady
				getter.AddLink(telemetry.SpanLink{Context: activation})
				close(gate)
				<-activationDone
				return nil, nil
			})
		})
		if err != nil {
			errs <- err
		}
	}()

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	spans := provider.GetSpans()
	callerSpans := caller.GetSpans()
	root := thlpFindSpan(t, spans, "activation")
	if !root.ParentContext.IsNull() {
		t.Fatalf("expected null activation.ParentContext, got %v", root.ParentContext)
	}
	first := thlpFindSpan(t, spans, "first.method")
	later := thlpFindSpan(t, spans, "later.method")
	if diff := cmp.Diff(jsonx.Some(callerSpans[0].Context), first.ParentContext, thlpCmpOpts()...); diff != "" {
		t.Fatalf("first.ParentContext mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(jsonx.Some(callerSpans[1].Context), later.ParentContext, thlpCmpOpts()...); diff != "" {
		t.Fatalf("later.ParentContext mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(first.Context, root.Links[0].Context, thlpCmpOpts()...); diff != "" {
		t.Fatalf("root.Links[0].Context mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(root.Context, later.Links[0].Context, thlpCmpOpts()...); diff != "" {
		t.Fatalf("later.Links[0].Context mismatch (-want +got):\n%s", diff)
	}
}

func TestPropagation_DetachesCarrierAndLinkSnapshotsAndRetainsSettledTargets(t *testing.T) {
	adapter := telemetry.NewInMemoryTelemetryContext()
	var target telemetry.TelemetrySpan
	if _, err := adapter.StartSpan(&telemetry.SpanOptions{Name: "target"}, func(span telemetry.TelemetrySpan) (any, error) {
		target = span
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if target == nil {
		t.Fatalf("expected target span")
	}

	original := adapter.Inject(target)
	injected := adapter.Inject(target)
	if injected == nil {
		t.Fatalf("expected carrier")
	}
	injected.Set("pi-memory-span", "999")
	if diff := cmp.Diff(original, adapter.Inject(target), thlpCmpOpts()...); diff != "" {
		t.Fatalf("Inject(target) mutated (-want +got):\n%s", diff)
	}

	if _, err := adapter.StartSpan(&telemetry.SpanOptions{
		Name:  "linked",
		Links: []telemetry.SpanLink{{Context: target, Attributes: thlpAttrs("tags", []string{"original"})}},
	}, func(telemetry.TelemetrySpan) (any, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	spans := adapter.GetSpans()
	if len(spans) < 2 || len(spans[1].Links) == 0 {
		t.Fatalf("expected 2 spans with link, got %#v", spans)
	}
	spans[0].Context.Set("pi-memory-span", "123")
	spans[1].Links[0].Context.Set("pi-memory-span", "456")
	if tags, ok := spans[1].Links[0].Attributes.Get("tags"); ok {
		if s, ok := tags.([]string); ok && len(s) > 0 {
			s[0] = "mutated"
		}
	}
	spans[1].Links[0].Attributes.Set("tags", []string{"mutated"})

	wantLinks := []telemetry.RecordedTelemetryLink{
		{Context: original, Attributes: thlpAttrs("tags", []string{"original"})},
	}
	if diff := cmp.Diff(wantLinks, adapter.GetSpans()[1].Links, thlpCmpOpts()...); diff != "" {
		t.Fatalf("links mutated (-want +got):\n%s", diff)
	}
}

func TestPropagation_LinksNewRootToExtractedRemoteTarget(t *testing.T) {
	sender := telemetry.NewInMemoryTelemetryContext()
	receiver := telemetry.NewInMemoryTelemetryContext()
	var carrier telemetry.TelemetryCarrier
	if _, err := sender.StartSpan(&telemetry.SpanOptions{Name: "trigger"}, func(span telemetry.TelemetrySpan) (any, error) {
		carrier = sender.Inject(span)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	encoded, err := jsonx.Marshal(carrier)
	if err != nil {
		t.Fatal(err)
	}
	var wire telemetry.TelemetryCarrier
	if err := jsonx.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	target := receiver.Extract(wire)
	if _, err := receiver.StartSpan(&telemetry.SpanOptions{
		Name:  "background",
		Links: []telemetry.SpanLink{{Context: target, Attributes: thlpAttrs("reason", "restart")}},
	}, func(span telemetry.TelemetrySpan) (any, error) {
		span.AddLink(telemetry.SpanLink{Context: target})
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	spans := receiver.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 recorded span, got %d", len(spans))
	}
	if !spans[0].ParentContext.IsNull() {
		t.Fatalf("expected null parentContext, got %v", spans[0].ParentContext)
	}
	wantLinks := []telemetry.RecordedTelemetryLink{
		{Context: carrier, Attributes: thlpAttrs("reason", "restart")},
		{Context: carrier, Attributes: thlpAttrs()},
	}
	if diff := cmp.Diff(wantLinks, spans[0].Links, thlpCmpOpts()...); diff != "" {
		t.Fatalf("links mismatch (-want +got):\n%s", diff)
	}
}

func TestPropagation_UsesRootForAbsentAndUnknownCarriers(t *testing.T) {
	adapter := telemetry.NewInMemoryTelemetryContext()
	carriers := []telemetry.TelemetryCarrier{
		nil,
		omap.NewMap[string, string](),
		thlpCarrier("unknown", "format"),
	}
	for _, carrier := range carriers {
		if adapter.Extract(carrier) != adapter {
			t.Fatalf("expected Extract(%v) to return root adapter", carrier)
		}
	}
	if got := telemetry.NoopTelemetryPropagation.Inject(telemetry.NoopTelemetryContext); got != nil {
		t.Fatalf("expected nil carrier from noop inject, got %v", got)
	}
	if got := telemetry.NoopTelemetryPropagation.Extract(thlpCarrier("unknown", "format")); got != telemetry.NoopTelemetryContext {
		t.Fatalf("expected NoopTelemetryContext from noop extract, got %v", got)
	}
	if _, err := telemetry.NoopTelemetryContext.StartSpan(&telemetry.SpanOptions{Name: "noop"}, func(span telemetry.TelemetrySpan) (any, error) {
		span.AddLink(telemetry.SpanLink{Context: span})
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPropagation_TreatsRecognizedCarrierIdentifiersAsOpaqueStrings(t *testing.T) {
	adapter := telemetry.NewInMemoryTelemetryContext()
	carrier := thlpCarrier(
		"pi-memory-scope", "external-recorder",
		"pi-memory-span", "external-span",
		"pi-memory-trace", "external-trace",
	)
	if _, err := adapter.Extract(carrier).StartSpan(&telemetry.SpanOptions{Name: "receiver"}, func(telemetry.TelemetrySpan) (any, error) {
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	spans := adapter.GetSpans()
	if diff := cmp.Diff(jsonx.Some(carrier), spans[0].ParentContext, thlpCmpOpts()...); diff != "" {
		t.Fatalf("parentContext mismatch (-want +got):\n%s", diff)
	}
}
