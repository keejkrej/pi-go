// Ported from packages/durable/src/testing/assertions.ts (pi v1.0.0).

package durabletest

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// TestingT is the minimal testing.TB subset needed by CreateTestingAssertions.
type TestingT interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	FailNow()
}

type dtaAssertions struct {
	t TestingT
}

// CreateTestingAssertions adapts a testing.TB (such as *testing.T) into StorageConformanceAssertions.
func CreateTestingAssertions(t TestingT) StorageConformanceAssertions {
	return &dtaAssertions{t: t}
}

func (a *dtaAssertions) Ok(value bool, message ...string) {
	a.t.Helper()
	if !value {
		if len(message) > 0 && message[0] != "" {
			a.t.Fatalf("%s", message[0])
			return
		}
		a.t.Fatalf("expected true, got false")
	}
}

func (a *dtaAssertions) StrictEqual(actual any, expected any) {
	a.t.Helper()
	if dtaSameValue(actual, expected) {
		return
	}
	a.t.Fatalf("strictEqual mismatch:\n  got:  %s\n  want: %s", dtaFormat(actual), dtaFormat(expected))
}

func (a *dtaAssertions) DeepEqual(actual any, expected any) {
	a.t.Helper()
	if dtaDeepEqual(actual, expected) {
		return
	}
	a.t.Fatalf("deepEqual mismatch:\n  got:  %s\n  want: %s", dtaFormat(actual), dtaFormat(expected))
}

func (a *dtaAssertions) PartialDeepEqual(actual any, expected any) {
	a.t.Helper()
	if reason := dtaPartialMatch(actual, expected); reason != "" {
		a.t.Fatalf("partialDeepEqual mismatch (%s):\n  got:  %s\n  want: %s", reason, dtaFormat(actual), dtaFormat(expected))
	}
}

func (a *dtaAssertions) GreaterThan(actual int64, expected int64) {
	a.t.Helper()
	if actual <= expected {
		a.t.Fatalf("expected %d > %d", actual, expected)
	}
}

func (a *dtaAssertions) Rejects(operation func() error, messageIncludes string) {
	a.t.Helper()
	err := operation()
	if err == nil {
		a.t.Fatalf("expected error containing %q, got nil", messageIncludes)
		return
	}
	if messageIncludes != "" && !strings.Contains(err.Error(), messageIncludes) {
		a.t.Fatalf("expected error containing %q, got %q", messageIncludes, err.Error())
	}
}

func dtaIsNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Slice, reflect.Map, reflect.Interface, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

func dtaSameValue(actual, expected any) bool {
	if dtaIsNil(actual) && dtaIsNil(expected) {
		return true
	}
	if dtaIsNil(actual) || dtaIsNil(expected) {
		return false
	}
	ra := reflect.ValueOf(actual)
	re := reflect.ValueOf(expected)
	if ra.Type() == re.Type() && ra.Type().Comparable() {
		return actual == expected
	}
	return dtaDeepEqual(actual, expected)
}

func dtaDeepEqual(actual, expected any) bool {
	if dtaIsNil(actual) && dtaIsNil(expected) {
		return true
	}
	if dtaIsNil(actual) || dtaIsNil(expected) {
		return false
	}
	if reflect.DeepEqual(actual, expected) {
		return true
	}
	if objA, okA := actual.(*jsonx.Object); okA {
		if objB, okB := expected.(*jsonx.Object); okB {
			return objA.Equal(objB)
		}
	}
	return jsonx.DeepEqual(actual, expected)
}

func dtaPartialMatch(actual, expected any) string {
	if dtaIsNil(expected) {
		if dtaIsNil(actual) {
			return ""
		}
		return "expected nil"
	}
	if dtaIsNil(actual) {
		return "actual is nil"
	}

	if expObj, ok := expected.(*jsonx.Object); ok {
		actObj, okAct := actual.(*jsonx.Object)
		if !okAct {
			encoded, err := jsonx.Encode(actual)
			if err != nil {
				return fmt.Sprintf("cannot encode actual (%T): %v", actual, err)
			}
			actObj, okAct = encoded.(*jsonx.Object)
			if !okAct {
				return fmt.Sprintf("actual %T is not an object", actual)
			}
		}
		if actObj == nil {
			return "actual object is nil"
		}
		for _, k := range expObj.Keys() {
			expV, _ := expObj.Get(k)
			actV, has := actObj.Get(k)
			if !has {
				return fmt.Sprintf("missing key %q", k)
			}
			if reason := dtaPartialMatch(actV, expV); reason != "" {
				return fmt.Sprintf("key %q: %s", k, reason)
			}
		}
		return ""
	}

	encActual, errA := jsonx.Encode(actual)
	encExpected, errE := jsonx.Encode(expected)
	if errA == nil && errE == nil {
		if _, isObjE := encExpected.(*jsonx.Object); isObjE {
			return dtaPartialMatch(encActual, encExpected)
		}
		if jsonx.DeepEqual(encActual, encExpected) {
			return ""
		}
	}
	if dtaDeepEqual(actual, expected) {
		return ""
	}
	return "values differ"
}

func dtaFormat(v any) string {
	if dtaIsNil(v) {
		return "nil"
	}
	if s, err := jsonx.Stringify(v); err == nil {
		return s
	}
	return fmt.Sprintf("%#v", v)
}
