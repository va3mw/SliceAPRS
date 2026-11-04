//go:build tune

package modem

import (
	"math/rand/v2"
	"testing"
)

func TestTune(t *testing.T) {
	const fs = 24000
	var sets [][]Burst
	var iqs [][]complex64
	for _, snr := range []float64{8, 9, 10} {
		for _, sg := range []float64{1, 1.8, 0.55} {
			g := &Generator{FS: fs, R: rand.New(rand.NewPCG(uint64(snr*10), uint64(sg*10)))}
			var bs []Burst
			for i := 0; i < 30; i++ {
				bs = append(bs, Burst{Data: frame(t, i), SNR: snr, SpaceGain: sg, GapSec: 0.2, OffsetHz: float64(i%5-2) * 500})
			}
			sets = append(sets, bs)
			iqs = append(iqs, g.IQ(bs))
		}
	}
	for _, pre := range []float64{0, 0.8, 1.2, 2} {
		for _, lpb := range []float64{0.8, 1.0, 1.2, 1.4, 1.7} {
			for _, cut := range []float64{1, 3} {
				o := Options{PreBits: pre, LPBits: lpb, Window: int(cut), NoPre: pre == 0}
				tot := 0
				for _, iq := range iqs {
					tot += len(decodeIQ(fs, iq, o))
				}
				t.Logf("pre %.1f lp %.1f cut %.1f: %d", pre, lpb, cut, tot)
			}
		}
	}
}
