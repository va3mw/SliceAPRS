// Package geo has Maidenhead grids, great-circle distance and bearing. The
// grid code comes from FlexWSPR.
package geo

import (
	"math"
	"strings"
)

const earthKm = 6371.0

// GridLatLon returns the centre of a 4, 6 or 8 character grid square.
func GridLatLon(g string) (lat, lon float64, ok bool) {
	g = strings.ToUpper(strings.TrimSpace(g))
	if len(g) != 4 && len(g) != 6 && len(g) != 8 {
		return 0, 0, false
	}
	if g[0] < 'A' || g[0] > 'R' || g[1] < 'A' || g[1] > 'R' || g[2] < '0' || g[2] > '9' || g[3] < '0' || g[3] > '9' {
		return 0, 0, false
	}
	lon = float64(g[0]-'A')*20 - 180 + float64(g[2]-'0')*2
	lat = float64(g[1]-'A')*10 - 90 + float64(g[3]-'0')
	lonW, latW := 2.0, 1.0
	if len(g) >= 6 {
		if g[4] < 'A' || g[4] > 'X' || g[5] < 'A' || g[5] > 'X' {
			return 0, 0, false
		}
		lonW, latW = 2.0/24, 1.0/24
		lon += float64(g[4]-'A') * lonW
		lat += float64(g[5]-'A') * latW
	}
	if len(g) == 8 {
		if g[6] < '0' || g[6] > '9' || g[7] < '0' || g[7] > '9' {
			return 0, 0, false
		}
		lonW, latW = lonW/10, latW/10
		lon += float64(g[6]-'0') * lonW
		lat += float64(g[7]-'0') * latW
	}
	return lat + latW/2, lon + lonW/2, true
}

// Grid returns the 6-character grid of a position.
func Grid(lat, lon float64) string {
	lon += 180
	lat += 90
	b := []byte{
		'A' + byte(lon/20), 'A' + byte(lat/10),
		'0' + byte(math.Mod(lon, 20)/2), '0' + byte(math.Mod(lat, 10)),
		'a' + byte(math.Mod(lon, 2)*12), 'a' + byte(math.Mod(lat, 1)*24),
	}
	return string(b)
}

func rad(d float64) float64 { return d * math.Pi / 180 }

// Km is the great-circle distance between two points.
func Km(lat1, lon1, lat2, lon2 float64) float64 {
	p1, p2 := rad(lat1), rad(lat2)
	dp, dl := p2-p1, rad(lon2-lon1)
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * earthKm * math.Asin(math.Min(1, math.Sqrt(a)))
}

// Bearing is the initial great-circle bearing from point 1 to point 2, in
// degrees from true north (0-360).
func Bearing(lat1, lon1, lat2, lon2 float64) float64 {
	p1, p2, dl := rad(lat1), rad(lat2), rad(lon2-lon1)
	y := math.Sin(dl) * math.Cos(p2)
	x := math.Cos(p1)*math.Sin(p2) - math.Sin(p1)*math.Cos(p2)*math.Cos(dl)
	b := math.Atan2(y, x) * 180 / math.Pi
	return math.Mod(b+360, 360)
}

// Destination is the point km away on bearing deg from a start point.
func Destination(lat, lon, deg, km float64) (float64, float64) {
	d := km / earthKm
	p1, l1, th := rad(lat), rad(lon), rad(deg)
	p2 := math.Asin(math.Sin(p1)*math.Cos(d) + math.Cos(p1)*math.Sin(d)*math.Cos(th))
	l2 := l1 + math.Atan2(math.Sin(th)*math.Sin(d)*math.Cos(p1), math.Cos(d)-math.Sin(p1)*math.Sin(p2))
	return p2 * 180 / math.Pi, math.Mod(l2*180/math.Pi+540, 360) - 180
}

// Compass names a bearing: N, NNE, NE ...
func Compass(deg float64) string {
	names := [...]string{"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE", "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"}
	return names[int(math.Mod(deg+11.25+360, 360)/22.5)%16]
}
