package track

import (
	"sort"
	"time"
)

// Mobiles: stations that move (a vehicle symbol, or a reported speed),
// plotted on the map however they were heard. They play no part in
// openings: where a digipeated mobile is says nothing about the band.

// MobileWindow is how long a mobile stays on the map after its last
// position, and how long its trail is.
const MobileWindow = time.Hour

const maxTrail = 120 // points kept per mobile

// TrailPoint is one reported position of a mobile.
type TrailPoint struct {
	Lat  float64   `json:"lat"`
	Lon  float64   `json:"lon"`
	Time time.Time `json:"t"`
}

type trail struct {
	pts []TrailPoint
	via string // how the last position arrived: "" direct, a digipeater, or a Kind
}

// Mobile is a moving station for the map.
type Mobile struct {
	Call      string       `json:"call"`
	Symbol    string       `json:"symbol,omitempty"`
	Lat       float64      `json:"lat"`
	Lon       float64      `json:"lon"`
	Km        float64      `json:"km"`
	Bearing   float64      `json:"brg"`
	Time      time.Time    `json:"t"` // last position
	HasCourse bool         `json:"has_course,omitempty"`
	Course    int          `json:"course,omitempty"`
	SpeedKmh  float64      `json:"speed_kmh,omitempty"`
	Via       string       `json:"via"` // "" direct, else the digipeater, or a Kind
	ViaPos    bool         `json:"via_pos,omitempty"`
	ViaLat    float64      `json:"via_lat,omitempty"`
	ViaLon    float64      `json:"via_lon,omitempty"`
	Trail     []TrailPoint `json:"trail"` // oldest first, last hour
}

// Vehicle symbols (APRS 1.0.1 and the symbols update). Primary table '/',
// and the alternate table or its overlays.
var (
	primaryMobile   = "><bjkuvRsYUOX'^fa=[P"
	alternateMobile = ">kuvsj^O"
)

// IsMobileSymbol reports whether an APRS symbol (table and code) is a
// vehicle, vessel, aircraft or person on the move.
func IsMobileSymbol(sym string) bool {
	if len(sym) != 2 {
		return false
	}
	set := alternateMobile
	if sym[0] == '/' {
		set = primaryMobile
	}
	for i := 0; i < len(set); i++ {
		if set[i] == sym[1] {
			return true
		}
	}
	return false
}

func (s *Station) mobile() bool { return IsMobileSymbol(s.Symbol) || s.SpeedKmh > 0 }

// addTrail records an accepted position of a mobile.
func (t *Tracker) addTrail(s *Station, via string) {
	if !s.mobile() {
		return
	}
	tr := t.trails[s.Call]
	if tr == nil {
		tr = &trail{}
		t.trails[s.Call] = tr
	}
	tr.via = via
	tr.pts = append(tr.pts, TrailPoint{Lat: s.Lat, Lon: s.Lon, Time: s.PosTime})
	if len(tr.pts) > maxTrail {
		tr.pts = append([]TrailPoint(nil), tr.pts[len(tr.pts)-maxTrail:]...)
	}
}

// trimTrails drops trail points past the window, and mobiles with none.
func (t *Tracker) trimTrails(now time.Time) {
	for call, tr := range t.trails {
		i := 0
		for i < len(tr.pts) && now.Sub(tr.pts[i].Time) > MobileWindow {
			i++
		}
		if i == len(tr.pts) {
			delete(t.trails, call)
		} else if i > 0 {
			tr.pts = append([]TrailPoint(nil), tr.pts[i:]...)
		}
	}
}

// Mobiles returns the mobiles with a position in the last hour, newest
// first.
func (t *Tracker) Mobiles(now time.Time) []Mobile {
	t.trimTrails(now)
	out := []Mobile{}
	for call, tr := range t.trails {
		s := t.stations[call]
		if s == nil || !s.HasPos || !s.mobile() {
			continue
		}
		m := Mobile{Call: call, Symbol: s.Symbol, Lat: s.Lat, Lon: s.Lon, Km: round1(s.Km), Bearing: s.Bearing,
			Time: s.PosTime, HasCourse: s.HasCourse, Course: s.Course, SpeedKmh: s.SpeedKmh, Via: tr.via,
			Trail: append([]TrailPoint(nil), tr.pts...)}
		if d := t.stations[tr.via]; tr.via != "" && d != nil && d.HasPos {
			m.ViaPos, m.ViaLat, m.ViaLon = true, d.Lat, d.Lon
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}
