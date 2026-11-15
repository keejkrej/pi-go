package ai

import (
	"crypto/rand"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var testUUIDv7RE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func testParseUUIDv7Timestamp(t *testing.T, id string) int64 {
	t.Helper()
	hexStr := strings.ReplaceAll(id, "-", "")[:12]
	v, err := strconv.ParseInt(hexStr, 16, 64)
	if err != nil {
		t.Fatalf("parse timestamp from %q: %v", id, err)
	}
	return v
}

func resetUUIDStateForTest(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		uuMu.Lock()
		uuLastOrdinaryTimestamp = -1
		uuSequenceInit = false
		uuSequence = 0
		uuMu.Unlock()
		uuNowMs = func() int64 { return time.Now().UnixMilli() }
		uuRandRead = rand.Read
	})
}

func TestUuidv7(t *testing.T) {
	t.Run("generates ordered UUIDv7s while preserving follower timestamps", func(t *testing.T) {
		resetUUIDStateForTest(t)
		const ts int64 = 0x0123456789ab
		now := ts
		uuNowMs = func() int64 { return now }

		first := Uuidv7()
		second := Uuidv7()
		now = ts - 1
		afterRollback := Uuidv7()
		now = ts + 1
		afterAdvance := Uuidv7()

		ordinary := []string{first, second, afterRollback, afterAdvance}
		followerTimestamp := ts - 1_000
		f1, err := Uuidv7At(float64(followerTimestamp))
		if err != nil {
			t.Fatalf("Uuidv7At 1: %v", err)
		}
		f2, err := Uuidv7At(float64(followerTimestamp))
		if err != nil {
			t.Fatalf("Uuidv7At 2: %v", err)
		}
		followers := []string{f1, f2}

		for _, id := range append(slices.Clone(ordinary), followers...) {
			if !testUUIDv7RE.MatchString(id) {
				t.Fatalf("invalid UUIDv7 format: %q", id)
			}
		}

		sorted := slices.Clone(ordinary)
		slices.Sort(sorted)
		if !slices.Equal(ordinary, sorted) {
			t.Fatalf("ordinary IDs not sorted: %v", ordinary)
		}
		uniq := map[string]struct{}{}
		for _, id := range ordinary {
			uniq[id] = struct{}{}
		}
		if len(uniq) != len(ordinary) {
			t.Fatalf("duplicate ordinary IDs: %v", ordinary)
		}

		gotTS := []int64{
			testParseUUIDv7Timestamp(t, ordinary[0]),
			testParseUUIDv7Timestamp(t, ordinary[1]),
			testParseUUIDv7Timestamp(t, ordinary[2]),
			testParseUUIDv7Timestamp(t, ordinary[3]),
		}
		wantTS := []int64{ts, ts, ts, ts + 1}
		if !slices.Equal(gotTS, wantTS) {
			t.Fatalf("ordinary timestamps = %v, want %v", gotTS, wantTS)
		}
		if testParseUUIDv7Timestamp(t, f1) != followerTimestamp || testParseUUIDv7Timestamp(t, f2) != followerTimestamp || f1 == f2 {
			t.Fatalf("unexpected followers: %q, %q", f1, f2)
		}
	})

	t.Run("uses fresh randomness for every UUID tail", func(t *testing.T) {
		resetUUIDStateForTest(t)
		const ts int64 = 0x0123456789ab
		var randomByte byte
		uuRandRead = func(p []byte) (int, error) {
			randomByte++
			for i := range p {
				p[i] = randomByte
			}
			return len(p), nil
		}

		id1, err := Uuidv7At(float64(ts))
		if err != nil {
			t.Fatalf("Uuidv7At 1: %v", err)
		}
		id2, err := Uuidv7At(float64(ts))
		if err != nil {
			t.Fatalf("Uuidv7At 2: %v", err)
		}
		if got1, got2 := id1[len(id1)-8:], id2[len(id2)-8:]; got1 != "01010101" || got2 != "02020202" {
			t.Fatalf("tails = [%s, %s], want [01010101, 02020202]", got1, got2)
		}
	})

	t.Run("accepts timestamp boundaries", func(t *testing.T) {
		resetUUIDStateForTest(t)
		for _, ts := range []float64{0, float64((int64(1) << 48) - 1)} {
			id, err := Uuidv7At(ts)
			if err != nil {
				t.Fatalf("Uuidv7At(%v): %v", ts, err)
			}
			if got := float64(testParseUUIDv7Timestamp(t, id)); got != ts {
				t.Fatalf("timestamp = %v, want %v", got, ts)
			}
		}
	})

	t.Run("rejects invalid timestamps", func(t *testing.T) {
		resetUUIDStateForTest(t)
		invalid := []float64{-1, float64(int64(1) << 48), 1.5, math.NaN(), math.Inf(1)}
		for _, ts := range invalid {
			if _, err := Uuidv7At(ts); err == nil {
				t.Fatalf("expected error for invalid timestamp %v", ts)
			}
		}
	})
}
