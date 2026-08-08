# StarMusician

A from-scratch, single-player auto-player for the in-game instrument feature
in **Blue Protocol: Star Resonance (BPSR)**. It reads a standard MIDI file
and "plays" it on the in-game instrument by injecting real hardware
scancodes into the focused game window — no memory reads, no packet
injection, no hooks into the game process.

See [PROJECT_CONTEXT.md](PROJECT_CONTEXT.md) for the full design rationale.

## Features

- Plays `.mid` files into BPSR's 36-key instrument (C2–B6 with octave-shift
  modifiers), with correct pitches and octaves.
- **Instrument/track picker**: if a MIDI file has multiple instrument parts
  (e.g. piano, guitar, drums), pick which one actually plays instead of
  mashing every track onto the same 36 keys.
- Sustain pedal (MIDI CC64 → Space).
- High-precision scheduling (sleep-then-spin), tuned for musical timing
  rather than plain `time.Sleep` accuracy.
- **Panic-stop hotkey** (default **F9**, configurable to Esc/F8/F10) that
  works system-wide, even while the game — not this app — has focus.
  Guaranteed to release every held key on every exit path.
- **Foreground guard**: refuses to start unless the game window is actually
  focused, with a short countdown so you can switch to it first.
- Both a CLI (`starmusician-cli`) and a Fyne-based GUI (`starmusician-gui`),
  sharing the same underlying engine.
- Single-player only — no networking, no telemetry, no auto-update.
  Portable: no installer, just an `.exe` and a `midi/` folder next to it.

## Requirements

- **Windows** (this tool calls `user32!SendInput`; it cannot run on
  macOS/Linux).
- **Administrator privileges.** Both `.exe`s are built with a manifest that
  auto-prompts for elevation (UAC) on launch. This isn't optional: if BPSR
  is running elevated (common — many games are), Windows' UIPI silently
  blocks a non-elevated process's injected keystrokes from ever reaching it.
  Running at the same integrity level as the game is the fix.
- [Go](https://go.dev/) 1.25.1 or newer.
- **A C compiler (mingw-w64) — GUI build only.** The GUI uses Fyne, which
  needs cgo. The CLI does not need this. On Windows, the easiest way to get
  one:
  ```
  scoop install gcc
  ```
  (or install mingw-w64 by any other method, as long as `gcc` ends up on
  `PATH`). Verify with `go env CGO_ENABLED` — it should print `1`.
- **[rcedit](https://github.com/electron/rcedit) — for building a
  distributable `.exe`, not for `go build`/`go test` during development.**
  Embeds the elevation manifest into an already-built binary as a separate
  step (see Building) rather than baking it in at compile time — baking it
  in via a linked `.syso` would make `go test` itself require elevation to
  even launch its test binary, which isn't something we want. On Windows:
  ```
  scoop install rcedit
  ```

## Building

From the repository root, in a bash shell (e.g. Git Bash):

```bash
./build.sh
```

This builds `starmusician-gui.exe` — the one end users actually run — and
embeds the elevation manifest via `rcedit`. The CLI exists only to exercise
the engine during development, not for end users; run `./build.sh all` to
also build `starmusician-cli.exe`. Equivalent manual commands:

```bash
# GUI (needs mingw-w64, see above) — the main build
go build -ldflags "-H windowsgui" -o starmusician-gui.exe ./cmd/starmusician-gui
rcedit starmusician-gui.exe --application-manifest cmd/starmusician-gui/app.manifest

# CLI (no C compiler needed) — dev/test only
go build -o starmusician-cli.exe ./cmd/starmusician-cli
rcedit starmusician-cli.exe --application-manifest cmd/starmusician-cli/app.manifest
```

The `-ldflags "-H windowsgui"` flag suppresses the console window for the
GUI build. Drop it (`go build -o starmusician-gui.exe ./cmd/starmusician-gui`)
if you want the console visible for troubleshooting.

The `rcedit` step embeds the elevation manifest (`cmd/*/app.manifest`) into
the already-built `.exe`. It's what makes Windows auto-prompt for
Administrator on launch (see Requirements) — skip it and the binary will
still run, just without the auto-elevation prompt, meaning you'd need to
right-click → "Run as administrator" yourself instead.

Both binaries are portable — copy the `.exe` anywhere, no installation step.

## Where do MIDI files go?

Create a `midi` folder **next to the `.exe`** (not next to your terminal's
working directory — the app finds its own folder regardless of where it's
launched from) and drop your `.mid` files in there:

```
your-folder/
  starmusician-gui.exe
  midi/
    some-song.mid
    another-song.mid
```

- **GUI**: the file dropdown lists everything in `midi/` automatically (hit
  "Refresh" after adding files); "Browse..." opens a normal file picker if
  you'd rather pick a `.mid` file from somewhere else.
- **CLI**: run it with no arguments and it lists/prompts from `midi/`, or
  pass a path directly: `starmusician-cli.exe path\to\song.mid`.

## Usage

1. Launch BPSR and get to the instrument.
2. Launch `starmusician-gui.exe` (or `starmusician-cli.exe`) and accept the
   UAC prompt — it needs to run elevated to reach BPSR (see Requirements).
3. Pick a `.mid` file and, if it has more than one instrument part, pick
   which one to play.
4. Hit Play, switch focus to the game during the countdown.
5. Press **F9** at any time to stop instantly — this works even though the
   game has focus, not this app.

CLI flags (`starmusician-cli.exe -h`): `-window` (game window title
substring to check for), `-countdown`, `-stopkey`, `-track`, `-midi-dir`.

## Status

CLI and GUI are both built and unit-tested. Injected input is now confirmed
reaching BPSR (needs elevation — see Requirements). A real-file diagnostic
found and fixed a pitch-correctness bug: the octave-shift modifier toggling
while an earlier note was still held would silently retune that note (see
PROJECT_CONTEXT.md section 6) — held notes are now cut short instead. Still
pending: a real in-game playthrough to confirm pitches/octaves/sustain/
panic-stop actually sound correct with this fix, and confirming BPSR's
actual window title and key bindings match what's currently assumed — see
PROJECT_CONTEXT.md for details.
