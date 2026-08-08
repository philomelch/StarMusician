// Package input injects keystrokes at the Windows OS level as hardware
// scancodes via SendInput — never virtual keys — because games (BPSR
// included) read DirectInput/Raw Input at the scancode layer. See
// PROJECT_CONTEXT.md section 3 for why this is the one thing that must be
// right.
//
// The Injector also owns held-key bookkeeping: it reference-counts presses
// per physical key so an early note-off never releases a key a still-active,
// overlapping note needs, and it re-triggers (release, tiny pause, press) a
// key that's pressed again while already held so the game registers a fresh
// note-on.
package input

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// retriggerPause is how long to hold a key up when re-sounding a note that
// shares a physical key with another still-held note. Short enough to avoid
// an audible seam, long enough for the game to register a fresh note-on.
const retriggerPause = 10 * time.Millisecond

const (
	inputKeyboard = 1

	keyeventfExtendedKey = 0x0001
	keyeventfKeyUp       = 0x0002
	keyeventfScancode    = 0x0008

	mapvkVKToVSC = 0
)

// Win32 KEYBDINPUT (winuser.h).
type keybdInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

// Win32 INPUT. The trailing padding pads the union out to the size of its
// largest member (MOUSEINPUT, 32 bytes on 64-bit), matching sizeof(INPUT) as
// SendInput requires via its cbSize parameter — get this wrong and SendInput
// fails outright.
type input struct {
	inputType uint32
	ki        keybdInput
	padding   uint64
}

var (
	user32             = windows.NewLazySystemDLL("user32.dll")
	procSendInput      = user32.NewProc("SendInput")
	procMapVirtualKeyW = user32.NewProc("MapVirtualKeyW")
)

func vkToScancode(vk uint16) (uint16, error) {
	ret, _, callErr := procMapVirtualKeyW.Call(uintptr(vk), uintptr(mapvkVKToVSC))
	if ret == 0 {
		return 0, fmt.Errorf("MapVirtualKeyW(%#x): no scancode mapping (%v)", vk, callErr)
	}
	return uint16(ret), nil
}

// sendKeyEvent sends a single scancode-based key-down or key-up.
func sendKeyEvent(vk uint16, extended, keyUp bool) error {
	scan, err := vkToScancode(vk)
	if err != nil {
		return err
	}

	flags := uint32(keyeventfScancode)
	if extended {
		flags |= keyeventfExtendedKey
	}
	if keyUp {
		flags |= keyeventfKeyUp
	}

	in := input{
		inputType: inputKeyboard,
		ki: keybdInput{
			wScan:   scan,
			dwFlags: flags,
		},
	}

	ret, _, callErr := procSendInput.Call(
		1,
		uintptr(unsafe.Pointer(&in)),
		unsafe.Sizeof(in),
	)
	if ret != 1 {
		return fmt.Errorf("SendInput(vk=%#x, keyUp=%v): %v", vk, keyUp, callErr)
	}
	return nil
}

type heldKey struct {
	vk       uint16
	extended bool
}

type sendFunc func(vk uint16, extended, keyUp bool) error

// Injector tracks held keys and injects scancodes for them. The zero value
// is not usable; construct with New. An Injector is safe for concurrent use
// so a panic-stop hotkey on its own goroutine can call ReleaseAll while the
// scheduler goroutine is mid-playback.
type Injector struct {
	mu    sync.Mutex
	held  map[heldKey]int
	send  sendFunc
	sleep func(time.Duration)
}

// New returns an Injector ready to inject real keystrokes via SendInput.
func New() *Injector {
	return newInjector(sendKeyEvent, time.Sleep)
}

func newInjector(send sendFunc, sleep func(time.Duration)) *Injector {
	return &Injector{
		held:  make(map[heldKey]int),
		send:  send,
		sleep: sleep,
	}
}

// Press marks vk as held for one more note. If the key was not already held,
// it sends a key-down. If it was already held (an overlapping note shares
// this physical key), it re-triggers: key-up, a brief pause, then key-down,
// so the game registers a distinct note-on.
func (in *Injector) Press(vk uint16, extended bool) error {
	in.mu.Lock()
	defer in.mu.Unlock()

	k := heldKey{vk: vk, extended: extended}
	count := in.held[k]

	if count > 0 {
		if err := in.send(vk, extended, true); err != nil {
			return err
		}
		in.sleep(retriggerPause)
	}
	if err := in.send(vk, extended, false); err != nil {
		return err
	}

	in.held[k] = count + 1
	return nil
}

// Release marks one hold on vk as finished. It only sends a key-up once the
// last hold on that key is released; if another overlapping note still needs
// the key, it stays down. Releasing a key that isn't held is a no-op.
func (in *Injector) Release(vk uint16, extended bool) error {
	in.mu.Lock()
	defer in.mu.Unlock()

	k := heldKey{vk: vk, extended: extended}
	count := in.held[k]
	if count <= 0 {
		return nil
	}

	count--
	if count == 0 {
		delete(in.held, k)
		return in.send(vk, extended, true)
	}
	in.held[k] = count
	return nil
}

// ReleaseAll force-releases every currently held key regardless of its
// reference count and clears all bookkeeping. Called on panic-stop and on
// every playback-end path so no code path can ever leave a key down.
func (in *Injector) ReleaseAll() error {
	in.mu.Lock()
	defer in.mu.Unlock()

	var firstErr error
	for k := range in.held {
		if err := in.send(k.vk, k.extended, true); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	in.held = make(map[heldKey]int)
	return firstErr
}
