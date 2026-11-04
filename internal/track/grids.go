package track

import (
	"sort"
	"time"

	"github.com/va3mw/SliceAPRS/internal/geo"
)

// Grid is one 4-character Maidenhead square with a station heard directly
// in it: the grid-chasing log.
type Grid struct {
	Grid     string    `json:"grid"`
	First    time.Time `json:"first"`
	Last     time.Time `json:"last"`
	BestKm   float64   `json:"best_km"`
	BestCall string    `json:"best_call"`
	Bearing  float64   `json:"bearing"` // to the best station
	Calls    []string  `json:"calls"`   // stations heard directly there, first maxGridCalls
	Count    int       `json:"count"`   // direct paths
}

const maxGridCalls = 50

// addGrid records a direct path in the grid log.
func (t *Tracker) addGrid(p Path) {
	if p.Lat == 0 && p.Lon == 0 {
		return
	}
	g := geo.Grid(p.Lat, p.Lon)[:4]
	e := t.grids[g]
	if e == nil {
		e = &Grid{Grid: g, First: p.Time}
		t.grids[g] = e
	}
	if p.Time.Before(e.First) {
		e.First = p.Time
	}
	if p.Time.After(e.Last) {
		e.Last = p.Time
	}
	e.Count++
	if p.Km > e.BestKm || e.BestCall == "" {
		e.BestKm, e.BestCall, e.Bearing = p.Km, p.Call, p.Bearing
	}
	for _, c := range e.Calls {
		if c == p.Call {
			return
		}
	}
	if len(e.Calls) < maxGridCalls {
		e.Calls = append(e.Calls, p.Call)
	}
}

// LoadGrids replaces the grid log with stored grids.
func (t *Tracker) LoadGrids(gs []Grid) {
	t.grids = map[string]*Grid{}
	for i := range gs {
		g := gs[i]
		t.grids[g.Grid] = &g
	}
	t.gridsLoaded = true
}

// Grids returns the grid log, farthest first.
func (t *Tracker) Grids() []Grid {
	out := make([]Grid, 0, len(t.grids))
	for _, g := range t.grids {
		c := *g
		c.Calls = append([]string(nil), g.Calls...)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BestKm > out[j].BestKm })
	return out
}
