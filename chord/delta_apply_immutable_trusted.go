// Ported from packages/chord/src/delta/apply-immutable-trusted.ts (pi v1.0.0).

package chord

import (
	"slices"
	"unsafe"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type daitSliceKey struct {
	ptr *any
	len int
}

type daitOwnedSet struct {
	objects map[*jsonx.Object]struct{}
	slices  map[daitSliceKey]struct{}
}

func daitNewOwnedSet() *daitOwnedSet {
	return &daitOwnedSet{
		objects: make(map[*jsonx.Object]struct{}),
		slices:  make(map[daitSliceKey]struct{}),
	}
}

func (s *daitOwnedSet) has(v any) bool {
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
		_, ok := s.slices[daitSliceKey{ptr: unsafe.SliceData(c), len: len(c)}]
		return ok
	default:
		return false
	}
}

func (s *daitOwnedSet) add(v any) {
	switch c := v.(type) {
	case *jsonx.Object:
		if c != nil {
			s.objects[c] = struct{}{}
		}
	case []any:
		if len(c) > 0 {
			s.slices[daitSliceKey{ptr: unsafe.SliceData(c), len: len(c)}] = struct{}{}
		}
	}
}

// ApplyImmutableTrusted applies self-produced trusted operations while copying each touched container once.
func ApplyImmutableTrusted[T any](target T, operations OpList) (T, error) {
	root := any(target)
	owned := daitNewOwnedSet()
	for _, operation := range operations {
		if r, ok := operation.(OpReplace); ok {
			root = r.Value
			continue
		}
		switch op := operation.(type) {
		case OpSplice:
			copiedRoot, targetContainer, err := daitCopyPath(root, op.Path, len(op.Path), owned)
			if err != nil {
				var zero T
				return zero, err
			}
			root = copiedRoot
			arr, ok := targetContainer.([]any)
			if !ok || arr == nil {
				var zero T
				return zero, js.NewTypeError("Trusted array operation target is not an array")
			}
			spliced := diSpliceSlice(arr, op.Index, op.Remove, op.Items)
			owned.add(spliced)
			root, err = diAssignAtPath(root, op.Path, spliced, nil)
			if err != nil {
				var zero T
				return zero, err
			}
		case OpMove:
			copiedRoot, targetContainer, err := daitCopyPath(root, op.Path, len(op.Path), owned)
			if err != nil {
				var zero T
				return zero, err
			}
			root = copiedRoot
			arr, ok := targetContainer.([]any)
			if !ok || arr == nil {
				var zero T
				return zero, js.NewTypeError("Trusted array operation target is not an array")
			}
			if len(arr) != len(op.Permutation) {
				var zero T
				return zero, js.NewTypeError("Trusted permutation length mismatch")
			}
			prev := slices.Clone(arr)
			for i, idx := range op.Permutation {
				arr[i] = prev[idx]
			}
		case OpSet:
			copiedRoot, targetContainer, err := daitCopyPath(root, op.Path, len(op.Path)-1, owned)
			if err != nil {
				var zero T
				return zero, err
			}
			root = copiedRoot
			key := op.Path[len(op.Path)-1]
			switch c := targetContainer.(type) {
			case []any:
				idx, ok := key.(SegNumber)
				if !ok {
					var zero T
					return zero, js.NewTypeError("Trusted array key is not numeric")
				}
				if int(idx) == len(c) {
					resized := make([]any, len(c)+1)
					copy(resized, c)
					resized[len(c)] = op.Value
					owned.add(resized)
					root, err = diAssignAtPath(root, op.Path[:len(op.Path)-1], resized, nil)
					if err != nil {
						var zero T
						return zero, err
					}
				} else {
					c[idx] = op.Value
				}
			case *jsonx.Object:
				c.Set(diSegString(key), op.Value)
			}
		case OpDelete:
			copiedRoot, targetContainer, err := daitCopyPath(root, op.Path, len(op.Path)-1, owned)
			if err != nil {
				var zero T
				return zero, err
			}
			root = copiedRoot
			key := op.Path[len(op.Path)-1]
			switch c := targetContainer.(type) {
			case []any:
				idx, ok := key.(SegNumber)
				if !ok {
					var zero T
					return zero, js.NewTypeError("Trusted array deletion key is not numeric")
				}
				spliced := diSpliceSlice(c, int(idx), 1, nil)
				owned.add(spliced)
				root, err = diAssignAtPath(root, op.Path[:len(op.Path)-1], spliced, nil)
				if err != nil {
					var zero T
					return zero, err
				}
			case *jsonx.Object:
				c.Delete(diSegString(key))
			}
		case OpAppend:
			copiedRoot, targetContainer, err := daitCopyPath(root, op.Path, len(op.Path)-1, owned)
			if err != nil {
				var zero T
				return zero, err
			}
			root = copiedRoot
			key := op.Path[len(op.Path)-1]
			curVal, ok := daitRead(targetContainer, key)
			cur, isStr := curVal.(string)
			if !ok || !isStr {
				var zero T
				return zero, js.NewTypeError("Trusted append target is not a string")
			}
			if err := daitWrite(targetContainer, key, cur+op.Text); err != nil {
				var zero T
				return zero, err
			}
		case OpTruncate:
			copiedRoot, targetContainer, err := daitCopyPath(root, op.Path, len(op.Path)-1, owned)
			if err != nil {
				var zero T
				return zero, err
			}
			root = copiedRoot
			key := op.Path[len(op.Path)-1]
			curVal, ok := daitRead(targetContainer, key)
			cur, isStr := curVal.(string)
			if !ok || !isStr {
				var zero T
				return zero, js.NewTypeError("Trusted truncate target is not a string")
			}
			if err := daitWrite(targetContainer, key, js.SliceFrom(cur, op.Count)); err != nil {
				var zero T
				return zero, err
			}
		default:
			var zero T
			return zero, js.NewTypeError("Unsupported trusted operation")
		}
	}
	return diCastResult[T](root)
}

func daitCopyPath(root any, path Path, length int, owned *daitOwnedSet) (any, any, error) {
	if !diIsContainer(root) {
		return nil, nil, js.NewTypeError("Trusted operation root is not a container")
	}
	copiedRoot := root
	if !owned.has(root) {
		copiedRoot = diShallowCopy(root)
		owned.add(copiedRoot)
	}
	destination := copiedRoot
	for index := 0; index < length; index++ {
		seg := path[index]
		child, ok := daitRead(destination, seg)
		if !ok || !diIsContainer(child) {
			return nil, nil, js.NewTypeError("Trusted operation path is not a container")
		}
		if owned.has(child) {
			destination = child
			continue
		}
		copied := diShallowCopy(child)
		if err := daitWrite(destination, seg, copied); err != nil {
			return nil, nil, err
		}
		owned.add(copied)
		destination = copied
	}
	return copiedRoot, destination, nil
}

func daitRead(target any, key Seg) (any, bool) {
	switch c := target.(type) {
	case []any:
		idx, ok := key.(SegNumber)
		if !ok || idx < 0 || int(idx) >= len(c) {
			return nil, false
		}
		return c[idx], true
	case *jsonx.Object:
		return c.Get(diSegString(key))
	default:
		return nil, false
	}
}

func daitWrite(target any, key Seg, value any) error {
	switch c := target.(type) {
	case []any:
		idx, ok := key.(SegNumber)
		if !ok || idx < 0 || int(idx) >= len(c) {
			return js.NewTypeError("Trusted array index out of bounds")
		}
		c[idx] = value
		return nil
	case *jsonx.Object:
		c.Set(diSegString(key), value)
		return nil
	default:
		return js.NewTypeError("Trusted operation target is not a container")
	}
}
