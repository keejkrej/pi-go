// Ported from retry@0.12.0 lib/retry.js and lib/retry_operation.js (the copy bundled with proper-lockfile@4.1.2).

package lockfile

import (
	"errors"
	"math"
	"math/rand/v2"
	"sort"
	"time"
)

// RetryOptions are the `retry` module options accepted by Options.Retries. Nil fields use the retry
// defaults: Retries 10, Factor 2, MinTimeout 1000 ms, MaxTimeout and MaxRetryTime unbounded.
type RetryOptions struct {
	Retries      *int
	Factor       *float64
	MinTimeout   *int64
	MaxTimeout   *int64
	Randomize    bool
	Forever      bool
	Unref        bool // accepted for parity; Go timers never keep the process alive
	MaxRetryTime *int64
}

// Retries is the TS shorthand `retries: n` (equivalent to `{ retries: n }`).
func Retries(n int) *RetryOptions {
	return &RetryOptions{Retries: &n}
}

var errMinTimeoutGreater = errors.New("minTimeout is greater than maxTimeout")

// retryTimeouts is retry.timeouts(options).
func retryTimeouts(options *RetryOptions) ([]float64, error) {
	retries := 10
	factor := 2.0
	minTimeout := 1000.0
	maxTimeout := math.Inf(1)
	randomize := false
	forever := false
	if options != nil {
		if options.Retries != nil {
			retries = *options.Retries
		}
		if options.Factor != nil {
			factor = *options.Factor
		}
		if options.MinTimeout != nil {
			minTimeout = float64(*options.MinTimeout)
		}
		if options.MaxTimeout != nil {
			maxTimeout = float64(*options.MaxTimeout)
		}
		randomize = options.Randomize
		forever = options.Forever
	}

	if minTimeout > maxTimeout {
		return nil, errMinTimeoutGreater
	}

	createTimeout := func(attempt int) float64 {
		random := 1.0
		if randomize {
			random = rand.Float64() + 1
		}
		timeout := retryMathRound(random * minTimeout * math.Pow(factor, float64(attempt)))
		return math.Min(timeout, maxTimeout)
	}

	timeouts := []float64{}
	i := 0
	for ; i < retries; i++ {
		timeouts = append(timeouts, createTimeout(i))
	}

	if forever && len(timeouts) == 0 {
		timeouts = append(timeouts, createTimeout(i))
	}

	// sort the array numerically ascending
	sort.Float64s(timeouts)

	return timeouts, nil
}

// retryMathRound is Math.round (ties toward +Infinity).
func retryMathRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	f := math.Floor(x)
	if x-f >= 0.5 {
		return f + 1
	}
	return f
}

// retryOperation is RetryOperation, driven synchronously by the lock loop.
type retryOperation struct {
	timeouts       []float64
	cachedTimeouts []float64
	maxRetryTime   float64
	errors         []error
	operationStart int64
}

func newRetryOperation(options *RetryOptions) (*retryOperation, error) {
	timeouts, err := retryTimeouts(options)
	if err != nil {
		return nil, err
	}
	op := &retryOperation{timeouts: timeouts, maxRetryTime: math.Inf(1)}
	if options != nil && options.MaxRetryTime != nil && *options.MaxRetryTime != 0 {
		op.maxRetryTime = float64(*options.MaxRetryTime)
	}
	if options != nil && options.Forever {
		op.cachedTimeouts = append([]float64{}, timeouts...)
	}
	return op, nil
}

// attempt records the operation start (RetryOperation.attempt).
func (op *retryOperation) attempt() {
	op.operationStart = time.Now().UnixMilli()
}

// retry reports whether another attempt should run and after how many milliseconds.
func (op *retryOperation) retry(err error) (bool, float64) {
	if err == nil {
		return false, 0
	}
	currentTime := time.Now().UnixMilli()
	if float64(currentTime-op.operationStart) >= op.maxRetryTime {
		op.errors = append([]error{&Error{Message: "RetryOperation timeout occurred"}}, op.errors...)
		return false, 0
	}

	op.errors = append(op.errors, err)

	if len(op.timeouts) == 0 {
		if op.cachedTimeouts == nil {
			return false, 0
		}
		// retry forever, only keep last error
		op.errors = op.errors[:len(op.errors)-1]
		op.timeouts = append([]float64{}, op.cachedTimeouts...)
	}
	timeout := op.timeouts[0]
	op.timeouts = op.timeouts[1:]
	return true, timeout
}

// mainError returns the most frequent error by message; ties go to the later one.
func (op *retryOperation) mainError() error {
	if len(op.errors) == 0 {
		return nil
	}
	counts := map[string]int{}
	var mainError error
	mainErrorCount := 0
	for _, e := range op.errors {
		message := e.Error()
		count := counts[message] + 1
		counts[message] = count
		if count >= mainErrorCount {
			mainError = e
			mainErrorCount = count
		}
	}
	return mainError
}
