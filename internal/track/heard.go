// Package track works out, for each packet, which station's signal reached
// this receiver and how far away it is; keeps the station positions; learns
// the usual direct range; and detects 2 m openings. It does no I/O: the app
// feeds it packets and stores what it returns.
package track

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/va3mw/SliceAPRS/internal/aprs"
	"github.com/va3mw/SliceAPRS/internal/ax25"
	"github.com/va3mw/SliceAPRS/internal/geo"
)

// How a packet reached the receiver.
const (
	KindDirect    = "direct"    // the sender itself was heard
	KindDigi      = "digi"      // a digipeater (named) was heard
	KindUnknown   = "via-alias" // repeated by a digipeater that did not add its call
	KindGateway   = "gateway"   // came from the internet (third party, TCPIP)
	KindSatellite = "satellite" // satellite or ISS
	KindOwn       = "own"       // this station's own transmission or digipeat
)

// Pos is a position with the time it was reported.
type Pos struct {
	Lat  float64   `json:"lat"`
	Lon  float64   `json:"lon"`
	Time time.Time `json:"time"`
	Km   float64   `json:"km"`
}

// Signal is the measured level of a frame.
type Signal struct {
	LevelDB  float64
	SNR      float64
	HasLevel bool
}

// Heard is one packet after analysis.
type Heard struct {
	Seq     int64     `json:"seq"`
	Time    time.Time `json:"time"`
	Raw     string    `json:"raw"`
	Src     string    `json:"src"`
	Type    string    `json:"type"`
	Summary string    `json:"summary"`
	Error   string    `json:"error,omitempty"` // APRS parse problem

	Kind string `json:"kind"`
	// Via is the station whose signal reached the antenna: the sender
	// when Kind is direct, else the last digipeater that repeated it.
	Via       string  `json:"via,omitempty"`
	PathKnown bool    `json:"path_known"` // Via's position is known
	Lat       float64 `json:"lat,omitempty"`
	Lon       float64 `json:"lon,omitempty"`
	Km        float64 `json:"km,omitempty"`
	Bearing   float64 `json:"bearing,omitempty"`

	// PktPos is the sender's position in this packet; LastPos what was
	// known before, shown when they disagree.
	PktPos  *Pos `json:"pkt_pos,omitempty"`
	LastPos *Pos `json:"last_pos,omitempty"`
	Jump    bool `json:"jump,omitempty"`

	Counted  bool    `json:"counted"` // a direct path that counts for openings
	Note     string  `json:"note,omitempty"`
	LevelDB  float64 `json:"level_db,omitempty"`
	SNR      float64 `json:"snr,omitempty"`
	HasLevel bool    `json:"has_level"`
}

// Station is what is known about one callsign.
type Station struct {
	Call       string    `json:"call"`
	HasPos     bool      `json:"has_pos"`
	Lat        float64   `json:"lat,omitempty"`
	Lon        float64   `json:"lon,omitempty"`
	PosTime    time.Time `json:"pos_time,omitempty"`
	PosReports int       `json:"pos_reports,omitempty"` // reports at the same spot in a row
	Km         float64   `json:"km,omitempty"`
	Bearing    float64   `json:"bearing,omitempty"`
	Pending    *Pos      `json:"pending,omitempty"` // a jump waiting for a second report
	Symbol     string    `json:"symbol,omitempty"`
	Comment    string    `json:"comment,omitempty"`
	// Course and speed from its last position report, if it sent them.
	HasCourse bool    `json:"has_course,omitempty"`
	Course    int     `json:"course,omitempty"`
	SpeedKmh  float64 `json:"speed_kmh,omitempty"`

	FirstHeard time.Time `json:"first_heard"`
	LastHeard  time.Time `json:"last_heard"`
	Packets    int       `json:"packets"` // packets it sent, by any route
	// Direct counts the times its own signal was heard: its packets heard
	// direct, plus the packets it repeated as a digipeater.
	Direct int `json:"direct"`
	// DirectOwn and DirectRelay split Direct: its own packets heard before
	// any digipeater, and other stations' packets it repeated. Counts from
	// before they were kept are in neither.
	DirectOwn   int       `json:"direct_own"`
	DirectRelay int       `json:"direct_relay"`
	LastDirect  time.Time `json:"last_direct,omitempty"`
	LastVia     string    `json:"last_via"` // "" direct, else the digipeater, or a Kind
	BestKm      float64   `json:"best_km,omitempty"`
	BestTime    time.Time `json:"best_time,omitempty"`
	Digi        bool      `json:"digi,omitempty"` // has repeated packets here
	Usual       bool      `json:"usual,omitempty"`
}

// fixed reports whether the station has sat at one spot for a while.
func (s *Station) fixed() bool { return s.PosReports >= 3 }

// Settings are what the analysis needs from the configuration.
type Settings struct {
	Lat, Lon    float64
	MyCall      string  // base callsign; any SSID is ours
	OpenKm      float64 // any direct path this long is an opening
	Sensitivity string  // low, medium, high
	Satellite   bool    // listening on a satellite frequency
}

// Analysis limits.
const (
	samePlaceKm   = 1.5
	maxSpeedKmh   = 300
	moveLimitKm   = 500
	mobileFresh   = 2 * time.Hour
	fixedFresh    = 30 * 24 * time.Hour
	satelliteFreq = 145825000
)

// IsSatelliteFreq reports whether hz is the ISS/satellite APRS channel.
func IsSatelliteFreq(hz int64) bool { return hz > satelliteFreq-5000 && hz < satelliteFreq+5000 }

func baseCall(c string) string {
	b, _, _ := strings.Cut(c, "-")
	return b
}

// isAlias reports whether a path entry is a generic alias (WIDE2, TRACE,
// RELAY, a state alias like ON2) rather than a station.
func isAlias(a ax25.Address) bool {
	c := a.Call
	switch c {
	case "WIDE", "TRACE", "RELAY", "ECHO", "GATE", "TEMP", "RFONLY", "NOGATE", "HOP", "BEACON", "CQ", "QST", "APRS":
		return true
	}
	if strings.HasPrefix(c, "WIDE") || strings.HasPrefix(c, "TRACE") || strings.HasPrefix(c, "TEMP") || strings.HasPrefix(c, "HOP") {
		return true
	}
	// Letters then one digit (ON2, SS3): an n-N alias. Real calls have
	// letters after the digit.
	n := len(c)
	if n >= 2 && c[n-1] >= '0' && c[n-1] <= '9' {
		letters := true
		for i := 0; i < n-1; i++ {
			if c[i] < 'A' || c[i] > 'Z' {
				letters = false
			}
		}
		if letters {
			return true
		}
	}
	// No digit at all: not a callsign.
	return !strings.ContainsAny(c, "0123456789")
}

var satCalls = map[string]bool{"ARISS": true, "RS0ISS": true, "NA1SS": true, "ISS": true, "SGATE": true,
	"PSAT": true, "PSAT2": true, "PCSAT": true, "W3ADO": true, "APRSAT": true, "SATGTE": true, "RS0ISS-4": true}

func isSatellite(f *ax25.Frame) bool {
	if satCalls[f.Src.Call] || satCalls[f.Dest.Call] {
		return true
	}
	for _, d := range f.Path {
		if satCalls[d.Call] || satCalls[d.String()] {
			return true
		}
	}
	return false
}

func isGateway(f *ax25.Frame) bool {
	if len(f.Info) > 0 && f.Info[0] == '}' {
		return true
	}
	for _, d := range f.Path {
		if d.Call == "TCPIP" || d.Call == "TCPXX" || strings.HasPrefix(d.Call, "QA") && len(d.Call) == 3 {
			return true
		}
	}
	return false
}

// route works out how the frame reached us: its kind and the call of the
// station that was heard.
func (t *Tracker) route(f *ax25.Frame) (kind, via, note string) {
	src := f.Src.String()
	switch {
	case t.set.Satellite:
		return KindSatellite, "", "satellite frequency"
	case isSatellite(f):
		return KindSatellite, "", "satellite or ISS path"
	case isGateway(f):
		return KindGateway, "", "came through an internet gateway"
	case t.set.MyCall != "" && baseCall(src) == t.set.MyCall:
		return KindOwn, src, "my own transmission"
	}
	last := f.LastRepeated()
	if last < 0 {
		return KindDirect, src, ""
	}
	for k := last; k >= 0; k-- {
		d := f.Path[k]
		if !d.H || isAlias(d) {
			continue
		}
		v := d.String()
		if t.set.MyCall != "" && baseCall(v) == t.set.MyCall {
			return KindOwn, v, "repeated by my own digipeater"
		}
		return KindDigi, v, ""
	}
	return KindUnknown, "", "repeated by a digipeater that did not add its callsign"
}

// Packet analyses one frame. p is nil when the information field did not
// parse (perr says why).
func (t *Tracker) Packet(now time.Time, f *ax25.Frame, p *aprs.Packet, perr error, sig Signal) Heard {
	t.seq++
	h := Heard{Seq: t.seq, Time: now, Raw: f.String(), Src: f.Src.String(),
		LevelDB: sig.LevelDB, SNR: sig.SNR, HasLevel: sig.HasLevel}
	if p != nil {
		h.Type, h.Summary = p.Type, p.Summary()
	} else if perr != nil {
		h.Type, h.Error = "invalid", perr.Error()
	}
	h.Kind, h.Via, h.Note = t.route(f)
	t.countHour(now)

	src := t.station(h.Src, now)
	src.Packets++
	src.LastHeard = now
	switch h.Kind {
	case KindDirect:
		src.LastVia = ""
	case KindDigi:
		src.LastVia = h.Via
	default:
		src.LastVia = h.Kind
	}
	if p != nil && p.Symbol != "" && p.OwnPos {
		src.Symbol = p.Symbol
	}
	if p != nil && p.Type == aprs.TypeStatus {
		src.Comment = p.Status
	} else if p != nil && p.OwnPos && p.Comment != "" {
		src.Comment = p.Comment
	}

	// Learn the sender's position from any RF packet that carries its own
	// (not an object's) position.
	jump := false
	if p != nil && p.HasPos && p.OwnPos && h.Kind != KindGateway && usablePos(p) {
		pkt := &Pos{Lat: p.Lat, Lon: p.Lon, Time: now, Km: geo.Km(t.set.Lat, t.set.Lon, p.Lat, p.Lon)}
		h.PktPos = pkt
		if src.HasPos {
			h.LastPos = &Pos{Lat: src.Lat, Lon: src.Lon, Time: src.PosTime, Km: src.Km}
		}
		if p.HasCourse {
			src.HasCourse, src.Course, src.SpeedKmh = true, p.Course, round1(p.SpeedKt*1.852)
		} else {
			src.HasCourse, src.Course, src.SpeedKmh = false, 0, 0
		}
		jump = !t.updatePos(src, pkt)
		if !jump {
			via := h.Kind
			switch h.Kind {
			case KindDirect:
				via = ""
			case KindDigi:
				via = h.Via
			}
			t.addTrail(src, via)
		}
	}

	if h.Kind != KindDirect && h.Kind != KindDigi {
		t.addHeard(h)
		return h
	}
	via := src
	if h.Kind == KindDigi {
		via = t.station(h.Via, now)
		via.Digi = true
	}
	via.Direct++
	if via == src {
		via.DirectOwn++
	} else {
		via.DirectRelay++
	}
	via.LastDirect = now
	if via != src {
		via.LastHeard = now
	}

	switch {
	case h.Kind == KindDirect && jump:
		h.Jump = true
		h.Note = fmt.Sprintf("position jump: last known %.0f km away, this packet says %.0f km; not counted until a second report agrees",
			h.LastPos.Km, h.PktPos.Km)
	case !via.HasPos:
		if h.Kind == KindDigi {
			h.Note = "digipeater position not known yet"
		} else {
			h.Note = "position not known yet"
		}
	case h.PktPos == nil && h.Kind == KindDirect && !via.fixed() && now.Sub(via.PosTime) > mobileFresh:
		h.Note = "last position too old for a moving station"
	case h.Kind == KindDigi && now.Sub(via.PosTime) > fixedFresh:
		h.Note = "digipeater position too old"
	default:
		h.PathKnown, h.Counted = true, true
		h.Lat, h.Lon, h.Km, h.Bearing = via.Lat, via.Lon, via.Km, via.Bearing
		if h.Km > via.BestKm || via.BestTime.IsZero() {
			via.BestKm, via.BestTime = h.Km, now
		}
	}
	t.addHeard(h)
	if h.Counted {
		t.addPath(PathOf(h))
	}
	return h
}

// PathOf is the direct path of a counted packet.
func PathOf(h Heard) Path {
	p := Path{Time: h.Time, Call: h.Via, Km: round1(h.Km), Bearing: round1(h.Bearing),
		Lat: h.Lat, Lon: h.Lon, LevelDB: round1(h.LevelDB), SNR: round1(h.SNR), HasLevel: h.HasLevel}
	if h.Kind == KindDigi {
		p.Src = h.Src
	}
	return p
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// usablePos rejects positions that cannot be measured: none, 0/0, or only
// to the nearest degree.
func usablePos(p *aprs.Packet) bool {
	if p.Ambiguity >= 4 {
		return false
	}
	if math.Abs(p.Lat) < 0.01 && math.Abs(p.Lon) < 0.01 {
		return false
	}
	return math.Abs(p.Lat) <= 90 && math.Abs(p.Lon) <= 180
}

// updatePos records a reported position. It returns false when the
// report is a jump (too far, too fast) not yet confirmed by a second
// report; the station keeps its old position then.
func (t *Tracker) updatePos(s *Station, p *Pos) bool {
	accept := func(reports int) {
		s.HasPos, s.Lat, s.Lon, s.PosTime, s.PosReports, s.Pending = true, p.Lat, p.Lon, p.Time, reports, nil
		s.Km = p.Km
		s.Bearing = round1(geo.Bearing(t.set.Lat, t.set.Lon, p.Lat, p.Lon))
	}
	if !s.HasPos {
		accept(1)
		return true
	}
	d := geo.Km(s.Lat, s.Lon, p.Lat, p.Lon)
	if d < samePlaceKm {
		accept(s.PosReports + 1)
		return true
	}
	hours := math.Max(p.Time.Sub(s.PosTime).Hours(), 1.0/60)
	if d/hours <= maxSpeedKmh && d <= moveLimitKm {
		accept(1) // moving
		return true
	}
	if s.Pending != nil && geo.Km(s.Pending.Lat, s.Pending.Lon, p.Lat, p.Lon) < samePlaceKm {
		accept(2) // the new spot is confirmed
		return true
	}
	s.Pending = p
	return false
}
