// aprsgen writes a test recording: APRS packets on FM in noise, as 24 kHz
// IQ (I left, Q right) or as audio. The packets come from made-up stations
// placed around a grid: locals heard directly, a digipeater relaying a far
// station, the usual traps (internet, ISS, own call, a position jump), and,
// with -scenario opening, far stations heard directly. Feed the file to
// sliceaprs -decode or -replay to test without the radio or an opening.
package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"

	"github.com/va3mw/SliceAPRS/internal/ax25"
	"github.com/va3mw/SliceAPRS/internal/geo"
	"github.com/va3mw/SliceAPRS/internal/modem"
	"github.com/va3mw/SliceAPRS/internal/wav"
)

func main() {
	out := flag.String("o", "aprs-test.wav", "output WAV file")
	grid := flag.String("grid", "FN25dk", "home grid the stations are placed around")
	scenario := flag.String("scenario", "opening", "normal or opening")
	rounds := flag.Int("rounds", 3, "times through the station list")
	snr := flag.Float64("snr", 25, "channel SNR of local stations, dB (far ones are weaker)")
	audio := flag.Bool("audio", false, "write audio (48 kHz, 1 channel) instead of IQ")
	mycall := flag.String("mycall", "VA3MW", "your callsign, for the own-transmission trap")
	flag.Parse()

	lat, lon, ok := geo.GridLatLon(*grid)
	if !ok {
		fmt.Fprintln(os.Stderr, "bad grid")
		os.Exit(2)
	}
	at := func(km, brg float64) (float64, float64) { return geo.Destination(lat, lon, brg, km) }
	type tx struct {
		tnc2 string
		snr  float64
	}
	var list []tx
	add := func(s string, snr float64) { list = append(list, tx{s, snr}) }
	for k := 0; k < 6; k++ {
		la, lo := at(15+float64(k)*22, float64(k)*60+10)
		add(fmt.Sprintf("VE3L%02d>APRS,WIDE1-1,WIDE2-1:%s local station %d", k, pos(la, lo, '-'), k), *snr-float64(k))
	}
	dla, dlo := at(65, 200)
	add("VE3DIG-1>APN391,WIDE2-1:"+pos(dla, dlo, '#')+"W2 digipeater", *snr)
	fla, flo := at(480, 205)
	add("W2RLY>APRS,VE3DIG-1*,WIDE2-1:"+pos(fla, flo, '>')+" far, but relayed", *snr)
	add("VE3OBJ>APRS:;HAMFEST  *111111z"+pos(at(30, 90))[1:]+"object", *snr)
	add("VE3TXT>APRS,WIDE2-1:>status from a station with no position", *snr)
	add("VE3MSG>APRS,WIDE2-1::VA3MW    :hello from the test{12", *snr)
	add("VE3WX>APRS:"+pos(at(25, 300))[:19]+"_220/004g005t050h80b10132", *snr)
	add("VE3GW>APRS,WIDE2-1:}K1NET>APRS,TCPIP,VE3GW*:"+pos(at(700, 180))+"from the internet", *snr)
	add("RS0ISS>CQ,ARISS*:"+pos(at(900, 90))+"ISS", *snr)
	add(*mycall+"-9>APRS,WIDE1-1:"+pos(at(1, 0))+"my own", *snr)
	if *scenario == "opening" {
		for i, d := range []float64{230, 290, 340, 410} {
			la, lo := at(d, 215+float64(i)*8)
			add(fmt.Sprintf("W2TR%d>APRS,WIDE2-1:%s heard on tropo", i, pos(la, lo, '>')), *snr-12-float64(i)*1.5)
		}
	}

	g := &modem.Generator{FS: 24000, R: rand.New(rand.NewPCG(7, 11))}
	if *audio {
		g.FS = 48000
	}
	var bursts []modem.Burst
	jla, jlo := at(40, 120)
	for r := 0; r < *rounds; r++ {
		for i, t := range list {
			f, err := ax25.ParseTNC2(t.tnc2)
			if err != nil {
				fmt.Fprintln(os.Stderr, t.tnc2, err)
				os.Exit(1)
			}
			bursts = append(bursts, modem.Burst{Data: f.Encode(), SNR: t.snr, GapSec: 0.6 + 0.4*float64(i%3),
				OffsetHz: float64((i%5)-2) * 300, SpaceGain: []float64{1, 1.8, 0.7}[i%3]})
		}
		// A fixed station that jumps 900 km once in the middle round.
		p := pos(jla, jlo, '-')
		if r == 1 {
			p = pos(at(900, 300))
		}
		f, _ := ax25.ParseTNC2("VE3JMP>APRS:" + p + "jumpy")
		bursts = append(bursts, modem.Burst{Data: f.Encode(), SNR: *snr, GapSec: 0.5})
	}
	var w *wav.Writer
	var err error
	if *audio {
		w, err = wav.Create(*out, g.FS, 1)
		if err == nil {
			err = w.WriteMono(g.Audio(bursts, 0.05))
		}
	} else {
		w, err = wav.Create(*out, g.FS, 2)
		if err == nil {
			err = w.WriteIQ(g.IQ(bursts))
		}
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s: %d packets\n", *out, len(bursts))
}

// pos formats a plain position report with a symbol (table '/').
func pos(lat, lon float64, sym ...byte) string {
	s := byte('-')
	if len(sym) > 0 {
		s = sym[0]
	}
	ns, ew := 'N', 'W'
	if lat < 0 {
		ns, lat = 'S', -lat
	}
	if lon >= 0 {
		ew = 'E'
	} else {
		lon = -lon
	}
	ld, lm := math.Floor(lat), (lat-math.Floor(lat))*60
	od, om := math.Floor(lon), (lon-math.Floor(lon))*60
	return fmt.Sprintf("!%02.0f%05.2f%c/%03.0f%05.2f%c%c", ld, lm, ns, od, om, ew, s)
}
