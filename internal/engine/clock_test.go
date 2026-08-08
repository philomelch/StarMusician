package engine

import (
	"context"
	"testing"
	"time"
)

func TestWaitUntilTargetAlreadyPassed(t *testing.T) {
	start := time.Now()
	ok := waitUntil(context.Background(), realClock{}, start.Add(-time.Hour))
	if !ok {
		t.Fatal("waitUntil for a past target: want true, got false")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Millisecond {
		t.Errorf("waitUntil for a past target took %v, want ~immediate", elapsed)
	}
}

func TestWaitUntilRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	ok := waitUntil(ctx, realClock{}, start.Add(300*time.Millisecond))
	elapsed := time.Since(start)

	if ok {
		t.Error("waitUntil after cancellation: want false, got true")
	}
	if elapsed > 150*time.Millisecond {
		t.Errorf("waitUntil took %v after cancellation at 20ms, want well under the 300ms target", elapsed)
	}
}

// fakeClock lets Sleep-halving behavior be observed deterministically: Now
// only advances when Sleep is called, so the loop under test never reaches
// the real busy-wait phase on its own — callers stop it by cancelling ctx.
type fakeClock struct {
	now time.Time
}

func (f *fakeClock) Now() time.Time { return f.now }
func (f *fakeClock) Sleep(d time.Duration) {
	f.now = f.now.Add(d)
}

func TestWaitUntilSleepsHalfRemainingEachTime(t *testing.T) {
	fc := &fakeClock{now: time.Unix(0, 0)}
	// Stay under maxSleepChunk so halving (not the cap) governs sleep sizes.
	target := fc.now.Add(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())

	var sleeps []time.Duration
	recording := recordingClock{
		clock: fc,
		onSleep: func(d time.Duration) {
			sleeps = append(sleeps, d)
			if len(sleeps) == 4 {
				cancel() // stop once we've observed enough halvings
			}
		},
	}

	waitUntil(ctx, recording, target)

	if len(sleeps) != 4 {
		t.Fatalf("got %d recorded sleeps, want 4: %v", len(sleeps), sleeps)
	}
	for i, d := range sleeps {
		if i > 0 {
			want := sleeps[i-1] / 2
			if d != want {
				t.Errorf("sleep[%d] = %v, want exactly half of sleep[%d] (%v)", i, d, i-1, want)
			}
		}
	}
}

func TestWaitUntilCapsSleepChunksWhenTargetIsFarAway(t *testing.T) {
	fc := &fakeClock{now: time.Unix(0, 0)}
	target := fc.now.Add(10 * time.Second) // far enough that half-remaining would hugely exceed the cap

	ctx, cancel := context.WithCancel(context.Background())

	var sleeps []time.Duration
	recording := recordingClock{
		clock: fc,
		onSleep: func(d time.Duration) {
			sleeps = append(sleeps, d)
			if len(sleeps) == 3 {
				cancel()
			}
		},
	}

	waitUntil(ctx, recording, target)

	if len(sleeps) != 3 {
		t.Fatalf("got %d recorded sleeps, want 3: %v", len(sleeps), sleeps)
	}
	for i, d := range sleeps {
		if d != maxSleepChunk {
			t.Errorf("sleep[%d] = %v, want exactly maxSleepChunk (%v) while target is far away", i, d, maxSleepChunk)
		}
	}
}

type recordingClock struct {
	clock   *fakeClock
	onSleep func(time.Duration)
}

func (r recordingClock) Now() time.Time { return r.clock.Now() }
func (r recordingClock) Sleep(d time.Duration) {
	r.onSleep(d)
	r.clock.Sleep(d)
}
