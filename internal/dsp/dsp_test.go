package dsp

import (
	"math"
	"math/cmplx"
	"testing"
)

func tone(f float64, fs, n int) []complex64 {
	x := make([]complex64, n)
	for i := range x {
		x[i] = complex64(cmplx.Exp(complex(0, 2*math.Pi*f*float64(i)/float64(fs))))
	}
	return x
}

func power(y []complex64) float64 {
	y = y[len(y)/4:]
	var p float64
	for _, v := range y {
		p += float64(real(v)*real(v) + imag(v)*imag(v))
	}
	return p / float64(len(y))
}

func TestLowpassComplex(t *testing.T) {
	const fs = 24000
	taps := Lowpass(63, 8000.0/fs)
	for _, c := range []struct {
		f      float64
		lo, hi float64
	}{{0, 0.98, 1.02}, {5000, 0.9, 1.05}, {11000, 0, 0.001}} {
		fir := NewComplexFIR(taps, 1)
		y := fir.Process(tone(c.f, fs, 4000), nil)
		if p := power(y); p < c.lo || p > c.hi {
			t.Errorf("%v Hz: power %.4f, want %.3f..%.3f", c.f, p, c.lo, c.hi)
		}
	}
}

func TestDecimateBlocks(t *testing.T) {
	// Feeding in odd-sized blocks gives the same output as one block.
	taps := Lowpass(31, 0.2)
	x := tone(1000, 24000, 1000)
	a := NewComplexFIR(taps, 2).Process(x, nil)
	b := NewComplexFIR(taps, 2)
	var y []complex64
	for i := 0; i < len(x); i += 37 {
		y = b.Process(x[i:min(i+37, len(x))], y)
	}
	if len(a) != len(y) {
		t.Fatalf("len %d vs %d", len(a), len(y))
	}
	for i := range a {
		if cmplx.Abs(complex128(a[i]-y[i])) > 1e-5 {
			t.Fatalf("sample %d differs", i)
		}
	}
}

func TestBandpass(t *testing.T) {
	const fs = 24000
	taps := Bandpass(101, 900.0/fs, 2500.0/fs)
	gain := func(f float64) float64 {
		fir := NewRealFIR(taps, 1)
		x := make([]float32, 6000)
		for i := range x {
			x[i] = float32(math.Sin(2 * math.Pi * f * float64(i) / fs))
		}
		y := fir.Process(x, nil)[2000:]
		var p float64
		for _, v := range y {
			p += float64(v * v)
		}
		return math.Sqrt(2 * p / float64(len(y)))
	}
	if g := gain(1700); g < 0.95 || g > 1.05 {
		t.Errorf("centre gain %.3f", g)
	}
	if g := gain(1200); g < 0.8 {
		t.Errorf("1200 Hz gain %.3f", g)
	}
	if g := gain(6000); g > 0.01 {
		t.Errorf("6 kHz gain %.3f", g)
	}
}

func TestNCO(t *testing.T) {
	x := tone(3000, 24000, 2400)
	var n NCO
	n.SetFreq(3000, 24000)
	n.Mix(x)
	for i := 10; i < len(x); i += 100 {
		if cmplx.Abs(complex128(x[i])-1) > 1e-3 {
			t.Fatalf("sample %d: %v", i, x[i])
		}
	}
}
