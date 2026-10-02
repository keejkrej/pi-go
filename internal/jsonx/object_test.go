package jsonx_test

import (
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

func TestObject_PropertyOrderMatchesJS(t *testing.T) {
	// const o = {}; o.z = 1; o["10"] = 2; o["2"] = 3; o.a = 4; o["2"] = 5; delete o.z; o.z = 6;
	// JSON.stringify(o) === '{"2":5,"10":2,"a":4,"z":6}'
	o := jsonx.NewObject()
	o.Set("z", 1)
	o.Set("10", 2)
	o.Set("2", 3)
	o.Set("a", 4)
	o.Set("2", 5)
	if !o.Delete("z") {
		t.Fatal("Delete(z) = false")
	}
	o.Set("z", 6)
	if got := jsonx.MustStringify(o); got != `{"2":5,"10":2,"a":4,"z":6}` {
		t.Fatalf("got %s", got)
	}
	if diff := cmp.Diff([]string{"2", "10", "a", "z"}, o.Keys()); diff != "" {
		t.Fatal(diff)
	}
}

func TestObject_ArrayIndexKeys(t *testing.T) {
	// Object.keys of {a, "4294967295", "4294967294", "1", "01", "-1", "9007199254740993", "0"}
	// in node: ['0','1','4294967294','a','4294967295','01','-1','9007199254740993']
	o := &jsonx.Object{}
	for _, k := range []string{"a", "4294967295", "4294967294", "1", "01", "-1", "9007199254740993", "0"} {
		o.Set(k, true)
	}
	want := []string{"0", "1", "4294967294", "a", "4294967295", "01", "-1", "9007199254740993"}
	if diff := cmp.Diff(want, o.Keys()); diff != "" {
		t.Fatal(diff)
	}
	for k, want := range map[string]bool{"0": true, "4294967294": true, "4294967295": false, "01": false, "": false, "1a": false, "-0": false, "123": true} {
		if got := jsonx.IsArrayIndex(k); got != want {
			t.Errorf("IsArrayIndex(%q) = %v", k, got)
		}
	}
	if diff := cmp.Diff([]string{"1", "5", "b", "a", "07"}, jsonx.OrderKeys([]string{"b", "5", "a", "1", "07"})); diff != "" {
		t.Fatal(diff)
	}
}

func TestObject_GetHasDeleteLen(t *testing.T) {
	var zero jsonx.Object
	if zero.Len() != 0 || zero.Has("a") {
		t.Fatal("zero value not empty")
	}
	zero.Set("a", "x")
	if v, ok := zero.Get("a"); !ok || v != "x" {
		t.Fatalf("Get = %v %v", v, ok)
	}
	if _, ok := zero.Get("b"); ok {
		t.Fatal("Get(b) found")
	}
	if zero.Delete("b") {
		t.Fatal("Delete(b) = true")
	}
	var nilObj *jsonx.Object
	if nilObj.Len() != 0 || nilObj.Has("a") || len(nilObj.Keys()) != 0 {
		t.Fatal("nil object not empty")
	}
	if _, ok := nilObj.Get("a"); ok {
		t.Fatal("nil Get found")
	}
	// Numbers are stored as float64 like JS numbers.
	zero.Set("n", 3)
	if v, _ := zero.Get("n"); v != float64(3) {
		t.Fatalf("Set(int) stored %T", v)
	}
	if n, ok := zero.GetNumber("n"); !ok || n != 3 {
		t.Fatal("GetNumber")
	}
	if s, ok := zero.GetString("a"); !ok || s != "x" {
		t.Fatal("GetString")
	}
	if _, ok := zero.GetBool("a"); ok {
		t.Fatal("GetBool on string")
	}
}

func TestObject_LargeObjectsKeepOrderAcrossDeletes(t *testing.T) {
	o := jsonx.NewObject()
	var ref []string
	for i := 0; i < 50; i++ {
		k := "k" + strconv.Itoa(i)
		o.Set(k, float64(i))
		ref = append(ref, k)
		if i%7 == 3 {
			idx := strconv.Itoa(100 - i)
			o.Set(idx, float64(i))
		}
	}
	for _, i := range []int{0, 49, 25, 10, 11, 12} {
		k := "k" + strconv.Itoa(i)
		if !o.Delete(k) {
			t.Fatalf("Delete(%s) = false", k)
		}
		ref = slices.DeleteFunc(ref, func(s string) bool { return s == k })
	}
	o.Set("k25", "again")
	ref = append(ref, "k25")
	got := o.Keys()
	var named []string
	for _, k := range got {
		if !jsonx.IsArrayIndex(k) {
			named = append(named, k)
		}
	}
	if diff := cmp.Diff(ref, named); diff != "" {
		t.Fatal(diff)
	}
	for _, k := range ref {
		if !o.Has(k) {
			t.Fatalf("missing %s", k)
		}
	}
	if v, _ := o.Get("k25"); v != "again" {
		t.Fatalf("k25 = %v", v)
	}
	// Index keys come first and ascend.
	if got[0] != "55" || got[6] != "97" {
		t.Fatalf("first key %s", got[0])
	}
}

func TestObject_StructureIsAFunctionOfEntries(t *testing.T) {
	// reflect.DeepEqual must not depend on lookup history.
	a, b := jsonx.NewObject(), jsonx.NewObject()
	for i := 0; i < 20; i++ {
		a.Set("k"+strconv.Itoa(i), float64(i))
		b.Set("k"+strconv.Itoa(i), float64(i))
	}
	a.Get("k3")
	a.Has("missing")
	a.Delete("k19")
	b.Delete("k19")
	if !reflect.DeepEqual(a, b) {
		t.Fatal("objects with equal entries are not DeepEqual")
	}
	a.Delete("k4")
	a.Set("k4", float64(4))
	b.Delete("k4")
	b.Set("k4", float64(4))
	if !reflect.DeepEqual(a, b) {
		t.Fatal("objects with equal entries are not DeepEqual after delete")
	}
}

func TestObject_CloneIsDeep(t *testing.T) {
	v, err := jsonx.ParseString(`{"a":{"b":[1,{"c":2}]},"d":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*jsonx.Object)
	c := o.Clone()
	inner, _ := c.GetObject("a")
	arr, _ := inner.GetArray("b")
	arr[1].(*jsonx.Object).Set("c", 3)
	inner.Set("new", true)
	if got := jsonx.MustStringify(o); got != `{"a":{"b":[1,{"c":2}]},"d":"x"}` {
		t.Fatalf("original changed: %s", got)
	}
	if got := jsonx.MustStringify(c); got != `{"a":{"b":[1,{"c":3}],"new":true},"d":"x"}` {
		t.Fatalf("clone: %s", got)
	}
}

func TestObject_AssignAndObjectOf(t *testing.T) {
	base := jsonx.ObjectOf("type", "header", "version", 3)
	extra := jsonx.ObjectOf("id", "abc", "version", 4, "0", "first")
	base.Assign(extra)
	if got := jsonx.MustStringify(base); got != `{"0":"first","type":"header","version":4,"id":"abc"}` {
		t.Fatalf("got %s", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("ObjectOf with odd args did not panic")
		}
	}()
	jsonx.ObjectOf("a")
}

func TestObject_EqualIgnoresKeyOrder(t *testing.T) {
	a, _ := jsonx.ParseString(`{"a":1,"b":{"x":[1,2,{"y":null}]}}`)
	b, _ := jsonx.ParseString(`{"b":{"x":[1,2,{"y":null}]},"a":1}`)
	c, _ := jsonx.ParseString(`{"b":{"x":[1,2,{"y":0}]},"a":1}`)
	if !a.(*jsonx.Object).Equal(b.(*jsonx.Object)) {
		t.Fatal("a != b")
	}
	if a.(*jsonx.Object).Equal(c.(*jsonx.Object)) {
		t.Fatal("a == c")
	}
	if diff := cmp.Diff(a, b); diff != "" {
		t.Fatalf("cmp.Diff uses Equal: %s", diff)
	}
	if cmp.Equal(a, c) {
		t.Fatal("cmp.Equal(a, c)")
	}
	if !jsonx.DeepEqual(float64(1), 1) || jsonx.DeepEqual(1, "1") || !jsonx.DeepEqual(nil, nil) {
		t.Fatal("DeepEqual scalars")
	}
}

func TestObject_AllIteratesInOrder(t *testing.T) {
	o := jsonx.ObjectOf("b", 1, "1", 2, "a", 3)
	var keys []string
	var vals []any
	for k, v := range o.All() {
		keys = append(keys, k)
		vals = append(vals, v)
	}
	if diff := cmp.Diff([]string{"1", "b", "a"}, keys); diff != "" {
		t.Fatal(diff)
	}
	if diff := cmp.Diff([]any{2.0, 1.0, 3.0}, vals); diff != "" {
		t.Fatal(diff)
	}
	if diff := cmp.Diff([]any{2.0, 1.0, 3.0}, o.Values()); diff != "" {
		t.Fatal(diff)
	}
	for k := range o.All() {
		if k == "1" {
			break
		}
	}
}

func TestObject_JSONInterfaces(t *testing.T) {
	var o jsonx.Object
	if err := o.UnmarshalJSON([]byte(`{"b":1,"0":2}`)); err != nil {
		t.Fatal(err)
	}
	b, err := o.MarshalJSON()
	if err != nil || string(b) != `{"0":2,"b":1}` {
		t.Fatalf("MarshalJSON = %s %v", b, err)
	}
	if err := o.UnmarshalJSON([]byte(`[1]`)); err == nil || err.Error() != "json: cannot unmarshal array into Go value of type jsonx.Object" {
		t.Fatalf("UnmarshalJSON(array) = %v", err)
	}
	if err := o.UnmarshalJSON([]byte(`null`)); err != nil || o.Len() != 2 {
		t.Fatalf("UnmarshalJSON(null) changed object: %v", err)
	}
	if err := o.UnmarshalJSON([]byte(`{`)); err == nil || err.Error() != "Expected property name or '}' in JSON at position 1 (line 1 column 2)" {
		t.Fatalf("UnmarshalJSON({) = %v", err)
	}
	if o.String() != `{"0":2,"b":1}` {
		t.Fatalf("String() = %s", o.String())
	}
}
