// Package dsp has the small signal-processing blocks SliceAPRS needs: an NCO
// to shift the IQ, FIR filters and their design, and decimation. The NCO and
// the windowed-sinc design come from FlexWSPR.
package dsp

import (
	"math"
)

// NCO shifts a complex signal down by a frequency (positive freq moves a
// signal at +freq to 0 Hz).
type NCO struct {
	phase, step float64
}

// SetFreq sets the shift in Hz at sample rate fs.
func (n *NCO) SetFreq(freq float64, fs int) { n.step = -2 * math.Pi * freq / float64(fs) }

// Mix shifts x in place.
func (n *NCO) Mix(x []complex64) {
	if n.step == 0 {
		return
	}
	for i := range x {
		s, c := math.Sincos(n.phase)
		x[i] *= complex(float32(c), float32(s))
		n.phase += n.step
		if n.phase > math.Pi {
			n.phase -= 2 * math.Pi
		} else if n.phase < -math.Pi {
			n.phase += 2 * math.Pi
		}
	}
}

// ComplexFIR filters and optionally decimates a complex stream.
type ComplexFIR struct {
	taps  []float32
	m     int
	hist  []complex64 // last len(taps)-1 inputs, then the new block
	phase int
}

// NewComplexFIR returns a filter with taps that keeps every m-th output.
func NewComplexFIR(taps []float32, m int) *ComplexFIR {
	if m < 1 {
		m = 1
	}
	return &ComplexFIR{taps: taps, m: m, hist: make([]complex64, len(taps)-1)}
}

// Process filters in and appends the outputs to out.
func (f *ComplexFIR) Process(in, out []complex64) []complex64 {
	n := len(f.taps)
	f.hist = append(f.hist, in...)
	i := 0
	for ; i+n <= len(f.hist); i++ {
		if f.phase == 0 {
			var re, im float32
			w := f.hist[i : i+n]
			for k, t := range f.taps {
				re += real(w[k]) * t
				im += imag(w[k]) * t
			}
			out = append(out, complex(re, im))
		}
		f.phase++
		if f.phase == f.m {
			f.phase = 0
		}
	}
	f.hist = append(f.hist[:0], f.hist[i:]...)
	return out
}

// RealFIR filters and optionally decimates a real stream.
type RealFIR struct {
	taps  []float32
	m     int
	hist  []float32
	phase int
}

// NewRealFIR returns a filter with taps that keeps every m-th output.
func NewRealFIR(taps []float32, m int) *RealFIR {
	if m < 1 {
		m = 1
	}
	return &RealFIR{taps: taps, m: m, hist: make([]float32, len(taps)-1)}
}

// Process filters in and appends the outputs to out.
func (f *RealFIR) Process(in, out []float32) []float32 {
	n := len(f.taps)
	f.hist = append(f.hist, in...)
	i := 0
	for ; i+n <= len(f.hist); i++ {
		if f.phase == 0 {
			var acc float32
			w := f.hist[i : i+n]
			for k, t := range f.taps {
				acc += w[k] * t
			}
			out = append(out, acc)
		}
		f.phase++
		if f.phase == f.m {
			f.phase = 0
		}
	}
	f.hist = append(f.hist[:0], f.hist[i:]...)
	return out
}

// Lowpass designs a Blackman-windowed sinc with unity DC gain. fc is the
// cutoff as a fraction of the sample rate; n is made odd.
func Lowpass(n int, fc float64) []float32 {
	n |= 1
	h := make([]float64, n)
	mid := float64(n-1) / 2
	var sum float64
	for i := range h {
		x := float64(i) - mid
		s := 2 * fc
		if x != 0 {
			s = math.Sin(2*math.Pi*fc*x) / (math.Pi * x)
		}
		h[i] = s * blackman(i, n)
		sum += h[i]
	}
	out := make([]float32, n)
	for i := range h {
		out[i] = float32(h[i] / sum)
	}
	return out
}

// Bandpass designs a Blackman-windowed band-pass filter from lo to hi (as
// fractions of the sample rate) with unity gain at the band centre.
func Bandpass(n int, lo, hi float64) []float32 {
	n |= 1
	h := make([]float64, n)
	mid := float64(n-1) / 2
	for i := range h {
		x := float64(i) - mid
		var s float64
		if x == 0 {
			s = 2 * (hi - lo)
		} else {
			s = (math.Sin(2*math.Pi*hi*x) - math.Sin(2*math.Pi*lo*x)) / (math.Pi * x)
		}
		h[i] = s * blackman(i, n)
	}
	// Normalise to unity gain at the centre frequency.
	fc := (lo + hi) / 2
	var re, im float64
	for i, v := range h {
		re += v * math.Cos(2*math.Pi*fc*float64(i))
		im += v * math.Sin(2*math.Pi*fc*float64(i))
	}
	g := math.Hypot(re, im)
	out := make([]float32, n)
	for i := range h {
		out[i] = float32(h[i] / g)
	}
	return out
}

func blackman(i, n int) float64 {
	if n == 1 {
		return 1
	}
	a := 2 * math.Pi * float64(i) / float64(n-1)
	return 0.42 - 0.5*math.Cos(a) + 0.08*math.Cos(2*a)
}
