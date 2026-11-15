// Ported from packages/durable/test/storage.bench.ts (pi v1.0.0).

package durable_test

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/durabletest"
	"github.com/keejkrej/pi-go/durable/storage"
	"github.com/keejkrej/pi-go/durable/storage/jsonl"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
)

func sbtCreateStorage(tb testing.TB, backend string) durable.Storage {
	tb.Helper()
	ctx := chord.BackgroundContext
	switch backend {
	case "memory":
		return storage.NewMemoryStorage()
	case "sqlite":
		dir := tb.TempDir()
		st, err := sqlite.OpenNodeSqliteStorage(filepath.Join(dir, "storage.sqlite"))
		if err != nil {
			tb.Fatalf("OpenNodeSqliteStorage failed: %v", err)
		}
		tb.Cleanup(func() { _ = st.Close(ctx) })
		return st
	case "jsonl":
		dir := filepath.Join(tb.TempDir(), "storage")
		st, err := jsonl.OpenNodeJsonlStorage(dir, ctx)
		if err != nil {
			tb.Fatalf("OpenNodeJsonlStorage failed: %v", err)
		}
		tb.Cleanup(func() { _ = st.Close(ctx) })
		return st
	default:
		tb.Fatalf("unknown backend %q", backend)
		return nil
	}
}

func sbtCopyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		sp := filepath.Join(src, entry.Name())
		dp := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := sbtCopyDir(sp, dp); err != nil {
				return err
			}
			continue
		}
		in, err := os.Open(sp)
		if err != nil {
			return err
		}
		out, err := os.Create(dp)
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		_ = in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func sbtCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func TestStorageBenchmarkScenarios(t *testing.T) {
	ctx := chord.BackgroundContext
	smallScale := durabletest.StorageBenchmarkScale{
		Name:          "small",
		EntryCount:    120,
		TaskCount:     36,
		DocumentCount: 36,
	}

	for _, backend := range []string{"memory", "sqlite", "jsonl"} {
		t.Run("reads/"+backend, func(t *testing.T) {
			st := sbtCreateStorage(t, backend)
			dataset, err := durabletest.SeedStorageBenchmark(st, smallScale)
			if err != nil {
				t.Fatalf("SeedStorageBenchmark failed: %v", err)
			}
			for _, scenario := range durabletest.StorageReadBenchmarks {
				got, err := scenario.Run(st, dataset)
				if err != nil {
					t.Fatalf("%s failed: %v", scenario.Name, err)
				}
				if want := scenario.Expected(dataset); got != want {
					t.Fatalf("%s: expected %d, got %d", scenario.Name, want, got)
				}
			}
		})

		t.Run("writes/"+backend, func(t *testing.T) {
			for _, scenario := range durabletest.StorageWriteBenchmarks {
				st := sbtCreateStorage(t, backend)
				if err := durabletest.SeedStorageWriteBenchmark(st); err != nil {
					t.Fatalf("SeedStorageWriteBenchmark failed: %v", err)
				}
				got, err := scenario.Run(st)
				if err != nil {
					t.Fatalf("%s failed: %v", scenario.Name, err)
				}
				if got != scenario.Expected {
					t.Fatalf("%s: expected %d, got %d", scenario.Name, scenario.Expected, got)
				}
			}
		})

		if backend != "memory" {
			t.Run("reopen/"+backend, func(t *testing.T) {
				seedDir := t.TempDir()
				seedPath := filepath.Join(seedDir, "storage.sqlite")
				if backend == "jsonl" {
					seedPath = filepath.Join(seedDir, "storage")
				}
				var seed durable.Storage
				var err error
				if backend == "sqlite" {
					seed, err = sqlite.OpenNodeSqliteStorage(seedPath)
				} else {
					seed, err = jsonl.OpenNodeJsonlStorage(seedPath, ctx)
				}
				if err != nil {
					t.Fatalf("open seed failed: %v", err)
				}
				dataset, err := durabletest.SeedStorageBenchmark(seed, smallScale)
				_ = seed.Close(ctx)
				if err != nil {
					t.Fatalf("seed failed: %v", err)
				}

				copyDir := t.TempDir()
				copyPath := filepath.Join(copyDir, filepath.Base(seedPath))
				if backend == "sqlite" {
					err = sbtCopyFile(seedPath, copyPath)
				} else {
					err = sbtCopyDir(seedPath, copyPath)
				}
				if err != nil {
					t.Fatalf("copy seed failed: %v", err)
				}
				var reopened durable.Storage
				if backend == "sqlite" {
					reopened, err = sqlite.OpenNodeSqliteStorage(copyPath)
				} else {
					reopened, err = jsonl.OpenNodeJsonlStorage(copyPath, ctx)
				}
				if err != nil {
					t.Fatalf("reopen failed: %v", err)
				}
				defer func() { _ = reopened.Close(ctx) }()
				entry, err := reopened.Entry(ctx, dataset.FirstEntryId)
				if err != nil || entry == nil || entry.Entry.Id != dataset.FirstEntryId {
					t.Fatalf("expected first entry %d, got %+v err=%v", dataset.FirstEntryId, entry, err)
				}
			})
		}
	}
}

func BenchmarkStorageReads(b *testing.B) {
	backends := []string{"memory", "sqlite", "jsonl"}
	stores := make(map[string]durable.Storage, len(backends))
	datasets := make(map[string]durabletest.StorageBenchmarkDataset, len(backends))
	for _, backend := range backends {
		st := sbtCreateStorage(b, backend)
		ds, err := durabletest.SeedStorageBenchmark(st)
		if err != nil {
			b.Fatalf("seed %s failed: %v", backend, err)
		}
		stores[backend] = st
		datasets[backend] = ds
	}
	for _, scenario := range durabletest.StorageReadBenchmarks {
		for _, backend := range backends {
			st := stores[backend]
			ds := datasets[backend]
			b.Run(fmt.Sprintf("%s/%s", scenario.Name, backend), func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := scenario.Run(st, ds); err != nil {
						b.Fatalf("run failed: %v", err)
					}
				}
			})
		}
	}
}

func BenchmarkStorageWrites(b *testing.B) {
	backends := []string{"memory", "sqlite", "jsonl"}
	for _, scenario := range durabletest.StorageWriteBenchmarks {
		for _, backend := range backends {
			b.Run(fmt.Sprintf("%s/%s", scenario.Name, backend), func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					st := sbtCreateStorage(b, backend)
					if err := durabletest.SeedStorageWriteBenchmark(st); err != nil {
						b.Fatalf("seed failed: %v", err)
					}
					b.StartTimer()
					if _, err := scenario.Run(st); err != nil {
						b.Fatalf("run failed: %v", err)
					}
				}
			})
		}
	}
}
