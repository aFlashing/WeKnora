package limiter

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

// RPMLimiter caps how many outbound requests one model may issue per rolling
// minute, process-wide. It complements the concurrency governor above:
//
//   - the concurrency governor bounds how many calls are in flight at once and
//     applies only to background work, because a slow provider response should
//     never delay an interactive chat turn;
//   - this limiter bounds the request *rate*, and applies to every call,
//     because upstream providers count interactive and background requests
//     against the same per-minute quota. Exceeding it returns HTTP 429.
//
// The window is a rolling one: the timestamps of admitted requests are kept
// and anything older than a minute is dropped. A fixed window would allow a
// burst of 2x the limit across the window boundary, which is exactly the case
// a provider quota rejects.
//
// Callers block until a slot frees up rather than failing, so a queued
// ingestion job waits instead of erroring; a cancelled context aborts the wait
// and the call proceeds (fail open), matching the rest of this package.
type RPMLimiter struct {
	mu      sync.Mutex
	windows map[string][]time.Time
}

// NewRPMLimiter builds an empty, process-local rate limiter.
func NewRPMLimiter() *RPMLimiter {
	return &RPMLimiter{windows: make(map[string][]time.Time)}
}

// defaultRPM is the process-wide instance. Rate limiting is per model, so a
// single shared instance is enough and needs no wiring at startup.
var defaultRPM = NewRPMLimiter()

// Acquire blocks until the key may issue one more request within the trailing
// minute, then records it. A non-positive limit disables the check.
func (l *RPMLimiter) Acquire(ctx context.Context, key string, limit int) error {
	if l == nil || key == "" || limit <= 0 {
		return nil
	}

	for {
		l.mu.Lock()
		now := time.Now()
		cutoff := now.Add(-time.Minute)

		kept := l.windows[key][:0]
		for _, ts := range l.windows[key] {
			if ts.After(cutoff) {
				kept = append(kept, ts)
			}
		}

		if len(kept) < limit {
			kept = append(kept, now)
			l.windows[key] = kept
			l.mu.Unlock()
			return nil
		}

		// Full: wait exactly until the oldest admitted request leaves the window.
		wait := kept[0].Add(time.Minute).Sub(now)
		l.windows[key] = kept
		l.mu.Unlock()

		if wait <= 0 {
			// The window rolled between the check and the unlock; retry now.
			continue
		}

		// Debug, not Info: a low quota makes this fire on most calls, and the
		// queued request itself is the observable symptom worth preserving.
		logger.Debugf(ctx, "[ModelRPM] model=%s reached %d rpm, waiting %s for a slot",
			key, limit, wait.Round(10*time.Millisecond))

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Reset forgets the recorded requests for key. Used by tests and by model
// configuration changes that should take effect immediately.
func (l *RPMLimiter) Reset(key string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.windows, key)
}

// GateRPM blocks the caller until the model is under its per-minute quota.
// It always returns, never errors out to the caller: a cancelled context or a
// limiter problem must not block model traffic permanently.
func GateRPM(ctx context.Context, modelID string, limit int) {
	if modelID == "" || limit <= 0 {
		return
	}
	if err := defaultRPM.Acquire(ctx, modelID, limit); err != nil {
		logger.Warnf(ctx, "[ModelRPM] model=%s wait aborted, proceeding: %v", modelID, err)
	}
}
