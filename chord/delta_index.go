// Ported from packages/chord/src/delta/index.ts (pi v1.0.0).

package chord

import (
	"errors"
	"fmt"
	"iter"
	"math"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"unsafe"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
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
	return jsonx.Marshal(diPathToSlice(p))
}

func (p *Path) UnmarshalJSON(data []byte) error {
	val, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	path, ok := diDecodePath(val)
	if !ok {
		return js.NewTypeError("path is not an array")
	}
	*p = path
	return nil
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

func (u UnknownOp) Verb() string {
	if arr, ok := u.Raw.([]any); ok && len(arr) > 0 {
		if v, ok := arr[0].(string); ok {
			return v
		}
	}
	return ""
}

// OpList is a JSON array of decoded operation tuples.
type OpList []Op

func (l OpList) MarshalJSON() ([]byte, error) {
	out := make([]any, len(l))
	for i, op := range l {
		out[i] = diOpToValue(op)
	}
	return jsonx.Marshal(out)
}

func (l *OpList) UnmarshalJSON(data []byte) error {
	val, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	arr, ok := val.([]any)
	if !ok || arr == nil {
		return js.NewTypeError("op list is not an array")
	}
	ops := make(OpList, len(arr))
	for i, item := range arr {
		op, err := DecodeOp(item)
		if err != nil {
			return err
		}
		ops[i] = op
	}
	*l = ops
	return nil
}

// UnmarshalOp decodes one operation tuple.
func UnmarshalOp(data []byte) (Op, error) {
	val, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeOp(val)
}

// DecodeOp decodes one operation tuple from a jsonx value.
func DecodeOp(v any) (Op, error) {
	arr, ok := v.([]any)
	if !ok || arr == nil {
		return nil, js.NewTypeError("op is not a tuple")
	}
	if len(arr) == 0 {
		return UnknownOp{Raw: arr}, nil
	}
	verb, ok := arr[0].(string)
	if !ok {
		return UnknownOp{Raw: arr}, nil
	}
	switch verb {
	case "r":
		if len(arr) == 2 {
			return OpReplace{Value: arr[1]}, nil
		}
	case "s":
		if len(arr) == 3 {
			if p, ok := diDecodePath(arr[1]); ok && len(p) > 0 {
				return OpSet{Path: p, Value: arr[2]}, nil
			}
		}
	case "d":
		if len(arr) == 2 {
			if p, ok := diDecodePath(arr[1]); ok && len(p) > 0 {
				return OpDelete{Path: p}, nil
			}
		}
	case "a":
		if len(arr) == 3 {
			if p, ok := diDecodePath(arr[1]); ok && len(p) > 0 {
				if text, ok := arr[2].(string); ok {
					return OpAppend{Path: p, Text: text}, nil
				}
			}
		}
	case "t":
		if len(arr) == 3 {
			if p, ok := diDecodePath(arr[1]); ok && len(p) > 0 {
				if count, ok := diNonNegInt(arr[2]); ok {
					return OpTruncate{Path: p, Count: count}, nil
				}
			}
		}
	case "p":
		if len(arr) == 5 {
			if p, ok := diDecodePath(arr[1]); ok {
				if idx, okIdx := diNonNegInt(arr[2]); okIdx {
					if rem, okRem := diNonNegInt(arr[3]); okRem {
						if items, okItems := arr[4].([]any); okItems && items != nil {
							return OpSplice{Path: p, Index: idx, Remove: rem, Items: items}, nil
						}
					}
				}
			}
		}
	case "m":
		if len(arr) == 3 {
			if p, ok := diDecodePath(arr[1]); ok {
				if perm, okPerm := diDecodeIntSlice(arr[2]); okPerm {
					return OpMove{Path: p, Permutation: perm}, nil
				}
			}
		}
	}
	return UnknownOp{Raw: arr}, nil
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

func (u UnknownWireOp) Verb() string {
	if arr, ok := u.Raw.([]any); ok && len(arr) > 0 {
		if v, ok := arr[0].(string); ok {
			return v
		}
	}
	return ""
}

// WireOpList is a JSON array of wire operation tuples.
type WireOpList []WireOp

func (l WireOpList) MarshalJSON() ([]byte, error) {
	out := make([]any, len(l))
	for i, op := range l {
		out[i] = diWireOpToValue(op)
	}
	return jsonx.Marshal(out)
}

func (l *WireOpList) UnmarshalJSON(data []byte) error {
	val, err := jsonx.Parse(data)
	if err != nil {
		return err
	}
	arr, ok := val.([]any)
	if !ok || arr == nil {
		return js.NewTypeError("wire op list is not an array")
	}
	ops := make(WireOpList, len(arr))
	for i, item := range arr {
		op, err := DecodeWireOp(item)
		if err != nil {
			return err
		}
		ops[i] = op
	}
	*l = ops
	return nil
}

// UnmarshalWireOp decodes one wire operation tuple.
func UnmarshalWireOp(data []byte) (WireOp, error) {
	val, err := jsonx.Parse(data)
	if err != nil {
		return nil, err
	}
	return DecodeWireOp(val)
}

// DecodeWireOp decodes one wire operation tuple from a jsonx value.
func DecodeWireOp(v any) (WireOp, error) {
	arr, ok := v.([]any)
	if !ok || arr == nil {
		return nil, js.NewTypeError("op is not a tuple")
	}
	if len(arr) == 0 {
		return UnknownWireOp{Raw: arr}, nil
	}
	verb, ok := arr[0].(string)
	if !ok {
		return UnknownWireOp{Raw: arr}, nil
	}
	switch verb {
	case "r":
		if len(arr) == 2 {
			return WireOpReplace{Value: arr[1]}, nil
		}
	case "s":
		if len(arr) == 3 {
			if ref, ok := diDecodePathRef(arr[1]); ok {
				return WireOpSet{Ref: ref, Value: arr[2]}, nil
			}
		} else if len(arr) == 2 {
			return WireOpSetShort{Value: arr[1]}, nil
		}
	case "d":
		if len(arr) == 2 {
			if ref, ok := diDecodePathRef(arr[1]); ok {
				return WireOpDelete{Ref: ref}, nil
			}
		} else if len(arr) == 1 {
			return WireOpDeleteShort{}, nil
		}
	case "a":
		if len(arr) == 3 {
			if ref, ok := diDecodePathRef(arr[1]); ok {
				if text, ok := arr[2].(string); ok {
					return WireOpAppend{Ref: ref, Text: text}, nil
				}
			}
		} else if len(arr) == 2 {
			if text, ok := arr[1].(string); ok {
				return WireOpAppendShort{Text: text}, nil
			}
		}
	case "t":
		if len(arr) == 3 {
			if ref, ok := diDecodePathRef(arr[1]); ok {
				if count, ok := diNonNegInt(arr[2]); ok {
					return WireOpTruncate{Ref: ref, Count: count}, nil
				}
			}
		} else if len(arr) == 2 {
			if count, ok := diNonNegInt(arr[1]); ok {
				return WireOpTruncateShort{Count: count}, nil
			}
		}
	case "p":
		if len(arr) == 5 {
			if ref, ok := diDecodePathRef(arr[1]); ok {
				if idx, okIdx := diNonNegInt(arr[2]); okIdx {
					if rem, okRem := diNonNegInt(arr[3]); okRem {
						if items, okItems := arr[4].([]any); okItems && items != nil {
							return WireOpSplice{Ref: ref, Index: idx, Remove: rem, Items: items}, nil
						}
					}
				}
			}
		} else if len(arr) == 4 {
			if idx, okIdx := diNonNegInt(arr[1]); okIdx {
				if rem, okRem := diNonNegInt(arr[2]); okRem {
					if items, okItems := arr[3].([]any); okItems && items != nil {
						return WireOpSpliceShort{Index: idx, Remove: rem, Items: items}, nil
					}
				}
			}
		}
	case "m":
		if len(arr) == 3 {
			if ref, ok := diDecodePathRef(arr[1]); ok {
				if perm, okPerm := diDecodeIntSlice(arr[2]); okPerm {
					return WireOpMove{Ref: ref, Permutation: perm}, nil
				}
			}
		} else if len(arr) == 2 {
			if perm, okPerm := diDecodeIntSlice(arr[1]); okPerm {
				return WireOpMoveShort{Permutation: perm}, nil
			}
		}
	case "#":
		if len(arr) == 3 {
			if id, okId := diNonNegInt(arr[1]); okId {
				if p, okPath := diDecodePath(arr[2]); okPath {
					return WireOpDefine{Id: id, Path: p}, nil
				}
			}
		}
	}
	return UnknownWireOp{Raw: arr}, nil
}

// IsReplace reports whether op is a whole-value replacement (`verb == "r"`).
func IsReplace(op interface{ Verb() string }) bool {
	return op != nil && op.Verb() == "r"
}

// IsBase reports whether a batch begins with a replacement.
// Flush guarantees `r` is at index 0 or absent, so this is exact.
// The slice element may be an Op or a WireOp.
func IsBase[S ~[]E, E interface{ Verb() string }](ops S) bool {
	return len(ops) > 0 && ops[0].Verb() == "r"
}

// Overlap returns the longest suffix of a that is a prefix of b, scanning at most
// scan UTF-16 code units of a's tail.
// probeAndMax optionally overrides probe (default 64) and maxCandidates (default 8).
// Giving up returns 0, which emits a set: larger, never wrong.
func Overlap(a string, b string, scan int, probeAndMax ...int) int {
	if a == "" || b == "" || scan <= 0 {
		return 0
	}
	probe := 64
	maxCandidates := 8
	if len(probeAndMax) >= 1 {
		probe = probeAndMax[0]
	}
	if len(probeAndMax) >= 2 {
		maxCandidates = probeAndMax[1]
	}
	aLen := js.Len(a)
	tailStr := a
	if aLen > scan {
		tailStr = js.SliceFrom(a, aLen-scan)
	}
	tail := js.ToUTF16(tailStr)
	bU16 := js.ToUTF16(b)
	if len(tail) == 0 || len(bU16) == 0 {
		return 0
	}
	firstH := probe
	if len(bU16) < firstH {
		firstH = len(bU16)
	}
	probes := []int{firstH}
	if firstH != 1 {
		probes = append(probes, 1)
	}
	for _, h := range probes {
		if h <= 0 {
			continue
		}
		head := bU16[:h]
		tried := 0
		for k := diIndexOfUTF16(tail, head, 0); k != -1; k = diIndexOfUTF16(tail, head, k+1) {
			tried++
			if tried > maxCandidates {
				break
			}
			n := len(tail) - k
			if n <= len(bU16) && slices.Equal(tail[k:], bU16[:n]) {
				return n
			}
		}
	}
	return 0
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
	return &UnsafePathError{
		Segment: segment,
		Message: "unsafe path segment: " + diSegString(segment),
	}
}

func (e *UnsafePathError) Error() string { return e.Message }

func (e *UnsafePathError) Name() string { return "UnsafePathError" }

// AssertValidOp checks verb, arity, and payload shape for a decoded op.
// Paths are inline: no `#` and no short forms.
func AssertValidOp(op any) (Op, error) {
	if raw, ok := op.([]any); ok {
		return diAssertValidRawOp(raw)
	}
	typed, ok := op.(Op)
	if !ok || typed == nil {
		return nil, js.NewTypeError("op is not a tuple")
	}
	switch o := typed.(type) {
	case OpReplace:
		return o, nil
	case OpSet:
		if err := diAssertPathArg(o.Path, true); err != nil {
			return nil, err
		}
		return o, nil
	case OpDelete:
		if err := diAssertPathArg(o.Path, true); err != nil {
			return nil, err
		}
		return o, nil
	case OpAppend:
		if err := diAssertPathArg(o.Path, true); err != nil {
			return nil, err
		}
		return o, nil
	case OpTruncate:
		if o.Count < 0 {
			return nil, js.NewTypeError("t shape")
		}
		if err := diAssertPathArg(o.Path, true); err != nil {
			return nil, err
		}
		return o, nil
	case OpSplice:
		if err := diAssertPathArg(o.Path, false); err != nil {
			return nil, err
		}
		if o.Index < 0 {
			return nil, js.NewTypeError("p index")
		}
		if o.Remove < 0 {
			return nil, js.NewTypeError("p remove")
		}
		return o, nil
	case OpMove:
		if err := diAssertPathArg(o.Path, false); err != nil {
			return nil, err
		}
		if err := diAssertPermutation(o.Permutation); err != nil {
			return nil, err
		}
		return o, nil
	case UnknownOp:
		if raw, ok := o.Raw.([]any); ok {
			return diAssertValidRawOp(raw)
		}
		return nil, js.NewTypeError("unknown op verb: " + o.Verb())
	default:
		return nil, js.NewTypeError(fmt.Sprintf("unknown op verb: %s", typed.Verb()))
	}
}

// AssertValidWireOp checks the wire grammar. Ids and short forms are legal.
func AssertValidWireOp(op any) (WireOp, error) {
	if raw, ok := op.([]any); ok {
		return diAssertValidRawWireOp(raw)
	}
	typed, ok := op.(WireOp)
	if !ok || typed == nil {
		return nil, js.NewTypeError("op is not a tuple")
	}
	switch o := typed.(type) {
	case WireOpReplace:
		return o, nil
	case WireOpSet:
		if err := diAssertPathRef(o.Ref); err != nil {
			return nil, err
		}
		return o, nil
	case WireOpSetShort:
		return o, nil
	case WireOpDelete:
		if err := diAssertPathRef(o.Ref); err != nil {
			return nil, err
		}
		return o, nil
	case WireOpDeleteShort:
		return o, nil
	case WireOpAppend:
		if err := diAssertPathRef(o.Ref); err != nil {
			return nil, err
		}
		return o, nil
	case WireOpAppendShort:
		return o, nil
	case WireOpTruncate:
		if err := diAssertPathRef(o.Ref); err != nil {
			return nil, err
		}
		if o.Count < 0 {
			return nil, js.NewTypeError("t count")
		}
		return o, nil
	case WireOpTruncateShort:
		if o.Count < 0 {
			return nil, js.NewTypeError("t count")
		}
		return o, nil
	case WireOpSplice:
		if err := diAssertPathRef(o.Ref); err != nil {
			return nil, err
		}
		if o.Index < 0 {
			return nil, js.NewTypeError("p index")
		}
		if o.Remove < 0 {
			return nil, js.NewTypeError("p remove")
		}
		return o, nil
	case WireOpSpliceShort:
		if o.Index < 0 {
			return nil, js.NewTypeError("p index")
		}
		if o.Remove < 0 {
			return nil, js.NewTypeError("p remove")
		}
		return o, nil
	case WireOpMove:
		if err := diAssertPathRef(o.Ref); err != nil {
			return nil, err
		}
		if err := diAssertPermutation(o.Permutation); err != nil {
			return nil, err
		}
		return o, nil
	case WireOpMoveShort:
		if err := diAssertPermutation(o.Permutation); err != nil {
			return nil, err
		}
		return o, nil
	case WireOpDefine:
		if o.Id < 0 {
			return nil, js.NewTypeError("# shape")
		}
		if err := AssertSafePath(o.Path); err != nil {
			return nil, err
		}
		return o, nil
	case UnknownWireOp:
		if raw, ok := o.Raw.([]any); ok {
			return diAssertValidRawWireOp(raw)
		}
		return nil, js.NewTypeError("unknown op verb: " + o.Verb())
	default:
		return nil, js.NewTypeError(fmt.Sprintf("unknown op verb: %s", typed.Verb()))
	}
}

// AssertSafePath rejects prototype segments and non-integer or negative indexes.
func AssertSafePath(path Path) error {
	for _, seg := range path {
		switch s := seg.(type) {
		case SegString:
			if _, reserved := ReservedSegments[string(s)]; reserved {
				return NewUnsafePathError(s)
			}
		case SegNumber:
			if s < 0 {
				return NewUnsafePathError(s)
			}
		default:
			return NewUnsafePathError(seg)
		}
	}
	return nil
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
	var encoded string
	if path.IsId {
		encoded = strconv.Itoa(path.Id)
	} else {
		b, err := path.Path.MarshalJSON()
		if err != nil {
			encoded = "[]"
		} else {
			encoded = string(b)
		}
	}
	return &PathError{
		Path:    path,
		Message: "unresolvable path: " + encoded,
	}
}

func (e *PathError) Error() string { return e.Message }

func (e *PathError) Name() string { return "PathError" }

// Apply applies decoded ops to a plain mutable value and returns it, because `r`
// replaces the root and cannot be done in place. Path ids and omitted paths are a
// wire concern: decode first if the ops came from a boundary.
// A nil-capable T represents the TS `undefined` target.
func Apply[T any](target T, ops OpList) (T, error) {
	res, err := diApplyOps(any(target), ops)
	if err != nil {
		var zero T
		return zero, err
	}
	return diCastResult[T](res)
}

// ApplyImmutable applies one decoded batch without mutating the previous immutable value.
func ApplyImmutable[T any](target T, ops OpList) (T, error) {
	return ApplyImmutableBatches(target, func(yield func(OpList) bool) {
		yield(ops)
	})
}

// ApplyImmutableBatches applies decoded batches as one final-result-only replay.
// Containers copied for an earlier batch may be mutated while applying a later batch,
// so this exposes no intermediate revisions. batches is the TS Iterable.
func ApplyImmutableBatches[T any](target T, batches iter.Seq[OpList]) (T, error) {
	root := any(target)
	owned := diNewOwnedSet()
	if batches != nil {
		for ops := range batches {
			for _, op := range ops {
				valid, err := AssertValidOp(op)
				if err != nil {
					var zero T
					return zero, err
				}
				if r, ok := valid.(OpReplace); ok {
					root = r.Value
					continue
				}
				var copyPath Path
				switch o := valid.(type) {
				case OpSplice:
					copyPath = o.Path
				case OpMove:
					copyPath = o.Path
				case OpSet:
					copyPath = o.Path[:len(o.Path)-1]
				case OpDelete:
					copyPath = o.Path[:len(o.Path)-1]
				case OpAppend:
					copyPath = o.Path[:len(o.Path)-1]
				case OpTruncate:
					copyPath = o.Path[:len(o.Path)-1]
				}
				copiedRoot, err := diCopyContainers(root, copyPath, owned)
				if err != nil {
					var zero T
					return zero, err
				}
				root, err = diApplyOps(copiedRoot, OpList{valid}, owned)
				if err != nil {
					var zero T
					return zero, err
				}
			}
		}
	}
	return diCastResult[T](root)
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
	e.mu.Lock()
	defer e.mu.Unlock()

	var previous string
	hasPrevious := false
	out := make(WireOpList, 0, len(ops))
	for _, op := range ops {
		if r, ok := op.(OpReplace); ok {
			out = append(out, WireOpReplace{Value: r.Value})
			clear(e.seen)
			clear(e.ids)
			e.nextId = 0
			previous = ""
			hasPrevious = false
			continue
		}
		path := diOpPath(op)
		key := diPathKey(path)
		if hasPrevious && key == previous {
			switch o := op.(type) {
			case OpSet:
				out = append(out, WireOpSetShort{Value: o.Value})
			case OpDelete:
				out = append(out, WireOpDeleteShort{})
			case OpAppend:
				out = append(out, WireOpAppendShort{Text: o.Text})
			case OpTruncate:
				out = append(out, WireOpTruncateShort{Count: o.Count})
			case OpSplice:
				out = append(out, WireOpSpliceShort{Index: o.Index, Remove: o.Remove, Items: o.Items})
			case OpMove:
				out = append(out, WireOpMoveShort{Permutation: o.Permutation})
			}
			continue
		}

		ref := PathRef{Path: path}
		if existing, ok := e.ids[key]; ok {
			ref = PathRef{Id: existing, IsId: true}
		} else if _, seen := e.seen[key]; seen {
			id := e.nextId
			e.nextId++
			e.ids[key] = id
			out = append(out, WireOpDefine{Id: id, Path: path})
			ref = PathRef{Id: id, IsId: true}
		} else {
			e.seen[key] = struct{}{}
		}

		switch o := op.(type) {
		case OpSet:
			out = append(out, WireOpSet{Ref: ref, Value: o.Value})
		case OpDelete:
			out = append(out, WireOpDelete{Ref: ref})
		case OpAppend:
			out = append(out, WireOpAppend{Ref: ref, Text: o.Text})
		case OpTruncate:
			out = append(out, WireOpTruncate{Ref: ref, Count: o.Count})
		case OpSplice:
			out = append(out, WireOpSplice{Ref: ref, Index: o.Index, Remove: o.Remove, Items: o.Items})
		case OpMove:
			out = append(out, WireOpMove{Ref: ref, Permutation: o.Permutation})
		}
		previous = key
		hasPrevious = true
	}
	return out
}

// NewEncoder returns an encoder for one independent state stream.
// Every decoder must observe exactly the batches encoded by its matching encoder,
// beginning with that state's base.
func NewEncoder() Encoder {
	return &diEncoder{
		seen: make(map[string]struct{}),
		ids:  make(map[string]int),
	}
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
	d.mu.Lock()
	defer d.mu.Unlock()

	var previous Path
	hasPrevious := false
	out := make(OpList, 0, len(wire))
	for _, rawOp := range wire {
		valid, err := AssertValidWireOp(rawOp)
		if err != nil {
			return nil, err
		}
		if def, ok := valid.(WireOpDefine); ok {
			if err := AssertSafePath(def.Path); err != nil {
				return nil, err
			}
			d.paths[def.Id] = def.Path
			continue
		}
		if r, ok := valid.(WireOpReplace); ok {
			out = append(out, OpReplace{Value: r.Value})
			clear(d.paths)
			previous = nil
			hasPrevious = false
			continue
		}

		isShort, ref := diWireOpRef(valid)
		var path Path
		if isShort {
			if !hasPrevious {
				return nil, NewPathError(PathRef{Path: Path{}})
			}
			path = previous
		} else {
			if ref.IsId {
				resolved, ok := d.paths[ref.Id]
				if !ok {
					return nil, NewPathError(PathRef{Id: ref.Id, IsId: true})
				}
				path = resolved
			} else {
				path = ref.Path
			}
			previous = path
			hasPrevious = true
		}

		verb := valid.Verb()
		if verb != "p" && verb != "m" && len(path) == 0 {
			return nil, NewPathError(PathRef{Path: path})
		}
		switch o := valid.(type) {
		case WireOpSet:
			out = append(out, OpSet{Path: path, Value: o.Value})
		case WireOpSetShort:
			out = append(out, OpSet{Path: path, Value: o.Value})
		case WireOpDelete:
			out = append(out, OpDelete{Path: path})
		case WireOpDeleteShort:
			out = append(out, OpDelete{Path: path})
		case WireOpAppend:
			out = append(out, OpAppend{Path: path, Text: o.Text})
		case WireOpAppendShort:
			out = append(out, OpAppend{Path: path, Text: o.Text})
		case WireOpTruncate:
			out = append(out, OpTruncate{Path: path, Count: o.Count})
		case WireOpTruncateShort:
			out = append(out, OpTruncate{Path: path, Count: o.Count})
		case WireOpSplice:
			out = append(out, OpSplice{Path: path, Index: o.Index, Remove: o.Remove, Items: o.Items})
		case WireOpSpliceShort:
			out = append(out, OpSplice{Path: path, Index: o.Index, Remove: o.Remove, Items: o.Items})
		case WireOpMove:
			out = append(out, OpMove{Path: path, Permutation: o.Permutation})
		case WireOpMoveShort:
			out = append(out, OpMove{Path: path, Permutation: o.Permutation})
		}
	}
	return out, nil
}

// NewDecoder returns a decoder for one independent state stream.
func NewDecoder() Decoder {
	return &diDecoder{
		paths: make(map[int]Path),
	}
}

type diSliceKey struct {
	ptr *any
	len int
}

type diOwnedSet struct {
	objects map[*jsonx.Object]struct{}
	slices  map[diSliceKey]struct{}
}

func diNewOwnedSet() *diOwnedSet {
	return &diOwnedSet{
		objects: make(map[*jsonx.Object]struct{}),
		slices:  make(map[diSliceKey]struct{}),
	}
}

func (s *diOwnedSet) has(v any) bool {
	if s == nil {
		return false
	}
	switch c := v.(type) {
	case *jsonx.Object:
		if c == nil {
			return false
		}
		_, ok := s.objects[c]
		return ok
	case []any:
		if len(c) == 0 {
			return false
		}
		_, ok := s.slices[diSliceKey{ptr: unsafe.SliceData(c), len: len(c)}]
		return ok
	default:
		return false
	}
}

func (s *diOwnedSet) add(v any) {
	if s == nil {
		return
	}
	switch c := v.(type) {
	case *jsonx.Object:
		if c != nil {
			s.objects[c] = struct{}{}
		}
	case []any:
		if len(c) > 0 {
			s.slices[diSliceKey{ptr: unsafe.SliceData(c), len: len(c)}] = struct{}{}
		}
	}
}

func diSegString(seg Seg) string {
	switch s := seg.(type) {
	case SegString:
		return string(s)
	case SegNumber:
		return strconv.Itoa(int(s))
	default:
		return fmt.Sprint(seg)
	}
}

func diPathToSlice(p Path) []any {
	out := make([]any, len(p))
	for i, seg := range p {
		switch s := seg.(type) {
		case SegString:
			out[i] = string(s)
		case SegNumber:
			out[i] = float64(s)
		}
	}
	return out
}

func diPathKey(p Path) string {
	b, _ := p.MarshalJSON()
	return string(b)
}

func diNonEmptySlice[T any](s []T) []T {
	if s == nil {
		return make([]T, 0)
	}
	return s
}

func diIntsToFloats(s []int) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = float64(v)
	}
	return out
}

func diPathRefToValue(ref PathRef) any {
	if ref.IsId {
		return float64(ref.Id)
	}
	return diPathToSlice(ref.Path)
}

func diOpToValue(op Op) any {
	switch o := op.(type) {
	case OpReplace:
		return []any{"r", o.Value}
	case OpSet:
		return []any{"s", diPathToSlice(o.Path), o.Value}
	case OpDelete:
		return []any{"d", diPathToSlice(o.Path)}
	case OpAppend:
		return []any{"a", diPathToSlice(o.Path), o.Text}
	case OpTruncate:
		return []any{"t", diPathToSlice(o.Path), float64(o.Count)}
	case OpSplice:
		return []any{"p", diPathToSlice(o.Path), float64(o.Index), float64(o.Remove), diNonEmptySlice(o.Items)}
	case OpMove:
		return []any{"m", diPathToSlice(o.Path), diIntsToFloats(o.Permutation)}
	case UnknownOp:
		return o.Raw
	default:
		return nil
	}
}

func diWireOpToValue(op WireOp) any {
	switch o := op.(type) {
	case WireOpReplace:
		return []any{"r", o.Value}
	case WireOpSet:
		return []any{"s", diPathRefToValue(o.Ref), o.Value}
	case WireOpSetShort:
		return []any{"s", o.Value}
	case WireOpDelete:
		return []any{"d", diPathRefToValue(o.Ref)}
	case WireOpDeleteShort:
		return []any{"d"}
	case WireOpAppend:
		return []any{"a", diPathRefToValue(o.Ref), o.Text}
	case WireOpAppendShort:
		return []any{"a", o.Text}
	case WireOpTruncate:
		return []any{"t", diPathRefToValue(o.Ref), float64(o.Count)}
	case WireOpTruncateShort:
		return []any{"t", float64(o.Count)}
	case WireOpSplice:
		return []any{"p", diPathRefToValue(o.Ref), float64(o.Index), float64(o.Remove), diNonEmptySlice(o.Items)}
	case WireOpSpliceShort:
		return []any{"p", float64(o.Index), float64(o.Remove), diNonEmptySlice(o.Items)}
	case WireOpMove:
		return []any{"m", diPathRefToValue(o.Ref), diIntsToFloats(o.Permutation)}
	case WireOpMoveShort:
		return []any{"m", diIntsToFloats(o.Permutation)}
	case WireOpDefine:
		return []any{"#", float64(o.Id), diPathToSlice(o.Path)}
	case UnknownWireOp:
		return o.Raw
	default:
		return nil
	}
}

func diInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f || f < -9007199254740991 || f > 9007199254740991 {
		return 0, false
	}
	return int(f), true
}

func diNonNegInt(v any) (int, bool) {
	n, ok := diInt(v)
	return n, ok && n >= 0
}

func diDecodePath(v any) (Path, bool) {
	arr, ok := v.([]any)
	if !ok || arr == nil {
		return nil, false
	}
	path := make(Path, len(arr))
	for i, elem := range arr {
		switch s := elem.(type) {
		case string:
			path[i] = SegString(s)
		case float64:
			n, ok := diInt(s)
			if !ok {
				return nil, false
			}
			path[i] = SegNumber(n)
		default:
			return nil, false
		}
	}
	return path, true
}

func diDecodePathRef(v any) (PathRef, bool) {
	if id, ok := diNonNegInt(v); ok {
		return PathRef{Id: id, IsId: true}, true
	}
	if p, ok := diDecodePath(v); ok {
		return PathRef{Path: p}, true
	}
	return PathRef{}, false
}

func diDecodeIntSlice(v any) ([]int, bool) {
	arr, ok := v.([]any)
	if !ok || arr == nil {
		return nil, false
	}
	out := make([]int, len(arr))
	for i, elem := range arr {
		n, ok := diInt(elem)
		if !ok {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

func diAssertPathArg(p Path, nonEmpty bool) error {
	if nonEmpty && len(p) == 0 {
		return js.NewTypeError("path is empty")
	}
	return AssertSafePath(p)
}

func diAssertRawPathArg(v any, nonEmpty bool) (Path, error) {
	arr, ok := v.([]any)
	if !ok || arr == nil {
		return nil, js.NewTypeError("path is not an array")
	}
	if nonEmpty && len(arr) == 0 {
		return nil, js.NewTypeError("path is empty")
	}
	path := make(Path, len(arr))
	for i, elem := range arr {
		switch s := elem.(type) {
		case string:
			path[i] = SegString(s)
		case float64:
			n, ok := diInt(s)
			if !ok {
				return nil, NewUnsafePathError(SegNumber(-1))
			}
			path[i] = SegNumber(n)
		default:
			return nil, js.NewTypeError("path segment must be string or integer")
		}
	}
	if err := AssertSafePath(path); err != nil {
		return nil, err
	}
	return path, nil
}

func diAssertPathRef(ref PathRef) error {
	if ref.IsId {
		if ref.Id < 0 {
			return js.NewTypeError("bad path id")
		}
		return nil
	}
	return AssertSafePath(ref.Path)
}

func diAssertRawPathRef(v any) (PathRef, error) {
	if f, ok := v.(float64); ok {
		id, ok := diNonNegInt(f)
		if !ok {
			return PathRef{}, js.NewTypeError("bad path id")
		}
		return PathRef{Id: id, IsId: true}, nil
	}
	p, err := diAssertRawPathArg(v, false)
	if err != nil {
		return PathRef{}, err
	}
	return PathRef{Path: p}, nil
}

func diAssertPermutation(perm []int) error {
	if perm == nil {
		return js.NewTypeError("m permutation is not an array")
	}
	seen := make([]byte, len(perm))
	for _, index := range perm {
		if index < 0 || index >= len(perm) || seen[index] != 0 {
			return js.NewTypeError("m permutation is not a bijection")
		}
		seen[index] = 1
	}
	return nil
}

func diAssertRawPermutation(v any) ([]int, error) {
	arr, ok := v.([]any)
	if !ok || arr == nil {
		return nil, js.NewTypeError("m permutation is not an array")
	}
	seen := make([]byte, len(arr))
	perm := make([]int, len(arr))
	for i, elem := range arr {
		idx, ok := diInt(elem)
		if !ok || idx < 0 || idx >= len(arr) || seen[idx] != 0 {
			return nil, js.NewTypeError("m permutation is not a bijection")
		}
		seen[idx] = 1
		perm[i] = idx
	}
	return perm, nil
}

func diAssertValidRawOp(op []any) (Op, error) {
	if op == nil || len(op) == 0 {
		return nil, js.NewTypeError("op is not a tuple")
	}
	verb, ok := op[0].(string)
	if !ok {
		return nil, js.NewTypeError(fmt.Sprintf("unknown op verb: %v", op[0]))
	}
	switch verb {
	case "r":
		if len(op) != 2 {
			return nil, js.NewTypeError("r arity")
		}
		return OpReplace{Value: op[1]}, nil
	case "s":
		if len(op) != 3 {
			return nil, js.NewTypeError("s arity")
		}
		p, err := diAssertRawPathArg(op[1], true)
		if err != nil {
			return nil, err
		}
		return OpSet{Path: p, Value: op[2]}, nil
	case "d":
		if len(op) != 2 {
			return nil, js.NewTypeError("d arity")
		}
		p, err := diAssertRawPathArg(op[1], true)
		if err != nil {
			return nil, err
		}
		return OpDelete{Path: p}, nil
	case "a":
		if len(op) != 3 {
			return nil, js.NewTypeError("a shape")
		}
		text, ok := op[2].(string)
		if !ok {
			return nil, js.NewTypeError("a shape")
		}
		p, err := diAssertRawPathArg(op[1], true)
		if err != nil {
			return nil, err
		}
		return OpAppend{Path: p, Text: text}, nil
	case "t":
		if len(op) != 3 {
			return nil, js.NewTypeError("t shape")
		}
		count, ok := diNonNegInt(op[2])
		if !ok {
			return nil, js.NewTypeError("t shape")
		}
		p, err := diAssertRawPathArg(op[1], true)
		if err != nil {
			return nil, err
		}
		return OpTruncate{Path: p, Count: count}, nil
	case "p":
		if len(op) != 5 {
			return nil, js.NewTypeError("p arity")
		}
		p, err := diAssertRawPathArg(op[1], false)
		if err != nil {
			return nil, err
		}
		idx, ok := diNonNegInt(op[2])
		if !ok {
			return nil, js.NewTypeError("p index")
		}
		rem, ok := diNonNegInt(op[3])
		if !ok {
			return nil, js.NewTypeError("p remove")
		}
		items, ok := op[4].([]any)
		if !ok || items == nil {
			return nil, js.NewTypeError("p items")
		}
		return OpSplice{Path: p, Index: idx, Remove: rem, Items: items}, nil
	case "m":
		if len(op) != 3 {
			return nil, js.NewTypeError("m arity")
		}
		p, err := diAssertRawPathArg(op[1], false)
		if err != nil {
			return nil, err
		}
		perm, err := diAssertRawPermutation(op[2])
		if err != nil {
			return nil, err
		}
		return OpMove{Path: p, Permutation: perm}, nil
	default:
		return nil, js.NewTypeError("unknown op verb: " + verb)
	}
}

func diAssertValidRawWireOp(op []any) (WireOp, error) {
	if op == nil || len(op) == 0 {
		return nil, js.NewTypeError("op is not a tuple")
	}
	verb, ok := op[0].(string)
	if !ok {
		return nil, js.NewTypeError(fmt.Sprintf("unknown op verb: %v", op[0]))
	}
	switch verb {
	case "r":
		if len(op) != 2 {
			return nil, js.NewTypeError("r arity")
		}
		return WireOpReplace{Value: op[1]}, nil
	case "s":
		if len(op) == 3 {
			ref, err := diAssertRawPathRef(op[1])
			if err != nil {
				return nil, err
			}
			return WireOpSet{Ref: ref, Value: op[2]}, nil
		}
		if len(op) == 2 {
			return WireOpSetShort{Value: op[1]}, nil
		}
		return nil, js.NewTypeError("s arity")
	case "d":
		if len(op) == 2 {
			ref, err := diAssertRawPathRef(op[1])
			if err != nil {
				return nil, err
			}
			return WireOpDelete{Ref: ref}, nil
		}
		if len(op) == 1 {
			return WireOpDeleteShort{}, nil
		}
		return nil, js.NewTypeError("d arity")
	case "a":
		if len(op) == 3 {
			ref, err := diAssertRawPathRef(op[1])
			if err != nil {
				return nil, err
			}
			text, ok := op[2].(string)
			if !ok {
				return nil, js.NewTypeError("a value")
			}
			return WireOpAppend{Ref: ref, Text: text}, nil
		}
		if len(op) == 2 {
			text, ok := op[1].(string)
			if !ok {
				return nil, js.NewTypeError("a value")
			}
			return WireOpAppendShort{Text: text}, nil
		}
		return nil, js.NewTypeError("a arity")
	case "t":
		if len(op) == 3 {
			ref, err := diAssertRawPathRef(op[1])
			if err != nil {
				return nil, err
			}
			count, ok := diNonNegInt(op[2])
			if !ok {
				return nil, js.NewTypeError("t count")
			}
			return WireOpTruncate{Ref: ref, Count: count}, nil
		}
		if len(op) == 2 {
			count, ok := diNonNegInt(op[1])
			if !ok {
				return nil, js.NewTypeError("t count")
			}
			return WireOpTruncateShort{Count: count}, nil
		}
		return nil, js.NewTypeError("t arity")
	case "p":
		var rawIdx, rawRem, rawItems any
		var ref PathRef
		hasRef := false
		if len(op) == 5 {
			var err error
			ref, err = diAssertRawPathRef(op[1])
			if err != nil {
				return nil, err
			}
			hasRef = true
			rawIdx, rawRem, rawItems = op[2], op[3], op[4]
		} else if len(op) == 4 {
			rawIdx, rawRem, rawItems = op[1], op[2], op[3]
		} else {
			return nil, js.NewTypeError("p arity")
		}
		idx, ok := diNonNegInt(rawIdx)
		if !ok {
			return nil, js.NewTypeError("p index")
		}
		rem, ok := diNonNegInt(rawRem)
		if !ok {
			return nil, js.NewTypeError("p remove")
		}
		items, ok := rawItems.([]any)
		if !ok || items == nil {
			return nil, js.NewTypeError("p items")
		}
		if hasRef {
			return WireOpSplice{Ref: ref, Index: idx, Remove: rem, Items: items}, nil
		}
		return WireOpSpliceShort{Index: idx, Remove: rem, Items: items}, nil
	case "m":
		perm, err := diAssertRawPermutation(op[len(op)-1])
		if err != nil {
			return nil, err
		}
		if len(op) == 3 {
			ref, err := diAssertRawPathRef(op[1])
			if err != nil {
				return nil, err
			}
			return WireOpMove{Ref: ref, Permutation: perm}, nil
		}
		if len(op) == 2 {
			return WireOpMoveShort{Permutation: perm}, nil
		}
		return nil, js.NewTypeError("m arity")
	case "#":
		if len(op) != 3 {
			return nil, js.NewTypeError("# shape")
		}
		id, ok := diNonNegInt(op[1])
		if !ok {
			return nil, js.NewTypeError("# shape")
		}
		p, err := diAssertRawPathArg(op[2], false)
		if err != nil {
			return nil, err
		}
		return WireOpDefine{Id: id, Path: p}, nil
	default:
		return nil, js.NewTypeError("unknown op verb: " + verb)
	}
}

func diOpPath(op Op) Path {
	switch o := op.(type) {
	case OpSet:
		return o.Path
	case OpDelete:
		return o.Path
	case OpAppend:
		return o.Path
	case OpTruncate:
		return o.Path
	case OpSplice:
		return o.Path
	case OpMove:
		return o.Path
	default:
		return nil
	}
}

func diWireOpRef(op WireOp) (bool, PathRef) {
	switch o := op.(type) {
	case WireOpSet:
		return false, o.Ref
	case WireOpSetShort:
		return true, PathRef{}
	case WireOpDelete:
		return false, o.Ref
	case WireOpDeleteShort:
		return true, PathRef{}
	case WireOpAppend:
		return false, o.Ref
	case WireOpAppendShort:
		return true, PathRef{}
	case WireOpTruncate:
		return false, o.Ref
	case WireOpTruncateShort:
		return true, PathRef{}
	case WireOpSplice:
		return false, o.Ref
	case WireOpSpliceShort:
		return true, PathRef{}
	case WireOpMove:
		return false, o.Ref
	case WireOpMoveShort:
		return true, PathRef{}
	default:
		return true, PathRef{}
	}
}

func diIndexOfUTF16(haystack, needle []uint16, from int) int {
	if len(needle) == 0 {
		if from <= len(haystack) {
			return from
		}
		return len(haystack)
	}
	limit := len(haystack) - len(needle)
	for i := from; i <= limit; i++ {
		if slices.Equal(haystack[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

func diIsContainer(v any) bool {
	switch c := v.(type) {
	case *jsonx.Object:
		return c != nil
	case []any:
		return c != nil
	default:
		return false
	}
}

func diResolveParent(root any, path Path) (any, Seg, error) {
	if len(path) == 0 {
		return nil, nil, NewPathError(PathRef{Path: path})
	}
	parent, err := diResolveContainer(root, path[:len(path)-1], path)
	if err != nil {
		return nil, nil, err
	}
	return parent, path[len(path)-1], nil
}

func diResolveContainer(root any, walk Path, fullPath Path) (any, error) {
	node := root
	for _, seg := range walk {
		if !diIsContainer(node) {
			return nil, NewPathError(PathRef{Path: fullPath})
		}
		switch c := node.(type) {
		case []any:
			idx, ok := seg.(SegNumber)
			if !ok {
				return nil, NewUnsafePathError(seg)
			}
			if idx < 0 || int(idx) >= len(c) {
				return nil, NewPathError(PathRef{Path: fullPath})
			}
			node = c[idx]
		case *jsonx.Object:
			val, exists := c.Get(diSegString(seg))
			if !exists {
				return nil, NewPathError(PathRef{Path: fullPath})
			}
			node = val
		}
	}
	if !diIsContainer(node) {
		return nil, NewPathError(PathRef{Path: fullPath})
	}
	return node, nil
}

func diAssignAtPath(root any, path Path, newVal any, owned *diOwnedSet) (any, error) {
	if len(path) == 0 {
		if owned != nil {
			owned.add(newVal)
		}
		return newVal, nil
	}
	parent, lastSeg, err := diResolveParent(root, path)
	if err != nil {
		return nil, err
	}
	switch p := parent.(type) {
	case *jsonx.Object:
		p.Set(diSegString(lastSeg), newVal)
	case []any:
		idx, ok := lastSeg.(SegNumber)
		if !ok {
			return nil, NewUnsafePathError(lastSeg)
		}
		if int(idx) < 0 || int(idx) > len(p) {
			return nil, NewUnsafePathError(idx)
		}
		if int(idx) == len(p) {
			resized := make([]any, len(p)+1)
			copy(resized, p)
			resized[len(p)] = newVal
			if owned != nil {
				owned.add(resized)
			}
			return diAssignAtPath(root, path[:len(path)-1], resized, owned)
		}
		p[idx] = newVal
	default:
		return nil, NewPathError(PathRef{Path: path})
	}
	return root, nil
}

func diSpliceSlice(target []any, index int, remove int, items []JsonValue) []any {
	start := index
	if start > len(target) {
		start = len(target)
	}
	del := remove
	if del > len(target)-start {
		del = len(target) - start
	}
	out := make([]any, 0, len(target)-del+len(items))
	out = append(out, target[:start]...)
	out = append(out, items...)
	out = append(out, target[start+del:]...)
	return out
}

func diApplyOps(target any, ops OpList, ownedOpt ...*diOwnedSet) (any, error) {
	var owned *diOwnedSet
	if len(ownedOpt) > 0 {
		owned = ownedOpt[0]
	}
	root := target
	for _, rawOp := range ops {
		op, err := AssertValidOp(rawOp)
		if err != nil {
			return nil, err
		}
		if r, ok := op.(OpReplace); ok {
			root = r.Value
			continue
		}
		path := diOpPath(op)
		if err := AssertSafePath(path); err != nil {
			return nil, err
		}
		switch o := op.(type) {
		case OpSplice:
			targetVal := root
			if len(path) > 0 {
				var err error
				targetVal, err = diResolveContainer(root, path, path)
				if err != nil {
					return nil, err
				}
			}
			arr, ok := targetVal.([]any)
			if !ok || arr == nil {
				return nil, NewPathError(PathRef{Path: path})
			}
			spliced := diSpliceSlice(arr, o.Index, o.Remove, o.Items)
			if len(path) == 0 {
				root = spliced
				if owned != nil {
					owned.add(spliced)
				}
			} else {
				var err error
				root, err = diAssignAtPath(root, path, spliced, owned)
				if err != nil {
					return nil, err
				}
			}
		case OpMove:
			targetVal := root
			if len(path) > 0 {
				var err error
				targetVal, err = diResolveContainer(root, path, path)
				if err != nil {
					return nil, err
				}
			}
			arr, ok := targetVal.([]any)
			if !ok || arr == nil || len(arr) != len(o.Permutation) {
				return nil, NewPathError(PathRef{Path: path})
			}
			prev := slices.Clone(arr)
			for i, srcIdx := range o.Permutation {
				arr[i] = prev[srcIdx]
			}
		case OpSet, OpDelete, OpAppend, OpTruncate:
			parent, key, err := diResolveParent(root, path)
			if err != nil {
				return nil, err
			}
			if arr, isArr := parent.([]any); isArr {
				idx, ok := key.(SegNumber)
				if !ok {
					return nil, NewUnsafePathError(key)
				}
				if int(idx) > len(arr) {
					return nil, NewUnsafePathError(idx)
				}
				switch o := op.(type) {
				case OpSet:
					root, err = diAssignAtPath(root, path, o.Value, owned)
					if err != nil {
						return nil, err
					}
				case OpDelete:
					if int(idx) >= len(arr) {
						return nil, NewPathError(PathRef{Path: path})
					}
					spliced := diSpliceSlice(arr, int(idx), 1, nil)
					root, err = diAssignAtPath(root, path[:len(path)-1], spliced, owned)
					if err != nil {
						return nil, err
					}
				case OpAppend:
					if int(idx) >= len(arr) {
						return nil, NewPathError(PathRef{Path: path})
					}
					cur, ok := arr[idx].(string)
					if !ok {
						return nil, NewPathError(PathRef{Path: path})
					}
					arr[idx] = cur + o.Text
				case OpTruncate:
					if int(idx) >= len(arr) {
						return nil, NewPathError(PathRef{Path: path})
					}
					cur, ok := arr[idx].(string)
					if !ok {
						return nil, NewPathError(PathRef{Path: path})
					}
					arr[idx] = js.SliceFrom(cur, o.Count)
				}
			} else if obj, isObj := parent.(*jsonx.Object); isObj {
				keyStr := diSegString(key)
				switch o := op.(type) {
				case OpSet:
					obj.Set(keyStr, o.Value)
				case OpDelete:
					obj.Delete(keyStr)
				case OpAppend:
					curAny, exists := obj.Get(keyStr)
					if !exists {
						return nil, NewPathError(PathRef{Path: path})
					}
					cur, ok := curAny.(string)
					if !ok {
						return nil, NewPathError(PathRef{Path: path})
					}
					obj.Set(keyStr, cur+o.Text)
				case OpTruncate:
					curAny, exists := obj.Get(keyStr)
					if !exists {
						return nil, NewPathError(PathRef{Path: path})
					}
					cur, ok := curAny.(string)
					if !ok {
						return nil, NewPathError(PathRef{Path: path})
					}
					obj.Set(keyStr, js.SliceFrom(cur, o.Count))
				}
			} else {
				return nil, NewPathError(PathRef{Path: path})
			}
		}
	}
	return root, nil
}

func diCopyContainers(root any, path Path, owned *diOwnedSet) (any, error) {
	if !diIsContainer(root) {
		return nil, NewPathError(PathRef{Path: path})
	}
	copiedRoot := root
	if !owned.has(root) {
		copiedRoot = diShallowCopy(root)
		owned.add(copiedRoot)
	}
	destination := copiedRoot
	for _, seg := range path {
		var child any
		switch d := destination.(type) {
		case []any:
			switch s := seg.(type) {
			case SegNumber:
				if s < 0 || int(s) >= len(d) {
					return nil, NewPathError(PathRef{Path: path})
				}
				child = d[s]
			case SegString:
				// Match TS copyContainers: check hasOwn before rejecting string segments on arrays.
				str := string(s)
				if str == "length" {
					return nil, NewUnsafePathError(seg)
				}
				if idx, convErr := strconv.Atoi(str); convErr == nil && idx >= 0 && idx < len(d) && strconv.Itoa(idx) == str {
					return nil, NewUnsafePathError(seg)
				}
				return nil, NewPathError(PathRef{Path: path})
			default:
				return nil, NewPathError(PathRef{Path: path})
			}
		case *jsonx.Object:
			var exists bool
			child, exists = d.Get(diSegString(seg))
			if !exists {
				return nil, NewPathError(PathRef{Path: path})
			}
		default:
			return nil, NewPathError(PathRef{Path: path})
		}
		if !diIsContainer(child) {
			return nil, NewPathError(PathRef{Path: path})
		}
		if owned.has(child) {
			destination = child
			continue
		}
		copiedChild := diShallowCopy(child)
		switch d := destination.(type) {
		case []any:
			d[seg.(SegNumber)] = copiedChild
		case *jsonx.Object:
			d.Set(diSegString(seg), copiedChild)
		}
		owned.add(copiedChild)
		destination = copiedChild
	}
	return copiedRoot, nil
}

func diShallowCopy(v any) any {
	switch c := v.(type) {
	case []any:
		out := make([]any, len(c))
		copy(out, c)
		return out
	case *jsonx.Object:
		out := jsonx.NewObject()
		for k, val := range c.All() {
			out.Set(k, val)
		}
		return out
	default:
		return v
	}
}

func diCastResult[T any](v any) (T, error) {
	if v == nil {
		var zero T
		rv := reflect.ValueOf(&zero).Elem()
		switch rv.Kind() {
		case reflect.Interface, reflect.Ptr, reflect.Slice, reflect.Map, reflect.Func, reflect.Chan:
			return zero, nil
		default:
			return zero, errors.New("delta apply: result is nil")
		}
	}
	out, ok := v.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("delta apply: result type %T does not match target type %T", v, zero)
	}
	return out, nil
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
