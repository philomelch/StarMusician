# StarMusician — Project Context

A from-scratch, single-player auto-player for the in-game instrument feature in
**Blue Protocol: Star Resonance (BPSR)**. It reads a standard MIDI file and
"plays" it on the in-game instrument by injecting keystrokes into the focused
game window.

This document is the starting context for the build. Read it fully before
writing code.

---

## 1. Why we're building our own

We evaluated three existing open-source players (`saptia14/bpsr_midi_player`,
`Jed556/AutoMidiPlayer`, `Asakitan/36-key-midi-player`). All three share the
same core mechanism, but each carries trust/quality issues we'd rather not
inherit:

- Prebuilt `.exe`s that can't be verified against their source.
- Networking surface we don't want (an MQTT "band sync" with an arbitrary
  file-write bug in one; a phone-home leaderboard in another).
- Single-author, low-review repos.

The core engine is small (~a few hundred lines), so owning it end-to-end removes
the trust question entirely and lets us keep the scope tight.

---

## 2. How it works (the mechanism)

The tool **never touches the game process** — no memory reads, no packet
injection, no hooks into the game. The game exposes its instrument as a set of
physical keyboard keys; we press those keys precisely and on time. From the
game's perspective it's indistinguishable from fast, accurate typing.

Pipeline:

1. **Parse** the MIDI file into a flat, time-sorted list of events with
   absolute timestamps (in seconds).
2. **Map** each MIDI note to a game key, applying octave-shift modifiers where
   needed.
3. **Schedule** each event and wait until its target time with high precision.
4. **Inject** the keypress at the OS level as a hardware **scancode**.
5. **Stop** cleanly on demand, releasing any held keys.

---

## 3. THE critical constraint: scancodes, not virtual keys

This is the one thing that must be right.

Windows input has layers: physical key → **scancode** (physical position) →
**virtual key (VK)** (logical meaning) → character. Ordinary desktop apps read
keyboard input as window messages carrying the VK, so VK-based injection works
for them. **Games do not.** BPSR (like most games) reads input through
DirectInput / Raw Input, which operate at the **scancode** layer, *below* the VK
interpretation.

Consequence: if we inject virtual keys, the game ignores them — even though a
focused text box would show the character fine. We must send **scancodes**.

Implementation on Windows:

- Use `SendInput` (the modern API), **not** the deprecated `keybd_event`.
- Build a `KEYBDINPUT` with the `KEYEVENTF_SCANCODE` flag set and `wScan`
  populated. When that flag is set, Windows uses the scancode and ignores `wVk`.
- Convert VK → scancode with `MapVirtualKey(vk, MAPVK_VK_TO_VSC)` (or store
  scancodes directly).
- Key-up events OR the flags: `KEYEVENTF_SCANCODE | KEYEVENTF_KEYUP`.

> Reference lesson (Rust, but the principle is universal): the `enigo` crate's
> `key(Key::Unicode('z'))` sends scancodes, but `key(Key::Shift)` / `Key::Space`
> and other *named* keys send **virtual keys** — so modifiers and sustain
> silently fail in-game while note letters work. In Go we call `SendInput`
> directly and send scancodes for **everything** (notes, octave modifiers,
> sustain), so we don't inherit that trap.

### 3.1. Second critical constraint: matching integrity level (UIPI)

Scancode injection alone isn't sufficient — confirmed empirically when
correctly-scancoded `SendInput` calls reached Notepad fine but produced
*nothing* in BPSR, not even in its chat box (a plain text field, no
DirectInput/Raw Input involved).

Cause: Windows enforces **UIPI** (User Interface Privilege Isolation)
between processes at different integrity levels. `SendInput` injects into
the system input queue, but the OS checks the calling process's integrity
level against the process owning the currently-focused window before
delivering it. BPSR commonly runs elevated (as Administrator); a
non-elevated StarMusician's input gets silently dropped rather than
delivered — no error, no partial delivery, just nothing.

Fix: **both `.exe`s get an embedded manifest requesting
`requireAdministrator`** (`cmd/*/app.manifest`), so Windows prompts for
elevation on launch instead of requiring a manual "Run as administrator"
every time. Applied via `rcedit` as a **post-build** step (see README
Building), not baked in at compile time via a linked `.syso` — a `.syso` in
a `cmd/*` package directory gets linked into *every* build from that
directory, including `go test`'s synthesized test binary, which would then
itself require elevation just to launch and fail outright on a non-elevated
dev/CI machine. Post-build injection keeps `go build`/`go test` completely
unaffected; only the deliberately-`rcedit`-patched distributable `.exe`
carries the manifest. This means StarMusician *always* elevates now,
regardless of whether the current game/user actually needs it — a
deliberate simplicity-over-optionality tradeoff, since there's no
manifest-level way to make elevation conditional on the foreground window's
integrity level at runtime.

---

## 4. Tech stack

- **Language:** Go
- **Target:** Windows `.exe` only (the tool calls `user32!SendInput`; it cannot
  run on macOS/Linux)
- **GUI:** Fyne
- **Key dependencies:**
  - `golang.org/x/sys/windows` — `SendInput` + `RegisterHotKey` via syscall
    (no cgo for the injection/hotkey layer)
  - `gitlab.com/gomidi/midi/v2` (with its `smf` reader) — MIDI parsing
  - `golang.design/x/hotkey` — global hotkey; composes with Fyne (no main-thread
    requirement under Fyne) and is cgo-free on Windows
  - `fyne.io/fyne/v2` — GUI (this is what pulls in cgo)

Development note: we build and run on the Windows gaming PC. Every single key dependencies need to be reviewed and checked carefully for possible CVE. Only use dependencies we can trust and the version used have no known vulnerability.

---

## 5. Architecture

Use clean code architecture. Keep the engine **framework-agnostic and headless**, with the GUI as a thin
layer on top. Also keep the game's key mapping modular so if we add new games in the future, we just need to build a new key mapping for the new game's profile. Build CLI-first to verify mechanics before adding Fyne.

```
/cmd
  /starmusician-cli        # CLI entry point (build & verify the engine first)
  /starmusician-gui        # Fyne entry point (added later)
/internal
  /midi            # MIDI file -> []Event (absolute-timed, sorted, flattened)
  /keymap          # MIDI note -> game key + octave-shift logic; instrument layout
  /input           # SendInput scancode wrapper; held-key tracking; press/release
  /engine          # Player: scheduler loop, Play/Stop, foreground guard
  /hotkey          # global stop-hotkey listener (RegisterHotKey)
/ui                # Fyne widgets (added later)
```

The `engine.Player` exposes something like `Play(ctx)`, `Stop()`, and progress
callbacks. `Stop()` cancels the scheduler context **and** releases held keys.
The GUI and CLI both drive the same `Player`.

---

## 6. Instrument key mapping (starting reference — verify in-game)

BPSR's instrument covers **C3–B5** (36 semitones = 3 octaves × 12). The layout
below is the starting point taken from an existing BPSR player; **confirm it
against the actual in-game key bindings** before trusting it.

| Octave | White keys (natural)      | Black keys (sharp)         |
|--------|---------------------------|----------------------------|
| C3–B3  | `Z X C V B N M`           | number-row / symbol keys   |
| C4–B4  | `A S D F G H J`           | number-row / symbol keys   |
| C5–B5  | `Q W E R T Y U`           | `I O P [ ]`                |

- **Octave up:** hold **Left Shift** (shifts whole board +1 octave)
- **Octave down:** hold **Left Ctrl** (−1 octave)
- **Sustain (pedal, MIDI CC64):** **Space**

Octave-shift logic: for a note outside the base 36-key range, pick the shift
state that reaches it, and **prefer the currently active shift state** to avoid
unnecessary modifier toggles (toggling is slow and causes audible seams in
chords). Hold the modifier down across runs of notes; only release/re-press when
the target octave actually changes.

Notes outside the reachable range (after shifting) are dropped or clamped.

**Held notes and modifier toggles:** the octave-shift modifier transposes the
*entire* physical keyboard, including keys already pressed for a
still-sustaining note — so if the modifier toggles while an earlier note is
still held, that note's actual in-game pitch silently follows the new shift
rather than staying what it was. Measured against real multi-octave piano
MIDI files (not synthetic test cases): the large majority of toggles happen
while at least one other note is still active, so this isn't a rare edge
case — it was the dominant cause of what looked, from a listener's
perspective, like "notes that don't belong in the song." Fix: `ensureShift`
now force-releases every currently-held note before actually toggling the
modifier (`Player.releaseActiveNotes`), cutting those notes short rather
than letting their pitch bleed into the new shift state. The tradeoff is
explicit — a note held across an octave-shift boundary ends early instead of
sounding wrong — and is preferred because a wrong pitch is worse than an
early release.

---

## 7. Timing

Musical timing is the quality bar. Plain `time.Sleep` on Windows has ~15 ms
granularity, which sounds sloppy. Use a **hybrid sleep-then-spin**:

- Compute each event's absolute target: `start + event.offset`.
- While far from the target, sleep for ~half the remaining time.
- For the last ~2 ms, busy-wait (spin) on `time.Now()` (backed by the
  high-resolution perf counter).

Target accuracy: low single-digit milliseconds. Optionally request
`timeBeginPeriod(1)` for finer OS timer resolution while playing (and reset it
after).

---

## 8. Key injection details

- **Overlapping notes → same key:** after transposition two MIDI notes can map
  to the same physical key. Reference-count holds per key so an early note-off
  doesn't release a key another note still needs. Re-triggering a held key =
  release → tiny pause → press.
- **Extended keys:** arrows, numpad, right Ctrl/Alt need
  `KEYEVENTF_EXTENDEDKEY`. None of BPSR's default keys are extended, so this is
  N/A unless we remap — but keep the flag path available.
- **Focus requirement:** `SendInput` injects into whatever window has focus.
  The game must be the foreground window when keys fire. This is inherent to the
  method, not a bug.

---

## 9. Global stop hotkey

A panic-stop is the most important safety feature, because the app is **not**
focused while playing (the game is). Use a **system-wide** hotkey via
`golang.design/x/hotkey` (wraps `RegisterHotKey`), which fires regardless of
focus.

- **Default key: F9** (configurable). Register with **no modifiers**.
  - Avoid `Esc` and `Alt`: `RegisterHotKey` *consumes* the key, so registering
    `Esc` would stop the game from receiving Esc (no in-game menu); a bare `Alt`
    isn't registerable and is heavily used in-game. F8/F9/F10 are unused by the
    game and safe to swallow.
- On trigger: cancel the playback context, then **release all held keys**
  (send scancode key-ups for any note / Shift / Ctrl / Space still down) so the
  game isn't left with a stuck modifier. Do the same release on normal
  end-of-song — no code path should ever leave a key down.
- The listener runs on its own goroutine; UI updates from it must be marshaled
  to Fyne's thread (`fyne.Do`).

**Foreground guard:** before the first note, check `GetForegroundWindow` (and
optionally the window title/process) and refuse to start if BPSR isn't in front,
plus a short countdown. Cheap insurance against dumping a song into the wrong
window.

---

## 10. Build & run

CLI / minimal:

```bash
# on Windows, with mingw-w64 available (needed because of Fyne/cgo)
go build -ldflags "-H windowsgui" -o bpsr-player.exe ./cmd/bpsr-gui
```

- `-H windowsgui` suppresses the console window for the GUI build.
- For an embedded icon + version metadata, use the `fyne` packaging CLI.
- To cross-compile from macOS/Linux, use `fyne-cross` (Docker) to supply the
  mingw toolchain.

---

## 11. Scope & non-goals (important — do not add these)

- **Windows-only.** No cross-platform runtime support needed.
- **Portable.** Executable is portable, no installs needed, midi files stored in the directory of the portable app directory inside "./midi".
- **Single-player only.** **No networking of any kind** — no MQTT/band-sync, no
  leaderboard, no telemetry, no auto-update. This deliberately removes the
  entire class of network/file-write bugs the existing tools had.

---

## 12. MVP acceptance criteria

1. Load a `.mid` from a path and parse it into timed events. ✅
2. Play it into a focused BPSR window with correct pitches **and correct
   octaves** (modifiers register — proves scancode injection works).
   Input now confirmed reaching BPSR at all once run elevated (see 3.1) —
   pitch/octave correctness in-game still needs a real playthrough to confirm.
3. Sustain (CC64 → Space) handled. Implemented, not yet confirmed in-game.
4. **Stop playback instantly from within the game**, with no stuck keys.
   Default is now **F9**, not Esc (see section 9). Implemented and unit
   tested; not yet confirmed in-game.
5. Foreground guard prevents playing into the wrong window. ✅ (code-verified;
   `-window`/window-title-substring default is a guess, unconfirmed)
6. Builds to a single `.exe` that launches without a console window. ✅

---

## 13. Roadmap (post-MVP GUI features)

- File browser / playlist of MIDI files
- Transpose and tempo (playback-speed) controls
- Configurable key bindings and stop-hotkey
- Live progress bar / current-note display
- Per-track mute (drop channels, e.g. drums)
- Configurable countdown before start