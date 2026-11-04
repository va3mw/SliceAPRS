//go:build !windows

package config

// DefaultConfigPath is where SliceAPRS looks for its configuration.
func DefaultConfigPath() string { return "/etc/sliceaprs/sliceaprs.yaml" }

// DefaultDataDir holds the history: packets, paths, openings, stations.
func DefaultDataDir() string { return "/var/lib/sliceaprs" }

// DefaultListen serves the web page to the whole LAN, so a headless Pi can be
// set up from another computer.
const DefaultListen = ":8081"
