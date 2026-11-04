// Package store keeps SliceAPRS's history in the data folder so it
// survives a restart: every packet (one text file per UTC day), the direct
// paths (one JSON-lines file per UTC day), the openings and the stations.
// It never deletes anything.
package store

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/va3mw/SliceAPRS/internal/track"
)

// Store writes and reads the history files in one folder.
type Store struct {
	dir string
	mu  sync.Mutex
}

// Open uses dir, creating it if needed.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Dir is the data folder.
func (s *Store) Dir() string { return s.dir }

func day(t time.Time) string { return t.UTC().Format("2006-01-02") }

func (s *Store) appendLine(name, line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(s.dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, line)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Packet logs one decoded packet: UTC time, how it was heard, the station
// heard, level and SNR (dB, blank if not measured) and the packet in TNC2
// monitor format.
func (s *Store) Packet(h track.Heard) error {
	lv, snr := "", ""
	if h.HasLevel {
		lv, snr = fmt.Sprintf("%.1f", h.LevelDB), fmt.Sprintf("%.1f", h.SNR)
	}
	line := strings.Join([]string{h.Time.UTC().Format(time.RFC3339Nano), h.Kind, h.Via, lv, snr, h.Raw}, "\t")
	return s.appendLine("packets-"+day(h.Time)+".log", line)
}

// Path logs one direct path.
func (s *Store) Path(p track.Path) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.appendLine("paths-"+day(p.Time)+".jsonl", string(b))
}

// Opening logs a finished opening.
func (s *Store) Opening(o track.Opening) error {
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return s.appendLine("openings.jsonl", string(b))
}

// SaveStations writes the station list (atomically).
func (s *Store) SaveStations(st []track.Station) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := filepath.Join(s.dir, "stations.json")
	if err := os.WriteFile(p+".tmp", b, 0o644); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

// LoadStations reads the station list; a missing file is no stations.
func (s *Store) LoadStations() ([]track.Station, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, "stations.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st []track.Station
	return st, json.Unmarshal(b, &st)
}

// SaveGrids writes the grid log (atomically).
func (s *Store) SaveGrids(g []track.Grid) error { return s.saveJSON("grids.json", g) }

// LoadGrids reads the grid log; ok is false when there is no file yet.
func (s *Store) LoadGrids() (g []track.Grid, ok bool, err error) {
	b, err := os.ReadFile(filepath.Join(s.dir, "grids.json"))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return g, true, json.Unmarshal(b, &g)
}

func (s *Store) saveJSON(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := filepath.Join(s.dir, name)
	if err := os.WriteFile(p+".tmp", b, 0o644); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

// LoadPaths reads the direct paths of the last days days, oldest first.
func (s *Store) LoadPaths(now time.Time, days int) ([]track.Path, error) {
	var out []track.Path
	for d := days; d >= 0; d-- {
		name := filepath.Join(s.dir, "paths-"+day(now.AddDate(0, 0, -d))+".jsonl")
		err := eachLine(name, func(line string) {
			var p track.Path
			if json.Unmarshal([]byte(line), &p) == nil && !p.Time.IsZero() {
				out = append(out, p)
			}
		})
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// LoadOpenings reads the logged openings, oldest first.
func (s *Store) LoadOpenings() ([]track.Opening, error) {
	var out []track.Opening
	err := eachLine(filepath.Join(s.dir, "openings.jsonl"), func(line string) {
		var o track.Opening
		if json.Unmarshal([]byte(line), &o) == nil && !o.Start.IsZero() {
			out = append(out, o)
		}
	})
	return out, err
}

// LoadHours counts the packets logged per Unix hour over the last 26 hours.
func (s *Store) LoadHours(now time.Time) (map[int64]int, error) {
	out := map[int64]int{}
	from := now.Add(-26 * time.Hour)
	for t := from; day(t) <= day(now); t = t.Add(24 * time.Hour) {
		err := eachLine(filepath.Join(s.dir, "packets-"+day(t)+".log"), func(line string) {
			ts, _, _ := strings.Cut(line, "	")
			if tm, err := time.Parse(time.RFC3339Nano, ts); err == nil && tm.After(from) {
				out[tm.Unix()/3600]++
			}
		})
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// Recent reads the last n packets logged today and yesterday, for the
// Packets tab after a restart: time, kind, via, level, SNR and raw text.
func (s *Store) Recent(now time.Time, n int) ([][]string, error) {
	var out [][]string
	for _, t := range []time.Time{now.AddDate(0, 0, -1), now} {
		err := eachLine(filepath.Join(s.dir, "packets-"+day(t)+".log"), func(line string) {
			f := strings.SplitN(line, "\t", 6)
			if len(f) == 6 {
				out = append(out, f)
				if len(out) > 2*n {
					out = append([][]string(nil), out[len(out)-n:]...)
				}
			}
		})
		if err != nil {
			return nil, err
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}

func eachLine(name string, fn func(string)) error {
	f, err := os.Open(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if l := sc.Text(); l != "" {
			fn(l)
		}
	}
	return sc.Err()
}
