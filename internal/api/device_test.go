package api

import (
	crand "crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/wsk0715/agent-board/internal/model"
)

// fakeClock is a clock the test moves.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 19, 1, 20, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// countingReader yields 0, 1, 2, ... modulo 256, so every byte read is known.
type countingReader struct{ next byte }

func (r *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.next
		r.next++
	}
	return len(p), nil
}

// scriptedReader yields the given bytes and then reports the end.
func scriptedReader(b ...byte) io.Reader { return &scripted{b: b} }

type scripted struct{ b []byte }

func (s *scripted) Read(p []byte) (int, error) {
	if len(s.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.b)
	s.b = s.b[n:]
	return n, nil
}

var userCodeRE = regexp.MustCompile(`^[BCDFGHJKLMNPQRSTVWXZ]{4}-[BCDFGHJKLMNPQRSTVWXZ]{4}$`)

// newTestDevices builds a store on the real random source, for tests that do
// not depend on the bytes it yields.
func newTestDevices(t *testing.T) (*deviceStore, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	return newDeviceStore(clock.Now, crand.Reader), clock
}

func TestDeviceStoreCreate(t *testing.T) {
	clock := newFakeClock()
	// 32 bytes of device code, then the bytes of the user code.
	rnd := &countingReader{}
	s := newDeviceStore(clock.Now, rnd)
	d, err := s.create("codex", "DESKTOP-RPLTFHM")
	if err != nil {
		t.Fatal(err)
	}
	counted := make([]byte, 32)
	for i := range counted {
		counted[i] = byte(i)
	}
	if want := base64.RawURLEncoding.EncodeToString(counted); d.deviceCode != want || len(d.deviceCode) != 43 {
		t.Errorf("deviceCode = %q, want the base64url of the first 32 bytes, %q", d.deviceCode, want)
	}
	// Bytes 32 to 39 pick characters by value modulo the 20 letters: 32%20 = 12, ...
	want := ""
	for b := 32; b < 40; b++ {
		want += string(model.UserCodeAlphabet[b%20])
	}
	if d.userCode != model.FormatUserCode(want) || !userCodeRE.MatchString(d.userCode) {
		t.Errorf("userCode = %q, want %q", d.userCode, model.FormatUserCode(want))
	}
	if d.actor != "codex" || d.clientHost != "DESKTOP-RPLTFHM" || d.status != model.DevicePending || d.approvedBy != "" {
		t.Errorf("request = %+v", d)
	}
	if !d.createdAt.Equal(clock.Now()) || d.expiresAt.Sub(d.createdAt) != 600*time.Second {
		t.Errorf("times = %v, %v", d.createdAt, d.expiresAt)
	}
	got, err := s.get(d.userCode)
	if err != nil || got != d {
		t.Errorf("get = %+v, %v; want %+v", got, err, d)
	}
}

func TestDeviceStoreUserCodeSkipsBytesThatWouldBiasTheLetters(t *testing.T) {
	// Of 256 byte values only 0..239 split evenly over 20 letters, so 240..255
	// are skipped, not folded onto the first letters.
	script := make([]byte, 0, 80)
	script = append(script, make([]byte, 32)...) // the device code
	for b := 255; b >= 240; b-- {
		script = append(script, byte(b)) // 16 bytes that must all be skipped
	}
	script = append(script, 19, 18, 0, 1, 239, 238, 20, 21) // the eight letters: Z, X, B, C, ... (239%20 = 19, 238%20 = 18)
	s := newDeviceStore(newFakeClock().Now, scriptedReader(script...))
	d, err := s.create("codex", "pc")
	if err != nil {
		t.Fatal(err)
	}
	var letters string
	for _, b := range []int{19, 18, 0, 1, 239, 238, 20, 21} {
		letters += string(model.UserCodeAlphabet[b%20])
	}
	if d.userCode != model.FormatUserCode(letters) {
		t.Errorf("userCode = %q, want %q", d.userCode, model.FormatUserCode(letters))
	}
}

func TestDeviceStoreRandomFailureIsAnError(t *testing.T) {
	s := newDeviceStore(newFakeClock().Now, scriptedReader(1, 2, 3))
	if _, err := s.create("codex", "pc"); err == nil {
		t.Error("a random source that ends early did not fail the request")
	}
	if len(s.byCode) != 0 || len(s.byUser) != 0 {
		t.Error("a failed create left a request behind")
	}
}

func TestDeviceStoreCodesDoNotRepeat(t *testing.T) {
	s, _ := newTestDevices(t)
	devices, users := map[string]bool{}, map[string]bool{}
	for i := 0; i < 300; i++ {
		d, err := s.create("codex", "pc")
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		if devices[d.deviceCode] || users[d.userCode] {
			t.Fatalf("code repeated at %d: %q %q", i, d.deviceCode, d.userCode)
		}
		devices[d.deviceCode], users[d.userCode] = true, true
		if _, err := s.decide(d.userCode, model.DeviceDenied, "owner"); err != nil { // free the slot
			t.Fatal(err)
		}
	}
}

// readerFunc fills each read itself.
type readerFunc func(p []byte)

func (f readerFunc) Read(p []byte) (int, error) {
	f(p)
	return len(p), nil
}

func TestDeviceStoreGivesUpWhenNoFreeUserCodeExists(t *testing.T) {
	// The device code is read in one call and the user code byte by byte. A
	// source that yields new device codes but always the same user code must
	// end in an error, not in a loop.
	var device byte
	s := newDeviceStore(newFakeClock().Now, readerFunc(func(p []byte) {
		for i := range p {
			if len(p) == 1 {
				p[i] = byte(i)
				continue
			}
			p[i] = device
		}
		if len(p) > 1 {
			device++
		}
	}))
	if _, err := s.create("codex", "pc"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.create("codex", "pc"); err == nil {
		t.Error("a user code that is taken was handed out again")
	}
	if len(s.byCode) != 1 || len(s.byUser) != 1 {
		t.Errorf("a failed create left a request behind: %d, %d", len(s.byCode), len(s.byUser))
	}
}

func TestDeviceStoreLimitsPendingRequests(t *testing.T) {
	s, clock := newTestDevices(t)
	var codes []string
	for i := 0; i < model.MaxPendingDevices; i++ {
		d, err := s.create("codex", "pc")
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		codes = append(codes, d.userCode)
	}
	if _, err := s.create("codex", "pc"); !errors.Is(err, errTooManyDevices) {
		t.Fatalf("the 21st pending request: err = %v, want errTooManyDevices", err)
	}

	// A decided request no longer waits, so it frees its place.
	if _, err := s.decide(codes[0], model.DeviceApproved, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.create("codex", "pc"); err != nil {
		t.Errorf("after one request was approved: %v", err)
	}
	if _, err := s.create("codex", "pc"); !errors.Is(err, errTooManyDevices) {
		t.Errorf("full again: err = %v", err)
	}
	if _, err := s.decide(codes[1], model.DeviceDenied, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.create("codex", "pc"); err != nil {
		t.Errorf("after one request was denied: %v", err)
	}

	// Expired requests free their places too.
	clock.Advance(600 * time.Second)
	if _, err := s.create("codex", "pc"); err != nil {
		t.Errorf("after the pending requests expired: %v", err)
	}
}

func TestDeviceStoreExpiry(t *testing.T) {
	s, clock := newTestDevices(t)
	d, err := s.create("codex", "pc")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(600*time.Second - time.Millisecond)
	if _, err := s.get(d.userCode); err != nil {
		t.Errorf("just before the expiry: %v", err)
	}
	clock.Advance(time.Millisecond)
	if _, err := s.get(d.userCode); !errors.Is(err, errDeviceNotFound) {
		t.Errorf("at the expiry, get err = %v, want errDeviceNotFound", err)
	}
	if _, err := s.take(d.deviceCode); !errors.Is(err, errDeviceNotFound) {
		t.Errorf("at the expiry, take err = %v, want errDeviceNotFound", err)
	}
	if _, err := s.decide(d.userCode, model.DeviceApproved, "owner"); !errors.Is(err, errDeviceNotFound) {
		t.Errorf("at the expiry, decide err = %v, want errDeviceNotFound", err)
	}
	if len(s.byCode) != 0 || len(s.byUser) != 0 {
		t.Errorf("expired requests stay in memory: %d, %d", len(s.byCode), len(s.byUser))
	}
}

func TestDeviceStoreDecide(t *testing.T) {
	s, _ := newTestDevices(t)
	d, _ := s.create("codex", "pc")
	approved, err := s.decide(d.userCode, model.DeviceApproved, "owner")
	if err != nil || approved.status != model.DeviceApproved || approved.approvedBy != "owner" {
		t.Fatalf("approve = %+v, %v", approved, err)
	}
	for _, status := range []model.DeviceStatus{model.DeviceApproved, model.DeviceDenied} {
		if _, err := s.decide(d.userCode, status, "boss"); !errors.Is(err, errDeviceNotPending) {
			t.Errorf("deciding an approved request as %s: err = %v, want errDeviceNotPending", status, err)
		}
	}
	if got, _ := s.get(d.userCode); got.status != model.DeviceApproved || got.approvedBy != "owner" {
		t.Errorf("a rejected decision changed the request: %+v", got)
	}
	if _, err := s.decide("BCDF-GHJK", model.DeviceApproved, "owner"); !errors.Is(err, errDeviceNotFound) {
		t.Errorf("unknown code: err = %v", err)
	}
}

func TestDeviceStoreTake(t *testing.T) {
	s, _ := newTestDevices(t)

	pending, _ := s.create("codex", "pc")
	if _, err := s.take(pending.deviceCode); !errors.Is(err, errDevicePending) {
		t.Errorf("pending: err = %v, want errDevicePending", err)
	}
	if _, err := s.get(pending.userCode); err != nil {
		t.Errorf("a pending request must stay after a failed exchange: %v", err)
	}

	approved, _ := s.create("claude", "laptop")
	if _, err := s.decide(approved.userCode, model.DeviceApproved, "owner"); err != nil {
		t.Fatal(err)
	}
	got, err := s.take(approved.deviceCode)
	if err != nil || got.actor != "claude" || got.clientHost != "laptop" || got.approvedBy != "owner" {
		t.Fatalf("approved: %+v, %v", got, err)
	}
	if _, err := s.take(approved.deviceCode); !errors.Is(err, errDeviceNotFound) {
		t.Errorf("a second exchange: err = %v, want errDeviceNotFound", err)
	}
	if _, err := s.get(approved.userCode); !errors.Is(err, errDeviceNotFound) {
		t.Errorf("an exchanged request is still readable: %v", err)
	}

	denied, _ := s.create("codex", "pc2")
	if _, err := s.decide(denied.userCode, model.DeviceDenied, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.take(denied.deviceCode); !errors.Is(err, errDeviceDenied) {
		t.Errorf("denied: err = %v, want errDeviceDenied", err)
	}
	if _, err := s.take(denied.deviceCode); !errors.Is(err, errDeviceNotFound) {
		t.Errorf("a denial is reported once: err = %v, want errDeviceNotFound", err)
	}

	if _, err := s.take(""); !errors.Is(err, errDeviceNotFound) {
		t.Errorf("empty device code: err = %v", err)
	}
	if _, err := s.take(pending.userCode); !errors.Is(err, errDeviceNotFound) {
		t.Errorf("the user code is not a device code: err = %v", err)
	}
}
