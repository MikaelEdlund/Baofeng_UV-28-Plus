//go:build windows

package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	bt "tinygo.org/x/bluetooth"
	"uv28prog/internal/radio"
)

// GATT property bits (standard Bluetooth values).
const (
	propWriteNoResp = 0x04
	propWrite       = 0x08
)

type bleTransport struct {
	dev    bt.Device
	ctrl   bt.DeviceCharacteristic // ...ff31
	data   bt.DeviceCharacteristic // ...ffe1
	notify []bt.DeviceCharacteristic
	events chan []byte
	log    radio.Logger
}

// ScanInfo describes a discovered radio bridge.
type ScanInfo struct {
	Address string
	Name    string
	RSSI    int16
}

var (
	enableOnce sync.Once
	enableErr  error
)

// enableAdapter initialises the Windows BLE runtime exactly once per process.
// RoInitialize must only be called once; a second call returns a benign
// "already initialised" status that Windows surfaces as "Incorrect function",
// so we guard it with sync.Once and keep only the first (real) result.
func enableAdapter() error {
	enableOnce.Do(func() {
		if err := bt.DefaultAdapter.Enable(); err != nil {
			enableErr = fmt.Errorf("enabling Bluetooth (is it switched on?): %w", err)
		}
	})
	return enableErr
}

// Scan looks for the radio's bridge (advertised name "walkie-talkie") for up
// to timeout. If wantAddr is non-empty only that address is returned.
func Scan(timeout time.Duration, wantAddr string, log radio.Logger) ([]ScanInfo, error) {
	if err := enableAdapter(); err != nil {
		return nil, err
	}
	wantAddr = strings.ToUpper(strings.TrimSpace(wantAddr))
	seen := map[string]ScanInfo{}
	done := make(chan struct{})
	var scanErr error

	go func() {
		t := time.NewTimer(timeout)
		defer t.Stop()
		<-mergeDone(done, t.C)
		_ = bt.DefaultAdapter.StopScan()
	}()

	scanErr = bt.DefaultAdapter.Scan(func(a *bt.Adapter, r bt.ScanResult) {
		name := r.LocalName()
		addr := strings.ToUpper(r.Address.String())
		isRadio := name == radio.ScanName
		if wantAddr != "" && addr == wantAddr {
			isRadio = true
		}
		if !isRadio {
			return
		}
		if log != nil {
			log("scan: found %s %q rssi=%d", addr, name, r.RSSI)
		}
		seen[addr] = ScanInfo{Address: addr, Name: name, RSSI: r.RSSI}
		if wantAddr != "" && addr == wantAddr {
			close(done)
			_ = a.StopScan()
		}
	})
	select {
	case <-done:
	default:
		close(done)
	}
	if scanErr != nil {
		return nil, scanErr
	}
	out := make([]ScanInfo, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	return out, nil
}

func mergeDone(done <-chan struct{}, tick <-chan time.Time) <-chan struct{} {
	out := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-tick:
		}
		close(out)
	}()
	return out
}

// Connect scans for the radio (optionally by address), connects, discovers
// the bridge characteristics and subscribes to notifications.
func Connect(addr string, timeout time.Duration, log radio.Logger) (radio.Transport, error) {
	if err := enableAdapter(); err != nil {
		return nil, err
	}
	found, err := Scan(timeout, addr, log)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no radio found (looking for a device named %q). Turn the radio on and keep it close", radio.ScanName)
	}
	target := found[0]
	if addr != "" {
		target = ScanInfo{Address: strings.ToUpper(addr)}
	}
	mac, err := bt.ParseMAC(target.Address)
	if err != nil {
		return nil, fmt.Errorf("bad address %q: %w", target.Address, err)
	}
	btAddr := bt.Address{MACAddress: bt.MACAddress{MAC: mac}}

	if log != nil {
		log("connect: %s", target.Address)
	}
	dev, err := bt.DefaultAdapter.Connect(btAddr, bt.ConnectionParams{})
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", target.Address, err)
	}

	tr := &bleTransport{dev: dev, events: make(chan []byte, 256), log: log}
	if err := tr.discover(); err != nil {
		_ = dev.Disconnect()
		return nil, err
	}
	if err := tr.subscribe(); err != nil {
		_ = dev.Disconnect()
		return nil, err
	}
	return tr, nil
}

func (t *bleTransport) discover() error {
	services, err := t.dev.DiscoverServices(nil)
	if err != nil {
		return fmt.Errorf("discovering services: %w", err)
	}
	var haveCtrl, haveData bool
	for _, s := range services {
		chars, err := s.DiscoverCharacteristics(nil)
		if err != nil {
			continue
		}
		for _, c := range chars {
			u := strings.ToLower(c.UUID().String())
			switch {
			case strings.Contains(u, "ff31"):
				t.ctrl, haveCtrl = c, true
			case strings.Contains(u, "ffe1"):
				t.data, haveData = c, true
			case strings.Contains(u, "ff32"):
				t.notify = append(t.notify, c)
			}
		}
	}
	if !haveData {
		return fmt.Errorf("radio data characteristic (ffe1) not found; is this the right device?")
	}
	if !haveCtrl {
		// Some modules expose only ffe1; control writes then go to ffe1.
		t.ctrl = t.data
		if t.log != nil {
			t.log("discover: no ff31 control characteristic; using ffe1 for control")
		}
	}
	// ffe1 is also a notify source on this module.
	t.notify = append(t.notify, t.data)
	return nil
}

func (t *bleTransport) subscribe() error {
	var subscribed int
	for _, c := range t.notify {
		cc := c
		err := cc.EnableNotifications(func(b []byte) {
			cp := make([]byte, len(b))
			copy(cp, b)
			select {
			case t.events <- cp:
			default:
				if t.log != nil {
					t.log("notification dropped (buffer full); will retry on next timeout")
				}
				// Drop forces a later timeout in the Driver, which triggers a retry.
			}
		})
		if err == nil {
			subscribed++
		} else if t.log != nil {
			t.log("subscribe: %s: %v", cc.UUID().String(), err)
		}
	}
	if subscribed == 0 {
		return fmt.Errorf("could not subscribe to any notification characteristic")
	}
	return nil
}

func (t *bleTransport) WriteControl(p []byte) error { return writeChar(t.ctrl, p) }
func (t *bleTransport) WriteData(p []byte) error    { return writeChar(t.data, p) }
func (t *bleTransport) Events() <-chan []byte       { return t.events }

func (t *bleTransport) Close() error {
	for _, c := range t.notify {
		_ = c.EnableNotifications(nil)
	}
	return t.dev.Disconnect()
}

func writeChar(c bt.DeviceCharacteristic, p []byte) error {
	if c.Properties()&propWriteNoResp != 0 {
		if _, err := c.WriteWithoutResponse(p); err == nil {
			return nil
		}
	}
	_, err := c.Write(p)
	return err
}
