package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsAndResolve(t *testing.T) {
	c := Default()
	if err := c.Resolve(); err != nil {
		t.Fatal(err)
	}
	if c.FreqHz != 144390000 || c.DAXIQ.Channel != 2 || c.Openings.DistanceKm != 300 || c.Openings.Sensitivity != "medium" {
		t.Fatalf("%+v", c)
	}
	c.Callsign, c.Grid = " va3mw-9 ", "fn25DK"
	if err := c.Resolve(); err != nil {
		t.Fatal(err)
	}
	if c.Callsign != "VA3MW" || c.Grid != "FN25dk" || !c.Ready() {
		t.Fatalf("%q %q", c.Callsign, c.Grid)
	}
	for _, bad := range []func(*Config){
		func(c *Config) { c.Grid = "XX99" },
		func(c *Config) { c.DAXIQ.Channel = 5 },
		func(c *Config) { c.FrequencyMHz = 0.5 },
		func(c *Config) { c.Openings.Sensitivity = "max" },
		func(c *Config) { c.Openings.DistanceKm = 10 },
	} {
		d := Default()
		bad(d)
		if err := d.Resolve(); err == nil {
			t.Errorf("accepted %+v", d)
		}
	}
}

func TestSaveLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x", "sliceaprs.yaml")
	c := Default()
	c.Callsign, c.Grid, c.FrequencyMHz = "VA3MW", "FN25dk", 144.8
	if err := c.Resolve(); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.FreqHz != 144800000 || got.Callsign != "VA3MW" {
		t.Fatalf("%+v", got)
	}
	if _, saved, err := LoadOrDefault(filepath.Join(t.TempDir(), "none.yaml")); err != nil || saved {
		t.Fatal("missing file should give unsaved defaults")
	}
	os.WriteFile(p, []byte("daxiq: {channel: 9}\n"), 0o644)
	if _, err := Load(p); err == nil {
		t.Fatal("bad file loaded")
	}
}
