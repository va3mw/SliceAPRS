//go:build tune

package modem

import (
	"fmt"
	"math"
	"math/cmplx"
	"os"
	"strconv"
	"testing"

	"github.com/va3mw/SliceAPRS/internal/dsp"
	"github.com/va3mw/SliceAPRS/internal/wav"
)

// TestProbe FM-demodulates PROBE_FILE shifted by PROBE_SHIFT and reports,
// per 0.5 s, the audio power at 1200 and 2200 Hz against 600/3000 Hz.
func TestProbe(t *testing.T) {
	r, err := wav.Open(os.Getenv("PROBE_FILE"))
	if err != nil {
		t.Skip(err)
	}
	sh, _ := strconv.ParseFloat(os.Getenv("PROBE_SHIFT"), 64)
	var nco dsp.NCO
	nco.SetFreq(sh, r.Rate)
	fm := NewFMDemod(r.Rate, 7500)
	fr := [][]float32{make([]float32, r.Rate/10), make([]float32, r.Rate/10)}
	out, _ := wav.Create(os.Getenv("PROBE_FILE")+".audio.wav", r.Rate, 1)
	defer out.Close()
	sec := 0.0
	for {
		n, err := r.Read(fr)
		if n == 0 || err != nil {
			break
		}
		iq := make([]complex64, n)
		for i := range iq {
			iq[i] = complex(fr[0][i], fr[1][i])
		}
		nco.Mix(iq)
		a, _ := fm.Process(iq)
		out.WriteMono(a)
		g := func(f float64) float64 {
			var acc complex128
			for i, v := range a {
				acc += complex(float64(v), 0) * cmplx.Exp(complex(0, -2*math.Pi*f*float64(i)/float64(r.Rate)))
			}
			return 20 * math.Log10(cmplx.Abs(acc)/float64(len(a))+1e-12)
		}
		var rms float64
		for _, v := range a {
			rms += float64(v * v)
		}
		fmt.Printf("%5.1fs rms %.2f  600:%5.1f 1200:%5.1f 1700:%5.1f 2200:%5.1f 3000:%5.1f 4500:%5.1f\n", sec, math.Sqrt(rms/float64(len(a))), g(600), g(1200), g(1700), g(2200), g(3000), g(4500))
		sec += float64(n) / float64(r.Rate)
	}
}
