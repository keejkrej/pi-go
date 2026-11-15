package core

import (
	"os"
	"strings"
	"sync"
	"testing"
)

func TestOutputAccumulator_StreamingTruncationAndConcurrency(t *testing.T) {
	t.Run("decodes UTF-8 split across chunks without temp file", func(t *testing.T) {
		acc := NewOutputAccumulator(&OutputAccumulatorOptions{
			TempFilePrefix: "pi-acc-test",
		})
		euro := []byte("€") // 0xe2, 0x82, 0xac
		if err := acc.Append(euro[:1]); err != nil {
			t.Fatal(err)
		}
		if err := acc.Append(euro[1:]); err != nil {
			t.Fatal(err)
		}
		acc.Finish()

		snap := acc.Snapshot(&SnapshotOptions{PersistIfTruncated: true})
		if snap.Content != "€" || snap.Truncation.Truncated || snap.FullOutputPath != nil {
			t.Fatalf("unexpected snapshot: %+v", snap)
		}
		if err := acc.CloseTempFile(); err != nil {
			t.Fatal(err)
		}
		full, err := acc.ReadFullOutput(1024)
		if err != nil || full.Truncated || full.Content != "€" {
			t.Fatalf("unexpected ReadFullOutput: (%+v, %v)", full, err)
		}
	})

	t.Run("rejects append after finish", func(t *testing.T) {
		acc := NewOutputAccumulator()
		acc.Finish()
		if err := acc.Append([]byte("late")); err == nil {
			t.Fatalf("expected error appending after Finish")
		}
	})

	t.Run("spills to temp file and truncates large ReadFullOutput cleanly", func(t *testing.T) {
		maxLines := 3
		maxBytes := 40
		acc := NewOutputAccumulator(&OutputAccumulatorOptions{
			MaxLines:       &maxLines,
			MaxBytes:       &maxBytes,
			TempFilePrefix: "pi-acc-spill",
		})
		var payload strings.Builder
		for i := 0; i < 10; i++ {
			payload.WriteString("line content here!\n")
		}
		raw := payload.String()
		if err := acc.Append([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		acc.Finish()
		snap := acc.Snapshot(&SnapshotOptions{PersistIfTruncated: true})
		if err := acc.CloseTempFile(); err != nil {
			t.Fatal(err)
		}
		if snap.FullOutputPath != nil {
			defer os.Remove(*snap.FullOutputPath)
		}

		if !snap.Truncation.Truncated || snap.FullOutputPath == nil {
			t.Fatalf("expected truncated snapshot with temp file, got %+v", snap)
		}
		if snap.Truncation.TotalLines != 10 {
			t.Fatalf("TotalLines=%d, want 10", snap.Truncation.TotalLines)
		}

		full, err := acc.ReadFullOutput(1024)
		if err != nil || full.Truncated || full.Content != raw {
			t.Fatalf("ReadFullOutput(1024)=(%+v, %v)", full, err)
		}

		bounded, err := acc.ReadFullOutput(20)
		if err != nil || !bounded.Truncated || !strings.Contains(bounded.Content, "bytes omitted") {
			t.Fatalf("ReadFullOutput(20)=(%+v, %v)", bounded, err)
		}
	})

	t.Run("concurrent appends and snapshots are race free", func(t *testing.T) {
		acc := NewOutputAccumulator(&OutputAccumulatorOptions{TempFilePrefix: "pi-acc-race"})
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					_ = acc.Append([]byte("chunk line\n"))
					_ = acc.Snapshot()
					_ = acc.GetLastLineBytes()
				}
			}()
		}
		wg.Wait()
		acc.Finish()
		_ = acc.CloseTempFile()
	})
}
