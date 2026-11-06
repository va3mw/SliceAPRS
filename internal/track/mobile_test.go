package track

import (
	"math"
	"testing"
	"time"
)

func TestMobiles(t *testing.T) {
	tr := newT()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	dlat, dlon := at(40, 90)
	feed(t, tr, now, "VE3DIG-1>APN391,WIDE2-1:"+PosInfo(dlat, dlon, '#'))

	// A car heard through the digipeater, moving.
	c1lat, c1lon := at(120, 80)
	feed(t, tr, now.Add(time.Minute), "VE3CAR-9>APRS,VE3DIG-1*,WIDE2-1:"+PosInfo(c1lat, c1lon, '>'))
	c2lat, c2lon := at(122, 80)
	feed(t, tr, now.Add(3*time.Minute), "VE3CAR-9>APRS,VE3DIG-1*,WIDE2-1:"+PosInfo(c2lat, c2lon, '>')+"080/060")
	// A house through the same digipeater: not a mobile.
	hlat, hlon := at(100, 70)
	feed(t, tr, now.Add(4*time.Minute), "VE3HSE>APRS,VE3DIG-1*,WIDE2-1:"+PosInfo(hlat, hlon, '-'))
	// A house symbol, but moving: counted.
	mlat, mlon := at(20, 180)
	feed(t, tr, now.Add(5*time.Minute), "VE3MOV>APRS:"+PosInfo(mlat, mlon, '-')+"180/030")

	ms := tr.Mobiles(now.Add(6 * time.Minute))
	if len(ms) != 2 {
		t.Fatalf("mobiles %+v", ms)
	}
	mov, car := ms[0], ms[1]
	if mov.Call != "VE3MOV" || mov.Via != "" || mov.ViaPos || !mov.HasCourse || mov.Course != 180 || math.Abs(mov.SpeedKmh-55.6) > 0.1 {
		t.Errorf("mover %+v", mov)
	}
	if car.Call != "VE3CAR-9" || car.Via != "VE3DIG-1" || !car.ViaPos || len(car.Trail) != 2 ||
		math.Abs(car.Km-122) > 0.5 || car.Course != 80 || car.Symbol != "/>" {
		t.Errorf("car %+v", car)
	}
	if math.Abs(car.ViaLat-dlat) > 1e-3 || math.Abs(car.ViaLon-dlon) > 1e-3 {
		t.Errorf("car via position %v %v", car.ViaLat, car.ViaLon)
	}

	// An hour after its last position, a mobile leaves the map.
	if ms := tr.Mobiles(now.Add(66 * time.Minute)); len(ms) != 0 {
		t.Errorf("after an hour: %+v", ms)
	}
}

func TestIsMobileSymbol(t *testing.T) {
	for sym, want := range map[string]bool{"/>": true, "/k": true, "\\>": true, "3>": true, "/-": false, "/#": false, "\\-": false, "": false} {
		if IsMobileSymbol(sym) != want {
			t.Errorf("%q: want %v", sym, want)
		}
	}
}
