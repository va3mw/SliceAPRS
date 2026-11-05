package track

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/va3mw/SliceAPRS/internal/aprs"
	"github.com/va3mw/SliceAPRS/internal/ax25"
	"github.com/va3mw/SliceAPRS/internal/geo"
)

// Home is FN25dk, near Ottawa.
var homeLat, homeLon, _ = geo.GridLatLon("FN25dk")

// PosInfo formats a plain APRS position report.
func PosInfo(lat, lon float64, sym byte) string {
	ns, ew := 'N', 'W'
	if lat < 0 {
		ns, lat = 'S', -lat
	}
	if lon >= 0 {
		ew = 'E'
	} else {
		lon = -lon
	}
	ld, lm := math.Floor(lat), (lat-math.Floor(lat))*60
	od, om := math.Floor(lon), (lon-math.Floor(lon))*60
	return fmt.Sprintf("!%02.0f%05.2f%c/%03.0f%05.2f%c%c", ld, lm, ns, od, om, ew, sym)
}

func newT() *Tracker {
	return New(Settings{Lat: homeLat, Lon: homeLon, MyCall: "VA3MW", OpenKm: 300, Sensitivity: "medium"})
}

func feed(t *testing.T, tr *Tracker, now time.Time, tnc2 string) Heard {
	t.Helper()
	f, err := ax25.ParseTNC2(tnc2)
	if err != nil {
		t.Fatalf("%s: %v", tnc2, err)
	}
	p, perr := aprs.Parse(f.Info, f.Dest.Call)
	return tr.Packet(now, f, p, perr, Signal{})
}

func at(km, brg float64) (float64, float64) { return geo.Destination(homeLat, homeLon, brg, km) }

func TestRoutes(t *testing.T) {
	tr := newT()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	dlat, dlon := at(60, 200)
	// A digipeater beacons its own position, heard direct.
	h := feed(t, tr, now, "VE3DIG-1>APN391,WIDE2-1:"+PosInfo(dlat, dlon, '#'))
	if h.Kind != KindDirect || !h.Counted || math.Abs(h.Km-60) > 0.5 {
		t.Fatalf("digi beacon: %+v", h)
	}
	// A far station relayed by that digi: the path is the digi's, 60 km.
	flat, flon := at(450, 200)
	h = feed(t, tr, now.Add(time.Minute), "VE3FAR>APRS,VE3DIG-1*,WIDE2-1:"+PosInfo(flat, flon, '>'))
	if h.Kind != KindDigi || h.Via != "VE3DIG-1" || !h.Counted || math.Abs(h.Km-60) > 0.5 {
		t.Fatalf("relayed: %+v", h)
	}
	// Tracing with an alias after the call: still the digi.
	h = feed(t, tr, now.Add(2*time.Minute), "VE3FAR>APRS,VE3DIG-1,WIDE1*,WIDE2-1:>hello")
	if h.Kind != KindDigi || h.Via != "VE3DIG-1" {
		t.Fatalf("traced alias: %+v", h)
	}
	// A digi that did not add its call: unknown, not counted.
	h = feed(t, tr, now.Add(3*time.Minute), "VE3FAR>APRS,WIDE2*:>hello")
	if h.Kind != KindUnknown || h.Counted {
		t.Fatalf("untraced: %+v", h)
	}
	// Unknown digipeater position.
	h = feed(t, tr, now.Add(4*time.Minute), "VE3FAR>APRS,VE3NEW*:>hello")
	if h.Kind != KindDigi || h.Counted || h.Note == "" {
		t.Fatalf("unknown digi: %+v", h)
	}
	// Internet traffic, satellites and my own packets never count.
	for _, s := range []string{
		"VE3GW>APRS,VE3DIG-1*:}VE3XX>APRS,TCPIP,VE3GW*:" + PosInfo(flat, flon, '>'),
		"VE3XX>APRS,TCPIP*:" + PosInfo(flat, flon, '>'),
		"VE3SAT>APRS,ARISS*:" + PosInfo(flat, flon, '>'),
		"RS0ISS>CQ,RS0ISS*:>ISS",
		"VA3MW-9>APRS,WIDE1-1:" + PosInfo(flat, flon, '>'),
		"VE3FAR>APRS,VA3MW-2*:>via my digi",
	} {
		h := feed(t, tr, now.Add(5*time.Minute), s)
		if h.Counted {
			t.Errorf("%s counted: %+v", s, h)
		}
	}
	// The relayed station's position was learned anyway, at 450 km.
	if s := tr.stations["VE3FAR"]; !s.HasPos || math.Abs(s.Km-450) > 1 || s.LastVia != KindOwn {
		t.Fatalf("VE3FAR: %+v", s)
	}
	// Objects do not move the sender.
	olat, olon := at(500, 90)
	h = feed(t, tr, now.Add(6*time.Minute), fmt.Sprintf("VE3DIG-1>APRS:;NET      *111111z%s", PosInfo(olat, olon, 'n')[1:]))
	if math.Abs(h.Km-60) > 0.5 {
		t.Fatalf("object moved the digi: %+v", h)
	}
	// What was heard directly: the digi's own beacon and object, and the
	// two packets it repeated for VE3FAR, who was never heard itself.
	if d := tr.stations["VE3DIG-1"]; d.Direct != 4 || d.DirectOwn != 2 || d.DirectRelay != 2 {
		t.Fatalf("VE3DIG-1 direct %d own %d relay %d", d.Direct, d.DirectOwn, d.DirectRelay)
	}
	if f := tr.stations["VE3FAR"]; f.Direct != 0 || f.DirectOwn != 0 || f.DirectRelay != 0 {
		t.Fatalf("VE3FAR direct %d own %d relay %d", f.Direct, f.DirectOwn, f.DirectRelay)
	}
}

func TestPositionJump(t *testing.T) {
	tr := newT()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	la, lo := at(40, 10)
	for i := 0; i < 3; i++ {
		feed(t, tr, now.Add(time.Duration(i)*10*time.Minute), "VE3HOM>APRS:"+PosInfo(la, lo, '-'))
	}
	// One report 800 km away a minute later: a jump, not counted.
	ja, jo := at(800, 270)
	h := feed(t, tr, now.Add(31*time.Minute), "VE3HOM>APRS:"+PosInfo(ja, jo, '-'))
	if !h.Jump || h.Counted || h.LastPos == nil || h.PktPos == nil || math.Abs(h.PktPos.Km-800) > 1 || math.Abs(h.LastPos.Km-40) > 1 {
		t.Fatalf("jump: %+v", h)
	}
	// Back home: counts at 40 km.
	h = feed(t, tr, now.Add(32*time.Minute), "VE3HOM>APRS:"+PosInfo(la, lo, '-'))
	if h.Jump || !h.Counted || math.Abs(h.Km-40) > 0.5 {
		t.Fatalf("back: %+v", h)
	}
	// A status from a fixed station uses its known position.
	h = feed(t, tr, now.Add(5*time.Hour), "VE3HOM>APRS:>status")
	if !h.Counted || math.Abs(h.Km-40) > 0.5 {
		t.Fatalf("status: %+v", h)
	}
	// A real move, confirmed by a second report.
	feed(t, tr, now.Add(5*time.Hour+time.Minute), "VE3HOM>APRS:"+PosInfo(ja, jo, '-'))
	h = feed(t, tr, now.Add(5*time.Hour+2*time.Minute), "VE3HOM>APRS:"+PosInfo(ja, jo, '-'))
	if h.Jump || !h.Counted || math.Abs(h.Km-800) > 1 {
		t.Fatalf("confirmed move: %+v", h)
	}
}

// local builds a normal day's traffic: fixed stations 20-140 km heard
// directly every 20 minutes.
func local(t *testing.T, tr *Tracker, day time.Time) {
	for i := 0; i < 72; i++ {
		tm := day.Add(time.Duration(i) * 20 * time.Minute)
		for k := 0; k < 8; k++ {
			la, lo := at(20+float64(k)*17, float64(k)*45)
			feed(t, tr, tm.Add(time.Duration(k)*time.Second), fmt.Sprintf("VE3L%02d>APRS:%s", k, PosInfo(la, lo, '-')))
		}
		tr.Evaluate(tm.Add(time.Minute))
	}
}

func TestOpeningDetection(t *testing.T) {
	tr := newT()
	var done []Opening
	tr.OnOpeningDone(func(o Opening) { done = append(done, o) })
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for d := 0; d < 10; d++ {
		local(t, tr, start.AddDate(0, 0, d))
	}
	now := start.AddDate(0, 0, 10).Add(14 * time.Hour)
	tr.Evaluate(now)
	n, pooled, ok := tr.Normal(14)
	if !ok || pooled || n < 130 || n > 145 {
		t.Fatalf("normal %.1f pooled %v ok %v", n, pooled, ok)
	}
	if tr.UsualCount() != 8 {
		t.Fatalf("usual %d", tr.UsualCount())
	}
	if s := tr.State(now); s.Level != LevelNormal {
		t.Fatalf("level %s", s.Level)
	}
	// A far station relayed by a local digipeater does not open the band.
	fa, fo := at(600, 225)
	l0a, l0o := at(20, 0)
	feed(t, tr, now, "VE3L00>APRS:"+PosInfo(l0a, l0o, '-'))
	feed(t, tr, now.Add(time.Second), "W2FAR>APRS,VE3L00*:"+PosInfo(fa, fo, '>'))
	if s := tr.State(now.Add(time.Second)); s.Level != LevelNormal {
		t.Fatalf("relayed far station opened the band: %s", s.Level)
	}

	// One new station beyond normal: enhanced.
	t0 := now.Add(5 * time.Minute)
	la, lo := at(210, 230)
	feed(t, tr, t0, "W2AAA>APRS:"+PosInfo(la, lo, '-'))
	if s := tr.State(t0); s.Level != LevelEnhanced || s.Current == nil {
		t.Fatalf("one far: level %s", s.Level)
	}
	// Two more within the window: open, toward the south-west.
	for i, km := range []float64{240, 260} {
		la, lo := at(km, 220+float64(i)*15)
		feed(t, tr, t0.Add(time.Duration(i+1)*4*time.Minute), fmt.Sprintf("W2AB%d>APRS:%s", i, PosInfo(la, lo, '-')))
	}
	s := tr.State(t0.Add(9 * time.Minute))
	if s.Level != LevelOpen || s.Current == nil || len(s.Current.Stations) != 3 || s.Current.Direction != "SW" {
		t.Fatalf("open: %+v %+v", s.Level, s.Current)
	}
	// Quiet for the window: the opening ends at the last far packet.
	for m := 10; m <= 50; m += 5 {
		tr.Evaluate(t0.Add(time.Duration(m) * time.Minute))
	}
	if len(done) != 1 || done[0].Peak != LevelOpen || !done[0].End.Equal(t0.Add(8*time.Minute)) || done[0].Start != t0 {
		t.Fatalf("logged: %+v", done)
	}
	if done[0].MaxCall != "W2AB1" || math.Abs(done[0].MaxKm-260) > 1 || done[0].Active {
		t.Fatalf("logged: %+v", done[0])
	}
	if s := tr.State(t0.Add(50 * time.Minute)); s.Level != LevelNormal {
		t.Fatalf("after: %s", s.Level)
	}
	// Any direct path over the set distance opens the band at once.
	la, lo = at(320, 180)
	feed(t, tr, t0.Add(2*time.Hour), "K3LONG>APRS:"+PosInfo(la, lo, '-'))
	if s := tr.State(t0.Add(2 * time.Hour)); s.Level != LevelOpen {
		t.Fatalf("320 km: %s", s.Level)
	}
}

func TestLearning(t *testing.T) {
	tr := newT()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	tr.Evaluate(now)
	if _, _, ok := tr.Normal(12); ok {
		t.Fatal("normal with no history")
	}
	// With no history only the distance rule applies.
	la, lo := at(250, 90)
	feed(t, tr, now, "W1XX>APRS:"+PosInfo(la, lo, '-'))
	if s := tr.State(now); s.Level != LevelNormal {
		t.Fatalf("250 km while learning: %s", s.Level)
	}
	la, lo = at(310, 90)
	feed(t, tr, now.Add(time.Minute), "W1YY>APRS:"+PosInfo(la, lo, '-'))
	if s := tr.State(now.Add(time.Minute)); s.Level != LevelOpen {
		t.Fatalf("310 km while learning: %s", s.Level)
	}
}

func TestAlias(t *testing.T) {
	for c, want := range map[string]bool{"WIDE2": true, "WIDE": true, "TRACE3": true, "ON2": true, "RELAY": true,
		"VE3ABC": false, "K1A": false, "W3ADO": false, "N0CALL": false} {
		if got := isAlias(ax25.Address{Call: c}); got != want {
			t.Errorf("isAlias(%s) = %v", c, got)
		}
	}
}

func TestGrids(t *testing.T) {
	tr := newT()
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	la, lo := at(250, 225)
	feed(t, tr, now, "W2GRD>APRS:"+PosInfo(la, lo, '-'))
	feed(t, tr, now.Add(time.Minute), "W2GRD>APRS:>again")
	// Relayed: the grid is the digipeater's, not the far sender's.
	fa, fo := at(900, 260)
	feed(t, tr, now.Add(2*time.Minute), "K9FAR>APRS,W2GRD*:"+PosInfo(fa, fo, '>'))
	g := tr.Grids()
	want := geo.Grid(la, lo)[:4]
	if len(g) != 1 || g[0].Grid != want || g[0].Count != 3 || g[0].BestCall != "W2GRD" || len(g[0].Calls) != 1 {
		t.Fatalf("grids %+v", g)
	}
	// A restart with no grid file rebuilds the log from the paths.
	tr2 := newT()
	tr2.LoadPaths(tr.Paths(now.Add(-time.Hour)), now.Add(3*time.Minute))
	if g2 := tr2.Grids(); len(g2) != 1 || g2[0].Count != 3 {
		t.Fatalf("rebuilt %+v", g2)
	}
}
