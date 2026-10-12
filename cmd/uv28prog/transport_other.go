//go:build !windows

package main

import (
	"errors"
	"time"

	"uv28prog/internal/radio"
)

// ScanInfo mirrors the Windows type so the CLI compiles everywhere.
type ScanInfo struct {
	Address string
	Name    string
	RSSI    int16
}

var errNoBLE = errors.New("Bluetooth programming is only built into the Windows binary; " +
	"on this platform you can still use the offline commands (export, import, show)")

// Scan is unavailable off Windows.
func Scan(timeout time.Duration, wantAddr string, log radio.Logger) ([]ScanInfo, error) {
	return nil, errNoBLE
}

// Connect is unavailable off Windows.
func Connect(addr string, timeout time.Duration, log radio.Logger) (radio.Transport, error) {
	return nil, errNoBLE
}
