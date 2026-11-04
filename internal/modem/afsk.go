package modem

import (
	"math"

	"github.com/va3mw/SliceAPRS/internal/dsp"
)

// Frame is one frame with a good FCS.
type Frame struct {
	Data       []byte // addresses through info, FCS removed
	Start, End int64  // sample index of the opening and closing flags
	Slicer     int    // the slicer that decoded it first
}

// Options tune the AFSK demodulator. Zero values take the defaults.
type Options struct {
	Mark, Space, Baud float64 // 1200, 2200, 1200
	// Slicers is the number of decision slicers, each with a different
	// balance between the two tones, run on the same correlator outputs
	// (like Direwolf's multiple slicers). Default 9.
	Slicers int
	// PreBits and LPBits are the lengths of the band-pass pre-filter and
	// of the correlator low-pass filters, in bit times. LPCut is the
	// low-pass cutoff as a fraction of the baud rate.
	PreBits, LPBits, LPCut float64
	NoPre                  bool // skip the band-pass pre-filter
	// Window shapes the correlator filters: 1 rectangular (the default,
	// best on the synthetic tests), 2 windowed sinc with cutoff LPCut, 3 a
	// half-cosine.
	Window int
}

func (o *Options) defaults() {
	if o.Mark == 0 {
		o.Mark = 1200
	}
	if o.Space == 0 {
		o.Space = 2200
	}
	if o.Baud == 0 {
		o.Baud = 1200
	}
	if o.Slicers == 0 {
		o.Slicers = 9
	}
	if o.PreBits == 0 {
		o.PreBits = 1.2
	}
	if o.LPBits == 0 {
		o.LPBits = 1.4
	}
	if o.Window == 0 {
		o.Window = 1
	}
	if o.LPCut == 0 {
		o.LPCut = 0.9
	}
}

// Demod demodulates 1200 baud AFSK audio into frames. It works at any
// audio rate from about 8 kHz up.
type Demod struct {
	opt   Options
	fs    float64
	pre   *dsp.RealFIR
	lp    [4]*dsp.RealFIR // mark I, mark Q, space I, space Q
	mPh   float64
	sPh   float64
	mStep float64
	sStep float64
	delay int64 // filter group delay in samples

	mPeak, mValley, sPeak, sValley float32
	attack, decay                  float32

	slicers []*slicer
	n       int64 // input samples so far
	recent  []recentFrame

	buf  []float32
	mix  [4][]float32
	outs [4][]float32

	// Count is the frames decoded by each slicer (first or not).
	Count []int
}

type slicer struct {
	gain    float32 // space tone weight
	agc     bool    // use the tone-normalised amplitudes
	pll     int32
	prevOut bool
	level   int
	h       hdlc
}

type recentFrame struct {
	fcs uint16
	end int64
}

// NewDemod returns a demodulator for audio at fs samples per second.
func NewDemod(fs int, opt Options) *Demod {
	opt.defaults()
	d := &Demod{opt: opt, fs: float64(fs)}
	spb := d.fs / opt.Baud
	if !opt.NoPre {
		n := int(opt.PreBits*spb) | 1
		lo := (opt.Mark - 0.5*opt.Baud) / d.fs
		hi := (opt.Space + 0.5*opt.Baud) / d.fs
		d.pre = dsp.NewRealFIR(dsp.Bandpass(n, lo, hi), 1)
		d.delay += int64(n / 2)
	}
	ln := int(opt.LPBits*spb) | 1
	taps := corrTaps(ln, opt.Window, opt.LPCut*opt.Baud/d.fs)
	for i := range d.lp {
		d.lp[i] = dsp.NewRealFIR(taps, 1)
	}
	d.delay += int64(ln / 2)
	d.mStep = 2 * math.Pi * opt.Mark / d.fs
	d.sStep = 2 * math.Pi * opt.Space / d.fs
	// Peak/valley trackers: fast attack, slow decay (about 0.25 s), per
	// sample at this rate.
	d.attack = float32(1 - math.Pow(0.3, 44100/d.fs))
	d.decay = float32(1 - math.Pow(1-0.00009, 44100/d.fs))

	// Slicer 0 uses the tone-normalised amplitudes; the others weight the
	// raw space amplitude from -8 to +8 dB, for transmitters and receivers
	// with pre- or de-emphasis.
	d.slicers = append(d.slicers, &slicer{gain: 1, agc: true})
	for k := 1; k < opt.Slicers; k++ {
		db := -8 + 16*float64(k-1)/float64(max(1, opt.Slicers-2))
		d.slicers = append(d.slicers, &slicer{gain: float32(math.Pow(10, db/20))})
	}
	d.Count = make([]int, len(d.slicers))
	return d
}

// Rate is the audio sample rate.
func (d *Demod) Rate() int { return int(d.fs) }

// Process demodulates a block of audio, calling emit for each new frame.
// Frames found by more than one slicer are reported once.
func (d *Demod) Process(audio []float32, emit func(Frame)) {
	x := audio
	if d.pre != nil {
		d.buf = d.pre.Process(audio, d.buf[:0])
		x = d.buf
	}
	for i := range d.mix {
		d.mix[i] = d.mix[i][:0]
	}
	for _, v := range x {
		ms, mc := math.Sincos(d.mPh)
		ss, sc := math.Sincos(d.sPh)
		d.mix[0] = append(d.mix[0], v*float32(mc))
		d.mix[1] = append(d.mix[1], v*float32(ms))
		d.mix[2] = append(d.mix[2], v*float32(sc))
		d.mix[3] = append(d.mix[3], v*float32(ss))
		d.mPh += d.mStep
		if d.mPh > 2*math.Pi {
			d.mPh -= 2 * math.Pi
		}
		d.sPh += d.sStep
		if d.sPh > 2*math.Pi {
			d.sPh -= 2 * math.Pi
		}
	}
	for i := range d.lp {
		d.outs[i] = d.lp[i].Process(d.mix[i], d.outs[i][:0])
	}
	step := int32(math.Round(4294967296 * d.opt.Baud / d.fs))
	for i := range d.outs[0] {
		mi, mq, si, sq := d.outs[0][i], d.outs[1][i], d.outs[2][i], d.outs[3][i]
		m := float32(math.Sqrt(float64(mi*mi + mq*mq)))
		s := float32(math.Sqrt(float64(si*si + sq*sq)))
		d.mPeak, d.mValley = track(m, d.mPeak, d.mValley, d.attack, d.decay)
		d.sPeak, d.sValley = track(s, d.sPeak, d.sValley, d.attack, d.decay)
		var mn, sn float32
		if r := d.mPeak - d.mValley; r > 0 {
			mn = (m - d.mValley) / r
		}
		if r := d.sPeak - d.sValley; r > 0 {
			sn = (s - d.sValley) / r
		}
		at := d.n - d.delay
		for k, sl := range d.slicers {
			var out float32
			if sl.agc {
				out = mn - sn
			} else {
				out = m - s*sl.gain
			}
			if f := sl.sample(out > 0, step, at); f != nil {
				d.Count[k]++
				d.report(Frame{Data: f, Start: sl.h.got, End: at, Slicer: k}, emit)
			}
		}
		d.n++
	}
}

// corrTaps is the integrating filter behind each correlator.
func corrTaps(n, window int, fc float64) []float32 {
	if window == 2 {
		return dsp.Lowpass(n, fc)
	}
	t := make([]float32, n)
	var sum float32
	for i := range t {
		t[i] = 1
		if window == 3 {
			t[i] = float32(math.Sin(math.Pi * (float64(i) + 0.5) / float64(n)))
		}
		sum += t[i]
	}
	for i := range t {
		t[i] /= sum
	}
	return t
}

// track follows the peak and valley of an amplitude: quick to follow it
// outwards, slow to fall back.
func track(v, peak, valley, attack, decay float32) (float32, float32) {
	if v >= peak {
		peak += (v - peak) * attack
	} else {
		peak += (v - peak) * decay
	}
	if v <= valley {
		valley += (v - valley) * attack
	} else {
		valley += (v - valley) * decay
	}
	return peak, valley
}

// sample advances the slicer's PLL by one sample and returns a frame when
// the HDLC decoder completes one.
func (sl *slicer) sample(mark bool, step int32, at int64) []byte {
	prev := sl.pll
	sl.pll += step // wraps: the bit is sampled when it rolls over
	var frame []byte
	if prev > 0 && sl.pll < 0 {
		lv := 0
		if mark {
			lv = 1
		}
		b := 0
		if lv == sl.level {
			b = 1 // NRZI: no change is a 1
		}
		sl.level = lv
		frame = sl.h.bit(b, at)
	}
	// Pull the clock towards each transition: a transition belongs half
	// way between two sampling points, where the counter crosses zero.
	if mark != sl.prevOut {
		if sl.h.inFrame() {
			sl.pll = int32(float64(sl.pll) * 0.74)
		} else {
			sl.pll = int32(float64(sl.pll) * 0.50)
		}
	}
	sl.prevOut = mark
	return frame
}

func (d *Demod) report(f Frame, emit func(Frame)) {
	fcs := FCS(f.Data)
	keep := d.recent[:0]
	dup := false
	for _, r := range d.recent {
		if f.End-r.end > int64(d.fs) {
			continue
		}
		keep = append(keep, r)
		if r.fcs == fcs && f.End-r.end < int64(d.fs/10) {
			dup = true
		}
	}
	d.recent = keep
	if dup {
		return
	}
	d.recent = append(d.recent, recentFrame{fcs, f.End})
	emit(f)
}

// Modulate makes AFSK audio for line levels (after NRZI) at fs, with the
// space tone scaled by spaceGain (1 is flat). phase carries on between
// calls.
func Modulate(levels []int, fs int, spaceGain float64, phase *float64) []float32 {
	spb := float64(fs) / 1200
	out := make([]float32, 0, int(float64(len(levels))*spb)+1)
	var t float64
	for i, lv := range levels {
		f, a := 1200.0, 1.0
		if lv == 0 {
			f, a = 2200, spaceGain
		}
		end := float64(i+1) * spb
		for ; t < end; t++ {
			*phase += 2 * math.Pi * f / float64(fs)
			out = append(out, float32(a*math.Sin(*phase)))
		}
	}
	*phase = math.Mod(*phase, 2*math.Pi)
	return out
}
