// Package engine drives playback of a parsed MIDI event stream into a game
// window: it schedules each event to its precise target time, resolves
// notes to keys via a keymap.Profile, and injects them via an Injector. It
// is framework-agnostic — the CLI and the (future) GUI both drive the same
// Player.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/philomelch/StarMusician/internal/keymap"
	"github.com/philomelch/StarMusician/internal/midi"
)

// ErrAlreadyPlaying is returned by Play if playback is already in progress.
var ErrAlreadyPlaying = errors.New("engine: already playing")

// defaultProgressTickInterval is how often reportProgressTicks reports
// progress between MIDI events.
const defaultProgressTickInterval = 200 * time.Millisecond

// ErrForegroundMismatch is returned by Play if the foreground guard is
// configured and the expected game window is not focused when playback is
// about to start.
var ErrForegroundMismatch = errors.New("engine: expected game window is not the foreground window")

// Progress reports playback position, delivered via the OnProgress callback.
type Progress struct {
	Time  float64 // seconds elapsed into the song
	Total float64 // total song length in seconds
	Note  uint8   // the MIDI note of the event just processed
}

// keyInjector is the subset of *input.Injector the engine needs. Defined
// here (rather than depending on the concrete type) so the engine stays
// decoupled from exactly how keys get injected.
type keyInjector interface {
	Press(vk uint16, extended bool) error
	Release(vk uint16, extended bool) error
	ReleaseAll() error
}

// noteKey identifies one sounding note instance. Track is included (not
// just channel+note) because two different instrument parts merged via
// Song.FilterParts can share a MIDI channel — a very common pattern in
// real-world files — and would otherwise collide.
type noteKey struct {
	track   int
	channel uint8
	note    uint8
}

// Player schedules and plays a parsed MIDI event stream. The zero value is
// not usable; construct with New.
type Player struct {
	profile keymap.Profile
	inj     keyInjector
	clock   clock

	foreground  ForegroundChecker
	countdown   int
	onCountdown func(secondsLeft int)
	onProgress  func(Progress)

	// progressTickInterval paces reportProgressTicks; overridable (same
	// package only) in tests so they don't have to wait on the real default.
	progressTickInterval time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
	// stopRequested records a Stop() call that arrived before Play() had a
	// context to cancel yet (the caller marks the Player stoppable, e.g. by
	// enabling a Stop button, slightly before the goroutine that calls Play
	// actually runs). Play consumes it immediately after acquiring the
	// lock, treating it exactly like an immediate cancellation, so a
	// panic-stop press can never be silently dropped by that race.
	stopRequested bool
	// progressWG is waited on before Play returns, so the goroutine spawned
	// for reportProgressTicks is guaranteed to have exited — otherwise a
	// straggler tick could fire after Play returns and briefly overwrite a
	// subsequent Play call's progress with this call's stale data.
	progressWG sync.WaitGroup

	// currentShift and active are only ever touched from within Play's
	// goroutine, never concurrently, so they need no lock of their own.
	currentShift keymap.Shift
	active       map[noteKey][]keymap.Resolution
}

// Option configures a Player at construction time.
type Option func(*Player)

// WithForegroundChecker enables the foreground guard: Play refuses to start
// (after the countdown) unless fc.Matches() reports true.
func WithForegroundChecker(fc ForegroundChecker) Option {
	return func(p *Player) { p.foreground = fc }
}

// WithCountdown sets how many seconds Play waits (calling the countdown
// callback each second) before checking the foreground guard and starting
// playback. Default 3; 0 disables the countdown.
func WithCountdown(seconds int) Option {
	return func(p *Player) { p.countdown = seconds }
}

// WithCountdownCallback registers a callback invoked once per second during
// the pre-play countdown with the number of seconds remaining.
func WithCountdownCallback(cb func(secondsLeft int)) Option {
	return func(p *Player) { p.onCountdown = cb }
}

// WithProgress registers a callback invoked after every processed event.
func WithProgress(cb func(Progress)) Option {
	return func(p *Player) { p.onProgress = cb }
}

// New returns a Player that resolves notes via profile and injects keys via
// inj.
func New(profile keymap.Profile, inj keyInjector, opts ...Option) *Player {
	p := &Player{
		profile:              profile,
		inj:                  inj,
		clock:                realClock{},
		countdown:            3,
		active:               make(map[noteKey][]keymap.Resolution),
		progressTickInterval: defaultProgressTickInterval,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Play schedules and injects events in order, blocking until playback
// finishes, ctx is cancelled, or Stop is called. Every exit path — normal
// completion, cancellation, foreground-guard refusal, or an injection error
// — releases every held key before returning, so no code path can ever
// leave a key down in the game.
func (p *Player) Play(ctx context.Context, events []midi.Event) error {
	p.mu.Lock()
	if p.cancel != nil {
		p.mu.Unlock()
		return ErrAlreadyPlaying
	}
	// A Stop() that arrived before this call had a context to cancel is
	// treated exactly as if it had cancelled us immediately: it must not be
	// silently dropped just because it happened to land in the narrow gap
	// between a caller marking us stoppable and this call actually
	// starting (e.g. the GUI enables its Stop button/hotkey right before
	// spawning the goroutine that calls Play).
	stopped := p.stopRequested
	p.stopRequested = false
	if stopped {
		p.mu.Unlock()
		// Nothing has been pressed yet in this call, but ReleaseAll here
		// anyway to honor Play's contract that every exit path releases any
		// held keys — cheap, and safe: stopRequested being set at all means
		// no other Play() call is concurrently active, so there's nothing
		// legitimate for this to step on.
		p.inj.ReleaseAll()
		return context.Canceled
	}
	playCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.mu.Unlock()

	defer func() {
		// Cancel playCtx unconditionally: on a normal finish (as opposed to
		// a Stop() call) nothing else ever does, which would otherwise leak
		// the reportProgressTicks goroutine below for the rest of the
		// process's life. cancel is safe to call more than once (a Stop()
		// racing with normal completion may have already called it).
		cancel()
		// Wait for reportProgressTicks to actually exit before returning,
		// so a straggler tick can never fire after Play has returned (e.g.
		// overwriting a subsequent Play call's progress with stale data).
		p.progressWG.Wait()

		p.inj.ReleaseAll()

		// Reset state and release the "already playing" guard as a single
		// atomic step (both under the same lock acquisition): if these were
		// two separate critical sections, a Play() call on this same
		// instance could slip in after p.cancel is cleared but before
		// currentShift/active are reset, and race with this cleanup over
		// those unsynchronized fields.
		p.mu.Lock()
		p.currentShift = keymap.ShiftNone
		p.active = make(map[noteKey][]keymap.Resolution)
		p.cancel = nil
		p.mu.Unlock()
	}()

	if err := p.awaitForeground(playCtx); err != nil {
		return err
	}

	var total float64
	if len(events) > 0 {
		total = events[len(events)-1].Time
	}

	start := p.clock.Now()

	if p.onProgress != nil {
		p.progressWG.Go(func() { p.reportProgressTicks(playCtx, start, total) })
	}

	for _, ev := range events {
		target := start.Add(time.Duration(ev.Time * float64(time.Second)))
		if !waitUntil(playCtx, p.clock, target) {
			return playCtx.Err()
		}

		if err := p.dispatch(ev); err != nil {
			return fmt.Errorf("engine: injecting event at %.3fs: %w", ev.Time, err)
		}

		if p.onProgress != nil {
			p.onProgress(Progress{Time: ev.Time, Total: total, Note: ev.Note})
		}
	}

	return nil
}

// reportProgressTicks calls onProgress on a fixed interval for as long as
// ctx is alive, independent of how often (or rarely) MIDI events actually
// fire. Without this, a song with a multi-second gap between notes (a rest)
// would leave progress-bar/status listeners showing stale, unchanging data
// for the whole gap and then jumping — technically correct (nothing to
// report yet) but confusing to watch.
func (p *Player) reportProgressTicks(ctx context.Context, start time.Time, total float64) {
	ticker := time.NewTicker(p.progressTickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			elapsed := p.clock.Now().Sub(start).Seconds()
			if elapsed > total {
				elapsed = total
			}
			p.onProgress(Progress{Time: elapsed, Total: total})
		}
	}
}

// Stop cancels any in-progress Play call and immediately releases every held
// key. Safe to call at any time, including when nothing is playing — this
// is the panic-hotkey path, so it must never block or depend on the Play
// goroutine noticing cancellation first.
func (p *Player) Stop() {
	p.mu.Lock()
	cancel := p.cancel
	if cancel == nil {
		// Nothing is playing *yet* from this Player's point of view — but a
		// Play() call may already be racing us to store its cancel func (see
		// the comment in Play). Recording the request here means that
		// still-incoming Play() call won't proceed as if nothing happened.
		p.stopRequested = true
	}
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	p.inj.ReleaseAll()
}

// dispatch applies a single event: resolving notes to keys, tracking which
// key+shift a still-sounding note actually used (so its eventual note-off
// releases exactly that key regardless of any shift changes in between),
// and toggling the octave-shift modifier only when the target octave
// actually changes.
func (p *Player) dispatch(ev midi.Event) error {
	switch ev.Type {
	case midi.Sustain:
		key := p.profile.Sustain()
		if ev.On {
			return p.inj.Press(key.VK, key.Extended)
		}
		return p.inj.Release(key.VK, key.Extended)

	case midi.NoteOn:
		res, ok := p.profile.Resolve(ev.Note, p.currentShift)
		if !ok {
			return nil // out of the instrument's total range: drop
		}
		if err := p.ensureShift(res.Shift); err != nil {
			return err
		}
		if err := p.inj.Press(res.Key.VK, res.Key.Extended); err != nil {
			return err
		}
		nk := noteKey{track: ev.Track, channel: ev.Channel, note: ev.Note}
		// Push, not overwrite: a legitimate re-onset of the same note before
		// its matching note-off (a real pattern in some MIDI exports) must
		// not clobber the still-sounding earlier instance's Resolution —
		// that earlier instance's own note-off still needs to release the
		// correct key.
		p.active[nk] = append(p.active[nk], res)
		return nil

	case midi.NoteOff:
		nk := noteKey{track: ev.Track, channel: ev.Channel, note: ev.Note}
		stack := p.active[nk]
		if len(stack) == 0 {
			return nil // never sounded (dropped as out-of-range, or a stray note-off)
		}
		// Pop the most recently pressed instance (LIFO): matches how
		// input.Injector's own retrigger semantics treat repeated presses
		// of the same physical key.
		res := stack[len(stack)-1]
		if len(stack) == 1 {
			delete(p.active, nk)
		} else {
			p.active[nk] = stack[:len(stack)-1]
		}
		return p.inj.Release(res.Key.VK, res.Key.Extended)
	}
	return nil
}

// ensureShift switches the physical octave-shift modifier to s, releasing
// whichever modifier is currently held (if any) and pressing s's modifier
// (if any). It is a no-op if s is already the active shift, which is the
// common case: overlapping notes deliberately prefer the currently active
// shift (see keymap.Profile.Resolve) precisely so this toggle is rare.
//
// The modifier transposes every key on the board, including ones already
// held for a still-sustaining note — so switching it out from under an
// active note would silently change that note's in-game pitch to whatever
// the new shift makes its key sound like. Rather than let a held note bleed
// into a pitch that was never in the song, any note still active when a
// toggle becomes unavoidable is cut short first.
func (p *Player) ensureShift(s keymap.Shift) error {
	if s == p.currentShift {
		return nil
	}
	if err := p.releaseActiveNotes(); err != nil {
		return err
	}
	if key, ok := p.profile.ShiftKey(p.currentShift); ok {
		if err := p.inj.Release(key.VK, key.Extended); err != nil {
			return err
		}
	}
	if key, ok := p.profile.ShiftKey(s); ok {
		if err := p.inj.Press(key.VK, key.Extended); err != nil {
			return err
		}
	}
	p.currentShift = s
	return nil
}

// releaseActiveNotes force-releases every currently-held note's key and
// clears the active-note bookkeeping, without touching the octave-shift
// modifier itself. Used by ensureShift immediately before a toggle (see its
// comment); a subsequent note-off for one of these notes finds nothing left
// in p.active and is treated as a no-op, same as any other stray note-off.
func (p *Player) releaseActiveNotes() error {
	var firstErr error
	for _, stack := range p.active {
		for _, res := range stack {
			if err := p.inj.Release(res.Key.VK, res.Key.Extended); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	p.active = make(map[noteKey][]keymap.Resolution)
	return firstErr
}

// awaitForeground runs the pre-play countdown (if configured) and then
// verifies the foreground guard (if configured). It returns
// ErrForegroundMismatch if the guard is set but does not match, and the
// context's error if ctx is cancelled during the countdown.
func (p *Player) awaitForeground(ctx context.Context) error {
	for s := p.countdown; s > 0; s-- {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if p.onCountdown != nil {
			p.onCountdown(s)
		}
		// waitUntil (not clock.Sleep) so a Stop() during the countdown is
		// noticed within maxSleepChunk, same as during actual playback,
		// rather than lagging up to a full second.
		if !waitUntil(ctx, p.clock, p.clock.Now().Add(time.Second)) {
			return ctx.Err()
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if p.foreground == nil {
		return nil
	}
	ok, err := p.foreground.Matches()
	if err != nil {
		return fmt.Errorf("engine: checking foreground window: %w", err)
	}
	if !ok {
		return ErrForegroundMismatch
	}
	return nil
}
