// Ported from packages/chord/src/delta/tracker.ts (pi v1.0.0).

package chord

import (
	"sync"

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
	panic("unported: Track")
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

// dtParentKind is 0 object, 1 base-array entry, 2 inserted-array entry.
type dtParentKind int

const (
	dtParentKindObject        dtParentKind = 0
	dtParentKindBaseArray     dtParentKind = 1
	dtParentKindInsertedArray dtParentKind = 2
)

// dtStep is a piece direction, 1 or -1.
type dtStep int

const (
	dtStep1    dtStep = 1
	dtStepNeg1 dtStep = -1
)

type dtPieceKind string

const (
	dtPieceKindBase   dtPieceKind = "base"
	dtPieceKindInsert dtPieceKind = "insert"
)

// dtPrimitive, dtContainer, and dtStored are the TS aliases over JsonValue.
type (
	dtPrimitive = JsonValue
	dtContainer = JsonValue
	dtStored    = JsonValue
)

// dtStoredRef is a primitive or an index into the context store.
// A nil dtStoredRef means the TS property is absent. JSON null is dtStoredNull.
type dtStoredRef interface{ isDtStoredRef() }

type dtStoredNull struct{}

func (dtStoredNull) isDtStoredRef() {}

type dtStoredBool bool

func (dtStoredBool) isDtStoredRef() {}

type dtStoredNumber float64

func (dtStoredNumber) isDtStoredRef() {}

type dtStoredString string

func (dtStoredString) isDtStoredRef() {}

type dtStoredIndex struct {
	Index int `json:"index"`
}

func (dtStoredIndex) isDtStoredRef() {}

// dtInsertSource is the backing store of one inserted array run.
type dtInsertSource struct {
	Refs []dtStoredRef `json:"refs"`
}

// dtPiece is a base run or an insert run in an array overlay.
type dtPiece interface{ isDtPiece() }

type dtBasePiece struct {
	Kind   dtPieceKind `json:"kind"`
	Start  int         `json:"start"`
	Length int         `json:"length"`
	Step   dtStep      `json:"step"`
}

func (*dtBasePiece) isDtPiece() {}

type dtInsertPiece struct {
	Kind   dtPieceKind     `json:"kind"`
	Source *dtInsertSource `json:"source"`
	Start  int             `json:"start"`
	Length int             `json:"length"`
	Step   dtStep          `json:"step"`
}

func (*dtInsertPiece) isDtPiece() {}

type dtPieceNode struct {
	Piece    dtPiece      `json:"piece"`
	Left     *dtPieceNode `json:"left,omitzero"`
	Right    *dtPieceNode `json:"right,omitzero"`
	Priority uint32       `json:"priority"`
	Elements int          `json:"elements"`
}

type dtPieceLocation struct {
	Piece        dtPiece `json:"piece"`
	LogicalStart int     `json:"logicalStart"`
	Minimum      int     `json:"minimum"`
	Maximum      int     `json:"maximum"`
}

type dtDenseRegion struct {
	Start  int `json:"start"`
	Length int `json:"length"`
}

type dtDenseCandidates struct {
	Indices []int  `json:"indices"`
	Bits    []byte `json:"bits,omitzero"`
	Length  int    `json:"length"`
}

type dtArrayPlan struct {
	RemoveRuns  []int `json:"removeRuns"`
	Permutation []int `json:"permutation,omitzero"`
	InsertRuns  []int `json:"insertRuns"`
}

// dtArrayOverlay is the piece tree for one array node.
// Seed is a uint32 xorshift (TS >>> 0). New overlays start at 0x9e3779b9.
type dtArrayOverlay struct {
	Root            *dtPieceNode                                            `json:"root,omitzero"`
	Pieces          []dtPiece                                               `json:"pieces,omitzero"`
	BaseOverrides   *omap.Map[int, dtStoredRef]                             `json:"baseOverrides,omitzero"`
	InsertOverrides *omap.Map[*dtInsertSource, *omap.Map[int, dtStoredRef]] `json:"insertOverrides,omitzero"`
	Structural      bool                                                    `json:"structural"`
	Generation      int                                                     `json:"generation"`
	Plan            *dtArrayPlan                                            `json:"plan,omitzero"`
	Seed            uint32                                                  `json:"seed"`
	LocatedOffset   int                                                     `json:"locatedOffset"`
	BaseLocations   []dtPieceLocation                                       `json:"baseLocations,omitzero"`
	InsertLocations *omap.Map[*dtInsertSource, []dtPieceLocation]           `json:"insertLocations,omitzero"`
}

// dtOverlayNode is one proxied container. Field order follows the type alias;
// the constructor literal only writes the required keys and appends the rest.
type dtOverlayNode struct {
	Context         *dtOverlayContext              `json:"context"`
	BaseIndex       int                            `json:"baseIndex"`
	ParentIndex     int                            `json:"parentIndex"`
	ParentKind      dtParentKind                   `json:"parentKind"`
	ParentKey       Seg                            `json:"parentKey"`
	ParentSource    *dtInsertSource                `json:"parentSource,omitzero"`
	ParentPlacement bool                           `json:"parentPlacement"`
	Target          any                            `json:"target"`
	Proxy           any                            `json:"proxy"`
	WriteKey        *string                        `json:"writeKey,omitzero"`
	WriteValue      dtStoredRef                    `json:"writeValue,omitzero"`
	Writes          *omap.Map[string, dtStoredRef] `json:"writes,omitzero"`
	DeleteKey       *string                        `json:"deleteKey,omitzero"`
	Deletes         *omap.Set[string]              `json:"deletes,omitzero"`
	Readded         *omap.Set[string]              `json:"readded,omitzero"`
	Array           *dtArrayOverlay                `json:"array,omitzero"`
	Dirty           *bool                          `json:"dirty,omitzero"`
	SubtreeDirty    *bool                          `json:"subtreeDirty,omitzero"`
	PreparedPath    Path                           `json:"preparedPath,omitzero"`
}

// dtOverlayContext is the per-change overlay.
// Tracker is TS WeakRef<TrackerImpl<object>>. RawNodes is TS WeakMap<object, OverlayNode>.
// RegistryRef is TS WeakRef<OverlayContext> | undefined. Those three stay any: the
// key types are not comparable Go map keys.
type dtOverlayContext struct {
	Owner                       any              `json:"owner"`
	Tracker                     any              `json:"tracker"`
	BaseRevision                int              `json:"baseRevision"`
	Status                      *dtStatusCell    `json:"status"`
	Root                        *dtOverlayNode   `json:"root,omitzero"`
	Bases                       []dtContainer    `json:"bases,omitzero"`
	Stored                      []dtContainer    `json:"stored,omitzero"`
	Dirty                       []*dtOverlayNode `json:"dirty"`
	Nodes                       []*dtOverlayNode `json:"nodes"`
	RawNodes                    any              `json:"rawNodes,omitzero"`
	Ops                         []Op             `json:"ops,omitzero"`
	Replacement                 bool             `json:"replacement"`
	ReplacementNoop             bool             `json:"replacementNoop"`
	BaseValue                   any              `json:"baseValue,omitzero"`
	OverlayReleased             bool             `json:"overlayReleased"`
	SimpleObjectMaterialization bool             `json:"simpleObjectMaterialization"`
	RegistryRef                 any              `json:"registryRef,omitzero"`
}

// dtPreparedImpl is the TS class PreparedImpl.
type dtPreparedImpl[T any] struct {
	mu      sync.Mutex
	context *dtOverlayContext
	value   T
	base    T
	ops     []Op
}

func dtNewPreparedImpl[T any](context *dtOverlayContext, value T, operations []Op) *dtPreparedImpl[T] {
	panic("unported: PreparedImpl")
}

func (p *dtPreparedImpl[T]) Base() T { return p.base }

func (p *dtPreparedImpl[T]) Value() T { return p.value }

func (p *dtPreparedImpl[T]) Ops() []Op { return p.ops }

func (p *dtPreparedImpl[T]) BaseRevision() int { return p.context.BaseRevision }

func (p *dtPreparedImpl[T]) Abort() { panic("unported: Prepared.Abort") }

// dtChangeImpl is the TS class ChangeImpl.
type dtChangeImpl[T any] struct {
	mu             sync.Mutex
	context        *dtOverlayContext
	preparedStatus *dtStatusCell
	settled        bool
}

func dtNewChangeImpl[T any](context *dtOverlayContext) *dtChangeImpl[T] {
	panic("unported: ChangeImpl")
}

func (c *dtChangeImpl[T]) State() (Draft[T], error) {
	panic("unported: Change.State")
}

func (c *dtChangeImpl[T]) Prepare() (Prepared[T], error) {
	panic("unported: Change.Prepare")
}

func (c *dtChangeImpl[T]) Abort() { panic("unported: Change.Abort") }

// dtTrackerImpl is the TS class TrackerImpl.
// contexts stores *dtOverlayContext directly; TS stores WeakRef values.
// selfRef is TS WeakRef<TrackerImpl<object>>. pruneBudget starts at 256.
type dtTrackerImpl[T any] struct {
	mu          sync.Mutex
	owner       any
	contexts    *omap.Set[*dtOverlayContext]
	selfRef     any
	value       T
	revision    int
	pruneBudget int
}

func dtNewTrackerImpl[T any](initial T) *dtTrackerImpl[T] {
	panic("unported: TrackerImpl")
}

func (t *dtTrackerImpl[T]) Value() T { return t.value }

func (t *dtTrackerImpl[T]) Revision() int { return t.revision }

func (t *dtTrackerImpl[T]) BeginChange() Change[T] {
	panic("unported: Tracker.BeginChange")
}

func (t *dtTrackerImpl[T]) PrepareReplace(value T) (Prepared[T], error) {
	panic("unported: Tracker.PrepareReplace")
}

func (t *dtTrackerImpl[T]) Adopt(prepared Prepared[T]) error {
	panic("unported: Tracker.Adopt")
}

func (t *dtTrackerImpl[T]) releaseContext(context *dtOverlayContext) {
	panic("unported: TrackerImpl.releaseContext")
}

func (t *dtTrackerImpl[T]) register(context *dtOverlayContext) {
	panic("unported: TrackerImpl.register")
}

func (t *dtTrackerImpl[T]) prune() { panic("unported: TrackerImpl.prune") }

func (t *dtTrackerImpl[T]) invalidate(winner *dtOverlayContext) {
	panic("unported: TrackerImpl.invalidate")
}

// replacementBase returns false when TS returns undefined.
func (t *dtTrackerImpl[T]) replacementBase(context *dtOverlayContext) (T, bool) {
	panic("unported: TrackerImpl.replacementBase")
}

const (
	// dtMaxDeltaOperations is MAX_DELTA_OPERATIONS.
	dtMaxDeltaOperations = 4096
	// dtMaxSimpleObjectNodes is MAX_SIMPLE_OBJECT_NODES.
	dtMaxSimpleObjectNodes = 128
)

// dtNode is the overlay identity key. TS: Symbol("chord.delta.overlay.node").
var dtNode struct{}

// dtReleased is the emptied-container sentinel. TS: RELEASED = {}.
var dtReleased any = new(struct{})

// dtArrayMutators is ARRAY_MUTATORS. Membership only; not iterated.
var dtArrayMutators = map[string]struct{}{
	"push":       {},
	"pop":        {},
	"shift":      {},
	"unshift":    {},
	"splice":     {},
	"reverse":    {},
	"sort":       {},
	"fill":       {},
	"copyWithin": {},
}

// dtDataDescriptor is the reused defineProperty descriptor.
// Literal order is value, writable, enumerable, configurable.
var dtDataDescriptor = struct {
	Value        any
	Writable     bool
	Enumerable   bool
	Configurable bool
}{
	Writable:     true,
	Enumerable:   true,
	Configurable: true,
}

// dtLocatedDenseArray and dtLocatedDenseIndex are the module scratch from locateDenseArrayPosition.
var (
	dtLocatedDenseArray *dtOverlayNode
	dtLocatedDenseIndex int
)
