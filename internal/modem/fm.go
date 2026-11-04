package modem

import (
	"math"
	"math/cmplx"
	"math/rand/v2"
	"slices"

	"github.com/va3mw/SliceAPRS/internal/dsp"
)

// FMDemod filters the IQ to one 2 m FM channel and demodulates it. It also
// measures the channel power for each sample, for the signal level.
type FMDemod struct {
	fs    int
	ch    *dsp.ComplexFIR
	prev  complex64
	buf   []complex64
	audio []float32
	pow   []float32
	dc    float64 // running mean of the discriminator: the carrier offset
	dcA   float64
}

// NewFMDemod returns a demodulator at fs (the IQ rate) passing +-halfBW Hz
// around 0 Hz.
func NewFMDemod(fs int, halfBW float64) *FMDemod {
	n := int(float64(fs)/1000*2.5) | 1 // about 2.5 ms: 61 taps at 24 kHz
	// The carrier offset shows as DC; follow it with a 20 ms time constant
	// (fast enough to settle in the preamble flags).
	return &FMDemod{fs: fs, ch: dsp.NewComplexFIR(dsp.Lowpass(n, halfBW/float64(fs)), 1),
		dcA: 1 / (0.02 * float64(fs))}
}

// Process returns audio (deviation in units of 5 kHz) and the channel
// power of each sample, both reused on the next call.
func (f *FMDemod) Process(iq []complex64) (audio, power []float32) {
	f.buf = f.ch.Process(iq, f.buf[:0])
	f.audio, f.pow = f.audio[:0], f.pow[:0]
	scale := float64(f.fs) / (2 * math.Pi) / 5000
	for _, x := range f.buf {
		d := x * complex(real(f.prev), -imag(f.prev))
		f.prev = x
		v := math.Atan2(float64(imag(d)), float64(real(d))) * scale
		f.dc += (v - f.dc) * f.dcA
		f.audio = append(f.audio, float32(v-f.dc))
		f.pow = append(f.pow, real(x)*real(x)+imag(x)*imag(x))
	}
	return f.audio, f.pow
}

// Level keeps the channel power in 5 ms blocks for the last minute, to
// give a frame's signal level and the noise floor.
type Level struct {
	per   int
	acc   float64
	cnt   int
	ring  []float32
	total int64 // blocks completed
	n     int64 // samples added
}

// NewLevel returns a level meter for samples at fs.
func NewLevel(fs int) *Level {
	per := fs / 200
	return &Level{per: max(1, per), ring: make([]float32, 200*60)}
}

// Add appends per-sample powers.
func (l *Level) Add(p []float32) {
	for _, v := range p {
		l.acc += float64(v)
		l.cnt++
		if l.cnt == l.per {
			l.ring[l.total%int64(len(l.ring))] = float32(l.acc / float64(l.per))
			l.total++
			l.acc, l.cnt = 0, 0
		}
		l.n++
	}
}

// Mean is the mean power from sample start to end in dB full scale.
func (l *Level) Mean(start, end int64) (float64, bool) {
	a, b := start/int64(l.per), end/int64(l.per)
	if b > l.total {
		b = l.total
	}
	if a < l.total-int64(len(l.ring)) {
		a = l.total - int64(len(l.ring))
	}
	if b <= a {
		return 0, false
	}
	var s float64
	for k := a; k < b; k++ {
		s += float64(l.ring[k%int64(len(l.ring))])
	}
	return db(s / float64(b-a)), true
}

// Floor is the noise floor in dB full scale: the 20th percentile of the
// 5 ms blocks of the last 30 s.
func (l *Level) Floor() (float64, bool) {
	n := min(l.total, int64(200*30))
	if n < 200 {
		return 0, false
	}
	v := make([]float32, 0, n)
	for k := l.total - n; k < l.total; k++ {
		v = append(v, l.ring[k%int64(len(l.ring))])
	}
	slices.Sort(v)
	return db(float64(v[len(v)/5])), true
}

func db(p float64) float64 {
	if p <= 1e-20 {
		return -200
	}
	return 10 * math.Log10(p)
}

// FMModulate frequency-modulates audio (1.0 = devHz) onto a carrier at
// offsetHz, amplitude amp. phase carries on between calls.
func FMModulate(audio []float32, fs int, devHz, offsetHz, amp float64, phase *float64) []complex64 {
	out := make([]complex64, len(audio))
	for i, a := range audio {
		*phase += 2 * math.Pi * (offsetHz + devHz*float64(a)) / float64(fs)
		out[i] = complex64(cmplx.Rect(amp, *phase))
	}
	*phase = math.Mod(*phase, 2*math.Pi)
	return out
}

// AddNoise adds complex white Gaussian noise of total power p (over the
// whole IQ bandwidth).
func AddNoise(iq []complex64, p float64, r *rand.Rand) {
	s := math.Sqrt(p / 2)
	for i := range iq {
		iq[i] += complex(float32(r.NormFloat64()*s), float32(r.NormFloat64()*s))
	}
}
