package radio

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// Transport is a connected link to the radio's Bluetooth bridge module.
//
// The module exposes three GATT characteristics (matched by UUID substring):
//   - "ff31": control  — the module auth challenge is written here
//   - "ff32": control-notify (optional)
//   - "ffe1": data      — a transparent pipe to the radio's UART
//
// Notifications from every notifiable characteristic are delivered, in
// arrival order, on Events(). The two directions (control, data) are merged
// because the vendor app treats them as one byte stream.
type Transport interface {
	WriteControl(p []byte) error // write to ff31
	WriteData(p []byte) error    // write to ffe1 (caller pre-chunks if needed)
	Events() <-chan []byte       // merged notification payloads
	Close() error
}

// Tunables, matched to the vendor app's behaviour.
const (
	chunkSize    = 20                    // data writes are split into 20-byte BLE packets
	chunkDelay   = 10 * time.Millisecond // gap between chunks (app uses 5-10ms)
	ackTimeout   = 3 * time.Second       // wait for a single-byte ack
	frameTimeout = 4 * time.Second       // wait for a full block frame
	ack          = 0x06                  // radio/positive acknowledgement
	cmdRead      = 0x52                  // 'R'
	cmdWrite     = 0x57                  // 'W'
	blockLenByte = 0x80                  // length field in read/write frames (128)
	maxRetries   = 3                     // attempts for each block read/write
	retryDelay   = 50 * time.Millisecond
)

// BlockKeyIndex is the crypt table row used for block payloads. The vendor
// app and CHIRP both select row 1 ("CO 7") for this radio family.
const BlockKeyIndex = 1

// sendMagic is the fixed 25-byte key-select packet (CHIRP's reference value),
// which selects crypt row 1.
var sendMagic = []byte{
	0x53, 0x45, 0x4E, 0x44, 0x21, 0x05, 0x0D, 0x01, 0x01, 0x01, 0x04, 0x11, 0x08,
	0x05, 0x0D, 0x0D, 0x01, 0x11, 0x0F, 0x09, 0x12, 0x09, 0x10, 0x04, 0x00,
}

// Driver runs a programming session over a Transport.
type Driver struct {
	t    Transport
	log  Logger
	rnd  RandSource
	rx   *bytes.Buffer
	evCh <-chan []byte
}

// NewDriver creates a driver. log and rnd may be nil.
func NewDriver(t Transport, log Logger) *Driver {
	if log == nil {
		log = func(string, ...any) {}
	}
	return &Driver{t: t, log: log, rnd: defaultRand(), rx: new(bytes.Buffer), evCh: t.Events()}
}

// drain moves any buffered notifications into rx without blocking.
func (d *Driver) drain() {
	for {
		select {
		case b := <-d.evCh:
			d.log("  <= %s", hexPreview(b))
			d.rx.Write(b)
		default:
			return
		}
	}
}

// resetRx clears both the assembly buffer and any pending notifications.
func (d *Driver) resetRx() {
	for {
		select {
		case <-d.evCh:
		default:
			d.rx.Reset()
			return
		}
	}
}

// recv waits until at least n bytes have accumulated, or timeout elapses.
func (d *Driver) recv(n int, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for d.rx.Len() < n {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("timeout: wanted %d bytes, have %d", n, d.rx.Len())
		}
		select {
		case b := <-d.evCh:
			d.log("  <= %s", hexPreview(b))
			d.rx.Write(b)
		case <-time.After(remaining):
		}
	}
	return d.rx.Next(n), nil
}

func (d *Driver) writeData(p []byte) error {
	d.log("  => %s", hexPreview(p))
	for off := 0; off < len(p); off += chunkSize {
		end := off + chunkSize
		if end > len(p) {
			end = len(p)
		}
		if err := d.t.WriteData(p[off:end]); err != nil {
			return err
		}
		time.Sleep(chunkDelay)
	}
	return nil
}

// Authenticate passes the Bluetooth module's challenge gate. It writes a
// 20-byte challenge to the control characteristic and confirms the module
// answers with the expected derived key. A mismatch is logged but not fatal:
// some modules do not gate the data pipe.
func (d *Driver) Authenticate() error {
	d.resetRx()
	challenge := NewChallenge(d.rnd)
	want1, want2 := AuthResponses(challenge)
	d.log("auth: writing module challenge")
	d.log("  => [ctrl] %s", hexPreview(challenge))
	if err := d.t.WriteControl(challenge); err != nil {
		return fmt.Errorf("writing challenge: %w", err)
	}
	got, err := d.recv(20, ackTimeout)
	if err != nil {
		d.log("auth: no module reply (%v); continuing anyway", err)
		return nil
	}
	switch {
	case bytes.Equal(got, want1) || bytes.Equal(got, want2):
		d.log("auth: module key verified")
	default:
		d.log("auth: unexpected module reply %s (continuing)", hexPreview(got))
	}
	return nil
}

// EnterProgramMode runs the radio clone handshake over the data pipe:
// PROGRAM magic -> ack, 'F' -> 16-byte info, 'M' -> model ident,
// key-select packet -> ack.
func (d *Driver) EnterProgramMode() (ident string, info []byte, err error) {
	d.resetRx()

	d.log("program: sending magic %q", ProgramMagic)
	if err = d.writeData([]byte(ProgramMagic)); err != nil {
		return "", nil, err
	}
	a, err := d.recv(1, ackTimeout)
	if err != nil {
		return "", nil, fmt.Errorf("no ack to program magic (radio not answering over BLE): %w", err)
	}
	if a[0] != ack {
		return "", nil, fmt.Errorf("bad ack to program magic: 0x%02X", a[0])
	}

	d.log("program: requesting info ('F')")
	if err = d.writeData([]byte{'F'}); err != nil {
		return "", nil, err
	}
	info, err = d.recv(16, ackTimeout)
	if err != nil {
		return "", nil, fmt.Errorf("no info reply: %w", err)
	}
	d.log("program: info = %s", hexPreview(info))

	d.log("program: requesting model ('M')")
	if err = d.writeData([]byte{'M'}); err != nil {
		return "", nil, err
	}
	m, err := d.recv(7, ackTimeout)
	if err != nil {
		return "", nil, fmt.Errorf("no model reply: %w", err)
	}
	ident = string(bytes.TrimRight(m, "\x00"))
	d.log("program: model = %q", ident)

	d.log("program: selecting crypt key")
	if err = d.writeData(sendMagic); err != nil {
		return "", nil, err
	}
	a, err = d.recv(1, ackTimeout)
	if err != nil {
		return "", nil, fmt.Errorf("no ack to key select: %w", err)
	}
	if a[0] != ack {
		return "", nil, fmt.Errorf("bad ack to key select: 0x%02X", a[0])
	}
	return ident, info, nil
}

// readBlock reads one 128-byte block at addr and returns decrypted data.
// Retries up to maxRetries times on transient failures.
func (d *Driver) readBlock(addr int) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		d.resetRx()
		cmd := []byte{cmdRead, byte(addr >> 8), byte(addr), blockLenByte}
		if err := d.writeData(cmd); err != nil {
			lastErr = err
			d.log("read 0x%04X attempt %d/%d failed (write): %v", addr, attempt, maxRetries, err)
			time.Sleep(retryDelay)
			continue
		}
		frame, err := d.recv(4+BlockSize, frameTimeout)
		if err != nil {
			lastErr = fmt.Errorf("read 0x%04X: %w", addr, err)
			d.log("read 0x%04X attempt %d/%d failed (recv): %v", addr, attempt, maxRetries, err)
			time.Sleep(retryDelay)
			continue
		}
		if frame[0] != cmdRead || int(frame[3]) != blockLenByte {
			lastErr = fmt.Errorf("read 0x%04X: bad header % X", addr, frame[:4])
			d.log("read 0x%04X attempt %d/%d failed (header): %v", addr, attempt, maxRetries, lastErr)
			time.Sleep(retryDelay)
			continue
		}
		gotAddr := int(frame[1])<<8 | int(frame[2])
		if gotAddr != addr {
			lastErr = fmt.Errorf("read: asked 0x%04X got 0x%04X", addr, gotAddr)
			d.log("read 0x%04X attempt %d/%d failed (addr): %v", addr, attempt, maxRetries, lastErr)
			time.Sleep(retryDelay)
			continue
		}
		return Crypt(BlockKeyIndex, frame[4:]), nil
	}
	return nil, fmt.Errorf("read 0x%04X failed after %d attempts: %w", addr, maxRetries, lastErr)
}

// writeBlock encrypts and writes one 128-byte block at addr.
// Retries up to maxRetries times on transient failures.
func (d *Driver) writeBlock(addr int, data []byte) error {
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		d.resetRx()
		frame := make([]byte, 0, 4+BlockSize)
		frame = append(frame, cmdWrite, byte(addr>>8), byte(addr), blockLenByte)
		frame = append(frame, Crypt(BlockKeyIndex, data)...)
		if err := d.writeData(frame); err != nil {
			lastErr = err
			d.log("write 0x%04X attempt %d/%d failed (write): %v", addr, attempt, maxRetries, err)
			time.Sleep(retryDelay)
			continue
		}
		a, err := d.recv(1, ackTimeout)
		if err != nil {
			lastErr = fmt.Errorf("write 0x%04X: no ack: %w", addr, err)
			d.log("write 0x%04X attempt %d/%d failed (recv): %v", addr, attempt, maxRetries, err)
			time.Sleep(retryDelay)
			continue
		}
		if a[0] != ack {
			lastErr = fmt.Errorf("write 0x%04X: bad ack 0x%02X", addr, a[0])
			d.log("write 0x%04X attempt %d/%d failed (ack): %v", addr, attempt, maxRetries, lastErr)
			time.Sleep(retryDelay)
			continue
		}
		return nil
	}
	return fmt.Errorf("write 0x%04X failed after %d attempts: %w", addr, maxRetries, lastErr)
}

// ReadImage authenticates, enters program mode and reads all regions.
func (d *Driver) ReadImage(progress Progress) (*Image, error) {
	if err := d.Authenticate(); err != nil {
		return nil, err
	}
	ident, info, err := d.EnterProgramMode()
	if err != nil {
		return nil, err
	}
	im := NewBlankImage()
	im.Ident = ident
	im.Info = fmt.Sprintf("% X", info)
	im.Source = "radio"
	im.Captured = time.Now()

	blocks := Blocks()
	for i, addr := range blocks {
		data, err := d.readBlock(addr)
		if err != nil {
			return nil, err
		}
		im.SetBlock(addr, data)
		if progress != nil {
			progress(i+1, len(blocks))
		}
	}
	return im, nil
}

// WriteImage authenticates, enters program mode and writes the given blocks
// (or all blocks when onlyBlocks is nil). If forceMismatch is false, the
// radio's reported model must match im.Ident (case-insensitive); otherwise
// an error is returned so the caller can require explicit acknowledgement.
func (d *Driver) WriteImage(im *Image, onlyBlocks []int, forceMismatch bool, progress Progress) error {
	if err := d.Authenticate(); err != nil {
		return err
	}
	radioIdent, _, err := d.EnterProgramMode()
	if err != nil {
		return err
	}
	if !forceMismatch && im.Ident != "" && !strings.EqualFold(im.Ident, radioIdent) {
		return fmt.Errorf("model mismatch: image is %q but radio reports %q (re-run with -force-mismatch to override)", im.Ident, radioIdent)
	}
	if im.Ident != "" && !strings.EqualFold(im.Ident, radioIdent) {
		d.log("warning: model mismatch (image %q vs radio %q) — proceeding because -force-mismatch was set", im.Ident, radioIdent)
	}

	blocks := onlyBlocks
	if blocks == nil {
		blocks = Blocks()
	}
	for i, addr := range blocks {
		if !InRegions(addr, BlockSize) {
			return fmt.Errorf("refusing to write out-of-range block 0x%04X", addr)
		}
		if err := d.writeBlock(addr, im.Mem[addr:addr+BlockSize]); err != nil {
			return err
		}
		if progress != nil {
			progress(i+1, len(blocks))
		}
	}
	return nil
}

func hexPreview(b []byte) string {
	const max = 32
	if len(b) <= max {
		return fmt.Sprintf("% X", b)
	}
	return fmt.Sprintf("% X ...(%d)", b[:max], len(b))
}
