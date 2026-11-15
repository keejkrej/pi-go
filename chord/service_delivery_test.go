// Ported from packages/chord/test/service-delivery.test.ts (pi v1.0.0).

package chord

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tsdWireTransport struct {
	provider       *RemoteServiceProvider
	beforeActivate func()
	updatesMu      sync.Mutex
	updates        *[]ServiceProviderUpdate
}

func (w *tsdWireTransport) Invoke(call ServiceCall, callContext Context) (JsonValue, bool, error) {
	return w.provider.Invoke(call, callContext)
}

func (w *tsdWireTransport) Subscribe(serviceId string, mode ServiceMode, listener func(ServiceProviderUpdate, Context), callContext Context) (ServiceSubscription, error) {
	encoder := CreateServiceStateEncoder()
	decoder := CreateServiceStateDecoder()
	sub, err := w.provider.Subscribe(serviceId, mode, func(update ServiceProviderUpdate, ctx Context) error {
		encoded, err := encoder.EncodeUpdate(update)
		if err != nil {
			return err
		}
		wire, err := ParseWireServiceProviderUpdate(encoded)
		if err != nil {
			return err
		}
		decoded, err := decoder.DecodeUpdate(wire)
		if err != nil {
			return err
		}
		w.updatesMu.Lock()
		*w.updates = append(*w.updates, decoded)
		w.updatesMu.Unlock()
		listener(decoded, ctx)
		return nil
	})
	if err != nil {
		return nil, err
	}
	encSnap, err := encoder.EncodeSnapshot(sub.Snapshot())
	if err != nil {
		return nil, err
	}
	wireSnap, err := ParseWireServiceSubscriptionSnapshot(encSnap)
	if err != nil {
		return nil, err
	}
	snap, err := decoder.DecodeSnapshot(wireSnap)
	if err != nil {
		return nil, err
	}
	return &tsdWireSubscription{
		snap:           snap,
		beforeActivate: w.beforeActivate,
		sub:            sub,
	}, nil
}

type tsdWireSubscription struct {
	snap           ServiceSubscriptionSnapshot
	beforeActivate func()
	sub            ServiceSubscription
}

func (s *tsdWireSubscription) Snapshot() ServiceSubscriptionSnapshot { return s.snap }
func (s *tsdWireSubscription) Activate() error {
	if s.beforeActivate != nil {
		s.beforeActivate()
	}
	return s.sub.Activate()
}
func (s *tsdWireSubscription) Close(call Context) error { return s.sub.Close(call) }

func tsdMustService(t *testing.T, id string) *Service[any] {
	t.Helper()
	svc, err := DefineService[any](id, nil)
	if err != nil {
		t.Fatalf("DefineService(%q) failed: %v", id, err)
	}
	return svc
}

func tsdEntry(svc *Service[any], mode ...ServiceMode) RemoteServiceProviderEntry {
	var mPtr *ServiceMode
	if len(mode) > 0 {
		m := mode[0]
		mPtr = &m
	}
	return RemoteServiceProviderEntry{Id: svc.Id, Mode: mPtr}
}

func TestProviderDeliveryQueues(t *testing.T) {
	counterSvc := tsdMustService(t, "test.delivery-counter")
	pairSvc := tsdMustService(t, "test.delivery-pair")

	t.Run("appends reentrant updates to the activation FIFO", func(t *testing.T) {
		provider, _ := NewRemoteServiceProvider([]RemoteServiceProviderEntry{tsdEntry(counterSvc)})
		state := tservMustState(t, jsonx.ObjectOf("value", float64(0)))
		_ = provider.Provide(counterSvc, jsonx.ObjectOf("state", state))
		var received []int
		sub, _ := provider.Subscribe(counterSvc.Id, ServiceModeSingleton, func(u ServiceProviderUpdate, _ Context) error {
			st, ok := u.(ServiceProviderUpdateState)
			if !ok {
				return nil
			}
			received = append(received, st.Sequence)
			if st.Sequence == 1 {
				_ = state.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(3)))
			}
			return nil
		})
		_ = state.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(1)))
		_ = state.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(2)))
		_ = sub.Activate()
		_ = sub.Activate()
		if !reflect.DeepEqual(received, []int{1, 2, 3}) {
			t.Fatalf("expected [1 2 3], got %v", received)
		}
		_ = provider.Dispose()
	})

	for _, count := range []int{100, 101, 102, 201, 202} {
		t.Run(fmt.Sprintf("bounds %d pending updates with explicit root resets", count), func(t *testing.T) {
			provider, _ := NewRemoteServiceProvider([]RemoteServiceProviderEntry{tsdEntry(counterSvc)})
			state := tservMustState(t, jsonx.ObjectOf("value", float64(0)))
			_ = provider.Provide(counterSvc, jsonx.ObjectOf("state", state))
			var updates []ServiceProviderUpdate
			var contexts []Context
			sub, _ := provider.Subscribe(counterSvc.Id, ServiceModeSingleton, func(u ServiceProviderUpdate, ctx Context) error {
				updates = append(updates, u)
				contexts = append(contexts, ctx)
				return nil
			})
			cc := WithCancel(BackgroundContext)
			for value := 1; value <= count; value++ {
				_ = state.Replace(cc.Context, jsonx.ObjectOf("value", float64(value)))
			}
			if len(updates) != 0 {
				t.Fatalf("expected no updates before Activate, got %d", len(updates))
			}
			if sub.Snapshot().Instances[0].Members[0].(ServiceMemberSnapshotState).Sequence != 0 {
				t.Fatalf("expected snapshot sequence 0")
			}
			_ = sub.Activate()
			first := ((count-1)/100)*100 + 1
			if len(updates) != count-first+1 {
				t.Fatalf("expected %d updates, got %d", count-first+1, len(updates))
			}
			if count > 100 {
				rst := updates[0].(ServiceProviderUpdateReset)
				mem := rst.Snapshot.Instances[0].Members[0].(ServiceMemberSnapshotState)
				if mem.Sequence != first || tservGet(mem.Ops[0].(OpReplace).Value, "value") != float64(first) {
					t.Fatalf("unexpected overflow reset member: %+v", mem)
				}
			}
			for _, c := range contexts {
				if c != cc.Context {
					t.Fatalf("expected preserved context")
				}
			}
			_ = state.Replace(cc.Context, jsonx.ObjectOf("value", float64(count+1)))
			last := updates[len(updates)-1].(ServiceProviderUpdateState)
			if last.Sequence != count+1 {
				t.Fatalf("expected seq %d, got %d", count+1, last.Sequence)
			}
			_ = provider.Dispose()
		})
	}

	t.Run("does not compact the running delivery when activation overflows reentrantly", func(t *testing.T) {
		provider, _ := NewRemoteServiceProvider([]RemoteServiceProviderEntry{tsdEntry(counterSvc)})
		state := tservMustState(t, jsonx.ObjectOf("value", float64(0)))
		_ = provider.Provide(counterSvc, jsonx.ObjectOf("state", state))
		var updates []ServiceProviderUpdate
		sub, _ := provider.Subscribe(counterSvc.Id, ServiceModeSingleton, func(u ServiceProviderUpdate, _ Context) error {
			updates = append(updates, u)
			if st, ok := u.(ServiceProviderUpdateState); ok && st.Sequence == 1 {
				for value := 3; value <= 103; value++ {
					_ = state.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(value)))
				}
			}
			return nil
		})
		_ = state.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(1)))
		_ = state.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(2)))
		_ = sub.Activate()
		if len(updates) != 3 {
			t.Fatalf("expected 3 updates, got %d", len(updates))
		}
		if updates[0].(ServiceProviderUpdateState).Sequence != 1 {
			t.Fatalf("unexpected update 0: %+v", updates[0])
		}
		rstMem := updates[1].(ServiceProviderUpdateReset).Snapshot.Instances[0].Members[0].(ServiceMemberSnapshotState)
		if rstMem.Sequence != 102 || tservGet(rstMem.Ops[0].(OpReplace).Value, "value") != float64(102) {
			t.Fatalf("unexpected update 1: %+v", rstMem)
		}
		if updates[2].(ServiceProviderUpdateState).Sequence != 103 {
			t.Fatalf("unexpected update 2: %+v", updates[2])
		}
		_ = provider.Dispose()
	})

	t.Run("suppresses notifications already covered by an overflow snapshot", func(t *testing.T) {
		provider, _ := NewRemoteServiceProvider([]RemoteServiceProviderEntry{tsdEntry(counterSvc)})
		state := tservMustState(t, jsonx.ObjectOf("value", float64(0)))
		GetReplicatedStateInternals(state).Subscribe(func(_ OpList, seq int, _ Context) error {
			if seq == 101 {
				_ = state.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(102)))
				_ = state.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(103)))
			}
			return nil
		})
		_ = provider.Provide(counterSvc, jsonx.ObjectOf("state", state))
		var updates []ServiceProviderUpdate
		sub, _ := provider.Subscribe(counterSvc.Id, ServiceModeSingleton, func(u ServiceProviderUpdate, _ Context) error {
			updates = append(updates, u)
			return nil
		})
		for value := 1; value <= 101; value++ {
			_ = state.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(value)))
		}
		_ = sub.Activate()
		if len(updates) != 1 {
			t.Fatalf("expected 1 update after overflow activation, got %d", len(updates))
		}
		rstMem := updates[0].(ServiceProviderUpdateReset).Snapshot.Instances[0].Members[0].(ServiceMemberSnapshotState)
		if rstMem.Sequence != 103 || tservGet(rstMem.Ops[0].(OpReplace).Value, "value") != float64(103) {
			t.Fatalf("unexpected overflow snapshot: %+v", rstMem)
		}
		_ = state.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(104)))
		if updates[1].(ServiceProviderUpdateState).Sequence != 104 {
			t.Fatalf("expected seq 104")
		}
		_ = provider.Dispose()
	})

	t.Run("preserves lifecycle ordering across subscribers during reentrant publication", func(t *testing.T) {
		provider, _ := NewRemoteServiceProvider([]RemoteServiceProviderEntry{tsdEntry(counterSvc)})
		_ = provider.Provide(counterSvc, jsonx.ObjectOf("state", tservMustState(t, jsonx.ObjectOf("value", float64(0)))))
		var first, second []ServiceProviderUpdate
		s1, _ := provider.Subscribe(counterSvc.Id, ServiceModeSingleton, func(u ServiceProviderUpdate, _ Context) error {
			first = append(first, u)
			if len(first) == 1 {
				_ = provider.Replace(counterSvc, jsonx.ObjectOf("state", tservMustState(t, jsonx.ObjectOf("value", float64(2)))))
			}
			return nil
		})
		_ = s1.Activate()
		s2, _ := provider.Subscribe(counterSvc.Id, ServiceModeSingleton, func(u ServiceProviderUpdate, _ Context) error {
			second = append(second, u)
			return nil
		})
		_ = s2.Activate()
		_ = provider.Replace(counterSvc, jsonx.ObjectOf("state", tservMustState(t, jsonx.ObjectOf("value", float64(1)))))
		if len(first) != 2 || len(second) != 2 {
			t.Fatalf("expected 2 updates on both, got %d %d", len(first), len(second))
		}
		m1 := second[0].(ServiceProviderUpdateReplaced).Snapshot.Members[0].(ServiceMemberSnapshotState)
		m2 := second[1].(ServiceProviderUpdateReplaced).Snapshot.Members[0].(ServiceMemberSnapshotState)
		if tservGet(m1.Ops[0].(OpReplace).Value, "value") != float64(1) || tservGet(m2.Ops[0].(OpReplace).Value, "value") != float64(2) {
			t.Fatalf("out of order lifecycle replacements: %+v %+v", m1, m2)
		}
		_ = provider.Dispose()
	})

	t.Run("close during a callback prevents later buffered callbacks", func(t *testing.T) {
		provider, _ := NewRemoteServiceProvider([]RemoteServiceProviderEntry{tsdEntry(counterSvc)})
		state := tservMustState(t, jsonx.ObjectOf("value", float64(0)))
		_ = provider.Provide(counterSvc, jsonx.ObjectOf("state", state))
		var received []ServiceProviderUpdate
		var sub ServiceSubscription
		sub, _ = provider.Subscribe(counterSvc.Id, ServiceModeSingleton, func(u ServiceProviderUpdate, _ Context) error {
			received = append(received, u)
			return sub.Close(BackgroundContext)
		})
		_ = state.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(1)))
		_ = state.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(2)))
		_ = sub.Activate()
		if len(received) != 1 {
			t.Fatalf("expected 1 callback before close, got %d", len(received))
		}
		_ = provider.Dispose()
	})

	t.Run("drains terminal updates when disposed reentrantly", func(t *testing.T) {
		provider, _ := NewRemoteServiceProvider([]RemoteServiceProviderEntry{tsdEntry(counterSvc)})
		state := tservMustState(t, jsonx.ObjectOf("value", float64(0)))
		_ = provider.Provide(counterSvc, jsonx.ObjectOf("state", state))
		var types []string
		sub, _ := provider.Subscribe(counterSvc.Id, ServiceModeSingleton, func(u ServiceProviderUpdate, _ Context) error {
			switch u := u.(type) {
			case ServiceProviderUpdateState:
				types = append(types, u.Type)
				return provider.Dispose()
			case ServiceProviderUpdateUnavailable:
				types = append(types, u.Type)
			}
			return nil
		})
		_ = state.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(1)))
		_ = sub.Activate()
		if !reflect.DeepEqual(types, []string{"state", "unavailable"}) {
			t.Fatalf("expected [state unavailable], got %v", types)
		}
	})

	t.Run("rebaselines every member through wire codecs, then resumes contiguous deltas", func(t *testing.T) {
		provider, _ := NewRemoteServiceProvider([]RemoteServiceProviderEntry{tsdEntry(pairSvc)})
		left := tservMustState(t, jsonx.ObjectOf("value", float64(0)))
		right := tservMustState(t, jsonx.ObjectOf("value", float64(0)))
		_ = provider.Provide(pairSvc, jsonx.ObjectOf("left", left, "right", right))
		var errs []error
		var updates []ServiceProviderUpdate
		wt := &tsdWireTransport{
			provider: provider,
			beforeActivate: func() {
				for value := 1; value <= 101; value++ {
					v := float64(value)
					_ = left.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
						draft.Value.Set("value", v)
						return nil
					})
					_ = right.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
						draft.Value.Set("value", v)
						return nil
					})
				}
			},
			updates: &updates,
		}
		namespace, _ := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: pairSvc.Id}},
			Transport: wt,
			OnError:   func(e error) { errs = append(errs, e) },
		})
		pair := namespace.Use(pairSvc)
		if err := namespace.Ready(BackgroundContext); err != nil {
			t.Fatalf("Ready failed: %v", err)
		}
		wt.updatesMu.Lock()
		var utypes []string
		for _, u := range updates {
			switch x := u.(type) {
			case ServiceProviderUpdateReset:
				utypes = append(utypes, x.Type)
			case ServiceProviderUpdateState:
				utypes = append(utypes, x.Type)
			}
		}
		wt.updatesMu.Unlock()
		if !reflect.DeepEqual(utypes, []string{"reset", "state"}) {
			t.Fatalf("expected [reset state], got %v", utypes)
		}
		lv, _, _ := tservStateVal(t, tservMemberGet(t, pair, "left"))
		rv, _, _ := tservStateVal(t, tservMemberGet(t, pair, "right"))
		if tservGet(lv, "value") != float64(101) || tservGet(rv, "value") != float64(101) {
			t.Fatalf("unexpected values 101: %v %v", lv, rv)
		}
		for value := 102; value <= 104; value++ {
			v := float64(value)
			_ = left.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
				draft.Value.Set("value", v)
				return nil
			})
			_ = right.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
				draft.Value.Set("value", v)
				return nil
			})
		}
		lv, _, _ = tservStateVal(t, tservMemberGet(t, pair, "left"))
		rv, _, _ = tservStateVal(t, tservMemberGet(t, pair, "right"))
		if tservGet(lv, "value") != float64(104) || tservGet(rv, "value") != float64(104) {
			t.Fatalf("unexpected values 104: %v %v", lv, rv)
		}
		if len(errs) != 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		_ = namespace.Dispose(BackgroundContext)
		_ = provider.Dispose()
	})

	t.Run("an overflow reset can make a singleton unavailable before a later replacement", func(t *testing.T) {
		provider, _ := NewRemoteServiceProvider([]RemoteServiceProviderEntry{tsdEntry(counterSvc)})
		_ = provider.Provide(counterSvc, jsonx.ObjectOf("state", tservMustState(t, jsonx.ObjectOf("value", float64(0)))))
		var updates []ServiceProviderUpdate
		var errs []error
		wt := &tsdWireTransport{
			provider: provider,
			beforeActivate: func() {
				for value := 1; value <= 100; value++ {
					_ = provider.Replace(counterSvc, jsonx.ObjectOf("state", tservMustState(t, jsonx.ObjectOf("value", float64(value)))))
				}
				_ = provider.Withdraw(counterSvc)
			},
			updates: &updates,
		}
		namespace, _ := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: counterSvc.Id}},
			Transport: wt,
			OnError:   func(e error) { errs = append(errs, e) },
		})
		counter := namespace.Use(counterSvc)
		_ = namespace.Ready(BackgroundContext)
		wt.updatesMu.Lock()
		if len(updates) != 1 || len(updates[0].(ServiceProviderUpdateReset).Snapshot.Instances) != 0 {
			t.Fatalf("expected 1 empty reset update, got %+v", updates)
		}
		wt.updatesMu.Unlock()
		if _, ok, _ := tservStateVal(t, tservMemberGet(t, counter, "state")); ok {
			t.Fatal("expected unavailable counter state")
		}
		replacement := tservMustState(t, jsonx.ObjectOf("value", float64(200)))
		_ = provider.Replace(counterSvc, jsonx.ObjectOf("state", replacement))
		_ = replacement.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
			draft.Value.Set("value", float64(201))
			return nil
		})
		if namespace.Use(counterSvc) != counter {
			t.Fatal("expected stable handle")
		}
		v, ok, _ := tservStateVal(t, tservMemberGet(t, counter, "state"))
		if !ok || tservGet(v, "value") != float64(201) {
			t.Fatalf("expected 201, got %v", v)
		}
		if len(errs) != 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		_ = namespace.Dispose(BackgroundContext)
		_ = provider.Dispose()
	})

	t.Run("keyed resets retain live generations and reconcile closed and reused keys", func(t *testing.T) {
		provider, _ := NewRemoteServiceProvider([]RemoteServiceProviderEntry{tsdEntry(counterSvc, ServiceModeKeyed)})
		retained := tservMustState(t, jsonx.ObjectOf("value", float64(0)))
		_, _ = provider.Spawn(counterSvc, "retained", jsonx.ObjectOf("state", retained))
		closeOld, _ := provider.Spawn(counterSvc, "reused", jsonx.ObjectOf("state", tservMustState(t, jsonx.ObjectOf("value", float64(0)))))
		removed, _ := provider.Spawn(counterSvc, "removed", jsonx.ObjectOf("state", tservMustState(t, jsonx.ObjectOf("value", float64(0)))))

		var updates []ServiceProviderUpdate
		var errs []error
		wt := &tsdWireTransport{
			provider: provider,
			updates:  &updates,
		}
		namespace, _ := CreateRemoteServiceBinding(&RemoteServiceBindingOptions{
			Services:  []ServiceRef{{Id: counterSvc.Id}},
			Transport: wt,
			OnError:   func(e error) { errs = append(errs, e) },
		})
		type obs struct {
			svc any
			ctx Context
		}
		var observed []obs
		namespace.Observe(counterSvc, func(service any, call Context) error {
			observed = append(observed, obs{svc: service, ctx: call})
			return nil
		})
		_ = namespace.Ready(BackgroundContext)
		if len(observed) != 3 {
			t.Fatalf("expected 3 observed instances, got %d", len(observed))
		}
		stable := observed[1]
		old := observed[2]
		replacement := tservMustState(t, jsonx.ObjectOf("value", float64(500)))
		_, _ = tservStateSubscribe(t, tservMemberGet(t, stable.svc, "state"), func(v JsonValue, _ Context, _ ReplicatedStateDelivery) error {
			if tservGet(v, "value") != float64(1) {
				return nil
			}
			_ = closeOld()
			_ = removed()
			_, _ = provider.Spawn(counterSvc, "reused", jsonx.ObjectOf("state", replacement))
			_ = retained.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(102)))
			for next := 501; next <= 601; next++ {
				_ = replacement.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(next)))
			}
			return nil
		})
		_ = retained.Replace(BackgroundContext, jsonx.ObjectOf("value", float64(1)))

		wt.updatesMu.Lock()
		hasReset := false
		for _, u := range updates {
			if _, ok := u.(ServiceProviderUpdateReset); ok {
				hasReset = true
			}
		}
		wt.updatesMu.Unlock()
		if !hasReset || len(observed) != 4 {
			t.Fatalf("expected reset and 4 observed, hasReset=%v len=%d", hasReset, len(observed))
		}
		if stable.ctx.AbortSignal().Err() != nil {
			t.Fatal("expected stable instance context still active")
		}
		sv, _, _ := tservStateVal(t, tservMemberGet(t, stable.svc, "state"))
		if tservGet(sv, "value") != float64(102) {
			t.Fatalf("expected stable value 102, got %v", sv)
		}
		if old.ctx.AbortSignal().Err() == nil || observed[0].ctx.AbortSignal().Err() == nil {
			t.Fatal("expected closed and removed contexts aborted")
		}
		rv3, _, _ := tservStateVal(t, tservMemberGet(t, observed[3].svc, "state"))
		if tservGet(rv3, "value") != float64(601) {
			t.Fatalf("expected new reused value 601, got %v", rv3)
		}
		_ = replacement.Change(BackgroundContext, func(draft Draft[*jsonx.Object]) error {
			draft.Value.Set("value", float64(602))
			return nil
		})
		rv3, _, _ = tservStateVal(t, tservMemberGet(t, observed[3].svc, "state"))
		if tservGet(rv3, "value") != float64(602) {
			t.Fatalf("expected new reused value 602, got %v", rv3)
		}
		if len(errs) != 0 {
			t.Fatalf("unexpected errors: %v", errs)
		}
		_ = namespace.Dispose(BackgroundContext)
		_ = provider.Dispose()
	})
}

var _ = errors.New
var _ = strings.Contains
