package track

import (
	"math"
	"slices"
	"sort"
	"time"

	"github.com/va3mw/SliceAPRS/internal/geo"
)

// Path is one direct path that counts: a station heard directly (its own
// packet, or a packet it repeated) at a known distance.
type Path struct {
	Time     time.Time `json:"t"`
	Call     string    `json:"call"`          // station heard
	Src      string    `json:"src,omitempty"` // original sender, when Call repeated it
	Km       float64   `json:"km"`
	Bearing  float64   `json:"brg"`
	Lat      float64   `json:"lat"`
	Lon      float64   `json:"lon"`
	LevelDB  float64   `json:"lv,omitempty"`
	SNR      float64   `json:"snr,omitempty"`
	HasLevel bool      `json:"hl,omitempty"`
}

// History lengths.
const (
	NormalDays  = 14             // the normal range learns from this long
	keepPaths   = 26 * time.Hour // detailed paths kept in memory
	keepHeard   = 1000           // packets kept for the Packets tab
	usualDays   = 3              // heard directly on this many days: usual
	minNormalN  = 15             // station-hours needed for a normal value
	recalcEvery = 10 * time.Minute
)

type aggKey struct {
	hour int64 // Unix hour
	call string
}

// Tracker holds the analysis state. It is not safe for concurrent use.
type Tracker struct {
	set      Settings
	stations map[string]*Station
	paths    []Path             // last keepPaths, oldest first
	agg      map[aggKey]float64 // farthest km per station per hour, NormalDays
	heard    []Heard            // newest last
	seq      int64
	hours    map[int64]int // packets decoded per Unix hour, last 26 h
	grids    map[string]*Grid
	// gridsLoaded is set when the grid log came from its file; otherwise
	// LoadPaths rebuilds it from the stored paths.
	gridsLoaded bool

	normal     [24]float64
	normalOK   [24]bool
	pooled     float64
	pooledOK   bool
	usual      map[string]bool
	calcAt     time.Time
	openings   []Opening // closed, oldest first
	cur        *Opening
	level      string
	onOpenDone func(Opening)
}

// New returns a tracker.
func New(s Settings) *Tracker {
	return &Tracker{set: s, stations: map[string]*Station{}, agg: map[aggKey]float64{},
		hours: map[int64]int{}, grids: map[string]*Grid{}, usual: map[string]bool{}, level: LevelNormal}
}

// SetSettings changes the settings, keeping the history. Distances and
// bearings are worked out again if the home position moved.
func (t *Tracker) SetSettings(s Settings) {
	moved := s.Lat != t.set.Lat || s.Lon != t.set.Lon
	t.set = s
	if moved {
		for _, st := range t.stations {
			if st.HasPos {
				st.Km = geo.Km(s.Lat, s.Lon, st.Lat, st.Lon)
				st.Bearing = round1(geo.Bearing(s.Lat, s.Lon, st.Lat, st.Lon))
			}
		}
	}
	t.calcAt = time.Time{}
}

// OnOpeningDone sets a function called when an opening ends.
func (t *Tracker) OnOpeningDone(fn func(Opening)) { t.onOpenDone = fn }

func (t *Tracker) station(call string, now time.Time) *Station {
	s := t.stations[call]
	if s == nil {
		s = &Station{Call: call, FirstHeard: now}
		t.stations[call] = s
	}
	return s
}

func (t *Tracker) countHour(now time.Time) { t.hours[now.Unix()/3600]++ }

func (t *Tracker) addHeard(h Heard) {
	t.heard = append(t.heard, h)
	if len(t.heard) > keepHeard+keepHeard/4 {
		t.heard = append([]Heard(nil), t.heard[len(t.heard)-keepHeard:]...)
	}
}

// AddPast puts a packet from the log back in the packet list, at start-up.
func (t *Tracker) AddPast(h Heard) {
	t.seq++
	h.Seq = t.seq
	t.addHeard(h)
}

func (t *Tracker) addPath(p Path) {
	t.paths = append(t.paths, p)
	k := aggKey{p.Time.Unix() / 3600, p.Call}
	if p.Km > t.agg[k] {
		t.agg[k] = p.Km
	}
	t.addGrid(p)
	t.Evaluate(p.Time)
}

// LoadPaths adds stored paths (oldest first) at start-up.
func (t *Tracker) LoadPaths(ps []Path, now time.Time) {
	for _, p := range ps {
		if now.Sub(p.Time) > NormalDays*24*time.Hour {
			continue
		}
		k := aggKey{p.Time.Unix() / 3600, p.Call}
		if p.Km > t.agg[k] {
			t.agg[k] = p.Km
		}
		if now.Sub(p.Time) <= keepPaths {
			t.paths = append(t.paths, p)
		}
		if !t.gridsLoaded {
			t.addGrid(p)
		}
	}
	sort.SliceStable(t.paths, func(i, j int) bool { return t.paths[i].Time.Before(t.paths[j].Time) })
	t.calcAt = time.Time{}
}

// LoadHours adds stored packet counts per Unix hour.
func (t *Tracker) LoadHours(h map[int64]int) {
	for k, v := range h {
		t.hours[k] += v
	}
}

// LoadStations replaces the station list with stored stations.
func (t *Tracker) LoadStations(st []Station) {
	for i := range st {
		s := st[i]
		if s.HasPos {
			s.Km = geo.Km(t.set.Lat, t.set.Lon, s.Lat, s.Lon)
			s.Bearing = round1(geo.Bearing(t.set.Lat, t.set.Lon, s.Lat, s.Lon))
		}
		t.stations[s.Call] = &s
	}
}

// LoadOpenings adds stored openings (oldest first).
func (t *Tracker) LoadOpenings(o []Opening) { t.openings = append(t.openings, o...) }

// trim drops history past its keeping time.
func (t *Tracker) trim(now time.Time) {
	i := 0
	for i < len(t.paths) && now.Sub(t.paths[i].Time) > keepPaths {
		i++
	}
	if i > 0 {
		t.paths = append([]Path(nil), t.paths[i:]...)
	}
	oldest := now.Add(-NormalDays*24*time.Hour).Unix() / 3600
	for k := range t.agg {
		if k.hour < oldest {
			delete(t.agg, k)
		}
	}
	for k := range t.hours {
		if k < now.Unix()/3600-26 {
			delete(t.hours, k)
		}
	}
}

// recalc learns the normal range per UTC hour of day and the usual
// stations from the last NormalDays, leaving out the last two hours and
// the openings already logged.
func (t *Tracker) recalc(now time.Time) {
	t.trim(now)
	t.calcAt = now
	cut := now.Add(-2*time.Hour).Unix() / 3600
	inOpening := func(hour int64) bool {
		a, b := time.Unix(hour*3600, 0), time.Unix((hour+1)*3600, 0)
		for _, o := range t.openings {
			if o.Start.Before(b) && o.End.After(a) {
				return true
			}
		}
		return false
	}
	type dayCall struct {
		day  int64
		call string
	}
	var byHour [24]map[dayCall]float64
	pooled := map[dayCall]float64{}
	days := map[string]map[int64]bool{}
	skip := map[int64]bool{}
	for k, km := range t.agg {
		if days[k.call] == nil {
			days[k.call] = map[int64]bool{}
		}
		days[k.call][k.hour/24] = true
		if k.hour >= cut {
			continue
		}
		sk, seen := skip[k.hour]
		if !seen {
			sk = inOpening(k.hour)
			skip[k.hour] = sk
		}
		if sk {
			continue
		}
		dc := dayCall{k.hour / 24, k.call}
		hod := int(k.hour % 24)
		// Each hour also counts for its neighbours, to smooth the curve.
		for _, h := range []int{(hod + 23) % 24, hod, (hod + 1) % 24} {
			if byHour[h] == nil {
				byHour[h] = map[dayCall]float64{}
			}
			if km > byHour[h][dc] {
				byHour[h][dc] = km
			}
		}
		if km > pooled[dc] {
			pooled[dc] = km
		}
	}
	for h := 0; h < 24; h++ {
		vals := make([]float64, 0, len(byHour[h]))
		dset := map[int64]bool{}
		for dc, v := range byHour[h] {
			vals = append(vals, v)
			dset[dc.day] = true
		}
		t.normal[h], t.normalOK[h] = 0, false
		if len(vals) >= minNormalN && len(dset) >= 2 {
			t.normal[h], t.normalOK[h] = pct(vals, 0.9), true
		}
	}
	vals := make([]float64, 0, len(pooled))
	for _, v := range pooled {
		vals = append(vals, v)
	}
	t.pooled, t.pooledOK = 0, len(vals) >= minNormalN
	if t.pooledOK {
		t.pooled = pct(vals, 0.9)
	}
	t.usual = map[string]bool{}
	for call, d := range days {
		if len(d) >= usualDays {
			t.usual[call] = true
		}
	}
	for call, s := range t.stations {
		s.Usual = t.usual[call]
	}
}

func pct(v []float64, p float64) float64 {
	slices.Sort(v)
	i := int(math.Round(p * float64(len(v)-1)))
	return round1(v[i])
}

// Normal is the usual farthest direct distance at a UTC hour: the 90th
// percentile of each station's farthest distance per hour over the last
// NormalDays. ok is false while there is too little history; pooled is
// true when the value is for all hours together.
func (t *Tracker) Normal(hour int) (km float64, pooled, ok bool) {
	if t.normalOK[hour] {
		return t.normal[hour], false, true
	}
	if t.pooledOK {
		return t.pooled, true, true
	}
	return 0, false, false
}

// Stations returns copies of all stations.
func (t *Tracker) Stations() []Station {
	out := make([]Station, 0, len(t.stations))
	for _, s := range t.stations {
		c := *s
		if s.Pending != nil {
			p := *s.Pending
			c.Pending = &p
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastHeard.After(out[j].LastHeard) })
	return out
}

// HeardSince returns packets with Seq after seq, newest last.
func (t *Tracker) HeardSince(seq int64) []Heard {
	i := sort.Search(len(t.heard), func(i int) bool { return t.heard[i].Seq > seq })
	return append([]Heard(nil), t.heard[i:]...)
}

// Paths returns the direct paths heard since a time, oldest first.
func (t *Tracker) Paths(since time.Time) []Path {
	i := sort.Search(len(t.paths), func(i int) bool { return !t.paths[i].Time.Before(since) })
	return append([]Path(nil), t.paths[i:]...)
}

// Farthest is the farthest direct path since a time, or nil.
func (t *Tracker) Farthest(since time.Time) *Path {
	var best *Path
	for _, p := range t.Paths(since) {
		if best == nil || p.Km > best.Km {
			q := p
			best = &q
		}
	}
	return best
}

// HourCell is one UTC hour of the last day.
type HourCell struct {
	Start    time.Time `json:"start"`
	Packets  int       `json:"packets"` // all packets decoded
	Stations int       `json:"stations"`
	MaxKm    float64   `json:"max_km"`
	MaxCall  string    `json:"max_call,omitempty"`
	NormalKm float64   `json:"normal_km"`
	NormalOK bool      `json:"normal_ok"`
}

// Day returns the last 24 UTC hours, the current one last.
func (t *Tracker) Day(now time.Time) []HourCell {
	cur := now.Unix() / 3600
	out := make([]HourCell, 24)
	for i := range out {
		h := cur - 23 + int64(i)
		c := HourCell{Start: time.Unix(h*3600, 0).UTC(), Packets: t.hours[h]}
		c.NormalKm, _, c.NormalOK = t.Normal(int(h % 24))
		calls := map[string]bool{}
		for _, p := range t.paths {
			if p.Time.Unix()/3600 != h {
				continue
			}
			calls[p.Call] = true
			if p.Km > c.MaxKm {
				c.MaxKm, c.MaxCall = p.Km, p.Call
			}
		}
		c.Stations = len(calls)
		out[i] = c
	}
	return out
}

// UsualCount is how many stations are usually heard directly.
func (t *Tracker) UsualCount() int { return len(t.usual) }
