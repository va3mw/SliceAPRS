//go:build windows

package config

import (
	"os"
	"path/filepath"
)

// DefaultConfigPath is sliceaprs.yaml in %ProgramData%\SliceAPRS.
func DefaultConfigPath() string { return filepath.Join(programData(), "SliceAPRS", "sliceaprs.yaml") }

// DefaultDataDir is %ProgramData%\SliceAPRS\data.
func DefaultDataDir() string { return filepath.Join(programData(), "SliceAPRS", "data") }

// DefaultListen keeps the web page on this PC (no firewall prompt); tick
// "reachable from other computers" to serve it on the LAN.
const DefaultListen = "127.0.0.1:8081"

func programData() string {
	if d := os.Getenv("ProgramData"); d != "" {
		return d
	}
	return `C:\ProgramData`
}
