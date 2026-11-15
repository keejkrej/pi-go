// Ported from packages/chord/test/services.test.ts (pi v1.0.0).

package chord

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tservObj(kv ...any) *jsonx.Object {
	return jsonx.ObjectOf(kv...)
}

func tservGet(v any, key string) any {
	o, ok := v.(*jsonx.Object)
	if !ok || o == nil {
		return nil
	}
	val, _ := o.Get(key)
	return val
}

func tservHasCode(err error, code RemoteServiceErrorCode) bool {
	var rse *RemoteServiceError
	return errors.As(err, &rse) && rse.Code == code
}

func tservMustJson(t *testing.T, v any) string {
	t.Helper()
	b, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatalf("jsonx.Marshal failed: %v", err)
	}
	return string(b)
}

func tservMustDefine[T any](t *testing.T, id string, opts ...*DefineServiceOptions) *Service[T] {
	t.Helper()
	var opt *DefineServiceOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	svc, err := DefineService[T](id, opt)
	if err != nil {
		t.Fatalf("DefineService(%q) failed: %v", id, err)
	}
	return svc
}

func tservMustState[T any](t *testing.T, initial T) MutableReplicatedState[T] {
	t.Helper()
	st, err := NewReplicatedState(initial)
	if err != nil {
		t.Fatalf("NewReplicatedState failed: %v", err)
	}
	return st
}

func tservMustProvider(t *testing.T, entries ...RemoteServiceProviderEntry) *RemoteServiceProvider {
	t.Helper()
	p, err := NewRemoteServiceProvider(entries)
	if err != nil {
		t.Fatalf("NewRemoteServiceProvider failed: %v", err)
	}
	return p
}

func tservEntry(svc any, mode ...ServiceMode) RemoteServiceProviderEntry {
	head, err := spExtractServiceHead(svc)
	if err != nil {
		panic(err)
	}
	var mPtr *ServiceMode
	if len(mode) > 0 {
		m := mode[0]
		mPtr = &m
	}
	return RemoteServiceProviderEntry{
		Id:    head.id,
		Local: head.local,
		Mode:  mPtr,
	}
}

func tservCatchError(fn func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = ssToError(r)
		}
	}()
	fn()
	return nil
}

func tservWaitFor(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func tservMemberGet(t *testing.T, handle any, member string) any {
	t.Helper()
	switch h := handle.(type) {
	case *scServiceFacade:
		v, err := h.Get(member)
		if err != nil {
			t.Fatalf("facade.Get(%q) failed: %v", member, err)
		}
		return v
	case *shServiceView:
		v, err := h.Get(member)
		if err != nil {
			t.Fatalf("view.Get(%q) failed: %v", member, err)
		}
		return v
	default:
		t.Fatalf("unexpected handle type %T", handle)
		return nil
	}
}

func tservStateVal(t *testing.T, stateHandle any) (any, bool, error) {
	t.Helper()
	switch s := stateHandle.(type) {
	case *scMemberSlot:
		return s.StateValue()
	case *shValueView:
		return s.StateValue()
	default:
		t.Fatalf("unexpected state handle type %T", stateHandle)
		return nil, false, nil
	}
}

func tservStateSubscribe(t *testing.T, stateHandle any, fn func(JsonValue, Context, ReplicatedStateDelivery) error) (func(), error) {
	t.Helper()
	switch s := stateHandle.(type) {
	case *scMemberSlot:
		return s.Subscribe(fn)
	case *shValueView:
		return s.Subscribe(fn)
	default:
		t.Fatalf("unexpected state handle type %T", stateHandle)
		return nil, nil
	}
}

func tservCall(handle any, member string, args ...any) (any, error) {
	switch h := handle.(type) {
	case *scServiceFacade:
		return h.Call(member, args...)
	case *shServiceView:
		return h.Call(member, args...)
	case *scMemberSlot:
		if member != "" {
			return nil, fmt.Errorf("unexpected member name %q on slot", member)
		}
		return h.Invoke(args...)
	case *shValueView:
		if member != "" {
			return nil, fmt.Errorf("unexpected member name %q on value view", member)
		}
		return h.Invoke(args...)
	default:
		if member == "" {
			return shInvokeCallable(handle, args)
		}
		fn, err := shGetProperty(handle, member)
		if err != nil {
			return nil, err
		}
		if fn == nil {
			return nil, fmt.Errorf("missing callable member %q on %T", member, handle)
		}
		return shInvokeCallable(fn, args)
	}
}

type tservGatedTransport struct {
	inner      RemoteServiceTransport
	subscribed chan struct{}
	release    chan struct{}
	once       sync.Once
}

func (t *tservGatedTransport) Invoke(call ServiceCall, callContext Context) (JsonValue, bool, error) {
	return t.inner.Invoke(call, callContext)
}

func (t *tservGatedTransport) Subscribe(serviceId string, mode ServiceMode, listener func(ServiceProviderUpdate, Context), callContext Context) (ServiceSubscription, error) {
	sub, err := t.inner.Subscribe(serviceId, mode, listener, callContext)
	if err != nil {
		return nil, err
	}
	if t.subscribed != nil {
		t.once.Do(func() { close(t.subscribed) })
	}
	if t.release != nil {
		<-t.release
	}
	return sub, nil
}

type tservFuncTransport struct {
	invoke    func(ServiceCall, Context) (JsonValue, bool, error)
	subscribe func(string, ServiceMode, func(ServiceProviderUpdate, Context), Context) (ServiceSubscription, error)
}

func (t *tservFuncTransport) Invoke(call ServiceCall, callContext Context) (JsonValue, bool, error) {
	return t.invoke(call, callContext)
}

func (t *tservFuncTransport) Subscribe(serviceId string, mode ServiceMode, listener func(ServiceProviderUpdate, Context), callContext Context) (ServiceSubscription, error) {
	return t.subscribe(serviceId, mode, listener, callContext)
}

type tservSubscriptionFuncs struct {
	snapshot ServiceSubscriptionSnapshot
	activate func() error
	closeFn  func(Context) error
}

func (s *tservSubscriptionFuncs) Snapshot() ServiceSubscriptionSnapshot { return s.snapshot }
func (s *tservSubscriptionFuncs) Activate() error                       { return s.activate() }
func (s *tservSubscriptionFuncs) Close(c Context) error                 { return s.closeFn(c) }

func TestRemoteServices(t *testing.T) {
	modelsSvc := tservMustDefine[any](t, "test.models")
	questionDialogsSvc := tservMustDefine[any](t, "test.question-dialog")
	echoSvc := tservMustDefine[any](t, "test.echo")
	timelineSvc := tservMustDefine[any](t, "test.timeline")

	t.Run("marks services remotable by default and reserves Chord service IDs", func(t *testing.T) {
		localFlag := true
		passthrough := tservMustDefine[any](t, "test.json-passthrough")
		if passthrough.Local {
			t.Fatal("expected remotable service by default")
		}
		local := tservMustDefine[any](t, "test.local", &DefineServiceOptions{Local: &localFlag})
		if modelsSvc.Local || !local.Local {
			t.Fatalf("unexpected Local flags: models=%v local=%v", modelsSvc.Local, local.Local)
		}
		if _, err := DefineService[any]("$chord.internal", &DefineServiceOptions{Local: &localFlag}); err == nil || !strings.Contains(err.Error(), "Service IDs beginning with $chord. are reserved") {
			t.Fatalf("expected reserved ID error, got %v", err)
		}
		if _, err := NewRemoteServiceProvider([]RemoteServiceProviderEntry{tservEntry(local)}); err == nil || !strings.Contains(err.Error(), "cannot be published remotely") {
			t.Fatalf("expected local rejection error, got %v", err)
		}
	})

	t.Run("tracks mutable source state while publishing immutable revisions", func(t *testing.T) {
		initial := tservObj("selected", nil, "revision", float64(0))
		state := tservMustState(t, initial)
		var delivered any
		var deliveries []string
		unsub := state.Subscribe(func(value *jsonx.Object, _ Context, delivery ReplicatedStateDelivery) error {
			delivered = value
			deliveries = append(deliveries, string(delivery.Kind))
			return nil
		})
		cur, _ := state.Value()
		if cur != initial || delivered != cur {
			t.Fatal("expected initial reference identity on subscribe")
		}
		hydrated := delivered.(*jsonx.Object)

		if err := state.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
			draft.Value.Set("selected", tservObj("provider", "test", "modelId", "one"))
			draft.Value.Set("revision", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("Change failed: %v", err)
		}

		cur, _ = state.Value()
		if cur == initial || delivered != cur {
			t.Fatal("expected new immutable revision on change")
		}
		if got := tservMustJson(t, cur); got != `{"selected":{"provider":"test","modelId":"one"},"revision":1}` {
			t.Fatalf("got %s", got)
		}
		if got := tservMustJson(t, hydrated); got != `{"selected":null,"revision":0}` {
			t.Fatalf("hydrated mutated: %s", got)
		}
		if len(deliveries) != 2 || deliveries[0] != "hydrate" || deliveries[1] != "update" {
			t.Fatalf("unexpected deliveries %v", deliveries)
		}
		unsub()
	})

	t.Run("does not publish when a transaction restores the prior value", func(t *testing.T) {
		state := tservMustState(t, tservObj("value", float64(1)))
		var deliveries []ReplicatedStateDelivery
		state.Subscribe(func(_ *jsonx.Object, _ Context, delivery ReplicatedStateDelivery) error {
			deliveries = append(deliveries, delivery)
			return nil
		})
		if err := state.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
			draft.Value.Set("value", float64(2))
			draft.Value.Set("value", float64(1))
			return nil
		}); err != nil {
			t.Fatalf("Change failed: %v", err)
		}
		if cur, _ := state.Value(); tservMustJson(t, cur) != `{"value":1}` {
			t.Fatalf("got %s", tservMustJson(t, cur))
		}
		if len(deliveries) != 1 || deliveries[0].Kind != "hydrate" || deliveries[0].Sequence != 0 {
			t.Fatalf("unexpected deliveries: %+v", deliveries)
		}
	})

	t.Run("hydrates a new subscriber from the latest atomic revision", func(t *testing.T) {
		state := tservMustState(t, tservObj("entries", []any{tservObj("id", "one")}))
		var first []string
		state.Subscribe(func(v *jsonx.Object, _ Context, _ ReplicatedStateDelivery) error {
			first = append(first, tservMustJson(t, v))
			return nil
		})
		if err := state.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
			arr, _ := draft.Value.Get("entries")
			draft.Value.Set("entries", append(arr.([]any), tservObj("id", "two")))
			return nil
		}); err != nil {
			t.Fatalf("Change failed: %v", err)
		}
		var second []string
		state.Subscribe(func(v *jsonx.Object, _ Context, _ ReplicatedStateDelivery) error {
			second = append(second, tservMustJson(t, v))
			return nil
		})
		if len(first) != 2 || len(second) != 1 || second[0] != first[1] {
			t.Fatalf("unexpected hydration slices: first=%v second=%v", first, second)
		}
	})

	t.Run("does not defensively clone method arguments or results", func(t *testing.T) {
		provider := tservMustProvider(t, tservEntry(echoSvc))
		var received *jsonx.Object
		response := tservObj("value", "response")
		if err := provider.Provide(echoSvc, tservObj("echo", func(payload *jsonx.Object, _ Context) (*jsonx.Object, error) {
			received = payload
			return response, nil
		})); err != nil {
			t.Fatalf("Provide failed: %v", err)
		}
		namespace, err := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: echoSvc.Id}},
			Transport: CreateLoopbackServiceTransport(provider),
		})
		if err != nil {
			t.Fatalf("CreateRemoteServiceBinding failed: %v", err)
		}
		echo := namespace.Use(echoSvc)
		if err := namespace.Ready(BackgroundContext); err != nil {
			t.Fatalf("Ready failed: %v", err)
		}
		request := tservObj("value", "request")
		res, err := tservCall(echo, "echo", request, BackgroundContext)
		if err != nil {
			t.Fatalf("echo call failed: %v", err)
		}
		if res != response || received != request {
			t.Fatalf("expected direct pointer identity without cloning")
		}
		if err := namespace.Dispose(BackgroundContext); err != nil {
			t.Fatalf("Dispose failed: %v", err)
		}
		if err := provider.Dispose(); err != nil {
			t.Fatalf("provider.Dispose failed: %v", err)
		}
	})

	t.Run("provides and consumes one singleton with replicated state", func(t *testing.T) {
		provider := tservMustProvider(t, tservEntry(modelsSvc))
		if cat := provider.Catalogue(); len(cat) != 1 || cat[0].ServiceId != modelsSvc.Id || cat[0].Mode != ServiceModeSingleton {
			t.Fatalf("unexpected catalogue: %+v", cat)
		}
		initialState := tservObj("selected", nil, "revision", float64(0))
		state := tservMustState(t, initialState)
		var publishedState *jsonx.Object
		if err := provider.Provide(modelsSvc, tservObj(
			"state", state,
			"select", func(model *jsonx.Object, call Context) error {
				if err := state.Change(call, func(draft Draft[*jsonx.Object]) error {
					draft.Value.Set("selected", model)
					rev, _ := draft.Value.Get("revision")
					draft.Value.Set("revision", rev.(float64)+1)
					return nil
				}); err != nil {
					return err
				}
				v, _ := state.Value()
				publishedState = v
				return nil
			},
		)); err != nil {
			t.Fatalf("Provide failed: %v", err)
		}
		var errs []error
		gated := &tservGatedTransport{
			inner:   CreateLoopbackServiceTransport(provider),
			release: make(chan struct{}),
		}
		namespace, err := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: modelsSvc.Id}},
			Transport: gated,
			OnError:   func(e error) { errs = append(errs, e) },
		})
		if err != nil {
			t.Fatalf("CreateRemoteServiceBinding failed: %v", err)
		}
		first := namespace.Use(modelsSvc)
		second := namespace.Use(modelsSvc)
		if first != second {
			t.Fatal("expected identical singleton proxy handle")
		}
		stateHandle := tservMemberGet(t, first, "state")
		if _, ok, err := tservStateVal(t, stateHandle); err != nil || ok {
			t.Fatalf("expected unhydrated before ready, got ok=%v err=%v", ok, err)
		}
		close(gated.release)
		if err := namespace.Ready(BackgroundContext); err != nil {
			t.Fatalf("Ready failed: %v", err)
		}
		val, ok, err := tservStateVal(t, stateHandle)
		if err != nil || !ok || tservMustJson(t, val) != tservMustJson(t, initialState) {
			t.Fatalf("expected hydrated initial state, got ok=%v val=%v err=%v", ok, val, err)
		}

		var updates []string
		unsub, err := tservStateSubscribe(t, tservMemberGet(t, second, "state"), func(v JsonValue, _ Context, _ ReplicatedStateDelivery) error {
			updates = append(updates, tservMustJson(t, v))
			return nil
		})
		if err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}
		if _, err := tservCall(first, "select", tservObj("provider", "test", "modelId", "one"), BackgroundContext); err != nil {
			t.Fatalf("select call failed: %v", err)
		}
		curVal, ok, err := tservStateVal(t, stateHandle)
		if err != nil || !ok || tservMustJson(t, curVal) != tservMustJson(t, publishedState) {
			t.Fatalf("unexpected state after select: ok=%v val=%s err=%v", ok, tservMustJson(t, curVal), err)
		}
		if len(updates) != 2 || updates[0] != `{"selected":null,"revision":0}` || updates[1] != `{"selected":{"provider":"test","modelId":"one"},"revision":1}` {
			t.Fatalf("unexpected updates: %v", updates)
		}
		if len(errs) != 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}

		lateNamespace, err := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: modelsSvc.Id}},
			Transport: CreateLoopbackServiceTransport(provider),
		})
		if err != nil {
			t.Fatalf("late CreateRemoteServiceBinding failed: %v", err)
		}
		lateModels := lateNamespace.Use(modelsSvc)
		if err := lateNamespace.Ready(BackgroundContext); err != nil {
			t.Fatalf("late Ready failed: %v", err)
		}
		lateVal, ok, err := tservStateVal(t, tservMemberGet(t, lateModels, "state"))
		if err != nil || !ok {
			t.Fatalf("expected late state ok, err=%v", err)
		}
		rev, _ := lateVal.(*jsonx.Object).Get("revision")
		if rev != float64(1) {
			t.Fatalf("expected late revision 1, got %v", rev)
		}

		unsub()
		_ = namespace.Dispose(BackgroundContext)
		_ = lateNamespace.Dispose(BackgroundContext)
		_ = provider.Dispose()
	})

	t.Run("publishes compact tracked operations through the remote provider", func(t *testing.T) {
		provider := tservMustProvider(t, tservEntry(timelineSvc))
		initial := tservObj("entries", []any{tservObj("id", "one")}, "retained", tservObj("value", float64(1)))
		source := tservMustState(t, initial)
		if err := provider.Provide(timelineSvc, tservObj("state", source)); err != nil {
			t.Fatalf("Provide failed: %v", err)
		}
		var updates []ServiceProviderUpdate
		raw, err := provider.Subscribe(timelineSvc.Id, ServiceModeSingleton, func(u ServiceProviderUpdate, _ Context) error {
			updates = append(updates, u)
			return nil
		})
		if err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}
		if err := raw.Activate(); err != nil {
			t.Fatalf("Activate failed: %v", err)
		}

		namespace, err := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: timelineSvc.Id}},
			Transport: CreateLoopbackServiceTransport(provider),
		})
		if err != nil {
			t.Fatalf("CreateRemoteServiceBinding failed: %v", err)
		}
		timeline := namespace.Use(timelineSvc)
		if err := namespace.Ready(BackgroundContext); err != nil {
			t.Fatalf("Ready failed: %v", err)
		}
		prevVal, _, _ := tservStateVal(t, tservMemberGet(t, timeline, "state"))
		if err := source.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
			arr, _ := draft.Value.Get("entries")
			draft.Value.Set("entries", append(arr.([]any), tservObj("id", "two")))
			return nil
		}); err != nil {
			t.Fatalf("Change 1 failed: %v", err)
		}
		if len(updates) == 0 {
			t.Fatal("expected updates")
		}
		lastUpdate := updates[len(updates)-1].(ServiceProviderUpdateState)
		if lastUpdate.Sequence != 1 || lastUpdate.Ops[0].Verb() != "p" {
			t.Fatalf("expected splice op seq 1, got %+v", lastUpdate)
		}
		if tservMustJson(t, prevVal) != `{"entries":[{"id":"one"}],"retained":{"value":1}}` {
			t.Fatalf("previous mutated: %s", tservMustJson(t, prevVal))
		}
		nextVal, _, _ := tservStateVal(t, tservMemberGet(t, timeline, "state"))
		if tservMustJson(t, nextVal) != `{"entries":[{"id":"one"},{"id":"two"}],"retained":{"value":1}}` {
			t.Fatalf("unexpected timeline value: %s", tservMustJson(t, nextVal))
		}

		if err := source.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
			arr, _ := draft.Value.Get("entries")
			draft.Value.Set("entries", append(arr.([]any), tservObj("id", "three")))
			return nil
		}); err != nil {
			t.Fatalf("Change 2 failed: %v", err)
		}
		late, err := provider.Subscribe(timelineSvc.Id, ServiceModeSingleton, func(ServiceProviderUpdate, Context) error { return nil })
		if err != nil {
			t.Fatalf("late Subscribe failed: %v", err)
		}
		lateMember := late.Snapshot().Instances[0].Members[0].(ServiceMemberSnapshotState)
		if lateMember.Sequence != 2 || lateMember.Ops[0].Verb() != "r" {
			t.Fatalf("unexpected late snapshot member: %+v", lateMember)
		}
		_ = late.Close(BackgroundContext)
		_ = raw.Close(BackgroundContext)
		_ = namespace.Dispose(BackgroundContext)
		_ = provider.Dispose()
	})

	t.Run("publishes authoritative source references through services without re-diffing", func(t *testing.T) {
		initial := tservObj("entries", []any{tservObj("id", "one")}, "retained", tservObj("value", float64(1)))
		var publish func(ReplicatedStateSourceFrame[*jsonx.Object])
		disposed := false
		src := &tservSource[*jsonx.Object]{
			attachFn: func() ReplicatedStateSourceAttachment[*jsonx.Object] {
				return &tservAttachment[*jsonx.Object]{
					snap:     ReplicatedStateSourceSnapshot[*jsonx.Object]{Value: initial, Cursor: 20},
					activate: func(l func(ReplicatedStateSourceFrame[*jsonx.Object])) { publish = l },
					dispose:  func() { disposed = true },
				}
			},
		}
		state, err := NewReplicatedStateSource(src, nil)
		if err != nil {
			t.Fatalf("NewReplicatedStateSource failed: %v", err)
		}
		provider := tservMustProvider(t, tservEntry(timelineSvc))
		if err := provider.Provide(timelineSvc, tservObj("state", state)); err != nil {
			t.Fatalf("Provide failed: %v", err)
		}
		var updates []ServiceProviderUpdate
		sub, err := provider.Subscribe(timelineSvc.Id, ServiceModeSingleton, func(u ServiceProviderUpdate, _ Context) error {
			updates = append(updates, u)
			return nil
		})
		if err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}
		snapMember := sub.Snapshot().Instances[0].Members[0].(ServiceMemberSnapshotState)
		if snapMember.Ops[0].(OpReplace).Value != initial {
			t.Fatal("expected exact initial pointer reference in snapshot")
		}
		if err := sub.Activate(); err != nil {
			t.Fatalf("Activate failed: %v", err)
		}
		next := tservObj("entries", []any{tservObj("id", "one"), tservObj("id", "two")}, "retained", tservObj("value", float64(1)))
		ops := OpList{OpSplice{Path: Path{SegString("entries")}, Index: 1, Remove: 0, Items: []JsonValue{tservObj("id", "two")}}}
		publish(ReplicatedStateSourceFrame[*jsonx.Object]{
			Cursor:  21,
			Value:   next,
			Ops:     ops,
			Context: BackgroundContext,
		})
		if len(updates) != 1 {
			t.Fatalf("expected 1 update, got %d", len(updates))
		}
		u := updates[0].(ServiceProviderUpdateState)
		if reflect.ValueOf(u.Ops).Pointer() != reflect.ValueOf(ops).Pointer() {
			t.Fatal("expected authoritative ops slice reference preserved without re-diffing")
		}
		_ = sub.Close(BackgroundContext)
		_ = provider.Dispose()
		state.Dispose()
		if !disposed {
			t.Fatal("expected source attachment disposed")
		}
	})

	t.Run("keeps singleton facades stable when their provider is replaced", func(t *testing.T) {
		provider := tservMustProvider(t, tservEntry(modelsSvc))
		if err := provider.Provide(modelsSvc, tservObj(
			"state", tservMustState(t, tservObj("selected", nil, "revision", float64(1))),
			"select", func(_ any, _ Context) error { return nil },
		)); err != nil {
			t.Fatalf("Provide failed: %v", err)
		}
		namespace, err := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: modelsSvc.Id}},
			Transport: CreateLoopbackServiceTransport(provider),
		})
		if err != nil {
			t.Fatalf("CreateRemoteServiceBinding failed: %v", err)
		}
		models := namespace.Use(modelsSvc)
		stateMember := tservMemberGet(t, models, "state")
		selectMember := tservMemberGet(t, models, "select")
		if err := namespace.Ready(BackgroundContext); err != nil {
			t.Fatalf("Ready failed: %v", err)
		}
		v, ok, _ := tservStateVal(t, stateMember)
		if !ok || tservGet(v, "revision") != float64(1) {
			t.Fatalf("expected revision 1, got %v %v", v, ok)
		}

		if err := provider.Withdraw(modelsSvc); err != nil {
			t.Fatalf("Withdraw failed: %v", err)
		}
		if _, ok, _ := tservStateVal(t, stateMember); ok {
			t.Fatal("expected state unavailable after Withdraw")
		}
		if _, err := tservCall(selectMember, "", tservObj("provider", "test", "modelId", "unavailable"), BackgroundContext); !tservHasCode(err, RemoteServiceErrorCodeServiceNotFound) {
			t.Fatalf("expected service_not_found after Withdraw, got %v", err)
		}

		called := 0
		if err := provider.Replace(modelsSvc, tservObj(
			"state", tservMustState(t, tservObj("selected", nil, "revision", float64(2))),
			"select", func(_ any, _ Context) error {
				called++
				return nil
			},
		)); err != nil {
			t.Fatalf("Replace failed: %v", err)
		}
		if namespace.Use(modelsSvc) != models || tservMemberGet(t, models, "state") != stateMember {
			t.Fatal("expected stable singleton facade and member slot across Replace")
		}
		v, ok, _ = tservStateVal(t, stateMember)
		if !ok || tservGet(v, "revision") != float64(2) {
			t.Fatalf("expected revision 2, got %v %v", v, ok)
		}
		if _, err := tservCall(selectMember, "", tservObj("provider", "test", "modelId", "replacement"), BackgroundContext); err != nil || called != 1 {
			t.Fatalf("expected replacement select called once, called=%d err=%v", called, err)
		}
		if err := provider.Replace(modelsSvc, tservObj("select", func(_ Context) error { return nil })); err == nil || !strings.Contains(err.Error(), "replacement must preserve its member shape") {
			t.Fatalf("expected shape mismatch error, got %v", err)
		}
		if err := provider.Replace(modelsSvc, tservObj("state", func(_ Context) error { return nil }, "select", func(_ Context) error { return nil })); err == nil || !strings.Contains(err.Error(), "replacement must preserve its member shape") {
			t.Fatalf("expected shape mismatch error on kind change, got %v", err)
		}
		v, ok, _ = tservStateVal(t, stateMember)
		if !ok || tservGet(v, "revision") != float64(2) {
			t.Fatalf("expected revision still 2 after rejected Replace")
		}

		_ = namespace.Dispose(BackgroundContext)
		_ = provider.Dispose()
	})

	t.Run("delivers active subscriber updates before reporting listener failures", func(t *testing.T) {
		failure := errors.New("listener failed")
		provider := tservMustProvider(t, tservEntry(modelsSvc))
		if err := provider.Provide(modelsSvc, tservObj(
			"state", tservMustState(t, tservObj("selected", nil, "revision", float64(1))),
			"select", func(_ Context) error { return nil },
		)); err != nil {
			t.Fatalf("Provide failed: %v", err)
		}
		delivered := 0
		failing, _ := provider.Subscribe(modelsSvc.Id, ServiceModeSingleton, func(ServiceProviderUpdate, Context) error {
			return failure
		})
		succeeding, _ := provider.Subscribe(modelsSvc.Id, ServiceModeSingleton, func(ServiceProviderUpdate, Context) error {
			delivered++
			return nil
		})
		_ = failing.Activate()
		_ = succeeding.Activate()
		err := provider.Replace(modelsSvc, tservObj(
			"state", tservMustState(t, tservObj("selected", nil, "revision", float64(2))),
			"select", func(_ Context) error { return nil },
		))
		if !errors.Is(err, failure) || delivered != 1 {
			t.Fatalf("expected failure and delivered=1, got err=%v delivered=%d", err, delivered)
		}
		_ = failing.Close(BackgroundContext)
		_ = succeeding.Close(BackgroundContext)
		_ = provider.Dispose()
	})

	t.Run("does not replay a queued revision already covered by a new subscription snapshot", func(t *testing.T) {
		provider := tservMustProvider(t, tservEntry(modelsSvc))
		state := tservMustState(t, tservObj("selected", nil, "revision", float64(0)))
		if err := provider.Provide(modelsSvc, tservObj("state", state, "select", func(_ Context) error { return nil })); err != nil {
			t.Fatalf("Provide failed: %v", err)
		}
		var lateUpdates []int
		var late ServiceSubscription
		first, _ := provider.Subscribe(modelsSvc.Id, ServiceModeSingleton, func(u ServiceProviderUpdate, _ Context) error {
			st, ok := u.(ServiceProviderUpdateState)
			if !ok || st.Sequence != 1 {
				return nil
			}
			_ = state.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
				draft.Value.Set("revision", float64(2))
				return nil
			})
			late, _ = provider.Subscribe(modelsSvc.Id, ServiceModeSingleton, func(next ServiceProviderUpdate, _ Context) error {
				if st2, ok := next.(ServiceProviderUpdateState); ok {
					lateUpdates = append(lateUpdates, st2.Sequence)
				}
				return nil
			})
			_ = late.Activate()
			return nil
		})
		_ = first.Activate()
		_ = state.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
			draft.Value.Set("revision", float64(1))
			return nil
		})
		if len(lateUpdates) != 0 {
			t.Fatalf("expected no replayed update for rev 2, got %v", lateUpdates)
		}
		_ = state.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
			draft.Value.Set("revision", float64(3))
			return nil
		})
		if len(lateUpdates) != 1 || lateUpdates[0] != 3 {
			t.Fatalf("expected [3], got %v", lateUpdates)
		}
		_ = late.Close(BackgroundContext)
		_ = first.Close(BackgroundContext)
		_ = provider.Dispose()
	})

	t.Run("replays every buffered update before reporting listener failures", func(t *testing.T) {
		failure := errors.New("listener failed")
		provider := tservMustProvider(t, tservEntry(modelsSvc))
		state := tservMustState(t, tservObj("selected", nil, "revision", float64(0)))
		if err := provider.Provide(modelsSvc, tservObj("state", state, "select", func(_ Context) error { return nil })); err != nil {
			t.Fatalf("Provide failed: %v", err)
		}
		delivered := 0
		sub, _ := provider.Subscribe(modelsSvc.Id, ServiceModeSingleton, func(ServiceProviderUpdate, Context) error {
			delivered++
			return failure
		})
		for _, r := range []float64{1, 2} {
			rev := r
			_ = state.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
				draft.Value.Set("revision", rev)
				return nil
			})
		}
		err := sub.Activate()
		if err == nil || !strings.Contains(err.Error(), "Failed to activate remote service subscription") || delivered != 2 {
			t.Fatalf("expected activation error with delivered=2, got err=%v delivered=%d", err, delivered)
		}
		_ = sub.Close(BackgroundContext)
		_ = provider.Dispose()
	})

	t.Run("clears retained facades when providers and bindings are disposed", func(t *testing.T) {
		provider := tservMustProvider(t, tservEntry(modelsSvc))
		_ = provider.Provide(modelsSvc, tservObj(
			"state", tservMustState(t, tservObj("selected", nil, "revision", float64(1))),
			"select", func(_ Context) error { return nil },
		))
		namespace, err := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: modelsSvc.Id}},
			Transport: CreateLoopbackServiceTransport(provider),
		})
		if err != nil {
			t.Fatalf("CreateRemoteServiceBinding failed: %v", err)
		}
		models := namespace.Use(modelsSvc)
		stateMember := tservMemberGet(t, models, "state")
		_ = namespace.Ready(BackgroundContext)
		v, ok, _ := tservStateVal(t, stateMember)
		if !ok || tservGet(v, "revision") != float64(1) {
			t.Fatalf("expected revision 1")
		}
		_ = provider.Dispose()
		if _, ok, err := tservStateVal(t, stateMember); err != nil || ok {
			t.Fatalf("expected state cleared after provider dispose, ok=%v err=%v", ok, err)
		}
		if _, err := tservCall(models, "select", tservObj("provider", "test", "modelId", "one"), BackgroundContext); err == nil || !strings.Contains(err.Error(), "Remote service provider is disposed") {
			t.Fatalf("expected provider disposed error, got %v", err)
		}
		_ = namespace.Dispose(BackgroundContext)
		if _, _, err := tservStateVal(t, stateMember); err == nil || !strings.Contains(err.Error(), "Remote service binding is disposed") {
			t.Fatalf("expected binding disposed error, got %v", err)
		}
	})

	t.Run("applies provider disposal buffered while subscriptions are starting", func(t *testing.T) {
		provider := tservMustProvider(t, tservEntry(modelsSvc), tservEntry(questionDialogsSvc, ServiceModeKeyed))
		_ = provider.Provide(modelsSvc, tservObj(
			"state", tservMustState(t, tservObj("selected", nil, "revision", float64(1))),
			"select", func(_ Context) error { return nil },
		))
		_, _ = provider.Spawn(questionDialogsSvc, "pending", tservObj(
			"request", tservMustState(t, tservObj("question", "Pending?")),
			"submit", func(_ string, _ Context) (*jsonx.Object, error) { return tservObj("accepted", true), nil },
		))

		subscribedCount := atomic.Int32{}
		bothSubscribed := make(chan struct{})
		release := make(chan struct{})
		transport := &tservFuncTransport{
			invoke: provider.Invoke,
			subscribe: func(serviceId string, mode ServiceMode, listener func(ServiceProviderUpdate, Context), callContext Context) (ServiceSubscription, error) {
				sub, err := provider.Subscribe(serviceId, mode, func(u ServiceProviderUpdate, c Context) error {
					if listener != nil {
						listener(u, c)
					}
					return nil
				})
				if err != nil {
					return nil, err
				}
				if subscribedCount.Add(1) == 2 {
					close(bothSubscribed)
				}
				<-release
				return sub, nil
			},
		}

		namespace, _ := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: modelsSvc.Id}, {Id: questionDialogsSvc.Id}},
			Transport: transport,
		})
		models := namespace.Use(modelsSvc)
		var obsMu sync.Mutex
		var observed []any
		namespace.Observe(questionDialogsSvc, func(service any, _ Context) error {
			obsMu.Lock()
			observed = append(observed, service)
			obsMu.Unlock()
			return nil
		})
		<-bothSubscribed
		_ = provider.Dispose()
		close(release)
		if err := namespace.Ready(BackgroundContext); err != nil {
			t.Fatalf("Ready failed: %v", err)
		}
		if _, ok, err := tservStateVal(t, tservMemberGet(t, models, "state")); err != nil || ok {
			t.Fatalf("expected models state cleared, ok=%v err=%v", ok, err)
		}
		obsMu.Lock()
		if len(observed) != 0 {
			t.Fatalf("expected no observed keyed services, got %d", len(observed))
		}
		obsMu.Unlock()
		_ = namespace.Dispose(BackgroundContext)
	})

	t.Run("keeps deferred service handles inaccessible until host activation", func(t *testing.T) {
		provider := tservMustProvider(t, tservEntry(modelsSvc))
		_ = provider.Provide(modelsSvc, tservObj(
			"state", tservMustState(t, tservObj("selected", nil, "revision", float64(0))),
			"select", func(_ Context) error { return nil },
		))
		var active atomic.Bool
		boundFalse := false
		namespace, _ := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: modelsSvc.Id}},
			Transport: CreateLoopbackServiceTransport(provider),
			Bound:     &boundFalse,
			AssertAccess: func() {
				if !active.Load() {
					panic(errors.New("Service handles are not active"))
				}
			},
		})
		models := namespace.Use(modelsSvc)
		stateMember := tservMemberGet(t, models, "state")

		if _, _, err := tservStateVal(t, stateMember); err == nil || !strings.Contains(err.Error(), "Service handles are not active") {
			t.Fatalf("expected access guard on value, got %v", err)
		}
		if _, err := tservStateSubscribe(t, stateMember, func(JsonValue, Context, ReplicatedStateDelivery) error { return nil }); err == nil || !strings.Contains(err.Error(), "Service handles are not active") {
			t.Fatalf("expected access guard on subscribe, got %v", err)
		}
		if _, err := tservCall(models, "select", tservObj("provider", "test", "modelId", "one"), BackgroundContext); err == nil || !strings.Contains(err.Error(), "Service handles are not active") {
			t.Fatalf("expected access guard on method, got %v", err)
		}

		if err := namespace.Rebind(true, BackgroundContext); err != nil {
			t.Fatalf("Rebind(true) failed: %v", err)
		}
		active.Store(true)
		if err := namespace.Ready(BackgroundContext); err != nil {
			t.Fatalf("Ready failed: %v", err)
		}
		v, ok, err := tservStateVal(t, stateMember)
		if err != nil || !ok || tservGet(v, "revision") != float64(0) {
			t.Fatalf("expected revision 0, got %v %v %v", v, ok, err)
		}
		_ = namespace.Dispose(BackgroundContext)
		_ = provider.Dispose()
	})

	t.Run("rejects namespace readiness when initial hydration fails", func(t *testing.T) {
		failure := errors.New("initial hydration failed")
		var errsMu sync.Mutex
		var errs []error
		namespace, _ := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services: []ServiceRef{{Id: modelsSvc.Id}},
			Transport: &tservFuncTransport{
				invoke: func(ServiceCall, Context) (JsonValue, bool, error) {
					return nil, false, errors.New("unexpected invocation")
				},
				subscribe: func(string, ServiceMode, func(ServiceProviderUpdate, Context), Context) (ServiceSubscription, error) {
					return nil, failure
				},
			},
			OnError: func(e error) {
				errsMu.Lock()
				errs = append(errs, e)
				errsMu.Unlock()
			},
		})
		models := namespace.Use(modelsSvc)
		if err := namespace.Ready(BackgroundContext); !errors.Is(err, failure) {
			t.Fatalf("expected hydration failure, got %v", err)
		}
		if _, ok, _ := tservStateVal(t, tservMemberGet(t, models, "state")); ok {
			t.Fatal("expected unhydrated state")
		}
		tservWaitFor(t, "reported hydration failure", func() bool {
			errsMu.Lock()
			defer errsMu.Unlock()
			return len(errs) == 1 && errors.Is(errs[0], failure)
		})
		_ = namespace.Dispose(BackgroundContext)
	})

	t.Run("buffers state updates that race subscription hydration", func(t *testing.T) {
		provider := tservMustProvider(t, tservEntry(modelsSvc))
		state := tservMustState(t, tservObj("selected", nil, "revision", float64(0)))
		_ = provider.Provide(modelsSvc, tservObj("state", state, "select", func(_ Context) error { return nil }))
		transport := &tservFuncTransport{
			invoke: provider.Invoke,
			subscribe: func(serviceId string, mode ServiceMode, listener func(ServiceProviderUpdate, Context), _ Context) (ServiceSubscription, error) {
				sub, err := provider.Subscribe(serviceId, mode, func(u ServiceProviderUpdate, c Context) error {
					if listener != nil {
						listener(u, c)
					}
					return nil
				})
				if err != nil {
					return nil, err
				}
				_ = state.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
					draft.Value.Set("revision", float64(1))
					return nil
				})
				return sub, nil
			},
		}
		namespace, _ := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: modelsSvc.Id}},
			Transport: transport,
		})
		models := namespace.Use(modelsSvc)
		var revMu sync.Mutex
		var revisions []float64
		_, err := tservStateSubscribe(t, tservMemberGet(t, models, "state"), func(v JsonValue, _ Context, _ ReplicatedStateDelivery) error {
			revMu.Lock()
			revisions = append(revisions, tservGet(v, "revision").(float64))
			revMu.Unlock()
			return nil
		})
		if err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}
		tservWaitFor(t, "revisions [0, 1]", func() bool {
			revMu.Lock()
			defer revMu.Unlock()
			return len(revisions) == 2 && revisions[0] == 0 && revisions[1] == 1
		})
		v, ok, _ := tservStateVal(t, tservMemberGet(t, models, "state"))
		if !ok || tservGet(v, "revision") != float64(1) {
			t.Fatalf("expected revision 1")
		}
		_ = namespace.Dispose(BackgroundContext)
		_ = provider.Dispose()
	})

	for _, tc := range []struct {
		name string
		seq  int
	}{
		{name: "duplicate", seq: 0},
		{name: "gap", seq: 2},
	} {
		t.Run("clears replicated state after a "+tc.name+" operation sequence", func(t *testing.T) {
			var sendUpdate func(ServiceProviderUpdate)
			var errsMu sync.Mutex
			var errs []error
			namespace, _ := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
				Services: []ServiceRef{{Id: modelsSvc.Id}},
				Transport: &tservFuncTransport{
					invoke: func(ServiceCall, Context) (JsonValue, bool, error) {
						return nil, false, errors.New("unexpected invocation")
					},
					subscribe: func(_ string, _ ServiceMode, listener func(ServiceProviderUpdate, Context), _ Context) (ServiceSubscription, error) {
						sendUpdate = func(u ServiceProviderUpdate) {
							listener(u, BackgroundContext)
						}
						return &tservSubscriptionFuncs{
							snapshot: ServiceSubscriptionSnapshot{
								ServiceId: modelsSvc.Id,
								Mode:      ServiceModeSingleton,
								Instances: []ServiceInstanceSnapshot{{
									Members: ServiceMemberSnapshotList{
										ServiceMemberSnapshotMethod{Name: "select", Kind: "method"},
										ServiceMemberSnapshotState{Name: "state", Kind: "state", Sequence: 0, Ops: OpList{OpReplace{Value: tservObj("selected", nil, "revision", float64(0))}}},
									},
								}},
							},
							activate: func() error { return nil },
							closeFn:  func(Context) error { return nil },
						}, nil
					},
				},
				OnError: func(e error) {
					errsMu.Lock()
					errs = append(errs, e)
					errsMu.Unlock()
				},
			})
			models := namespace.Use(modelsSvc)
			if err := namespace.Ready(BackgroundContext); err != nil {
				t.Fatalf("Ready failed: %v", err)
			}
			sendUpdate(ServiceProviderUpdateState{
				Type:     "state",
				Member:   "state",
				Sequence: tc.seq,
				Ops:      OpList{OpReplace{Value: tservObj("selected", nil, "revision", float64(tc.seq))}},
			})
			if _, ok, _ := tservStateVal(t, tservMemberGet(t, models, "state")); ok {
				t.Fatal("expected state cleared")
			}
			errsMu.Lock()
			if len(errs) != 1 || !strings.Contains(errs[0].Error(), "sequence has a gap") {
				t.Fatalf("expected sequence gap error, got %v", errs)
			}
			errsMu.Unlock()
			_ = namespace.Dispose(BackgroundContext)
		})
	}

	t.Run("hydrates cold ReplicatedState replicas and replaces them across rebinds", func(t *testing.T) {
		provider := tservMustProvider(t, tservEntry(modelsSvc))
		state := tservMustState(t, tservObj("selected", nil, "revision", float64(0)))
		_ = provider.Provide(modelsSvc, tservObj("state", state, "select", func(_ Context) error { return nil }))
		boundFalse := false
		namespace, _ := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: modelsSvc.Id}},
			Transport: CreateLoopbackServiceTransport(provider),
			Bound:     &boundFalse,
		})
		models := namespace.Use(modelsSvc)
		stateMember := tservMemberGet(t, models, "state")
		var revMu sync.Mutex
		var revisions []float64
		_, _ = tservStateSubscribe(t, stateMember, func(v JsonValue, _ Context, _ ReplicatedStateDelivery) error {
			revMu.Lock()
			revisions = append(revisions, tservGet(v, "revision").(float64))
			revMu.Unlock()
			return nil
		})
		if _, ok, _ := tservStateVal(t, stateMember); ok {
			t.Fatal("expected cold replica unhydrated")
		}
		_ = state.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
			draft.Value.Set("revision", float64(1))
			return nil
		})
		if err := namespace.Rebind(true, BackgroundContext); err != nil {
			t.Fatalf("Rebind(true) failed: %v", err)
		}
		_ = state.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
			draft.Value.Set("revision", float64(2))
			return nil
		})
		if err := namespace.Rebind(false, BackgroundContext); err != nil {
			t.Fatalf("Rebind(false) failed: %v", err)
		}
		if _, ok, _ := tservStateVal(t, stateMember); ok {
			t.Fatal("expected cleared after Rebind(false)")
		}
		_ = state.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
			draft.Value.Set("revision", float64(3))
			return nil
		})
		if err := namespace.Rebind(true, BackgroundContext); err != nil {
			t.Fatalf("second Rebind(true) failed: %v", err)
		}
		revMu.Lock()
		if !reflect.DeepEqual(revisions, []float64{1, 2, 3}) {
			t.Fatalf("expected [1 2 3], got %v", revisions)
		}
		revMu.Unlock()
		_ = namespace.Dispose(BackgroundContext)
		_ = provider.Dispose()
	})

	t.Run("hydrates keyed state before observe handlers and fences reused keys", func(t *testing.T) {
		provider := tservMustProvider(t, tservEntry(questionDialogsSvc, ServiceModeKeyed))
		var errsMu sync.Mutex
		var errs []error
		namespace, _ := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: questionDialogsSvc.Id}},
			Transport: CreateLoopbackServiceTransport(provider),
			OnError: func(e error) {
				errsMu.Lock()
				errs = append(errs, e)
				errsMu.Unlock()
			},
		})
		type obsEntry struct {
			question string
			service  any
			ctx      Context
		}
		var obsMu sync.Mutex
		var observed []obsEntry
		stop := namespace.Observe(questionDialogsSvc, func(service any, call Context) error {
			reqVal, _, err := tservStateVal(t, tservMemberGet(t, service, "request"))
			if err != nil {
				return err
			}
			obsMu.Lock()
			observed = append(observed, obsEntry{
				question: tservGet(reqVal, "question").(string),
				service:  service,
				ctx:      call,
			})
			obsMu.Unlock()
			return nil
		})

		firstRequest := tservMustState(t, tservObj("question", "First?"))
		var submitted []string
		closeFirst, _ := provider.Spawn(questionDialogsSvc, "invocation-1", tservObj(
			"request", firstRequest,
			"submit", func(answer string, call Context) (*jsonx.Object, error) {
				submitted = append(submitted, answer)
				return tservObj("accepted", true), nil
			},
		))
		tservWaitFor(t, "first observed", func() bool {
			obsMu.Lock()
			defer obsMu.Unlock()
			return len(observed) == 1
		})
		obsMu.Lock()
		firstEntry := observed[0]
		obsMu.Unlock()
		if firstEntry.question != "First?" {
			t.Fatalf("got %q", firstEntry.question)
		}
		_ = firstRequest.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
			draft.Value.Set("question", "Updated?")
			return nil
		})
		reqVal, _, _ := tservStateVal(t, tservMemberGet(t, firstEntry.service, "request"))
		if tservGet(reqVal, "question") != "Updated?" {
			t.Fatalf("expected Updated?")
		}
		ans, err := tservCall(firstEntry.service, "submit", "yes", BackgroundContext)
		if err != nil || tservMustJson(t, ans) != `{"accepted":true}` {
			t.Fatalf("submit failed: ans=%v err=%v", ans, err)
		}
		retainedRequest := tservMemberGet(t, firstEntry.service, "request")
		retainedSubmit := tservMemberGet(t, firstEntry.service, "submit")
		_ = closeFirst()
		if firstEntry.ctx.AbortSignal() == nil || firstEntry.ctx.AbortSignal().Err() == nil {
			t.Fatal("expected first context aborted")
		}
		if _, err := firstEntry.service.(*shServiceView).Get("request"); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("expected observation closed on first service view, got %v", err)
		}
		if _, _, err := tservStateVal(t, retainedRequest); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("expected observation closed on request, got %v", err)
		}
		if _, err := tservCall(retainedSubmit, "", "late", BackgroundContext); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("expected observation closed on retained submit, got %v", err)
		}

		secondRequest := tservMustState(t, tservObj("question", "Again?"))
		closeSecond, _ := provider.Spawn(questionDialogsSvc, "invocation-1", tservObj(
			"request", secondRequest,
			"submit", func(_ string, _ Context) (*jsonx.Object, error) {
				return tservObj("accepted", false), nil
			},
		))
		tservWaitFor(t, "second observed", func() bool {
			obsMu.Lock()
			defer obsMu.Unlock()
			return len(observed) == 2
		})
		obsMu.Lock()
		secondEntry := observed[1]
		obsMu.Unlock()
		if secondEntry.question != "Again?" || secondEntry.service == firstEntry.service {
			t.Fatalf("unexpected second entry: %+v", secondEntry)
		}
		retainedSecondRequest := tservMemberGet(t, secondEntry.service, "request")
		retainedSecondSubmit := tservMemberGet(t, secondEntry.service, "submit")
		stop()
		if secondEntry.ctx.AbortSignal() == nil || secondEntry.ctx.AbortSignal().Err() == nil {
			t.Fatal("expected second context aborted after stop")
		}
		if _, err := secondEntry.service.(*shServiceView).Get("request"); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("expected observation closed on second service view, got %v", err)
		}
		if _, _, err := tservStateVal(t, retainedSecondRequest); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("expected observation closed on second request, got %v", err)
		}
		if _, err := tservCall(retainedSecondSubmit, "", "late", BackgroundContext); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("expected observation closed on second submit, got %v", err)
		}
		_ = closeSecond()
		_ = namespace.Dispose(BackgroundContext)
		_ = provider.Dispose()
		errsMu.Lock()
		if len(errs) != 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		errsMu.Unlock()
		_ = submitted
	})

	t.Run("rejects mode mixing and unsupported members", func(t *testing.T) {
		provider := tservMustProvider(t, tservEntry(modelsSvc), tservEntry(questionDialogsSvc, ServiceModeKeyed))
		_ = provider.Provide(modelsSvc, tservObj(
			"state", tservMustState(t, tservObj("selected", nil, "revision", float64(0))),
			"select", func(_ Context) error { return nil },
		))
		if _, err := provider.Spawn(modelsSvc, "wrong", tservObj()); err == nil || !strings.Contains(err.Error(), "singleton") {
			t.Fatalf("expected singleton mismatch error, got %v", err)
		}
		if _, err := provider.Spawn(questionDialogsSvc, "invalid", tservObj(
			"request", time.Now(),
			"submit", func(_ Context) (*jsonx.Object, error) { return tservObj("accepted", true), nil },
		)); err == nil || !strings.Contains(err.Error(), "not remotely exposable") {
			t.Fatalf("expected not remotely exposable error, got %v", err)
		}
		_ = provider.Dispose()
	})
}

type tservSource[T any] struct {
	attachFn func() ReplicatedStateSourceAttachment[T]
}

func (s *tservSource[T]) Attach() ReplicatedStateSourceAttachment[T] {
	return s.attachFn()
}

type tservAttachment[T any] struct {
	snap     ReplicatedStateSourceSnapshot[T]
	activate func(func(ReplicatedStateSourceFrame[T]))
	dispose  func()
}

func (a *tservAttachment[T]) Snapshot() ReplicatedStateSourceSnapshot[T] { return a.snap }
func (a *tservAttachment[T]) Activate(l func(ReplicatedStateSourceFrame[T])) {
	if a.activate != nil {
		a.activate(l)
	}
}
func (a *tservAttachment[T]) Dispose() {
	if a.dispose != nil {
		a.dispose()
	}
}

var _ = js.NewTypeError
