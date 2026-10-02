// Ported from packages/chord/src/delta/index.ts (pi v1.0.0).

package chord

import (
	"iter"
	"sync"
)

// Seg is one path segment: a string object key or a non-negative array index.
type Seg interface {
	isSeg()
}

// SegString is an object-key path segment.
type SegString string

func (SegString) isSeg() {}

// SegNumber is an array-index path segment.
type SegNumber int

func (SegNumber) isSeg() {}

// Path is an inline sequence of segments. JSON is a mixed array of strings and numbers.
type Path []Seg

func (p Path) MarshalJSON() ([]byte, error) {
	panic("unported: Path.MarshalJSON")
}

func (p *Path) UnmarshalJSON(data []byte) error {
	panic("unported: Path.UnmarshalJSON")
}

// NonEmptyPath is a Path that must contain at least one segment.
// Go has no non-empty slice type; the constraint is enforced by the validators.
type NonEmptyPath = Path

// PathRef is a path inline, or an id assigned by the encoder on second use.
// IsId selects Id; otherwise Path is the inline path.
type PathRef struct {
	Path Path
	Id   int
	IsId bool
}

// Op is one decoded operation. JSON is a tuple array, not an object.
// OpList owns encoding and decoding. `r` is the only op that replaces a whole
// value. `s`, `d`, `a`, and `t` cannot target the root. `p` and `m` may.
type Op interface {
	isOp()
	Verb() string
}

// OpReplace is `["r", value]`.
type OpReplace struct {
	Value JsonValue
}

func (OpReplace) isOp() {}

func (OpReplace) Verb() string { return "r" }

// OpSet is `["s", path, value]`. Path is non-empty.
type OpSet struct {
	Path  NonEmptyPath
	Value JsonValue
}

func (OpSet) isOp() {}

func (OpSet) Verb() string { return "s" }

// OpDelete is `["d", path]`. Path is non-empty.
type OpDelete struct {
	Path NonEmptyPath
}

func (OpDelete) isOp() {}

func (OpDelete) Verb() string { return "d" }

// OpAppend is `["a", path, text]`. Path is non-empty.
type OpAppend struct {
	Path NonEmptyPath
	Text string
}

func (OpAppend) isOp() {}

func (OpAppend) Verb() string { return "a" }

// OpTruncate is `["t", path, count]`. Path is non-empty. Count is a non-negative code-unit index.
type OpTruncate struct {
	Path  NonEmptyPath
	Count int
}

func (OpTruncate) isOp() {}

func (OpTruncate) Verb() string { return "t" }

// OpSplice is `["p", path, index, remove, items]`.
type OpSplice struct {
	Path   Path
	Index  int
	Remove int
	Items  []JsonValue
}

func (OpSplice) isOp() {}

func (OpSplice) Verb() string { return "p" }

// OpMove reorders an array in place: `new[i] = old[permutation[i]]`. Tuple `["m", path, permutation]`.
type OpMove struct {
	Path        Path
	Permutation []int
}

func (OpMove) isOp() {}

func (OpMove) Verb() string { return "m" }

// UnknownOp keeps an unrecognized operation tuple verbatim. Raw is the JSON array.
type UnknownOp struct {
	Raw any
}

func (UnknownOp) isOp() {}

func (UnknownOp) Verb() string { panic("unported: UnknownOp.Verb") }

// OpList is a JSON array of decoded operation tuples.
type OpList []Op

func (l OpList) MarshalJSON() ([]byte, error) {
	panic("unported: OpList.MarshalJSON")
}

func (l *OpList) UnmarshalJSON(data []byte) error {
	panic("unported: OpList.UnmarshalJSON")
}

// UnmarshalOp decodes one operation tuple.
func UnmarshalOp(data []byte) (Op, error) {
	panic("unported: UnmarshalOp")
}

// DecodeOp decodes one operation tuple from a jsonx value.
func DecodeOp(v any) (Op, error) {
	panic("unported: DecodeOp")
}

// WireOp is an operation as it crosses a boundary.
// Besides Op, the vocabulary adds path-id definitions (`["#", id, path]`),
// numeric path ids, and shortened tuples that reuse the previous op's path.
// JSON is a tuple array. WireOpList owns encoding and decoding.
type WireOp interface {
	isWireOp()
	Verb() string
}

// WireOpReplace is `["r", value]`.
type WireOpReplace struct {
	Value JsonValue
}

func (WireOpReplace) isWireOp() {}

func (WireOpReplace) Verb() string { return "r" }

// WireOpSet is `["s", ref, value]`.
type WireOpSet struct {
	Ref   PathRef
	Value JsonValue
}

func (WireOpSet) isWireOp() {}

func (WireOpSet) Verb() string { return "s" }

// WireOpSetShort is `["s", value]`, reusing the previous path.
type WireOpSetShort struct {
	Value JsonValue
}

func (WireOpSetShort) isWireOp() {}

func (WireOpSetShort) Verb() string { return "s" }

// WireOpDelete is `["d", ref]`.
type WireOpDelete struct {
	Ref PathRef
}

func (WireOpDelete) isWireOp() {}

func (WireOpDelete) Verb() string { return "d" }

// WireOpDeleteShort is `["d"]`, reusing the previous path.
type WireOpDeleteShort struct{}

func (WireOpDeleteShort) isWireOp() {}

func (WireOpDeleteShort) Verb() string { return "d" }

// WireOpAppend is `["a", ref, text]`.
type WireOpAppend struct {
	Ref  PathRef
	Text string
}

func (WireOpAppend) isWireOp() {}

func (WireOpAppend) Verb() string { return "a" }

// WireOpAppendShort is `["a", text]`, reusing the previous path.
type WireOpAppendShort struct {
	Text string
}

func (WireOpAppendShort) isWireOp() {}

func (WireOpAppendShort) Verb() string { return "a" }

// WireOpTruncate is `["t", ref, count]`.
type WireOpTruncate struct {
	Ref   PathRef
	Count int
}

func (WireOpTruncate) isWireOp() {}

func (WireOpTruncate) Verb() string { return "t" }

// WireOpTruncateShort is `["t", count]`, reusing the previous path.
type WireOpTruncateShort struct {
	Count int
}

func (WireOpTruncateShort) isWireOp() {}

func (WireOpTruncateShort) Verb() string { return "t" }

// WireOpSplice is `["p", ref, index, remove, items]`.
type WireOpSplice struct {
	Ref    PathRef
	Index  int
	Remove int
	Items  []JsonValue
}

func (WireOpSplice) isWireOp() {}

func (WireOpSplice) Verb() string { return "p" }

// WireOpSpliceShort is `["p", index, remove, items]`, reusing the previous path.
type WireOpSpliceShort struct {
	Index  int
	Remove int
	Items  []JsonValue
}

func (WireOpSpliceShort) isWireOp() {}

func (WireOpSpliceShort) Verb() string { return "p" }

// WireOpMove is `["m", ref, permutation]`.
type WireOpMove struct {
	Ref         PathRef
	Permutation []int
}

func (WireOpMove) isWireOp() {}

func (WireOpMove) Verb() string { return "m" }

// WireOpMoveShort is `["m", permutation]`, reusing the previous path.
type WireOpMoveShort struct {
	Permutation []int
}

func (WireOpMoveShort) isWireOp() {}

func (WireOpMoveShort) Verb() string { return "m" }

// WireOpDefine is `["#", id, path]`, emitted on a path's second use.
type WireOpDefine struct {
	Id   int
	Path Path
}

func (WireOpDefine) isWireOp() {}

func (WireOpDefine) Verb() string { return "#" }

// UnknownWireOp keeps an unrecognized wire tuple verbatim. Raw is the JSON array.
type UnknownWireOp struct {
	Raw any
}

func (UnknownWireOp) isWireOp() {}

func (UnknownWireOp) Verb() string { panic("unported: UnknownWireOp.Verb") }

// WireOpList is a JSON array of wire operation tuples.
type WireOpList []WireOp

func (l WireOpList) MarshalJSON() ([]byte, error) {
	panic("unported: WireOpList.MarshalJSON")
}

func (l *WireOpList) UnmarshalJSON(data []byte) error {
	panic("unported: WireOpList.UnmarshalJSON")
}

// UnmarshalWireOp decodes one wire operation tuple.
func UnmarshalWireOp(data []byte) (WireOp, error) {
	panic("unported: UnmarshalWireOp")
}

// DecodeWireOp decodes one wire operation tuple from a jsonx value.
func DecodeWireOp(v any) (WireOp, error) {
	panic("unported: DecodeWireOp")
}

// IsReplace reports whether op is a whole-value replacement (`verb == "r"`).
func IsReplace(op interface{ Verb() string }) bool {
	panic("unported: IsReplace")
}

// IsBase reports whether a batch begins with a replacement.
// Flush guarantees `r` is at index 0 or absent, so this is exact.
// The slice element may be an Op or a WireOp.
func IsBase[S ~[]E, E interface{ Verb() string }](ops S) bool {
	panic("unported: IsBase")
}

// Overlap returns the longest suffix of a that is a prefix of b, scanning at most
// scan UTF-16 code units of a's tail.
// probeAndMax optionally overrides probe (default 64) and maxCandidates (default 8).
// Giving up returns 0, which emits a set: larger, never wrong.
func Overlap(a string, b string, scan int, probeAndMax ...int) int {
	panic("unported: Overlap")
}

// ReservedSegments are the segments that reach the prototype chain.
// Membership is what callers use; the set is not iterated.
var ReservedSegments = map[string]struct{}{
	"__proto__":   {},
	"constructor": {},
	"prototype":   {},
}

// UnsafePathError is a rejected path segment. Name is "UnsafePathError".
// The message is `unsafe path segment: ` plus the segment's string form.
type UnsafePathError struct {
	Segment Seg
	Message string
}

// NewUnsafePathError returns an error for segment.
func NewUnsafePathError(segment Seg) *UnsafePathError {
	panic("unported: NewUnsafePathError")
}

func (e *UnsafePathError) Error() string { return e.Message }

func (e *UnsafePathError) Name() string { return "UnsafePathError" }

// AssertValidOp checks verb, arity, and payload shape for a decoded op.
// Paths are inline: no `#` and no short forms.
func AssertValidOp(op any) (Op, error) {
	panic("unported: AssertValidOp")
}

// AssertValidWireOp checks the wire grammar. Ids and short forms are legal.
func AssertValidWireOp(op any) (WireOp, error) {
	panic("unported: AssertValidWireOp")
}

// AssertSafePath rejects prototype segments and non-integer or negative indexes.
func AssertSafePath(path Path) error {
	panic("unported: AssertSafePath")
}

// PathError is an unresolvable path. Name is "PathError".
// The message is `unresolvable path: ` plus JSON.stringify of the path or path id.
// Path.IsId selects a numeric id; otherwise Path.Path is the inline path.
type PathError struct {
	Path    PathRef
	Message string
}

// NewPathError returns an error for an inline path or a numeric path id.
// Set PathRef.IsId for an id. An empty Path is the TS empty array.
func NewPathError(path PathRef) *PathError {
	panic("unported: NewPathError")
}

func (e *PathError) Error() string { return e.Message }

func (e *PathError) Name() string { return "PathError" }

// Apply applies decoded ops to a plain mutable value and returns it, because `r`
// replaces the root and cannot be done in place. Path ids and omitted paths are a
// wire concern: decode first if the ops came from a boundary.
// A nil-capable T represents the TS `undefined` target.
func Apply[T any](target T, ops OpList) (T, error) {
	panic("unported: Apply")
}

// ApplyImmutable applies one decoded batch without mutating the previous immutable value.
func ApplyImmutable[T any](target T, ops OpList) (T, error) {
	panic("unported: ApplyImmutable")
}

// ApplyImmutableBatches applies decoded batches as one final-result-only replay.
// Containers copied for an earlier batch may be mutated while applying a later batch,
// so this exposes no intermediate revisions. batches is the TS Iterable.
func ApplyImmutableBatches[T any](target T, batches iter.Seq[OpList]) (T, error) {
	panic("unported: ApplyImmutableBatches")
}

// Encoder interns paths for one independent state stream.
type Encoder interface {
	Encode(ops OpList) WireOpList
}

// diEncoder interns a path on its second use. Ids survive across batches; a
// replacement clears them. The encoder is safe for shared use.
type diEncoder struct {
	mu     sync.Mutex
	seen   map[string]struct{}
	ids    map[string]int
	nextId int
}

func (e *diEncoder) Encode(ops OpList) WireOpList {
	panic("unported: Encoder.Encode")
}

// NewEncoder returns an encoder for one independent state stream.
// Every decoder must observe exactly the batches encoded by its matching encoder,
// beginning with that state's base.
func NewEncoder() Encoder {
	panic("unported: NewEncoder")
}

// Decoder expands path ids and shortened tuples for one independent state stream.
type Decoder interface {
	Decode(wire WireOpList) (OpList, error)
}

// diDecoder resolves path ids defined by its matching encoder.
type diDecoder struct {
	mu    sync.Mutex
	paths map[int]Path
}

func (d *diDecoder) Decode(wire WireOpList) (OpList, error) {
	panic("unported: Decoder.Decode")
}

// NewDecoder returns a decoder for one independent state stream.
func NewDecoder() Decoder {
	panic("unported: NewDecoder")
}

var (
	_ Encoder = (*diEncoder)(nil)
	_ Decoder = (*diDecoder)(nil)
	_ error   = (*UnsafePathError)(nil)
	_ error   = (*PathError)(nil)
	_ Op      = OpReplace{}
	_ Op      = OpSet{}
	_ Op      = OpDelete{}
	_ Op      = OpAppend{}
	_ Op      = OpTruncate{}
	_ Op      = OpSplice{}
	_ Op      = OpMove{}
	_ Op      = UnknownOp{}
	_ WireOp  = WireOpReplace{}
	_ WireOp  = WireOpSet{}
	_ WireOp  = WireOpSetShort{}
	_ WireOp  = WireOpDelete{}
	_ WireOp  = WireOpDeleteShort{}
	_ WireOp  = WireOpAppend{}
	_ WireOp  = WireOpAppendShort{}
	_ WireOp  = WireOpTruncate{}
	_ WireOp  = WireOpTruncateShort{}
	_ WireOp  = WireOpSplice{}
	_ WireOp  = WireOpSpliceShort{}
	_ WireOp  = WireOpMove{}
	_ WireOp  = WireOpMoveShort{}
	_ WireOp  = WireOpDefine{}
	_ WireOp  = UnknownWireOp{}
)
