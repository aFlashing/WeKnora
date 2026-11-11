package limiter

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRPMLimiterAdmitsUpToLimit(t *testing.T) {
	l := NewRPMLimiter()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		require.NoError(t, l.Acquire(ctx, "model-a", 3), "request %d should be admitted", i+1)
	}
	buckets := l.windows["model-a"]
	require.Len(t, buckets, 3, "admitted requests must be recorded in the window")
}

func TestRPMLimiterIsPerModel(t *testing.T) {
	l := NewRPMLimiter()
	ctx := context.Background()

	require.NoError(t, l.Acquire(ctx, "model-a", 1))
	require.NoError(t, l.Acquire(ctx, "model-b", 1), "a different model has its own quota")
}

func TestRPMLimiterBlocksWhenFull(t *testing.T) {
	l := NewRPMLimiter()
	require.NoError(t, l.Acquire(context.Background(), "model-a", 1))

	// The second call must wait for the window; a short deadline proves it
	// blocks instead of being admitted immediately.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := l.Acquire(ctx, "model-a", 1)
	require.ErrorIs(t, err, context.DeadlineExceeded,
		"a full window must block rather than admit over the limit")
}

func TestRPMLimiterZeroLimitIsUnlimited(t *testing.T) {
	l := NewRPMLimiter()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		require.NoError(t, l.Acquire(ctx, "model-a", 0))
	}
	require.Empty(t, l.windows["model-a"], "a disabled limit must not record anything")
}

func TestRPMLimiterDropsExpiredEntries(t *testing.T) {
	l := NewRPMLimiter()
	l.windows["model-a"] = []time.Time{time.Now().Add(-2 * time.Minute)}

	require.NoError(t, l.Acquire(context.Background(), "model-a", 1),
		"entries older than a minute must not count against the quota")
	require.Len(t, l.windows["model-a"], 1)
}

func TestRPMLimiterReset(t *testing.T) {
	l := NewRPMLimiter()
	require.NoError(t, l.Acquire(context.Background(), "model-a", 1))

	l.Reset("model-a")
	require.NoError(t, l.Acquire(context.Background(), "model-a", 1))
}

func TestRPMLimiterConcurrentAdmitsExactlyLimit(t *testing.T) {
	l := NewRPMLimiter()
	const limit = 5

	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			defer cancel()
			if err := l.Acquire(ctx, "model-a", limit); err == nil {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	require.Equal(t, limit, admitted, "concurrent callers must not exceed the limit")
}

func TestGateRPMFailsOpenOnCancelledContext(t *testing.T) {
	// GateRPM must never block a call permanently: a cancelled wait lets the
	// request proceed so the inner client can surface its own error.
	require.NoError(t, defaultRPM.Acquire(context.Background(), "gate-test", 1))
	t.Cleanup(func() { defaultRPM.Reset("gate-test") })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		GateRPM(ctx, "gate-test", 1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("GateRPM did not return after the context was cancelled")
	}
}
