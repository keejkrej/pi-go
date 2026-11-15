// Ported from packages/ai/src/utils/uuid.ts (pi v1.5.2).

package ai

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/keejkrej/pi-go/internal/js"
)

const (
	uuMaxTimestampMs = int64(0xffffffffffff)
	uuMaxSequence    = (uint64(1) << 41) - 1
)

var (
	uuMu                    sync.Mutex
	uuLastOrdinaryTimestamp = int64(-1)
	uuSequenceInit          bool
	uuSequence              uint64

	uuNowMs    = func() int64 { return time.Now().UnixMilli() }
	uuRandRead = rand.Read
)

// Uuidv7 generates a time-ordered UUIDv7 using the current wall-clock timestamp.
func Uuidv7() string {
	id, err := uuGenerate(float64(uuNowMs()), false)
	if err != nil {
		panic(err)
	}
	return id
}

// Uuidv7At generates a time-ordered UUIDv7 for a supplied timestamp in milliseconds.
// Follower timestamps are preserved verbatim without advancing the ordinary-clock high-water mark.
func Uuidv7At(timestampMs float64) (string, error) {
	return uuGenerate(timestampMs, true)
}

func uuGenerate(requestedTimestamp float64, hasSupplied bool) (string, error) {
	if math.IsNaN(requestedTimestamp) || math.IsInf(requestedTimestamp, 0) ||
		math.Trunc(requestedTimestamp) != requestedTimestamp ||
		requestedTimestamp < 0 || requestedTimestamp > float64(uuMaxTimestampMs) {
		return "", js.NewRangeError(fmt.Sprintf("UUIDv7 timestamp must be an integer between 0 and %d", uuMaxTimestampMs))
	}
	tsInt := int64(requestedTimestamp)

	uuMu.Lock()
	defer uuMu.Unlock()

	effectiveTimestamp := tsInt
	if !hasSupplied {
		if uuLastOrdinaryTimestamp > effectiveTimestamp {
			effectiveTimestamp = uuLastOrdinaryTimestamp
		}
		uuLastOrdinaryTimestamp = effectiveTimestamp
	}

	var b [16]byte
	if _, err := uuRandRead(b[:]); err != nil {
		return "", err
	}
	if !uuSequenceInit {
		uuSequence = (uint64(b[1]) << 32) |
			(uint64(b[2]) << 24) |
			(uint64(b[3]) << 16) |
			(uint64(b[4]) << 8) |
			uint64(b[5])
		uuSequenceInit = true
	} else {
		if uuSequence == uuMaxSequence {
			return "", js.NewRangeError("UUIDv7 generator sequence exhausted")
		}
		uuSequence++
	}

	ts := uint64(effectiveTimestamp)
	for index := 5; index >= 0; index-- {
		b[index] = byte(ts>>uint((5-index)*8)) & 0xff
	}
	b[6] = 0x70 | byte((uuSequence>>37)&0x0f)
	b[7] = byte((uuSequence >> 29) & 0xff)
	b[8] = 0x80 | byte((uuSequence>>23)&0x3f)
	b[9] = byte((uuSequence >> 15) & 0xff)
	b[10] = byte((uuSequence >> 7) & 0xff)
	b[11] = byte((uuSequence&0x7f)<<1) | (b[11] & 0x01)

	var dst [36]byte
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst[:]), nil
}
