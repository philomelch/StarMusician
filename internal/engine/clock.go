package engine

import (
	"context"
	"time"
)

// spinThreshold is how close to the target time waitUntil switches from
// sleeping to busy-waiting. Plain time.Sleep on Windows has ~15ms
// granularity, too coarse for musical timing, so the last stretch is spent
// spinning on the high-resolution clock instead. See PROJECT_CONTEXT.md
// section 7.
const spinThreshold = 2 * time.Millisecond

// maxSleepChunk bounds any single Sleep call regardless of how far away the
// target is. Without this cap, "sleep half the remaining time" means a note
// several seconds out produces a multi-second Sleep — during which the ESC
// panic-stop hotkey (context cancellation) wouldn't be noticed until that
// Sleep call returns. Capping each chunk keeps worst-case stop latency
// bounded to ~maxSleepChunk regardless of how sparse the song is.
const maxSleepChunk = 15 * time.Millisecond

// clock abstracts time so the scheduler is deterministically testable.
type clock interface {
	Now() time.Time
	Sleep(d time.Duration)
}

type realClock struct{}

func (realClock) Now() time.Time        { return time.Now() }
func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

// waitUntil blocks until c.Now() reaches target, ctx is cancelled, or
// (whichever first). It reports whether target was reached (false means ctx
// was cancelled first). Far from the target it sleeps for half the
// remaining time, halving again each time it wakes; inside spinThreshold it
// busy-waits, re-checking ctx every iteration so cancellation is honored
// within microseconds rather than up to a full sleep granularity.
func waitUntil(ctx context.Context, c clock, target time.Time) bool {
	for {
		if ctx.Err() != nil {
			return false
		}
		remaining := target.Sub(c.Now())
		if remaining <= 0 {
			return true
		}
		if remaining > spinThreshold {
			c.Sleep(min(remaining/2, maxSleepChunk))
			continue
		}
		// Busy-wait: intentionally no sleep here.
	}
}
