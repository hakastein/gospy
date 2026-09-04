package pyroscope

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRetryAttempts  = 5
	defaultRetryBaseDelay = 500 * time.Millisecond
	defaultRetryMaxDelay  = 30 * time.Second
)

// A zero Retry takes the defaults: 5 attempts, 500ms doubling up to 30s. Any non-zero field
// takes the struct as written; MaxDelay caps every wait, a server's Retry-After included.
type Retry struct {
	Attempts            int
	BaseDelay, MaxDelay time.Duration
}

type retryPolicy struct {
	attempts            int
	baseDelay, maxDelay time.Duration
}

func newRetryPolicy(cfg Retry) retryPolicy {
	if cfg == (Retry{}) {
		cfg = Retry{
			Attempts:  defaultRetryAttempts,
			BaseDelay: defaultRetryBaseDelay,
			MaxDelay:  defaultRetryMaxDelay,
		}
	}

	return retryPolicy{
		attempts:  max(cfg.Attempts, 1),
		baseDelay: max(cfg.BaseDelay, 0),
		maxDelay:  max(cfg.MaxDelay, 0),
	}
}

func (policy retryPolicy) delay(attempt int, err error, now time.Time) time.Duration {
	delay := policy.backoff(attempt)
	if hinted, ok := retryAfter(err, now); ok {
		delay = hinted
	}

	return min(delay, policy.maxDelay)
}

// The loop stops at the cap so the doubling cannot overflow the Duration.
func (policy retryPolicy) backoff(attempt int) time.Duration {
	delay := policy.baseDelay
	for i := 1; i < attempt && delay < policy.maxDelay; i++ {
		delay *= 2
	}

	return delay
}

func retryable(err error) bool {
	var permanent *permanentError
	if errors.As(err, &permanent) {
		return false
	}

	var status *statusError
	if errors.As(err, &status) {
		return status.status == http.StatusTooManyRequests || status.status >= http.StatusInternalServerError
	}

	return true
}

func retryAfter(err error, now time.Time) (time.Duration, bool) {
	var status *statusError
	if !errors.As(err, &status) {
		return 0, false
	}

	header := strings.TrimSpace(status.retryAfter)
	if header == "" {
		return 0, false
	}

	if seconds, parseErr := strconv.Atoi(header); parseErr == nil {
		return max(time.Duration(seconds)*time.Second, 0), true
	}

	if date, parseErr := http.ParseTime(header); parseErr == nil {
		return max(date.Sub(now), 0), true
	}

	return 0, false
}

func wait(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
