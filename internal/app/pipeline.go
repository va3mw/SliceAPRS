package app

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/va3mw/SliceAPRS/internal/ax25"
	"github.com/va3mw/SliceAPRS/internal/modem"
	"github.com/va3mw/SliceAPRS/internal/track"
	"github.com/va3mw/SliceAPRS/internal/wav"
)

// pipeline decodes IQ (or audio) into frames and hands them to the
// analyzer.
type pipeline struct {
	rate  int
	rx    *modem.Receiver
	arx   *modem.AudioReceiver
	an    *Analyzer
	mon   *monitor
	rec   *wav.Writer // optional IQ recording
	onRx  func(track.Heard)
	ticks int
}

func newPipeline(rate int, audio bool, an *Analyzer, mon *monitor) *pipeline {
	p := &pipeline{rate: rate, an: an, mon: mon}
	if audio {
		p.arx = modem.NewAudioReceiver(rate, modem.Options{})
	} else {
		p.rx = modem.NewReceiver(rate, modem.Options{})
	}
	mon.set(func(s *Status) { s.Decoder.Rate = rate })
	return p
}

func (p *pipeline) frame(r modem.Received) {
	f, err := ax25.Decode(r.Data)
	if err != nil {
		p.mon.set(func(s *Status) { s.Decoder.Frames++; s.Decoder.BadAX25++ })
		return
	}
	sig := track.Signal{LevelDB: r.LevelDB, SNR: r.SNR(), HasLevel: r.HasLevel}
	h := p.an.Packet(f, sig)
	p.mon.set(func(s *Status) {
		s.Decoder.Frames++
		if r.HasLevel {
			s.Decoder.FloorDB, s.Decoder.HasFloor = r.FloorDB, true
		}
	})
	if p.onRx != nil {
		p.onRx(h)
	}
}

func (p *pipeline) iq(iq []complex64) {
	if p.rec != nil {
		p.rec.WriteIQ(iq)
	}
	p.rx.IQ(iq, p.frame)
	p.stats(p.rx.Demod())
}

func (p *pipeline) audio(a []float32) {
	p.arx.Audio(a, p.frame)
	p.stats(p.arx.Demod())
}

func (p *pipeline) stats(d *modem.Demod) {
	p.ticks++
	if p.ticks%50 != 0 {
		return
	}
	c := append([]int(nil), d.Count...)
	p.mon.set(func(s *Status) { s.Decoder.Slicers = c })
}

// replay decodes a WAV file: 2 channels are IQ (I left, Q right), 1 is
// audio. With realtime it keeps to the recording's pace, so the GUI shows
// the packets as they would arrive on air.
func replay(name string, an *Analyzer, mon *monitor, realtime bool, stop <-chan struct{}) error {
	r, err := wav.Open(name)
	if err != nil {
		return err
	}
	defer r.Close()
	if r.Channels > 2 {
		return fmt.Errorf("%s has %d channels; want 1 (audio) or 2 (IQ)", name, r.Channels)
	}
	p := newPipeline(r.Rate, r.Channels == 1, an, mon)
	block := r.Rate / 20
	fr := [][]float32{make([]float32, block), make([]float32, block)}
	iq := make([]complex64, block)
	t0 := time.Now()
	var done int64
	for {
		select {
		case <-stop:
			return nil
		default:
		}
		n, err := r.Read(fr)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if r.Channels == 2 {
			for i := 0; i < n; i++ {
				iq[i] = complex(fr[0][i], fr[1][i])
			}
			p.iq(iq[:n])
		} else {
			p.audio(fr[0][:n])
		}
		done += int64(n)
		if realtime {
			if ahead := time.Duration(done)*time.Second/time.Duration(r.Rate) - time.Since(t0); ahead > 0 {
				select {
				case <-stop:
					return nil
				case <-time.After(ahead):
				}
			}
		}
	}
}
