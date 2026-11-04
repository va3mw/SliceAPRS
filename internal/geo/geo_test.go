package geo

import (
	"math"
	"testing"
)

func TestGrid(t *testing.T) {
	lat, lon, ok := GridLatLon("FN25")
	if !ok || lat != 45.5 || lon != -75 {
		t.Fatalf("FN25: %v %v %v", lat, lon, ok)
	}
	lat, lon, ok = GridLatLon("FN25dk")
	if !ok || math.Abs(lat-(45+10.5/24)) > 1e-9 || math.Abs(lon-(-76+3.5/12)) > 1e-9 {
		t.Fatalf("FN25dk: %v %v", lat, lon)
	}
	if g := Grid(lat, lon); g != "FN25dk" {
		t.Fatalf("Grid = %s", g)
	}
	if _, _, ok := GridLatLon("FN25d"); ok {
		t.Fatal("5 characters accepted")
	}
	if _, _, ok := GridLatLon("FN25dk44"); !ok {
		t.Fatal("8 characters refused")
	}
}

func TestKmBearing(t *testing.T) {
	// Ottawa to Toronto: about 352 km, bearing about 233 degrees.
	km := Km(45.4215, -75.6972, 43.6532, -79.3832)
	if km < 345 || km > 360 {
		t.Fatalf("km %.1f", km)
	}
	if b := Bearing(45.4215, -75.6972, 43.6532, -79.3832); b < 228 || b > 238 {
		t.Fatalf("bearing %.1f", b)
	}
	la, lo := Destination(45, -75, 90, 300)
	if d := Km(45, -75, la, lo); math.Abs(d-300) > 0.01 {
		t.Fatalf("destination %.3f km", d)
	}
	if b := Bearing(45, -75, la, lo); math.Abs(b-90) > 2.5 {
		t.Fatalf("destination bearing %.2f", b)
	}
	if Compass(0) != "N" || Compass(359) != "N" || Compass(225) != "SW" || Compass(100) != "E" {
		t.Fatal("compass")
	}
}
