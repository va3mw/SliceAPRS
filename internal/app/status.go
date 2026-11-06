package app

import (
	"sync"
	"time"
)

// Receiver states shown in the GUI.
const (
	StateSetup      = "setup"      // callsign/grid not set yet
	StateSearching  = "searching"  // looking for a radio
	StateConnecting = "connecting" // talking to the radio
	StateRunning    = "running"    // streaming and decoding
	StateReplay     = "replay"     // decoding a recording
	StateDone       = "done"       // recording finished
	StateError      = "error"      // last session failed; retrying
	StateStopped    = "stopped"
)

// Decoder counts what the decoder has done since start.
type Decoder struct {
	Frames   int     `json:"frames"`    // good FCS
	BadAX25  int     `json:"bad_ax25"`  // good FCS but not a UI frame we can read
	Slicers  []int   `json:"slicers"`   // frames per slicer (first or not)
	Lost     int     `json:"lost"`      // DAX IQ packets lost
	FloorDB  float64 `json:"floor_db"`  // channel noise floor
	HasFloor bool    `json:"has_floor"` // FloorDB is measured
	Rate     int     `json:"rate"`      // IQ samples per second
}

// Status is a snapshot of the receiver for the GUI.
type Status struct {
	// Changed lists what another program changed on our panadapter or
	// slice; the GUI asks whether to restore it.
	Changed     []string  `json:"changed,omitempty"`
	Version     string    `json:"version"`
	State       string    `json:"state"`
	Error       string    `json:"error,omitempty"`
	Since       time.Time `json:"since"`
	Radio       string    `json:"radio"`
	FreqHz      int64     `json:"freq_hz"`
	RxAnt       string    `json:"rxant"`
	Antennas    []string  `json:"antennas"`
	DAXIQ       int       `json:"daxiq"`
	Others      []string  `json:"others"` // other programs connected to the radio
	Replay      string    `json:"replay,omitempty"`
	Decoder     Decoder   `json:"decoder"`
	ConfigPath  string    `json:"config_path"`
	ConfigSaved bool      `json:"config_saved"`
	DataDir     string    `json:"data_dir"`
	Now         time.Time `json:"now"`
}

type monitor struct {
	mu sync.Mutex
	st Status
}

func (m *monitor) set(fn func(s *Status)) {
	m.mu.Lock()
	fn(&m.st)
	m.mu.Unlock()
}

func (m *monitor) state(state, errText string) {
	m.set(func(s *Status) {
		if s.State != state {
			s.Since = time.Now().UTC()
		}
		s.State, s.Error = state, errText
	})
}

func (m *monitor) snapshot() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st
	s.Antennas = append([]string(nil), s.Antennas...)
	s.Others = append([]string(nil), s.Others...)
	s.Changed = append([]string(nil), s.Changed...)
	s.Decoder.Slicers = append([]int(nil), s.Decoder.Slicers...)
	s.Now = time.Now().UTC()
	return s
}
