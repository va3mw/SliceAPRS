package app

import (
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/va3mw/SliceAPRS/internal/aprs"
	"github.com/va3mw/SliceAPRS/internal/ax25"
	"github.com/va3mw/SliceAPRS/internal/config"
	"github.com/va3mw/SliceAPRS/internal/store"
	"github.com/va3mw/SliceAPRS/internal/track"
)

// Analyzer is the tracker and its history files, shared by the receiver
// and the GUI.
type Analyzer struct {
	mu  sync.Mutex
	tr  *track.Tracker
	st  *store.Store
	log *slog.Logger
	dir string
}

func trackSettings(c *config.Config) track.Settings {
	lat, lon, _ := c.LatLon()
	return track.Settings{Lat: lat, Lon: lon, MyCall: c.Callsign, OpenKm: c.Openings.DistanceKm,
		Sensitivity: c.Openings.Sensitivity, Satellite: track.IsSatelliteFreq(c.FreqHz)}
}

// newAnalyzer opens the data folder and loads the history. onUp is called
// when the level rises after the history is loaded.
func newAnalyzer(c *config.Config, log *slog.Logger, onUp func(string, track.Opening)) (*Analyzer, error) {
	st, err := store.Open(c.DataDir)
	if err != nil {
		return nil, err
	}
	a := &Analyzer{tr: track.New(trackSettings(c)), st: st, log: log, dir: c.DataDir}
	now := time.Now().UTC()
	if s, err := st.LoadStations(); err != nil {
		log.Warn("stations file", "err", err)
	} else {
		a.tr.LoadStations(s)
	}
	if g, ok, err := st.LoadGrids(); err != nil {
		log.Warn("grids file", "err", err)
	} else if ok {
		a.tr.LoadGrids(g)
	}
	ps, err := st.LoadPaths(now, track.NormalDays)
	if err != nil {
		log.Warn("paths files", "err", err)
	}
	a.tr.LoadPaths(ps, now)
	if h, err := st.LoadHours(now); err == nil {
		a.tr.LoadHours(h)
	}
	if o, err := st.LoadOpenings(); err != nil {
		log.Warn("openings file", "err", err)
	} else {
		a.tr.LoadOpenings(o)
	}
	if rec, err := st.Recent(now, 300); err == nil {
		for _, f := range rec {
			a.tr.AddPast(pastHeard(f))
		}
	}
	a.tr.OnOpeningDone(func(o track.Opening) {
		log.Info("opening ended", "start", o.Start.Format(time.RFC3339), "end", o.End.Format(time.RFC3339),
			"peak", o.Peak, "stations", len(o.Stations), "max_km", o.MaxKm, "max_call", o.MaxCall, "direction", o.Direction)
		if err := st.Opening(o); err != nil {
			log.Error("openings file", "err", err)
		}
	})
	a.tr.Evaluate(now)
	a.tr.OnLevelUp(onUp) // after the first Evaluate: a restart mid-opening does not alert again
	log.Info("history loaded", "data_dir", c.DataDir, "stations", len(a.tr.Stations()), "paths_14d", len(ps))
	return a, nil
}

// pastHeard rebuilds a packet-list entry from a logged line.
func pastHeard(f []string) track.Heard {
	h := track.Heard{Kind: f[1], Via: f[2], Raw: f[5]}
	h.Time, _ = time.Parse(time.RFC3339Nano, f[0])
	if f[3] != "" {
		h.HasLevel = true
		h.LevelDB = atof(f[3])
		h.SNR = atof(f[4])
	}
	if fr, err := ax25.ParseTNC2(f[5]); err == nil {
		h.Src = fr.Src.String()
		if p, err := aprs.Parse(fr.Info, fr.Dest.Call); err == nil {
			h.Type, h.Summary = p.Type, p.Summary()
		}
	}
	return h
}

func atof(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// setConfig applies new settings without losing history.
func (a *Analyzer) setConfig(c *config.Config) {
	a.mu.Lock()
	a.tr.SetSettings(trackSettings(c))
	a.tr.Evaluate(time.Now().UTC())
	a.mu.Unlock()
}

// Packet analyses a frame and logs it.
func (a *Analyzer) Packet(f *ax25.Frame, sig track.Signal) track.Heard {
	p, perr := aprs.Parse(f.Info, f.Dest.Call)
	now := time.Now().UTC()
	a.mu.Lock()
	h := a.tr.Packet(now, f, p, perr, sig)
	a.mu.Unlock()
	if err := a.st.Packet(h); err != nil {
		a.log.Error("packet log", "err", err)
	}
	if h.Counted {
		if err := a.st.Path(track.PathOf(h)); err != nil {
			a.log.Error("paths file", "err", err)
		}
	}
	return h
}

// tick re-evaluates the level, so openings end when it goes quiet.
func (a *Analyzer) tick() {
	a.mu.Lock()
	a.tr.Evaluate(time.Now().UTC())
	a.mu.Unlock()
}

// save writes the stations file.
func (a *Analyzer) save() {
	a.mu.Lock()
	st, g := a.tr.Stations(), a.tr.Grids()
	a.mu.Unlock()
	if err := a.st.SaveStations(st); err != nil {
		a.log.Error("stations file", "err", err)
	}
	if err := a.st.SaveGrids(g); err != nil {
		a.log.Error("grids file", "err", err)
	}
}

// Overview is the Openings screen's data.
type Overview struct {
	State    track.State      `json:"state"`
	Day      []track.HourCell `json:"day"`
	Openings []track.Opening  `json:"openings"`
	Paths    []track.Path     `json:"paths"` // last hour, oldest first
	HomeLat  float64          `json:"home_lat"`
	HomeLon  float64          `json:"home_lon"`
}

// Overview returns the Openings screen's data.
func (a *Analyzer) Overview(lat, lon float64) Overview {
	now := time.Now().UTC()
	a.mu.Lock()
	defer a.mu.Unlock()
	o := Overview{State: a.tr.State(now), Day: a.tr.Day(now), Openings: a.tr.Openings(),
		Paths: a.tr.Paths(now.Add(-time.Hour)), HomeLat: lat, HomeLon: lon}
	if len(o.Openings) > 200 {
		o.Openings = o.Openings[:200]
	}
	return o
}

// Stations returns all stations.
func (a *Analyzer) Stations() []track.Station {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tr.Stations()
}

// Grids returns the grid log.
func (a *Analyzer) Grids() []track.Grid {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tr.Grids()
}

// HeardSince returns packets after seq.
func (a *Analyzer) HeardSince(seq int64) []track.Heard {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tr.HeardSince(seq)
}

// PathsSince returns direct paths since t.
func (a *Analyzer) PathsSince(t time.Time) []track.Path {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tr.Paths(t)
}
