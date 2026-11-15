// Ported from packages/durable/src/testing/runner.ts (pi v1.0.0).

package durabletest

import (
	"sync"
	"testing"
)

type dtrSubtestAssertions struct {
	mu      sync.RWMutex
	current TestingT
}

func (a *dtrSubtestAssertions) setTarget(t TestingT) {
	a.mu.Lock()
	a.current = t
	a.mu.Unlock()
}

func (a *dtrSubtestAssertions) target() TestingT {
	a.mu.RLock()
	t := a.current
	a.mu.RUnlock()
	return t
}

func (a *dtrSubtestAssertions) Ok(value bool, message ...string) {
	t := a.target()
	t.Helper()
	CreateTestingAssertions(t).Ok(value, message...)
}

func (a *dtrSubtestAssertions) StrictEqual(actual any, expected any) {
	t := a.target()
	t.Helper()
	CreateTestingAssertions(t).StrictEqual(actual, expected)
}

func (a *dtrSubtestAssertions) DeepEqual(actual any, expected any) {
	t := a.target()
	t.Helper()
	CreateTestingAssertions(t).DeepEqual(actual, expected)
}

func (a *dtrSubtestAssertions) PartialDeepEqual(actual any, expected any) {
	t := a.target()
	t.Helper()
	CreateTestingAssertions(t).PartialDeepEqual(actual, expected)
}

func (a *dtrSubtestAssertions) GreaterThan(actual int64, expected int64) {
	t := a.target()
	t.Helper()
	CreateTestingAssertions(t).GreaterThan(actual, expected)
}

func (a *dtrSubtestAssertions) Rejects(operation func() error, messageIncludes string) {
	t := a.target()
	t.Helper()
	CreateTestingAssertions(t).Rejects(operation, messageIncludes)
}

// RegisterStorageConformance runs the full StorageConformance suite as subtests of t.
func RegisterStorageConformance(t *testing.T, name string, withStorage StorageConformanceProvider) {
	t.Helper()
	adapter := &dtrSubtestAssertions{current: t}
	cases := CreateStorageConformance(StorageConformanceOptions{
		Assertions:  adapter,
		WithStorage: withStorage,
	})
	t.Run(name, func(t *testing.T) {
		for _, tc := range cases {
			t.Run(tc.Name, func(t *testing.T) {
				adapter.setTarget(t)
				if err := tc.Run(); err != nil {
					t.Fatalf("%v", err)
				}
			})
		}
	})
}
