package chord

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

type tsdeKind string

const (
	tsdeKindMutable  tsdeKind = "mutable"
	tsdeKindAttached tsdeKind = "attached"
	tsdeKindReplica  tsdeKind = "replica"
)

type tsdeFixture struct {
	state   ReplicatedState[any]
	publish func(value int, ctx Context) error
}

func tsdeObjVal(n int) *jsonx.Object {
	return jsonx.ObjectOf("value", float64(n))
}

func tsdeNumVal(v any) int {
	n, _ := v.(*jsonx.Object).Get("value")
	return int(n.(float64))
}

func tsdeNewFixture(t *testing.T, kind tsdeKind, onError func(error)) tsdeFixture {
	t.Helper()
	if onError == nil {
		onError = func(error) {}
	}
	switch kind {
	case tsdeKindMutable:
		m, err := NewReplicatedState[any](tsdeObjVal(0))
		if err != nil {
			t.Fatalf("NewReplicatedState failed: %v", err)
		}
		return tsdeFixture{
			state: m,
			publish: func(value int, ctx Context) error {
				return m.Replace(ctx, tsdeObjVal(value))
			},
		}
	case tsdeKindAttached:
		var receive func(ReplicatedStateSourceFrame[any])
		src := &tsdeSimpleSource{
			onActivate: func(listener func(ReplicatedStateSourceFrame[any])) {
				receive = listener
			},
		}
		s, err := NewReplicatedStateSource[any](src, &ReplicatedStateSourceOptions{OnError: onError})
		if err != nil {
			t.Fatalf("NewReplicatedStateSource failed: %v", err)
		}
		return tsdeFixture{
			state: s,
			publish: func(value int, ctx Context) error {
				receive(ReplicatedStateSourceFrame[any]{
					Value:   tsdeObjVal(value),
					Cursor:  value,
					Ops:     OpList{OpSet{Path: Path{SegString("value")}, Value: float64(value)}},
					Context: ctx,
				})
				return nil
			},
		}
	case tsdeKindReplica:
		r := NewReplicatedStateReplica[any](onError)
		if err := r.Hydrate(0, OpList{OpReplace{Value: tsdeObjVal(0)}}, BackgroundContext); err != nil {
			t.Fatalf("Hydrate failed: %v", err)
		}
		return tsdeFixture{
			state: r,
			publish: func(value int, ctx Context) error {
				return r.Update(value, OpList{OpSet{Path: Path{SegString("value")}, Value: float64(value)}}, ctx)
			},
		}
	default:
		t.Fatalf("unknown fixture kind: %s", kind)
		return tsdeFixture{}
	}
}

type tsdeSimpleSource struct {
	onActivate func(func(ReplicatedStateSourceFrame[any]))
}

func (s *tsdeSimpleSource) Attach() ReplicatedStateSourceAttachment[any] {
	return &tsdeSimpleAttachment{onActivate: s.onActivate}
}

type tsdeSimpleAttachment struct {
	onActivate func(func(ReplicatedStateSourceFrame[any]))
}

func (a *tsdeSimpleAttachment) Snapshot() ReplicatedStateSourceSnapshot[any] {
	return ReplicatedStateSourceSnapshot[any]{Value: tsdeObjVal(0), Cursor: 0}
}

func (a *tsdeSimpleAttachment) Activate(listener func(ReplicatedStateSourceFrame[any])) {
	a.onActivate(listener)
}

func (a *tsdeSimpleAttachment) Dispose() {}

func TestStateDeliveryAcrossKinds(t *testing.T) {
	kinds := []tsdeKind{tsdeKindMutable, tsdeKindAttached, tsdeKindReplica}

	for _, kind := range kinds {
		kind := kind
		t.Run(string(kind)+"/awaits_hydration_and_updates_independently", func(t *testing.T) {
			f := tsdeNewFixture(t, kind, nil)
			hydrationGate := make(chan struct{})
			updateGate := make(chan struct{})
			started0 := make(chan struct{})
			started1 := make(chan struct{})
			doneSub := make(chan struct{})

			var mu sync.Mutex
			var events []string
			var exact []int
			var fast []int

			if internals := GetReplicatedStateInternals(f.state); internals != nil {
				internals.Subscribe(func(_ OpList, seq int, _ Context) error {
					mu.Lock()
					exact = append(exact, seq)
					mu.Unlock()
					return nil
				})
			}

			unsubSlowCh := make(chan func(), 1)
			go func() {
				unsubSlowCh <- f.state.Subscribe(func(value any, _ Context, _ ReplicatedStateDelivery) error {
					n := tsdeNumVal(value)
					mu.Lock()
					events = append(events, fmt.Sprintf("start:%d", n))
					mu.Unlock()
					if n == 0 {
						close(started0)
						<-hydrationGate
					} else if n == 1 {
						close(started1)
						<-updateGate
					}
					mu.Lock()
					events = append(events, fmt.Sprintf("end:%d", n))
					mu.Unlock()
					if n == 2 {
						close(doneSub)
					}
					return nil
				})
			}()

			<-started0
			f.state.Subscribe(func(value any, _ Context, _ ReplicatedStateDelivery) error {
				mu.Lock()
				fast = append(fast, tsdeNumVal(value))
				mu.Unlock()
				return nil
			})
			_ = f.publish(1, BackgroundContext)
			_ = f.publish(2, BackgroundContext)

			mu.Lock()
			if !slices.Equal(events, []string{"start:0"}) {
				t.Fatalf("expected events [start:0], got %v", events)
			}
			if !slices.Equal(fast, []int{0, 1, 2}) {
				t.Fatalf("expected fast [0 1 2], got %v", fast)
			}
			if kind != tsdeKindReplica && !slices.Equal(exact, []int{1, 2}) {
				t.Fatalf("expected exact [1 2], got %v", exact)
			}
			mu.Unlock()

			cur, _ := f.state.Value()
			if tsdeNumVal(cur) != 2 {
				t.Fatalf("expected state value 2, got %d", tsdeNumVal(cur))
			}

			close(hydrationGate)
			<-started1
			mu.Lock()
			if !slices.Equal(events, []string{"start:0", "end:0", "start:1"}) {
				t.Fatalf("unexpected events after hydration gate: %v", events)
			}
			mu.Unlock()

			close(updateGate)
			<-doneSub
			mu.Lock()
			wantAll := []string{"start:0", "end:0", "start:1", "end:1", "start:2", "end:2"}
			if !slices.Equal(events, wantAll) {
				t.Fatalf("expected %v, got %v", wantAll, events)
			}
			mu.Unlock()
			unsubSlow := <-unsubSlowCh
			unsubSlow()
		})

		for _, count := range []int{100, 101, 102, 201, 202} {
			count := count
			t.Run(fmt.Sprintf("%s/bounds_%d_pending_deliveries", kind, count), func(t *testing.T) {
				f := tsdeNewFixture(t, kind, nil)
				hydrationGate := make(chan struct{})
				started0 := make(chan struct{})
				done := make(chan struct{})

				var mu sync.Mutex
				var received []int
				var exact []int

				if internals := GetReplicatedStateInternals(f.state); internals != nil {
					internals.Subscribe(func(_ OpList, seq int, _ Context) error {
						mu.Lock()
						exact = append(exact, seq)
						mu.Unlock()
						return nil
					})
				}

				go func() {
					f.state.Subscribe(func(value any, _ Context, _ ReplicatedStateDelivery) error {
						n := tsdeNumVal(value)
						mu.Lock()
						received = append(received, n)
						mu.Unlock()
						if n == 0 {
							close(started0)
							<-hydrationGate
						}
						if n == count {
							close(done)
						}
						return nil
					})
				}()

				<-started0
				for v := 1; v <= count; v++ {
					_ = f.publish(v, BackgroundContext)
				}
				mu.Lock()
				if !slices.Equal(received, []int{0}) {
					t.Fatalf("expected [0] before hydration resolves, got %v", received)
				}
				if kind != tsdeKindReplica {
					if len(exact) != count || exact[0] != 1 || exact[count-1] != count {
						t.Fatalf("expected exact 1..%d, got len=%d", count, len(exact))
					}
				}
				mu.Unlock()

				close(hydrationGate)
				<-done

				first := ((count-1)/100)*100 + 1
				want := []int{0}
				for v := first; v <= count; v++ {
					want = append(want, v)
				}
				mu.Lock()
				if !slices.Equal(received, want) {
					t.Fatalf("expected received %v, got %v", want, received)
				}
				mu.Unlock()
			})
		}

		t.Run(string(kind)+"/excludes_running_update_from_overflow_and_retains_metadata", func(t *testing.T) {
			f := tsdeNewFixture(t, kind, nil)
			gate := make(chan struct{})
			started1 := make(chan struct{})
			done103 := make(chan struct{})
			ctx := WithCancel(BackgroundContext).Context

			type rec struct {
				value    any
				ctx      Context
				delivery ReplicatedStateDelivery
			}
			var mu sync.Mutex
			var received []rec

			readySub := make(chan struct{})
			go func() {
				f.state.Subscribe(func(value any, c Context, d ReplicatedStateDelivery) error {
					mu.Lock()
					received = append(received, rec{value: value, ctx: c, delivery: d})
					mu.Unlock()
					n := tsdeNumVal(value)
					if n == 0 {
						close(readySub)
					} else if n == 1 {
						close(started1)
						<-gate
					} else if n == 103 {
						close(done103)
					}
					return nil
				})
			}()
			<-readySub

			go func() {
				_ = f.publish(1, BackgroundContext)
			}()
			<-started1

			for v := 2; v < 102; v++ {
				_ = f.publish(v, BackgroundContext)
			}
			_ = f.publish(102, ctx)
			adopted, _ := f.state.Value()
			_ = f.publish(103, BackgroundContext)

			mu.Lock()
			var gotVals []int
			for _, r := range received {
				gotVals = append(gotVals, tsdeNumVal(r.value))
			}
			if !slices.Equal(gotVals, []int{0, 1}) {
				t.Fatalf("expected [0 1] while gate blocks, got %v", gotVals)
			}
			mu.Unlock()

			close(gate)
			<-done103

			mu.Lock()
			defer mu.Unlock()
			gotVals = nil
			for _, r := range received {
				gotVals = append(gotVals, tsdeNumVal(r.value))
			}
			if !slices.Equal(gotVals, []int{0, 1, 102, 103}) {
				t.Fatalf("expected [0 1 102 103], got %v", gotVals)
			}
			if received[2].value != adopted {
				t.Fatal("expected exact adopted value reference at index 2")
			}
			if received[2].ctx != ctx {
				t.Fatal("expected exact context at index 2")
			}
			if received[2].delivery != (ReplicatedStateDelivery{Kind: ReplicatedStateDeliveryKindUpdate, Sequence: 102}) {
				t.Fatalf("expected update seq 102, got %v", received[2].delivery)
			}
		})

		t.Run(string(kind)+"/serializes_reentrant_callbacks", func(t *testing.T) {
			f := tsdeNewFixture(t, kind, nil)
			var events []string
			f.state.Subscribe(func(value any, _ Context, _ ReplicatedStateDelivery) error {
				n := tsdeNumVal(value)
				events = append(events, fmt.Sprintf("start:%d", n))
				if n < 2 {
					_ = f.publish(n+1, BackgroundContext)
				}
				events = append(events, fmt.Sprintf("end:%d", n))
				return nil
			})
			want := []string{"start:0", "end:0", "start:1", "end:1", "start:2", "end:2"}
			if !slices.Equal(events, want) {
				t.Fatalf("expected %v, got %v", want, events)
			}
		})

		t.Run(string(kind)+"/treats_two_subscriptions_of_same_callback_independently", func(t *testing.T) {
			f := tsdeNewFixture(t, kind, nil)
			gate := make(chan struct{})
			startedFirst := make(chan struct{})
			doneFirst := make(chan struct{})
			doneSecond2 := make(chan struct{})
			var mu sync.Mutex
			var received []int
			firstOne := true

			listener := func(value any, _ Context, _ ReplicatedStateDelivery) error {
				n := tsdeNumVal(value)
				mu.Lock()
				received = append(received, n)
				block := firstOne && n == 1
				if block {
					firstOne = false
				}
				mu.Unlock()
				if block {
					close(startedFirst)
					<-gate
					close(doneFirst)
				} else if n == 2 {
					close(doneSecond2)
				}
				return nil
			}

			stopFirst := f.state.Subscribe(listener)
			stopSecond := f.state.Subscribe(listener)

			go func() {
				_ = f.publish(1, BackgroundContext)
			}()
			<-startedFirst
			stopFirst()
			stopFirst()
			_ = f.publish(2, BackgroundContext)
			close(gate)
			<-doneFirst
			<-doneSecond2

			mu.Lock()
			if !slices.Equal(received, []int{0, 0, 1, 1, 2}) {
				t.Fatalf("expected [0 0 1 1 2], got %v", received)
			}
			mu.Unlock()
			stopSecond()
		})

		t.Run(string(kind)+"/unsubscribe_drops_queued_callbacks_without_aborting", func(t *testing.T) {
			f := tsdeNewFixture(t, kind, nil)
			gate := make(chan struct{})
			started1 := make(chan struct{})
			done1 := make(chan struct{})
			ctx := WithCancel(BackgroundContext).Context
			var mu sync.Mutex
			var received []int
			completed := false

			stop := f.state.Subscribe(func(value any, _ Context, _ ReplicatedStateDelivery) error {
				n := tsdeNumVal(value)
				mu.Lock()
				received = append(received, n)
				mu.Unlock()
				if n == 1 {
					close(started1)
					<-gate
					mu.Lock()
					completed = true
					mu.Unlock()
					close(done1)
				}
				return nil
			})

			go func() {
				_ = f.publish(1, ctx)
			}()
			<-started1
			_ = f.publish(2, ctx)
			stop()
			_ = f.publish(3, ctx)

			if ctx.AbortSignal().Err() != nil {
				t.Fatal("expected context abort signal not aborted")
			}
			mu.Lock()
			if completed {
				t.Fatal("expected running callback not completed yet")
			}
			mu.Unlock()

			close(gate)
			<-done1
			mu.Lock()
			if !completed || !slices.Equal(received, []int{0, 1}) {
				t.Fatalf("expected completed=true and received=[0 1], got completed=%v received=%v", completed, received)
			}
			mu.Unlock()
		})
	}
}

func TestStateDeliveryListenerErrorsForAttachedAndReplica(t *testing.T) {
	for _, kind := range []tsdeKind{tsdeKindAttached, tsdeKindReplica} {
		kind := kind
		t.Run(string(kind)+"/isolates_hydration_failure_and_continues", func(t *testing.T) {
			var errs []error
			f := tsdeNewFixture(t, kind, func(err error) { errs = append(errs, err) })
			var received []int
			failure := errors.New("sync hydration")
			f.state.Subscribe(func(value any, _ Context, _ ReplicatedStateDelivery) error {
				n := tsdeNumVal(value)
				received = append(received, n)
				if n == 0 {
					return failure
				}
				return nil
			})
			_ = f.publish(1, BackgroundContext)
			if len(errs) != 1 || errs[0] != failure {
				t.Fatalf("expected [failure], got %v", errs)
			}
			if !slices.Equal(received, []int{0, 1}) {
				t.Fatalf("expected [0 1], got %v", received)
			}
		})

		t.Run(string(kind)+"/observes_multiple_failures_while_continuing_delivery", func(t *testing.T) {
			var mu sync.Mutex
			var errs []error
			f := tsdeNewFixture(t, kind, func(err error) {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			})
			gate := make(chan struct{})
			started0 := make(chan struct{})
			done3 := make(chan struct{})
			var received []int
			var fast []int

			go func() {
				f.state.Subscribe(func(value any, _ Context, _ ReplicatedStateDelivery) error {
					n := tsdeNumVal(value)
					mu.Lock()
					received = append(received, n)
					mu.Unlock()
					switch n {
					case 0:
						close(started0)
						<-gate
						return errors.New("async hydration")
					case 1:
						return errors.New("sync update")
					case 2:
						return errors.New("async update")
					case 3:
						close(done3)
					}
					return nil
				})
			}()

			<-started0
			f.state.Subscribe(func(value any, _ Context, _ ReplicatedStateDelivery) error {
				mu.Lock()
				fast = append(fast, tsdeNumVal(value))
				mu.Unlock()
				return nil
			})
			for v := 1; v <= 3; v++ {
				_ = f.publish(v, BackgroundContext)
			}
			close(gate)
			<-done3

			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(received, []int{0, 1, 2, 3}) {
				t.Fatalf("expected received [0 1 2 3], got %v", received)
			}
			if !slices.Equal(fast, []int{0, 1, 2, 3}) {
				t.Fatalf("expected fast [0 1 2 3], got %v", fast)
			}
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, e.Error())
			}
			want := []string{"async hydration", "sync update", "async update"}
			if !slices.Equal(msgs, want) {
				t.Fatalf("expected errors %v, got %v", want, msgs)
			}
		})

		t.Run(string(kind)+"/observes_error_after_unsubscribe", func(t *testing.T) {
			var mu sync.Mutex
			var errs []error
			f := tsdeNewFixture(t, kind, func(err error) {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			})
			gate := make(chan struct{})
			started1 := make(chan struct{})
			done1 := make(chan struct{})
			var received []int
			failure := errors.New("stopped callback")

			stop := f.state.Subscribe(func(value any, _ Context, _ ReplicatedStateDelivery) error {
				n := tsdeNumVal(value)
				mu.Lock()
				received = append(received, n)
				mu.Unlock()
				if n == 1 {
					close(started1)
					<-gate
					defer close(done1)
					return failure
				}
				return nil
			})

			go func() {
				_ = f.publish(1, BackgroundContext)
			}()
			<-started1
			stop()
			_ = f.publish(2, BackgroundContext)
			close(gate)
			<-done1

			mu.Lock()
			defer mu.Unlock()
			if len(errs) != 1 || errs[0] != failure {
				t.Fatalf("expected [failure] after unsubscribe, got %v", errs)
			}
			if !slices.Equal(received, []int{0, 1}) {
				t.Fatalf("expected [0 1], got %v", received)
			}
		})
	}
}

func TestStateDeliveryMutableReportsAsyncDefaultHook(t *testing.T) {
	var hookMu sync.Mutex
	var reports []error
	prevHook := ssAsyncErrorHook
	ssAsyncErrorHook = func(err error) {
		hookMu.Lock()
		reports = append(reports, err)
		hookMu.Unlock()
	}
	defer func() { ssAsyncErrorHook = prevHook }()

	state, err := NewReplicatedState[any](tsdeObjVal(0))
	if err != nil {
		t.Fatalf("NewReplicatedState failed: %v", err)
	}
	failure := errors.New("async mutable hydration")
	var received []int
	state.Subscribe(func(value any, _ Context, _ ReplicatedStateDelivery) error {
		n := tsdeNumVal(value)
		received = append(received, n)
		if n == 0 {
			return failure
		}
		return nil
	})
	if err := state.Replace(BackgroundContext, tsdeObjVal(1)); err != nil {
		t.Fatalf("Replace failed: %v", err)
	}
	if !slices.Equal(received, []int{0, 1}) {
		t.Fatalf("expected received [0 1], got %v", received)
	}
	hookMu.Lock()
	defer hookMu.Unlock()
	if len(reports) != 1 || reports[0] != failure {
		t.Fatalf("expected [failure] in ssAsyncErrorHook, got %v", reports)
	}
}

func TestReplicaDisconnectDropsPendingAndWaitsForRunningCallbackBeforeRehydration(t *testing.T) {
	replica := NewReplicatedStateReplica[any](func(error) {})
	gate := make(chan struct{})
	started0 := make(chan struct{})
	done51 := make(chan struct{})

	var mu sync.Mutex
	var received []ReplicatedStateDelivery

	if err := replica.Hydrate(0, OpList{OpReplace{Value: tsdeObjVal(0)}}, BackgroundContext); err != nil {
		t.Fatalf("Hydrate 0 failed: %v", err)
	}
	go func() {
		replica.Subscribe(func(_ any, _ Context, d ReplicatedStateDelivery) error {
			mu.Lock()
			received = append(received, d)
			mu.Unlock()
			if d.Sequence == 0 {
				close(started0)
				<-gate
			} else if d.Sequence == 51 {
				close(done51)
			}
			return nil
		})
	}()

	<-started0
	_ = replica.Update(1, OpList{OpSet{Path: Path{SegString("value")}, Value: float64(1)}}, BackgroundContext)
	replica.Clear()
	_ = replica.Hydrate(50, OpList{OpReplace{Value: tsdeObjVal(50)}}, BackgroundContext)
	_ = replica.Update(51, OpList{OpSet{Path: Path{SegString("value")}, Value: float64(51)}}, BackgroundContext)

	mu.Lock()
	if !slices.Equal(received, []ReplicatedStateDelivery{{Kind: ReplicatedStateDeliveryKindHydrate, Sequence: 0}}) {
		t.Fatalf("expected only seq 0 before gate closes, got %v", received)
	}
	mu.Unlock()

	close(gate)
	<-done51

	mu.Lock()
	want := []ReplicatedStateDelivery{
		{Kind: ReplicatedStateDeliveryKindHydrate, Sequence: 0},
		{Kind: ReplicatedStateDeliveryKindHydrate, Sequence: 50},
		{Kind: ReplicatedStateDeliveryKindUpdate, Sequence: 51},
	}
	if !slices.Equal(received, want) {
		t.Fatalf("expected %v, got %v", want, received)
	}
	mu.Unlock()
}

func TestColdReplicaHydratesAllListenersBeforeReentrantUpdates(t *testing.T) {
	replica := NewReplicatedStateReplica[any](func(error) {})
	var second []ReplicatedStateDelivery
	replica.Subscribe(func(_ any, _ Context, d ReplicatedStateDelivery) error {
		if d.Kind == ReplicatedStateDeliveryKindHydrate {
			_ = replica.Update(1, OpList{OpSet{Path: Path{SegString("value")}, Value: float64(1)}}, BackgroundContext)
		}
		return nil
	})
	replica.Subscribe(func(_ any, _ Context, d ReplicatedStateDelivery) error {
		second = append(second, d)
		return nil
	})
	if err := replica.Hydrate(0, OpList{OpReplace{Value: tsdeObjVal(0)}}, BackgroundContext); err != nil {
		t.Fatalf("Hydrate failed: %v", err)
	}
	want := []ReplicatedStateDelivery{
		{Kind: ReplicatedStateDeliveryKindHydrate, Sequence: 0},
		{Kind: ReplicatedStateDeliveryKindUpdate, Sequence: 1},
	}
	if !slices.Equal(second, want) {
		t.Fatalf("expected %v, got %v", want, second)
	}
}
