// Ported from packages/durable/src/testing/types.ts (pi v1.0.0).

package durabletest

import "github.com/keejkrej/pi-go/durable"

// StorageConformanceAssertions defines assertion hooks required by CreateStorageConformance.
type StorageConformanceAssertions interface {
	Ok(value bool, message ...string)
	StrictEqual(actual any, expected any)
	DeepEqual(actual any, expected any)
	PartialDeepEqual(actual any, expected any)
	GreaterThan(actual int64, expected int64)
	Rejects(operation func() error, messageIncludes string)
}

// StorageConformanceProvider constructs a fresh Storage for one conformance case and tears it down afterwards.
type StorageConformanceProvider func(use func(storage durable.Storage) error) error

// StorageConformanceOptions configures CreateStorageConformance.
type StorageConformanceOptions struct {
	Assertions  StorageConformanceAssertions
	WithStorage StorageConformanceProvider
}

// StorageConformanceCase represents a single storage conformance test case.
type StorageConformanceCase struct {
	Name string
	Run  func() error
}
