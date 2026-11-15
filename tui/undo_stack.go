// Ported from packages/tui/src/undo-stack.ts (pi v1.0.0).

package tui

import (
	"reflect"
	"unsafe"
)

type usCloneKey struct {
	typ reflect.Type
	ptr uintptr
}

func usDeepClone[S any](state S) S {
	if cloner, ok := any(state).(interface{ Clone() S }); ok {
		return cloner.Clone()
	}
	v := reflect.ValueOf(&state).Elem()
	if !v.IsValid() {
		return state
	}
	visited := map[usCloneKey]reflect.Value{}
	cloned := usCloneValue(v, visited)
	return cloned.Interface().(S)
}

func usCloneValue(v reflect.Value, visited map[usCloneKey]reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		key := usCloneKey{typ: v.Type(), ptr: v.Pointer()}
		if existing, ok := visited[key]; ok {
			return existing
		}
		dst := reflect.New(v.Type().Elem())
		visited[key] = dst
		dst.Elem().Set(usCloneValue(v.Elem(), visited))
		return dst
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		dst := reflect.New(v.Type()).Elem()
		dst.Set(usCloneValue(v.Elem(), visited))
		return dst
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		dst := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			dst.Index(i).Set(usCloneValue(v.Index(i), visited))
		}
		return dst
	case reflect.Array:
		dst := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			dst.Index(i).Set(usCloneValue(v.Index(i), visited))
		}
		return dst
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		key := usCloneKey{typ: v.Type(), ptr: v.Pointer()}
		if existing, ok := visited[key]; ok {
			return existing
		}
		dst := reflect.MakeMapWithSize(v.Type(), v.Len())
		visited[key] = dst
		iter := v.MapRange()
		for iter.Next() {
			k := usCloneValue(iter.Key(), visited)
			val := usCloneValue(iter.Value(), visited)
			dst.SetMapIndex(k, val)
		}
		return dst
	case reflect.Struct:
		dst := reflect.New(v.Type()).Elem()
		srcCopy := reflect.New(v.Type()).Elem()
		srcCopy.Set(v)
		for i := 0; i < v.NumField(); i++ {
			df := dst.Field(i)
			sf := srcCopy.Field(i)
			if df.CanSet() {
				df.Set(usCloneValue(sf, visited))
			} else {
				// Copy unexported fields (such as ceEditorSnapshot.lines) like TS structuredClone.
				rf := reflect.NewAt(df.Type(), unsafe.Pointer(df.UnsafeAddr())).Elem()
				rsf := reflect.NewAt(sf.Type(), unsafe.Pointer(sf.UnsafeAddr())).Elem()
				rf.Set(usCloneValue(rsf, visited))
			}
		}
		return dst
	default:
		return v
	}
}

// UndoStack stores deep clones of state snapshots.
// Pop returns the stored clone directly, without cloning it again.
type UndoStack[S any] struct {
	stack []S
}

// NewUndoStack returns an empty stack.
func NewUndoStack[S any]() *UndoStack[S] {
	return &UndoStack[S]{stack: []S{}}
}

// Push stores a deep clone of state, matching structuredClone.
func (s *UndoStack[S]) Push(state S) {
	s.stack = append(s.stack, usDeepClone(state))
}

// Pop returns the most recent snapshot, or nil when the stack is empty.
func (s *UndoStack[S]) Pop() *S {
	if len(s.stack) == 0 {
		return nil
	}
	top := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	return &top
}

// Clear removes every snapshot.
func (s *UndoStack[S]) Clear() {
	s.stack = s.stack[:0]
}

// Length returns the number of snapshots.
func (s *UndoStack[S]) Length() int {
	return len(s.stack)
}
