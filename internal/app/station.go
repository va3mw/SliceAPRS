// Package app runs SliceAPRS: it connects to the radio (or replays a
// recording), decodes APRS from the DAX IQ, analyses each packet and keeps
// the history. Both GUIs and the headless program drive a Station.
package app

import (
	"context"
	"io"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/va3mw/SliceAPRS/internal/config"
	"github.com/va3mw/SliceAPRS/internal/track"
)

// Station runs the receiver and restarts it when the configuration changes.
type Station struct {
	Version string
	Logs    *LogRing
	// Replay, when set, decodes this WAV file instead of using the radio.
	Replay string
	// ReplayFast decodes the recording as fast as possible.
	ReplayFast bool
	// RecordIQ, when set, writes the radio's IQ to this WAV file.
	RecordIQ string

	path  string
	mon   monitor
	mu    sync.Mutex
	cfg   *config.Config
	saved bool
	apply chan struct{}
	onCfg []func(*config.Config)
	an    *Analyzer
	level *slog.LevelVar
	log   *slog.Logger
}

// NewStation returns a station for the config at path. saved is false when
// the file does not exist yet and cfg holds the defaults. Log lines go to out
// (and to the ring the GUI shows).
func NewStation(path string, cfg *config.Config, saved bool, version string, out io.Writer) *Station {
	s := &Station{Version: version, Logs: NewLogRing(500), path: path, cfg: cfg, saved: saved,
		apply: make(chan struct{}, 1), level: new(slog.LevelVar)}
	s.setLevel(cfg)
	s.log = slog.New(slog.NewTextHandler(io.MultiWriter(out, s.Logs), &slog.HandlerOptions{Level: s.level}))
	s.mon.st = Status{Version: version, State: StateStopped, ConfigPath: path, ConfigSaved: saved}
	return s
}

func (s *Station) setLevel(c *config.Config) {
	if c.Debug {
		s.level.Set(slog.LevelDebug)
	} else {
		s.level.Set(slog.LevelInfo)
	}
}

// Log is the station's logger.
func (s *Station) Log() *slog.Logger { return s.log }

// Config returns a copy of the current configuration.
func (s *Station) Config() *config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Clone()
}

// Status returns a snapshot for the GUI.
func (s *Station) Status() Status {
	st := s.mon.snapshot()
	s.mu.Lock()
	st.ConfigSaved = s.saved
	st.DataDir = s.cfg.DataDir
	st.Replay = s.Replay
	if st.FreqHz == 0 {
		st.FreqHz = s.cfg.FreqHz
	}
	s.mu.Unlock()
	return st
}

// Apply checks and saves a new configuration, then restarts the receiver
// with it.
func (s *Station) Apply(c *config.Config) error {
	c = c.Clone()
	if err := c.Resolve(); err != nil {
		return err
	}
	if err := c.Save(s.path); err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg, s.saved = c, true
	hooks := append([]func(*config.Config){}, s.onCfg...)
	s.mu.Unlock()
	s.setLevel(c)
	for _, fn := range hooks {
		fn(c.Clone())
	}
	s.log.Info("configuration saved; restarting", "file", s.path)
	select {
	case s.apply <- struct{}{}:
	default:
	}
	return nil
}

// OnConfig registers fn to be called with each newly applied configuration.
func (s *Station) OnConfig(fn func(*config.Config)) {
	s.mu.Lock()
	s.onCfg = append(s.onCfg, fn)
	s.mu.Unlock()
}

// Run runs the receiver until ctx is cancelled, restarting it after Apply.
func (s *Station) Run(ctx context.Context) {
	for {
		cfg := s.Config()
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.runOnce(runCtx, cfg)
		}()
		select {
		case <-ctx.Done():
			cancel()
			<-done
			s.mon.state(StateStopped, "")
			return
		case <-s.apply:
			cancel()
			<-done // lets the radio cleanup commands go out
		}
	}
}

// analyzer returns the analyzer for the config's data folder, loading the
// history once per folder and updating its settings.
func (s *Station) analyzer(cfg *config.Config) (*Analyzer, error) {
	s.mu.Lock()
	an := s.an
	s.mu.Unlock()
	if an != nil && an.dir == cfg.DataDir {
		an.setConfig(cfg)
		return an, nil
	}
	if an != nil {
		an.save()
	}
	an, err := newAnalyzer(cfg, s.log)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.an = an
	s.mu.Unlock()
	return an, nil
}

// Analyzer is the current analyzer, or nil before setup.
func (s *Station) Analyzer() *Analyzer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.an
}

func (s *Station) runOnce(ctx context.Context, cfg *config.Config) {
	if !cfg.Ready() {
		s.mon.state(StateSetup, "Enter your callsign and grid in Settings to start.")
		s.log.Info("waiting for setup: callsign and grid are not set", "config", s.path)
		<-ctx.Done()
		return
	}
	an, err := s.analyzer(cfg)
	if err != nil {
		s.mon.state(StateError, err.Error())
		s.log.Error("data folder", "err", err)
		<-ctx.Done()
		return
	}
	if track.IsSatelliteFreq(cfg.FreqHz) {
		s.log.Warn("145.825 MHz is the satellite and ISS channel: packets are shown but none count towards openings")
	}
	s.log.Info("SliceAPRS starting", "version", s.Version, "config", s.path, "callsign", cfg.Callsign, "grid", cfg.Grid,
		"MHz", float64(cfg.FreqHz)/1e6, "daxiq", cfg.DAXIQ.Channel, "data_dir", cfg.DataDir)

	// Keep the level current and the station list saved.
	bg := make(chan struct{})
	go func() {
		defer close(bg)
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		n := 0
		for {
			select {
			case <-ctx.Done():
				an.save()
				return
			case <-t.C:
				an.tick()
				if n++; n%10 == 0 {
					an.save()
				}
			}
		}
	}()
	defer func() { <-bg }()

	if s.Replay != "" {
		s.mon.state(StateReplay, "")
		s.log.Info("replaying a recording instead of the radio", "file", s.Replay)
		if err := replay(s.Replay, an, &s.mon, !s.ReplayFast, ctx.Done()); err != nil {
			s.mon.state(StateError, err.Error())
			s.log.Error("replay", "err", err)
		} else if ctx.Err() == nil {
			s.mon.state(StateDone, "")
			st := s.mon.snapshot()
			s.log.Info("replay finished", "frames", st.Decoder.Frames, "slicers", st.Decoder.Slicers)
		}
		<-ctx.Done()
		return
	}
	(&radioRun{cfg: cfg, log: s.log, mon: &s.mon, an: an, record: s.RecordIQ}).run(ctx)
	s.log.Info("SliceAPRS stopped")
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
