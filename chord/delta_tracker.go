// Ported from packages/chord/src/delta/tracker.ts (pi v1.0.0).

package chord

import (
	"errors"
	"math"
	"reflect"
	"sync"
	"unsafe"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// Prepared is a settled overlay batch. Abort marks it aborted; Adopt consumes it.
// TS readonly data is methods because the value has identity and Abort.
type Prepared[T any] interface {
	Base() T
	Value() T
	Ops() []Op
	BaseRevision() int
	Abort()
}

// Change is one open overlay mutation. State fails after Prepare or Abort.
type Change[T any] interface {
	State() (Draft[T], error)
	Prepare() (Prepared[T], error)
	Abort()
}

// Tracker owns an alias-free strict-JSON root and the revisions cut from it.
type Tracker[T any] interface {
	Value() T
	Revision() int
	BeginChange() Change[T]
	PrepareReplace(value T) (Prepared[T], error)
	Adopt(prepared Prepared[T]) error
}

// Track takes immutable ownership of an alias-free strict-JSON root in O(1).
func Track[T any](initial T) Tracker[T] {
	return dtNewTrackerImpl(initial)
}

// dtStatus is the overlay lifecycle. TS: "open" | "prepared" | "consumed" | "aborted" | "stale".
type dtStatus string

const (
	dtStatusOpen     dtStatus = "open"
	dtStatusPrepared dtStatus = "prepared"
	dtStatusConsumed dtStatus = "consumed"
	dtStatusAborted  dtStatus = "aborted"
	dtStatusStale    dtStatus = "stale"
)

// dtStatusCell is shared by a context and a prepared change so Abort can flip it.
type dtStatusCell struct {
	Value dtStatus `json:"value"`
}

type dtTrackerOps interface {
	dtAbortContext(context *dtOverlayContext)
	dtContextSnapshot(context *dtOverlayContext) (base any, open bool)
	dtMarkPrepared(context *dtOverlayContext) error
	dtMarkAborted(context *dtOverlayContext)
	dtMarkOverlayReleased(context *dtOverlayContext)
}

// dtOverlayContext is the per-change overlay tracking transaction state.
type dtOverlayContext struct {
	Owner           any
	Tracker         dtTrackerOps
	BaseRevision    int
	Status          *dtStatusCell
	Ops             []Op
	Replacement     bool
	BaseValue       any
	OverlayReleased bool
}

// dtPreparedImpl is the TS class PreparedImpl.
type dtPreparedImpl[T any] struct {
	context *dtOverlayContext
	value   T
	base    T
	ops     []Op
}

func dtNewPreparedImpl[T any](context *dtOverlayContext, value T, operations []Op) *dtPreparedImpl[T] {
	ops := operations
	if ops == nil {
		ops = []Op{}
	}
	base, _ := dtCastValue[T](context.BaseValue)
	return &dtPreparedImpl[T]{
		context: context,
		value:   value,
		base:    base,
		ops:     ops,
	}
}

func (p *dtPreparedImpl[T]) Base() T { return p.base }

func (p *dtPreparedImpl[T]) Value() T { return p.value }

func (p *dtPreparedImpl[T]) Ops() []Op { return p.ops }

func (p *dtPreparedImpl[T]) BaseRevision() int { return p.context.BaseRevision }

func (p *dtPreparedImpl[T]) Abort() {
	if p.context == nil || p.context.Tracker == nil {
		return
	}
	p.context.Tracker.dtAbortContext(p.context)
}

// dtChangeImpl is the TS class ChangeImpl.
type dtChangeImpl[T any] struct {
	mu              sync.Mutex
	context         *dtOverlayContext
	preparedStatus  *dtStatusCell
	preparedTracker dtTrackerOps
	preparedContext *dtOverlayContext
	hasDraft        bool
	draftRoot       T
	settled         bool
}

func dtNewChangeImpl[T any](context *dtOverlayContext) *dtChangeImpl[T] {
	return &dtChangeImpl[T]{
		context: context,
	}
}

func (c *dtChangeImpl[T]) State() (Draft[T], error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var zero Draft[T]
	if c.settled || c.context == nil || c.context.Tracker == nil {
		return zero, js.NewTypeError("Cannot use a settled overlay")
	}
	baseAny, open := c.context.Tracker.dtContextSnapshot(c.context)
	if !open {
		c.clearDraftLocked()
		return zero, js.NewTypeError("Cannot use a settled overlay")
	}
	if !c.hasDraft {
		cloned, err := dtCloneStrictRoot[T](baseAny)
		if err != nil {
			return zero, err
		}
		c.draftRoot = cloned
		c.hasDraft = true
	}
	return Draft[T]{Value: c.draftRoot}, nil
}

func (c *dtChangeImpl[T]) clearDraftLocked() {
	var zero T
	c.draftRoot = zero
	c.hasDraft = false
}

func (c *dtChangeImpl[T]) Prepare() (Prepared[T], error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.settled {
		return nil, errors.New("Change has already been settled")
	}
	context := c.context
	if context == nil || context.Tracker == nil {
		c.settled = true
		c.clearDraftLocked()
		return nil, js.NewTypeError("Cannot use a settled overlay")
	}
	if err := context.Tracker.dtMarkPrepared(context); err != nil {
		c.clearDraftLocked()
		c.settled = true
		c.context = nil
		return nil, err
	}

	var operations []Op
	var nextValue T
	var err error
	if !c.hasDraft {
		operations = []Op{}
		nextValue, err = dtCastValue[T](context.BaseValue)
	} else {
		var cleanDraft any
		cleanDraft, err = dtSanitizeDraftValue(any(c.draftRoot))
		if err == nil {
			ops := DiffRevisions(context.BaseValue, cleanDraft)
			if ops == nil {
				ops = OpList{}
			}
			operations = ops
			if len(ops) == 0 {
				nextValue, err = dtCastValue[T](context.BaseValue)
			} else {
				var materialized any
				materialized, err = ApplyImmutableTrusted(context.BaseValue, ops)
				if err == nil {
					nextValue, err = dtCastValue[T](materialized)
				}
			}
		}
	}

	if err != nil {
		context.Tracker.dtMarkAborted(context)
		c.clearDraftLocked()
		c.settled = true
		c.context = nil
		return nil, err
	}

	prepared := dtNewPreparedImpl[T](context, nextValue, operations)
	c.preparedStatus = context.Status
	c.preparedTracker = context.Tracker
	c.preparedContext = context
	c.clearDraftLocked()
	context.Tracker.dtMarkOverlayReleased(context)
	c.settled = true
	c.context = nil
	return prepared, nil
}

func (c *dtChangeImpl[T]) Abort() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.settled {
		if c.preparedTracker != nil && c.preparedContext != nil {
			c.preparedTracker.dtAbortContext(c.preparedContext)
		}
		c.preparedStatus = nil
		c.preparedTracker = nil
		c.preparedContext = nil
		return
	}
	c.settled = true
	c.clearDraftLocked()
	if c.context != nil && c.context.Tracker != nil {
		c.context.Tracker.dtAbortContext(c.context)
	}
	c.context = nil
}

// dtOwnerToken provides non-zero-sized unique pointer identity for a Tracker instance.
type dtOwnerToken struct{ _ byte }

// dtTrackerImpl is the TS class TrackerImpl.
type dtTrackerImpl[T any] struct {
	mu          sync.Mutex
	owner       *dtOwnerToken
	contexts    *omap.Set[*dtOverlayContext]
	value       T
	revision    int
	pruneBudget int
}

func dtNewTrackerImpl[T any](initial T) *dtTrackerImpl[T] {
	return &dtTrackerImpl[T]{
		owner:       &dtOwnerToken{},
		contexts:    omap.NewSet[*dtOverlayContext](),
		value:       initial,
		revision:    0,
		pruneBudget: 256,
	}
}

func (t *dtTrackerImpl[T]) Value() T {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.value
}

func (t *dtTrackerImpl[T]) Revision() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.revision
}

func (t *dtTrackerImpl[T]) BeginChange() Change[T] {
	t.mu.Lock()
	defer t.mu.Unlock()

	context := &dtOverlayContext{
		Owner:        t.owner,
		Tracker:      t,
		BaseRevision: t.revision,
		Status:       &dtStatusCell{Value: dtStatusOpen},
		BaseValue:    t.value,
	}
	t.registerLocked(context)
	return dtNewChangeImpl[T](context)
}

func (t *dtTrackerImpl[T]) PrepareReplace(value T) (Prepared[T], error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	base := t.value
	var operations []Op
	candidate := value
	if dtEqualTrustedJson(any(base), any(value)) {
		operations = []Op{}
		candidate = base
	} else {
		operations = []Op{OpReplace{Value: any(value)}}
	}
	context := &dtOverlayContext{
		Owner:           t.owner,
		Tracker:         t,
		BaseRevision:    t.revision,
		Status:          &dtStatusCell{Value: dtStatusPrepared},
		Ops:             operations,
		Replacement:     true,
		BaseValue:       base,
		OverlayReleased: true,
	}
	t.registerLocked(context)
	return dtNewPreparedImpl[T](context, candidate, operations), nil
}

func (t *dtTrackerImpl[T]) Adopt(prepared Prepared[T]) error {
	if prepared == nil {
		return errors.New("Prepared change belongs to a different tracker")
	}
	p, ok := prepared.(*dtPreparedImpl[T])
	if !ok || p == nil || p.context == nil {
		return errors.New("Prepared change belongs to a different tracker")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	context := p.context
	if context.Owner != t.owner {
		return errors.New("Prepared change belongs to a different tracker")
	}
	switch context.Status.Value {
	case dtStatusConsumed:
		return errors.New("Prepared change has already been used")
	case dtStatusAborted:
		return errors.New("Prepared change has been aborted")
	case dtStatusStale:
		return errors.New("Prepared change is stale")
	case dtStatusPrepared:
	default:
		return errors.New("Prepared change is not ready")
	}

	if context.BaseRevision != t.revision || !dtSameIdentity(any(t.value), any(p.base)) {
		context.Status.Value = dtStatusStale
		context.OverlayReleased = true
		t.contexts.Delete(context)
		return errors.New("Prepared change is stale")
	}

	t.value = p.value
	context.Status.Value = dtStatusConsumed
	t.revision++
	t.invalidateLocked(context)
	return nil
}

func (t *dtTrackerImpl[T]) dtContextSnapshot(context *dtOverlayContext) (any, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if context.OverlayReleased || context.Status.Value != dtStatusOpen {
		return nil, false
	}
	return context.BaseValue, true
}

func (t *dtTrackerImpl[T]) dtMarkPrepared(context *dtOverlayContext) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if context.OverlayReleased ||
		context.Status.Value == dtStatusConsumed ||
		context.Status.Value == dtStatusAborted ||
		context.Status.Value == dtStatusStale {
		return js.NewTypeError("Cannot use a settled overlay")
	}
	if context.Status.Value != dtStatusOpen {
		return js.NewTypeError("Prepared overlays are read-only")
	}
	context.Status.Value = dtStatusPrepared
	return nil
}

func (t *dtTrackerImpl[T]) dtMarkAborted(context *dtOverlayContext) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if context.Status.Value == dtStatusAborted || context.Status.Value == dtStatusConsumed || context.Status.Value == dtStatusStale {
		return
	}
	context.Status.Value = dtStatusAborted
	context.OverlayReleased = true
	context.Ops = nil
	context.BaseValue = nil
	t.contexts.Delete(context)
}

func (t *dtTrackerImpl[T]) dtMarkOverlayReleased(context *dtOverlayContext) {
	t.mu.Lock()
	defer t.mu.Unlock()
	context.OverlayReleased = true
}

func (t *dtTrackerImpl[T]) dtAbortContext(context *dtOverlayContext) {
	t.dtMarkAborted(context)
}

func (t *dtTrackerImpl[T]) registerLocked(context *dtOverlayContext) {
	t.contexts.Add(context)
	t.pruneBudget--
	if t.pruneBudget <= 0 {
		t.pruneLocked()
		size := t.contexts.Len()
		if size < 256 {
			size = 256
		}
		t.pruneBudget = size
	}
}

func (t *dtTrackerImpl[T]) pruneLocked() {
	for _, ctx := range t.contexts.Values() {
		if ctx.Status.Value != dtStatusOpen && ctx.Status.Value != dtStatusPrepared {
			t.contexts.Delete(ctx)
		}
	}
}

func (t *dtTrackerImpl[T]) invalidateLocked(winner *dtOverlayContext) {
	for _, ctx := range t.contexts.Values() {
		if ctx == winner {
			continue
		}
		if ctx.Status.Value == dtStatusOpen || ctx.Status.Value == dtStatusPrepared {
			ctx.Status.Value = dtStatusStale
		}
		ctx.OverlayReleased = true
	}
	t.contexts.Clear()
	t.pruneBudget = 256
}

type dtSliceKey struct {
	ptr *any
	len int
}

func dtCloneStrictRoot[T any](base any) (T, error) {
	var zero T
	if base == nil {
		return dtCastValue[T](nil)
	}
	cloned, err := CopyJson(base, nil)
	if err != nil {
		return zero, err
	}
	return dtCastValue[T](cloned)
}

func dtCastValue[T any](v any) (T, error) {
	if v == nil {
		var zero T
		rv := reflect.ValueOf(&zero).Elem()
		switch rv.Kind() {
		case reflect.Interface, reflect.Ptr, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
			return zero, nil
		default:
			return zero, js.NewTypeError("Draft root value cannot be nil")
		}
	}
	out, ok := v.(T)
	if !ok {
		var zero T
		return zero, js.NewTypeError("Draft root value type does not match target type")
	}
	return out, nil
}

func dtSanitizeDraftValue(root any) (any, error) {
	ancObj := make(map[*jsonx.Object]struct{})
	ancSlice := make(map[dtSliceKey]struct{})
	seenObj := make(map[*jsonx.Object]struct{})
	seenSlice := make(map[dtSliceKey]struct{})
	return dtSanitizeNode(root, ancObj, ancSlice, seenObj, seenSlice)
}

func dtSanitizeNode(
	v any,
	ancObj map[*jsonx.Object]struct{},
	ancSlice map[dtSliceKey]struct{},
	seenObj map[*jsonx.Object]struct{},
	seenSlice map[dtSliceKey]struct{},
) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case bool, string:
		return x, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, js.NewTypeError("Draft values must be strict JSON (finite numbers only)")
		}
		return x, nil
	case []any:
		if x == nil {
			return nil, js.NewTypeError("Draft values must be strict JSON (dense plain arrays only)")
		}
		if len(x) == 0 {
			return []any{}, nil
		}
		key := dtSliceKey{ptr: unsafe.SliceData(x), len: len(x)}
		if _, isAnc := ancSlice[key]; isAnc {
			return nil, js.NewTypeError("Draft values must be strict JSON without cycles")
		}
		if _, wasSeen := seenSlice[key]; wasSeen {
			cloned, err := CopyJson(x, nil)
			if err != nil {
				return nil, err
			}
			return cloned, nil
		}
		seenSlice[key] = struct{}{}
		ancSlice[key] = struct{}{}
		defer delete(ancSlice, key)

		for i, elem := range x {
			sanitized, err := dtSanitizeNode(elem, ancObj, ancSlice, seenObj, seenSlice)
			if err != nil {
				return nil, err
			}
			if !dtSameIdentity(elem, sanitized) {
				x[i] = sanitized
			}
		}
		return x, nil
	case *jsonx.Object:
		if x == nil {
			return nil, js.NewTypeError("Draft values must be strict JSON (plain objects only)")
		}
		if _, isAnc := ancObj[x]; isAnc {
			return nil, js.NewTypeError("Draft values must be strict JSON without cycles")
		}
		if _, wasSeen := seenObj[x]; wasSeen {
			cloned, err := CopyJson(x, nil)
			if err != nil {
				return nil, err
			}
			return cloned, nil
		}
		seenObj[x] = struct{}{}
		ancObj[x] = struct{}{}
		defer delete(ancObj, x)

		for k, val := range x.All() {
			sanitized, err := dtSanitizeNode(val, ancObj, ancSlice, seenObj, seenSlice)
			if err != nil {
				return nil, err
			}
			if !dtSameIdentity(val, sanitized) {
				x.Set(k, sanitized)
			}
		}
		return x, nil
	default:
		return nil, js.NewTypeError("Draft values must be strict JSON (plain objects, dense plain arrays, strings, finite numbers, booleans, or null)")
	}
}

func dtEqualTrustedJson(left, right JsonValue) bool {
	if dtSameIdentity(left, right) {
		return true
	}
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	switch lv := left.(type) {
	case bool:
		rv, ok := right.(bool)
		return ok && lv == rv
	case float64:
		rv, ok := right.(float64)
		if !ok {
			return false
		}
		if math.IsNaN(lv) && math.IsNaN(rv) {
			return true
		}
		return lv == rv && math.Signbit(lv) == math.Signbit(rv)
	case string:
		rv, ok := right.(string)
		return ok && lv == rv
	case []any:
		rv, ok := right.([]any)
		if !ok || (lv == nil) != (rv == nil) || len(lv) != len(rv) {
			return false
		}
		for i := range lv {
			if !dtEqualTrustedJson(lv[i], rv[i]) {
				return false
			}
		}
		return true
	case *jsonx.Object:
		rv, ok := right.(*jsonx.Object)
		if !ok || (lv == nil) != (rv == nil) || lv.Len() != rv.Len() {
			return false
		}
		for k, val := range lv.All() {
			other, exists := rv.Get(k)
			if !exists || !dtEqualTrustedJson(val, other) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func dtSameIdentity(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch va := a.(type) {
	case bool:
		vb, ok := b.(bool)
		return ok && va == vb
	case float64:
		vb, ok := b.(float64)
		if !ok {
			return false
		}
		if math.IsNaN(va) && math.IsNaN(vb) {
			return true
		}
		return va == vb && math.Signbit(va) == math.Signbit(vb)
	case string:
		vb, ok := b.(string)
		return ok && va == vb
	case []any:
		vb, ok := b.([]any)
		if !ok || (va == nil) != (vb == nil) {
			return false
		}
		if len(va) == 0 && len(vb) == 0 {
			return unsafe.SliceData(va) == unsafe.SliceData(vb)
		}
		return len(va) == len(vb) && unsafe.SliceData(va) == unsafe.SliceData(vb)
	case *jsonx.Object:
		vb, ok := b.(*jsonx.Object)
		return ok && va == vb
	default:
		ra := reflect.ValueOf(a)
		rb := reflect.ValueOf(b)
		if ra.Type() != rb.Type() {
			return false
		}
		if ra.Type().Comparable() {
			return a == b
		}
		switch ra.Kind() {
		case reflect.Slice:
			return ra.IsNil() == rb.IsNil() && ra.Len() == rb.Len() && ra.Pointer() == rb.Pointer()
		case reflect.Map:
			return ra.IsNil() == rb.IsNil() && ra.Pointer() == rb.Pointer()
		default:
			return false
		}
	}
}
