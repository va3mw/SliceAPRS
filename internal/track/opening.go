package track

import (
	"math"
	"sort"
	"time"

	"github.com/va3mw/SliceAPRS/internal/geo"
)

// Band levels.
const (
	LevelNormal   = "normal"
	LevelEnhanced = "enhanced"
	LevelOpen     = "open"
)

// Window is how far back the opening rules look.
const Window = 30 * time.Minute

// Rules is what a sensitivity setting means.
type Rules struct {
	Factor   float64 `json:"factor"`   // beyond normal × Factor is far
	MarginKm float64 `json:"margin"`   // and at least this much beyond normal
	Enhanced int     `json:"enhanced"` // far stations in Window for Enhanced
	Open     int     `json:"open"`     // far stations in Window for Open
}

// RulesFor returns the rules for "low", "medium" or "high".
func RulesFor(sens string) Rules {
	switch sens {
	case "low":
		return Rules{Factor: 1.5, MarginKm: 60, Enhanced: 2, Open: 4}
	case "high":
		return Rules{Factor: 1.15, MarginKm: 25, Enhanced: 1, Open: 2}
	}
	return Rules{Factor: 1.3, MarginKm: 40, Enhanced: 1, Open: 3}
}

// FarStation is a station heard beyond normal range during an opening.
type FarStation struct {
	Call    string    `json:"call"`
	Km      float64   `json:"km"`
	Bearing float64   `json:"bearing"`
	Lat     float64   `json:"lat"`
	Lon     float64   `json:"lon"`
	First   time.Time `json:"first"`
	Last    time.Time `json:"last"`
	Count   int       `json:"count"`
	SNR     float64   `json:"snr,omitempty"`
}

// Opening is one period of enhanced propagation.
type Opening struct {
	Start     time.Time    `json:"start"`
	End       time.Time    `json:"end"` // last far path; zero while active
	Active    bool         `json:"active"`
	Peak      string       `json:"peak"` // enhanced or open
	PeakCount int          `json:"peak_count"`
	MaxKm     float64      `json:"max_km"`
	MaxCall   string       `json:"max_call"`
	NormalKm  float64      `json:"normal_km"` // normal at the start
	Bearing   float64      `json:"bearing"`   // mean bearing of the far stations
	Spread    float64      `json:"spread"`    // their angular spread, degrees
	Direction string       `json:"direction"`
	Strength  string       `json:"strength"` // weak, moderate, strong
	Stations  []FarStation `json:"stations"`
}

// threshold is the distance beyond which a path at time tm is far.
func (t *Tracker) threshold(tm time.Time, r Rules) (float64, bool) {
	n, _, ok := t.Normal(int(tm.UTC().Hour()))
	if !ok {
		return 0, false
	}
	return math.Max(n*r.Factor, n+r.MarginKm), true
}

// qualifies reports whether a path counts towards an opening.
func (t *Tracker) qualifies(p Path, r Rules) bool {
	if t.set.OpenKm > 0 && p.Km >= t.set.OpenKm {
		return true
	}
	if t.usual[p.Call] {
		return false
	}
	thr, ok := t.threshold(p.Time, r)
	return ok && p.Km > thr
}

// Evaluate updates the level and the current opening at now. The app calls
// it for each path and once a minute.
func (t *Tracker) Evaluate(now time.Time) {
	if now.Sub(t.calcAt) >= recalcEvery {
		t.recalc(now)
	}
	r := RulesFor(t.set.Sensitivity)
	far := map[string]bool{}
	abs := false
	var first time.Time
	for _, p := range t.Paths(now.Add(-Window)) {
		if !t.qualifies(p, r) {
			continue
		}
		if first.IsZero() {
			first = p.Time
		}
		far[p.Call] = true
		if t.set.OpenKm > 0 && p.Km >= t.set.OpenKm {
			abs = true
		}
	}
	level := LevelNormal
	switch {
	case abs || len(far) >= r.Open:
		level = LevelOpen
	case len(far) >= r.Enhanced && len(far) > 0:
		level = LevelEnhanced
	}
	t.level = level

	if level == LevelNormal {
		if t.cur != nil {
			o := *t.cur
			o.Active = false
			o.End = o.lastSeen()
			t.openings = append(t.openings, o)
			t.cur = nil
			if t.onOpenDone != nil {
				t.onOpenDone(o)
			}
		}
		return
	}
	if t.cur == nil {
		n, _, _ := t.Normal(int(first.UTC().Hour()))
		t.cur = &Opening{Start: first, Active: true, NormalKm: n}
	}
	o := t.cur
	if rank(level) > rank(o.Peak) {
		o.Peak = level
	}
	o.PeakCount = max(o.PeakCount, len(far))
	// Gather every qualifying path since the opening started.
	byCall := map[string]*FarStation{}
	for _, fs := range o.Stations {
		c := fs
		byCall[fs.Call] = &c
	}
	for _, p := range t.Paths(o.Start) {
		if !t.qualifies(p, r) {
			continue
		}
		fs := byCall[p.Call]
		if fs == nil {
			fs = &FarStation{Call: p.Call, First: p.Time}
			byCall[p.Call] = fs
		}
		if !p.Time.After(fs.Last) && fs.Count > 0 {
			continue // already counted
		}
		fs.Count++
		fs.Last = p.Time
		if p.Km >= fs.Km {
			fs.Km, fs.Bearing, fs.Lat, fs.Lon, fs.SNR = p.Km, p.Bearing, p.Lat, p.Lon, p.SNR
		}
	}
	o.Stations = o.Stations[:0]
	for _, fs := range byCall {
		o.Stations = append(o.Stations, *fs)
	}
	sort.Slice(o.Stations, func(i, j int) bool { return o.Stations[i].Km > o.Stations[j].Km })
	if len(o.Stations) > 0 {
		o.MaxKm, o.MaxCall = o.Stations[0].Km, o.Stations[0].Call
	}
	o.Bearing, o.Spread = meanBearing(o.Stations)
	o.Direction = geo.Compass(o.Bearing)
	if o.Spread > 60 {
		o.Direction = "several directions"
	}
	o.Strength = strength(o)
}

func (o *Opening) lastSeen() time.Time {
	var last time.Time
	for _, s := range o.Stations {
		if s.Last.After(last) {
			last = s.Last
		}
	}
	if last.IsZero() {
		return o.Start
	}
	return last
}

func rank(l string) int {
	switch l {
	case LevelOpen:
		return 2
	case LevelEnhanced:
		return 1
	}
	return 0
}

// meanBearing is the circular mean of the far stations' bearings and their
// spread (circular standard deviation), both in degrees.
func meanBearing(st []FarStation) (float64, float64) {
	if len(st) == 0 {
		return 0, 0
	}
	var x, y float64
	for _, s := range st {
		a := s.Bearing * math.Pi / 180
		x += math.Cos(a)
		y += math.Sin(a)
	}
	n := float64(len(st))
	r := math.Hypot(x, y) / n
	mean := math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
	spread := 0.0
	if r < 1 {
		spread = math.Sqrt(-2*math.Log(math.Max(r, 1e-9))) * 180 / math.Pi
	}
	return round1(mean), round1(spread)
}

// strength rates an opening by how many stations were far and how far.
func strength(o *Opening) string {
	ratio := 0.0
	if o.NormalKm > 0 {
		ratio = o.MaxKm / o.NormalKm
	}
	switch {
	case len(o.Stations) >= 6 || ratio >= 3:
		return "strong"
	case len(o.Stations) >= 3 || ratio >= 2:
		return "moderate"
	}
	return "weak"
}

// State is the opening picture now, for the main screen.
type State struct {
	Level      string    `json:"level"`
	Current    *Opening  `json:"current,omitempty"`
	NormalKm   float64   `json:"normal_km"`
	NormalOK   bool      `json:"normal_ok"`
	Pooled     bool      `json:"pooled"` // normal is for all hours, not enough per hour yet
	FarKm      float64   `json:"far_km"` // threshold for far now
	Rules      Rules     `json:"rules"`
	OpenKm     float64   `json:"open_km"`
	Far15      *Path     `json:"far15,omitempty"`
	Far60      *Path     `json:"far60,omitempty"`
	Usual      int       `json:"usual"`
	FarNow     []string  `json:"far_now"` // stations beyond normal in the window
	Satellite  bool      `json:"satellite"`
	At         time.Time `json:"at"`
	PathsHour  int       `json:"paths_hour"`
	StationsHr int       `json:"stations_hour"`
}

// State returns the opening picture at now.
func (t *Tracker) State(now time.Time) State {
	r := RulesFor(t.set.Sensitivity)
	s := State{Level: t.level, Rules: r, OpenKm: t.set.OpenKm, Usual: len(t.usual), Satellite: t.set.Satellite, At: now}
	s.NormalKm, s.Pooled, s.NormalOK = t.Normal(now.UTC().Hour())
	if thr, ok := t.threshold(now, r); ok {
		s.FarKm = thr
	}
	if t.cur != nil {
		c := *t.cur
		c.Stations = append([]FarStation(nil), t.cur.Stations...)
		s.Current = &c
	}
	s.Far15 = t.Farthest(now.Add(-15 * time.Minute))
	s.Far60 = t.Farthest(now.Add(-time.Hour))
	calls := map[string]bool{}
	far := map[string]bool{}
	for _, p := range t.Paths(now.Add(-time.Hour)) {
		s.PathsHour++
		calls[p.Call] = true
		if now.Sub(p.Time) <= Window && t.qualifies(p, r) {
			far[p.Call] = true
		}
	}
	s.StationsHr = len(calls)
	s.FarNow = []string{}
	for c := range far {
		s.FarNow = append(s.FarNow, c)
	}
	sort.Strings(s.FarNow)
	return s
}

// Openings returns the logged openings, newest first, and the current one
// if any at the top.
func (t *Tracker) Openings() []Opening {
	out := make([]Opening, 0, len(t.openings)+1)
	if t.cur != nil {
		c := *t.cur
		c.Stations = append([]FarStation(nil), t.cur.Stations...)
		out = append(out, c)
	}
	for i := len(t.openings) - 1; i >= 0; i-- {
		out = append(out, t.openings[i])
	}
	return out
}
