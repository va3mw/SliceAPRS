package modem

import (
	"math"
	"math/rand/v2"
)

// Burst is one transmission for the signal generator.
type Burst struct {
	Data      []byte  // frame without FCS
	SNR       float64 // carrier to noise in the 16 kHz channel, dB
	OffsetHz  float64 // carrier offset from the channel centre
	SpaceGain float64 // tone twist; 1 is flat, 1.8 is pre-emphasised
	GapSec    float64 // silence (noise only) before the burst
}

// Generator builds test IQ: bursts of AFSK on FM in Gaussian noise. The
// noise power is fixed; each burst's carrier is set for its SNR.
type Generator struct {
	FS    int
	DevHz float64 // peak deviation, default 3000
	R     *rand.Rand
	phase float64
	afPh  float64
}

// noiseP is the generator's total noise power across the IQ bandwidth.
const noiseP = 1e-4

// IQ returns the IQ for bursts, including their gaps.
func (g *Generator) IQ(bursts []Burst) []complex64 {
	if g.DevHz == 0 {
		g.DevHz = 3000
	}
	if g.R == nil {
		g.R = rand.New(rand.NewPCG(1, 2))
	}
	// Noise in the 16 kHz channel is noiseP*16000/FS.
	chanNoise := noiseP * 16000 / float64(g.FS)
	var out []complex64
	for _, b := range bursts {
		gap := make([]complex64, int(b.GapSec*float64(g.FS)))
		AddNoise(gap, noiseP, g.R)
		out = append(out, gap...)
		sg := b.SpaceGain
		if sg == 0 {
			sg = 1
		}
		lv := 0
		audio := Modulate(NRZI(HDLCBits(b.Data, 25, 3), &lv), g.FS, sg, &g.afPh)
		// Peak deviation is reached by the louder tone.
		scale := float32(1 / math.Max(1, sg))
		for i := range audio {
			audio[i] *= scale
		}
		amp := math.Sqrt(chanNoise * math.Pow(10, b.SNR/10))
		iq := FMModulate(audio, g.FS, g.DevHz, b.OffsetHz, amp, &g.phase)
		AddNoise(iq, noiseP, g.R)
		out = append(out, iq...)
	}
	tail := make([]complex64, g.FS/10)
	AddNoise(tail, noiseP, g.R)
	return append(out, tail...)
}

// Audio returns flat audio for bursts (no FM, no noise unless noise > 0,
// which is the noise RMS relative to a full-scale tone).
func (g *Generator) Audio(bursts []Burst, noise float64) []float32 {
	if g.R == nil {
		g.R = rand.New(rand.NewPCG(1, 2))
	}
	var out []float32
	for _, b := range bursts {
		out = append(out, make([]float32, int(b.GapSec*float64(g.FS)))...)
		sg := b.SpaceGain
		if sg == 0 {
			sg = 1
		}
		lv := 0
		out = append(out, Modulate(NRZI(HDLCBits(b.Data, 25, 3), &lv), g.FS, sg, &g.afPh)...)
	}
	out = append(out, make([]float32, g.FS/10)...)
	if noise > 0 {
		for i := range out {
			out[i] += float32(g.R.NormFloat64() * noise)
		}
	}
	return out
}
