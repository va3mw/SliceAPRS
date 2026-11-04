package modem

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/va3mw/SliceAPRS/internal/ax25"
)

func frame(t testing.TB, i int) []byte {
	t.Helper()
	f, err := ax25.ParseTNC2(fmt.Sprintf("VE3T%02d-%d>APRS,WIDE1-1,WIDE2-1:!4%d30.%02dN/07%d15.00W>SliceAPRS test packet %d %s",
		i%100, i%16, i%10, i%60, i%10, i, "with some more text to make it a usual length"))
	if err != nil {
		t.Fatal(err)
	}
	return f.Encode()
}

func TestFCS(t *testing.T) {
	// "123456789" is 0x906E for CRC-16/X-25.
	if got := FCS([]byte("123456789")); got != 0x906E {
		t.Fatalf("FCS = %04X", got)
	}
}

func TestHDLCRoundTrip(t *testing.T) {
	data := []byte{0x7E, 0xFF, 0xFF, 0x00, 0x1F, 0xF8, 0x7E, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	var h hdlc
	var got [][]byte
	for i, b := range HDLCBits(data, 3, 2) {
		if f := h.bit(b, int64(i)); f != nil {
			got = append(got, f)
		}
	}
	if len(got) != 1 || !bytes.Equal(got[0], data) {
		t.Fatalf("got %x", got)
	}
}

func decodeAudio(fs int, a []float32, opt Options) []Received {
	r := NewAudioReceiver(fs, opt)
	var out []Received
	for i := 0; i < len(a); i += 512 {
		r.Audio(a[i:min(i+512, len(a))], func(x Received) { out = append(out, x) })
	}
	return out
}

func decodeIQ(fs int, iq []complex64, opt Options) []Received {
	r := NewReceiver(fs, opt)
	var out []Received
	for i := 0; i < len(iq); i += 256 {
		r.IQ(iq[i:min(i+256, len(iq))], func(x Received) { out = append(out, x) })
	}
	return out
}

func TestCleanAudio(t *testing.T) {
	for _, fs := range []int{12000, 22050, 24000, 44100, 48000} {
		g := &Generator{FS: fs}
		var bs []Burst
		for i := 0; i < 20; i++ {
			bs = append(bs, Burst{Data: frame(t, i), GapSec: 0.3, SpaceGain: []float64{1, 0.5, 2}[i%3]})
		}
		got := decodeAudio(fs, g.Audio(bs, 0), Options{})
		if len(got) != len(bs) {
			t.Errorf("%d Hz: %d of %d frames", fs, len(got), len(bs))
			continue
		}
		for i, r := range got {
			if !bytes.Equal(r.Data, bs[i].Data) {
				t.Errorf("%d Hz: frame %d differs", fs, i)
			}
		}
	}
}

func TestIQLevelsAndOffset(t *testing.T) {
	const fs = 24000
	g := &Generator{FS: fs, R: rand.New(rand.NewPCG(3, 4))}
	bs := []Burst{
		{Data: frame(t, 1), SNR: 30, GapSec: 1},
		{Data: frame(t, 2), SNR: 20, OffsetHz: 2000, GapSec: 0.5},
		{Data: frame(t, 3), SNR: 25, OffsetHz: -2500, SpaceGain: 1.8, GapSec: 0.5},
	}
	got := decodeIQ(fs, g.IQ(bs), Options{})
	if len(got) != 3 {
		t.Fatalf("decoded %d of 3", len(got))
	}
	for i, r := range got {
		if !r.HasLevel {
			t.Fatalf("frame %d: no level", i)
		}
		if d := r.SNR() - bs[i].SNR; d < -3 || d > 3 {
			t.Errorf("frame %d: measured SNR %.1f dB, sent %.0f dB", i, r.SNR(), bs[i].SNR)
		}
	}
}

// TestSNRSweep reports how many frames decode at each channel SNR. Run
// with -v to see the table.
func TestSNRSweep(t *testing.T) {
	const fs = 24000
	const per = 40
	for _, snr := range []float64{6, 8, 10, 12, 15} {
		for _, sg := range []float64{1, 1.8, 0.55} {
			g := &Generator{FS: fs, R: rand.New(rand.NewPCG(uint64(snr*10), uint64(sg*10)))}
			var bs []Burst
			for i := 0; i < per; i++ {
				bs = append(bs, Burst{Data: frame(t, i), SNR: snr, SpaceGain: sg, GapSec: 0.2, OffsetHz: float64(i%5-2) * 500})
			}
			got := decodeIQ(fs, g.IQ(bs), Options{})
			t.Logf("SNR %4.1f dB  twist %4.2f: %2d/%d", snr, sg, len(got), per)
			if snr >= 12 && len(got) < per*9/10 {
				t.Errorf("SNR %.0f twist %.2f: only %d of %d", snr, sg, len(got), per)
			}
		}
	}
}
