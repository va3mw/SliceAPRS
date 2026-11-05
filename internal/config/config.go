// Package config loads, checks and saves the SliceAPRS configuration. The
// file pattern (YAML, defaults, atomic save, OS paths) follows FlexWSPR.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/va3mw/SliceAPRS/internal/geo"
)

// Config is the whole configuration file. The JSON names (used by the GUI)
// match the YAML ones.
type Config struct {
	Callsign string `yaml:"callsign" json:"callsign"`
	Grid     string `yaml:"grid" json:"grid"`

	Radio struct {
		IP     string `yaml:"ip" json:"ip"`         // blank: find the radio by discovery
		Serial string `yaml:"serial" json:"serial"` // with discovery: pick this radio
	} `yaml:"radio" json:"radio"`

	// FrequencyMHz is the APRS channel: 144.390 in North America, 144.800
	// in Europe.
	FrequencyMHz float64 `yaml:"frequency_mhz" json:"frequency_mhz"`
	RxAnt        string  `yaml:"rxant" json:"rxant"` // blank leaves the radio's choice

	DAXIQ struct {
		Channel int  `yaml:"channel" json:"channel"` // 1-4
		SwapIQ  bool `yaml:"swap_iq" json:"swap_iq"` // swap I and Q (mirrors the spectrum)
	} `yaml:"daxiq" json:"daxiq"`

	Openings struct {
		// DistanceKm: any direct path at least this long is an opening.
		DistanceKm float64 `yaml:"distance_km" json:"distance_km"`
		// Sensitivity: low, medium or high (how far beyond normal, and how
		// many stations, make an opening).
		Sensitivity string `yaml:"sensitivity" json:"sensitivity"`
	} `yaml:"openings" json:"openings"`

	Alerts Alerts `yaml:"alerts" json:"alerts"`

	Web struct {
		// Listen is the address of the built-in web page, e.g. ":8080";
		// "off" turns it off.
		Listen string `yaml:"listen" json:"listen"`
	} `yaml:"web" json:"web"`

	DataDir string `yaml:"data_dir" json:"data_dir"` // history: packets, paths, openings, stations
	Debug   bool   `yaml:"debug" json:"debug"`

	FreqHz int64 `yaml:"-" json:"-"` // resolved from FrequencyMHz
}

// Alerts are the voice and Pushover alerts sent when the band opens.
type Alerts struct {
	// Level is the lowest level that alerts: enhanced or open.
	Level string `yaml:"level" json:"level"`
	// Voice speaks alerts on the computer running SliceAPRS.
	Voice    bool     `yaml:"voice" json:"voice"`
	Pushover Pushover `yaml:"pushover" json:"pushover"`
}

// Pushover sends alerts to phones through pushover.net.
type Pushover struct {
	Enabled bool   `yaml:"enabled" json:"enabled"`
	UserKey string `yaml:"user_key" json:"user_key"` // your user (or group) key
	Token   string `yaml:"token" json:"token"`       // the API token of your Pushover application
	// Priority: -2 lowest, -1 quiet, 0 normal, 1 high.
	Priority int `yaml:"priority" json:"priority"`
}

// IQRate is the DAX IQ rate SliceAPRS asks for.
const IQRate = 24000

// Load reads and checks a configuration file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := Default()
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.Resolve(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save writes the configuration to path (atomically), creating its folder.
func (c *Config) Save(path string) error {
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	hdr := "# SliceAPRS configuration (written by SliceAPRS; edit here or in the GUI)\n"
	if err := os.WriteFile(tmp, append([]byte(hdr), b...), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Default returns the defaults applied before the file is read.
func Default() *Config {
	c := &Config{FrequencyMHz: 144.390, DataDir: DefaultDataDir()}
	c.DAXIQ.Channel = 2 // FlexWSPR uses 1
	c.Openings.DistanceKm = 300
	c.Openings.Sensitivity = "medium"
	c.Alerts.Level = "enhanced"
	c.Web.Listen = DefaultListen
	return c
}

// Clone returns a copy.
func (c *Config) Clone() *Config {
	n := *c
	return &n
}

// Ready reports whether the station details needed to run are filled in.
func (c *Config) Ready() bool { return c.Callsign != "" && len(c.Grid) >= 4 }

// Resolve tidies and checks the configuration and fills in derived values.
func (c *Config) Resolve() error {
	c.Callsign = strings.ToUpper(strings.TrimSpace(c.Callsign))
	if b, _, _ := strings.Cut(c.Callsign, "-"); b != c.Callsign {
		c.Callsign = b // the base call covers every SSID
	}
	c.Grid = strings.TrimSpace(c.Grid)
	if c.Grid != "" {
		g := strings.ToUpper(c.Grid)
		if len(g) >= 6 {
			g = g[:4] + strings.ToLower(g[4:6]) + g[6:]
		}
		if _, _, ok := geo.GridLatLon(g); !ok {
			return fmt.Errorf("grid %q is not a Maidenhead locator like FN25 or FN25dk", c.Grid)
		}
		c.Grid = g
	}
	c.Radio.IP = strings.TrimSpace(c.Radio.IP)
	c.RxAnt = strings.ToUpper(strings.TrimSpace(c.RxAnt))
	if c.FrequencyMHz == 0 {
		c.FrequencyMHz = 144.390
	}
	if c.FrequencyMHz < 1 || c.FrequencyMHz > 1300 {
		return fmt.Errorf("frequency %.4f MHz is outside 1-1300 MHz", c.FrequencyMHz)
	}
	c.FreqHz = int64(c.FrequencyMHz*1e6 + 0.5)
	if c.DAXIQ.Channel < 1 || c.DAXIQ.Channel > 4 {
		return fmt.Errorf("daxiq.channel must be 1-4, got %d", c.DAXIQ.Channel)
	}
	if c.Openings.DistanceKm == 0 {
		c.Openings.DistanceKm = 300
	}
	if c.Openings.DistanceKm < 50 || c.Openings.DistanceKm > 5000 {
		return fmt.Errorf("openings.distance_km must be 50-5000, got %.0f", c.Openings.DistanceKm)
	}
	c.Openings.Sensitivity = strings.ToLower(strings.TrimSpace(c.Openings.Sensitivity))
	switch c.Openings.Sensitivity {
	case "":
		c.Openings.Sensitivity = "medium"
	case "low", "medium", "high":
	default:
		return fmt.Errorf("openings.sensitivity must be low, medium or high, got %q", c.Openings.Sensitivity)
	}
	c.Alerts.Level = strings.ToLower(strings.TrimSpace(c.Alerts.Level))
	switch c.Alerts.Level {
	case "":
		c.Alerts.Level = "enhanced"
	case "enhanced", "open":
	default:
		return fmt.Errorf("alerts.level must be enhanced or open, got %q", c.Alerts.Level)
	}
	po := &c.Alerts.Pushover
	po.UserKey, po.Token = strings.TrimSpace(po.UserKey), strings.TrimSpace(po.Token)
	if po.Priority < -2 || po.Priority > 1 {
		return fmt.Errorf("alerts.pushover.priority must be -2 to 1, got %d", po.Priority)
	}
	if po.Enabled && (po.UserKey == "" || po.Token == "") {
		return fmt.Errorf("Pushover needs your user key and an API token")
	}
	if c.DataDir == "" {
		c.DataDir = DefaultDataDir()
	}
	if c.Web.Listen == "" {
		c.Web.Listen = DefaultListen
	}
	return nil
}

// LatLon is the station position from the grid.
func (c *Config) LatLon() (float64, float64, bool) { return geo.GridLatLon(c.Grid) }

// Find picks the config file: the given path, else the OS default, else
// sliceaprs.yaml next to the executable if one is there. When none exists
// it returns where a new one should go.
func Find(flagPath string) string {
	if flagPath != "" {
		return flagPath
	}
	def := DefaultConfigPath()
	if _, err := os.Stat(def); err == nil {
		return def
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "sliceaprs.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return def
}

// LoadOrDefault loads path; a missing file gives the defaults, so a new
// install can start and be set up from the GUI.
func LoadOrDefault(path string) (*Config, bool, error) {
	c, err := Load(path)
	if err == nil {
		return c, true, nil
	}
	if os.IsNotExist(err) {
		c = Default()
		return c, false, c.Resolve()
	}
	return nil, false, err
}
