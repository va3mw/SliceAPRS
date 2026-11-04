// SliceAPRS listens to APRS on 2 m through a FlexRadio and uses the packets
// to spot 2 m path openings. It takes a DAX IQ stream straight from the radio,
// decodes AFSK 1200 in Go, works out which station each packet was heard
// directly from, learns the normal range and flags openings.
//
// This is the headless program (Debian, Raspberry Pi, Windows). Its GUI is a
// web page it serves itself; cmd/sliceaprs-gui is the desktop app.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/va3mw/SliceAPRS/internal/app"
	"github.com/va3mw/SliceAPRS/internal/ax25"
	"github.com/va3mw/SliceAPRS/internal/config"
	"github.com/va3mw/SliceAPRS/internal/dsp"
	"github.com/va3mw/SliceAPRS/internal/modem"
	"github.com/va3mw/SliceAPRS/internal/wav"
	"github.com/va3mw/SliceAPRS/internal/web"
)

var version = "0.1.1" // set by the build scripts

func main() {
	cfgPath := flag.String("config", "", "configuration file (default: "+config.DefaultConfigPath()+
		", or sliceaprs.yaml next to the program)")
	listen := flag.String("listen", "", "web page address, e.g. :8081 or off (overrides web.listen)")
	decode := flag.String("decode", "", "decode a WAV file (1 channel: audio; 2 channels: IQ), print the packets and counts, and exit")
	replay := flag.String("replay", "", "run on a WAV recording instead of the radio (GUI and history as normal)")
	fast := flag.Bool("fast", false, "with -replay: decode as fast as possible instead of in real time")
	record := flag.String("record-iq", "", "write the radio's DAX IQ to this WAV file while running")
	shift := flag.Float64("shift", 0, "with -decode on IQ: move the signal down by this many Hz first (to find a frequency offset)")
	showVer := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVer {
		fmt.Println("SliceAPRS", version)
		return
	}
	if *decode != "" {
		os.Exit(decodeFile(*decode, *shift))
	}

	path := config.Find(*cfgPath)
	cfg, saved, err := config.LoadOrDefault(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st := app.NewStation(path, cfg, saved, version, os.Stderr)
	st.Replay, st.ReplayFast, st.RecordIQ = *replay, *fast, *record
	log := st.Log()
	if !saved {
		log.Info("no configuration file yet; open the web page to set up", "will_save_to", path)
	}
	addr := cfg.Web.Listen
	if *listen != "" {
		addr = *listen
	}
	// A -listen flag fixes the address; otherwise it follows the settings.
	defer web.Start(addr, st, *listen == "").Close()
	st.Run(ctx)
}

// decodeFile decodes a recording and prints each packet, then the totals:
// the decoder test bench.
func decodeFile(name string, shift float64) int {
	r, err := wav.Open(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer r.Close()
	iq := r.Channels == 2
	var demod *modem.Demod
	var feedIQ func([]complex64, func(modem.Received))
	var feedAudio func([]float32, func(modem.Received))
	if iq {
		rx := modem.NewReceiver(r.Rate, modem.Options{})
		demod, feedIQ = rx.Demod(), rx.IQ
	} else {
		rx := modem.NewAudioReceiver(r.Rate, modem.Options{})
		demod, feedAudio = rx.Demod(), rx.Audio
	}
	kind := "audio"
	if iq {
		kind = "IQ"
	}
	fmt.Fprintf(os.Stderr, "%s: %d Hz, %d channel(s), decoding as %s\n", name, r.Rate, r.Channels, kind)
	t0 := time.Now()
	good, bad := 0, 0
	emit := func(x modem.Received) {
		f, err := ax25.Decode(x.Data)
		at := float64(x.End) / float64(r.Rate)
		if err != nil {
			bad++
			fmt.Printf("%9.2fs  [not AX.25: %v]\n", at, err)
			return
		}
		good++
		lv := ""
		if x.HasLevel {
			lv = fmt.Sprintf(" %5.1f dBFS SNR %4.1f", x.LevelDB, x.SNR())
		}
		fmt.Printf("%9.2fs%s  %s\n", at, lv, f)
	}
	var nco dsp.NCO
	nco.SetFreq(shift, r.Rate)
	block := 4096
	fr := [][]float32{make([]float32, block), make([]float32, block)}
	buf := make([]complex64, block)
	var samples int64
	for {
		n, err := r.Read(fr)
		if errors.Is(err, io.EOF) || n == 0 {
			break
		}
		samples += int64(n)
		if iq {
			for i := 0; i < n; i++ {
				buf[i] = complex(fr[0][i], fr[1][i])
			}
			nco.Mix(buf[:n])
			feedIQ(buf[:n], emit)
		} else {
			feedAudio(fr[0][:n], emit)
		}
	}
	secs := float64(samples) / float64(r.Rate)
	fmt.Fprintf(os.Stderr, "%d packets decoded (%d more with a good FCS but not AX.25) from %.1f s of %s in %.1f s\n",
		good, bad, secs, kind, time.Since(t0).Seconds())
	fmt.Fprintf(os.Stderr, "per slicer (first or not): %v\n", demod.Count)
	return 0
}
