package store

import (
	"testing"
	"time"

	"github.com/va3mw/SliceAPRS/internal/track"
)

func TestRoundTrip(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 4, 1, 30, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		tm := now.Add(-time.Duration(i) * 10 * time.Hour)
		s.Path(track.Path{Time: tm, Call: "VE3A", Km: float64(i)})
		s.Packet(track.Heard{Time: tm, Kind: track.KindDirect, Via: "VE3A", Raw: "VE3A>APRS:>x", HasLevel: true, LevelDB: -60, SNR: 20})
	}
	ps, err := s.LoadPaths(now, 14)
	if err != nil || len(ps) != 5 || !ps[0].Time.Before(ps[4].Time) {
		t.Fatalf("paths %v %v", ps, err)
	}
	h, _ := s.LoadHours(now)
	n := 0
	for _, v := range h {
		n += v
	}
	if n != 3 { // 0, 10 and 20 hours ago
		t.Fatalf("hours %v", h)
	}
	r, _ := s.Recent(now, 2)
	if len(r) != 2 || r[1][5] != "VE3A>APRS:>x" || r[1][3] != "-60.0" {
		t.Fatalf("recent %v", r)
	}
	s.Opening(track.Opening{Start: now, End: now.Add(time.Hour), Peak: track.LevelOpen})
	o, _ := s.LoadOpenings()
	if len(o) != 1 || o[0].Peak != track.LevelOpen {
		t.Fatalf("openings %v", o)
	}
	s.SaveStations([]track.Station{{Call: "VE3A", HasPos: true, Lat: 45, Lon: -75}})
	st, err := s.LoadStations()
	if err != nil || len(st) != 1 || st[0].Lat != 45 {
		t.Fatalf("stations %v %v", st, err)
	}
}
