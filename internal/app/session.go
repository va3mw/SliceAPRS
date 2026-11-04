package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/va3mw/SliceAPRS/internal/config"
	"github.com/va3mw/SliceAPRS/internal/flex"
	"github.com/va3mw/SliceAPRS/internal/wav"
)

// errBusy is a session that could not start because someone else has the
// DAX IQ channel; it is retried less often.
type errBusy struct{ msg string }

func (e errBusy) Error() string { return e.msg }

// radioRun connects to the radio and reconnects until ctx is cancelled.
type radioRun struct {
	cfg    *config.Config
	log    *slog.Logger
	mon    *monitor
	an     *Analyzer
	record string // optional IQ recording file
}

func (a *radioRun) run(ctx context.Context) {
	for {
		err := a.session(ctx)
		if ctx.Err() != nil {
			return
		}
		a.mon.state(StateError, errText(err))
		wait := 5 * time.Second
		var busy errBusy
		if errors.As(err, &busy) {
			wait = 30 * time.Second
		}
		a.log.Warn("session ended; retrying", "in", wait, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (a *radioRun) findRadio(ctx context.Context) (string, error) {
	if a.cfg.Radio.IP != "" {
		a.mon.set(func(s *Status) { s.Radio = a.cfg.Radio.IP })
		return a.cfg.Radio.IP, nil
	}
	a.mon.state(StateSearching, "")
	a.log.Info("looking for a radio on the LAN", "serial", a.cfg.Radio.Serial)
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := flex.Discover(dctx, a.cfg.Radio.Serial)
	if err != nil {
		return "", fmt.Errorf("no radio found by discovery (set the radio's IP address to skip it): %w", err)
	}
	a.log.Info("found radio", "model", info.Model, "nickname", info.Nickname, "serial", info.Serial,
		"ip", info.IP, "version", info.Version, "status", info.Status)
	a.mon.set(func(s *Status) { s.Radio = fmt.Sprintf("%s %s (%s)", info.Model, info.Nickname, info.IP) })
	return info.IP, nil
}

// session is one connection: check, set up, stream, decode, tear down.
// SliceAPRS only receives: it never keys the radio.
func (a *radioRun) session(ctx context.Context) error {
	ip, err := a.findRadio(ctx)
	if err != nil {
		return err
	}
	a.mon.state(StateConnecting, "")
	r, err := flex.Dial(ip, a.log.With("src", "radio"))
	if err != nil {
		return err
	}
	defer r.Close()

	udp, port, err := openUDP()
	if err != nil {
		return err
	}
	defer udp.Close()

	cmd := func(c string) (string, error) { return r.Command(c) }
	must := func(c string) (string, error) {
		rep, err := cmd(c)
		if err != nil {
			a.log.Error("command failed", "cmd", c, "err", err)
		}
		return rep, err
	}

	// Only GUI clients can own panadapters, so connect as one, like
	// FlexWSPR. SmartSDR does not need to be running.
	cmd("client program SliceAPRS")
	cmd("client station " + flex.Escape("SliceAPRS "+a.cfg.Callsign))
	if _, err := must("client gui"); err != nil {
		return fmt.Errorf("the radio would not accept us as a GUI client (are SmartSDR and another program using all its GUI connections?): %w", err)
	}
	if _, err := must(fmt.Sprintf("client udpport %d", port)); err != nil {
		return err
	}
	cmd("sub client all")
	cmd("sub pan all")
	cmd("sub daxiq all")
	cmd("sub stream all")
	cmd("sub slice all")

	// Let the statuses of the other clients, panadapters and streams
	// arrive, then look before taking anything.
	time.Sleep(2 * time.Second)
	var others []string
	for _, c := range r.Clients() {
		n := c.Program
		if n == "" {
			n = fmt.Sprintf("0x%08X", c.Handle)
		}
		if c.Station != "" {
			n += " (" + c.Station + ")"
		}
		others = append(others, n)
	}
	if len(others) > 0 {
		a.log.Info("other programs on the radio", "clients", strings.Join(others, ", "))
	}
	a.mon.set(func(s *Status) { s.Others = others; s.DAXIQ = a.cfg.DAXIQ.Channel })
	if users := r.DAXIQUsers(a.cfg.DAXIQ.Channel); len(users) > 0 {
		return errBusy{fmt.Sprintf("DAX IQ channel %d is already used by %s; pick another channel in Settings (FlexWSPR normally uses 1)",
			a.cfg.DAXIQ.Channel, strings.Join(users, ", "))}
	}
	// The radio may restore a panadapter for a GUI client; remove only our
	// own.
	for _, p := range r.OurPans() {
		a.log.Info("removing our leftover panadapter", "pan", fmt.Sprintf("0x%08X", p.ID))
		cmd(fmt.Sprintf("display pan remove 0x%08X", p.ID))
	}

	// The DAX IQ stream is centred on the panadapter. Do not correct it
	// from the centre in panadapter statuses: the status sent when the
	// panadapter is created carries its old centre and can arrive after
	// ours is set, which shifted the IQ by kilohertz.
	var panIDa, sidA atomic.Uint32
	r.OnPan(func(p flex.Pan) {
		if p.ID != panIDa.Load() {
			return
		}
		a.mon.set(func(s *Status) {
			s.RxAnt = p.RxAnt
			if len(p.AntList) > 0 {
				s.Antennas = p.AntList
			}
		})
	})

	freq := a.cfg.FreqHz
	rep, err := must("display panafall create x=100 y=100")
	if err != nil {
		return err
	}
	panID, err := flex.FirstHex(rep)
	if err != nil {
		time.Sleep(time.Second) // some firmware replies empty; the status names it
		if ps := r.OurPans(); len(ps) > 0 {
			panID, err = ps[0].ID, nil
		}
	}
	if err != nil {
		return fmt.Errorf("panadapter: %w", err)
	}
	pan := fmt.Sprintf("0x%08X", panID)
	panIDa.Store(panID)
	var stream string
	defer func() {
		if stream != "" {
			cmd("stream remove " + stream)
		}
		cmd("display pan remove " + pan)
		a.log.Info("removed panadapter and stream", "pan", pan)
	}()
	if _, err := must(fmt.Sprintf("display pan set %s center=%.6f", pan, float64(freq)/1e6)); err != nil {
		return fmt.Errorf("the radio would not tune the panadapter to %.3f MHz (set up a 2 m transverter in the radio's XVTR settings): %w",
			float64(freq)/1e6, err)
	}
	if _, err := must(fmt.Sprintf("display pan set %s daxiq_channel=%d", pan, a.cfg.DAXIQ.Channel)); err != nil {
		return err
	}
	// Let the panadapter's status for the new centre arrive.
	time.Sleep(time.Second)
	rxant := a.cfg.RxAnt
	if rxant == "" {
		rxant = transverterAnt(r, panID)
		if rxant != "" {
			a.log.Info("using the transverter antenna", "rxant", rxant)
		}
	}
	if rxant != "" {
		must(fmt.Sprintf("display pan set %s rxant=%s", pan, rxant))
	}
	// The radio sets the receiver's band filters from the slices on a
	// panadapter, and gives a new one a slice on its last band (a 14.100
	// USB slice left the 2 m transverter IF behind the 20 m filter on a
	// FLEX-6600M). Put our slice on the APRS channel in FM.
	sl := r.SlicesOn(panID)
	ant := ""
	if rxant != "" {
		ant = " rxant=" + rxant
	}
	if len(sl) == 0 {
		if _, err := must(fmt.Sprintf("slice create pan=%s freq=%.6f mode=FM%s", pan, float64(freq)/1e6, ant)); err != nil {
			return fmt.Errorf("slice on %.3f MHz: %w", float64(freq)/1e6, err)
		}
	}
	for i, s := range sl {
		if i > 0 {
			cmd(fmt.Sprintf("slice remove %d", s.Index))
			continue
		}
		must(fmt.Sprintf("slice tune %d %.6f", s.Index, float64(freq)/1e6))
		must(fmt.Sprintf("slice set %d mode=FM%s", s.Index, ant))
		a.log.Info("moved the panadapter's slice to the APRS channel", "slice", s.Index, "was_MHz", float64(s.FreqHz)/1e6, "was_mode", s.Mode)
	}
	rep, err = must(fmt.Sprintf("stream create type=dax_iq daxiq_channel=%d", a.cfg.DAXIQ.Channel))
	if err != nil {
		return err
	}
	sid, err := flex.FirstHex(rep)
	if err != nil {
		return fmt.Errorf("DAX IQ stream: %w", err)
	}
	stream = fmt.Sprintf("0x%08X", sid)
	sidA.Store(sid)
	must(fmt.Sprintf("stream set %s daxiq_rate=%d", stream, config.IQRate))
	a.log.Info("receiving", "MHz", float64(freq)/1e6, "rxant", rxant, "daxiq", a.cfg.DAXIQ.Channel, "pan", pan, "stream", stream)
	a.mon.set(func(s *Status) { s.FreqHz = freq })
	a.mon.state(StateRunning, "")

	iqErr := make(chan error, 1)
	go func() { iqErr <- a.receiveIQ(udp, net.ParseIP(ip), &sidA) }()
	select {
	case <-ctx.Done():
		return nil
	case <-r.Done():
		return r.Err()
	case err := <-iqErr:
		return err
	}
}

// receiveIQ reads DAX IQ packets for our stream and decodes them.
func (a *radioRun) receiveIQ(c *net.UDPConn, radio net.IP, sid *atomic.Uint32) error {
	var (
		p      *pipeline
		fs     int
		last   = -1
		perPkt int
		iq     []complex64
		buf    = make([]byte, 16384)
		got    bool
	)
	var rec *wav.Writer
	defer func() {
		if rec != nil {
			rec.Close()
		}
	}()
	for {
		c.SetReadDeadline(time.Now().Add(10 * time.Second))
		n, src, err := c.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return fmt.Errorf("no DAX IQ data for 10 s")
			}
			return err
		}
		if radio != nil && !src.IP.Equal(radio) {
			continue
		}
		pk, err := flex.ParseVITA(buf[:n])
		if err != nil || pk.StreamID != sid.Load() {
			continue
		}
		rate := flex.IQRate(pk.PacketClass)
		if rate == 0 {
			continue
		}
		if rate != fs {
			fs, last = rate, -1
			p = newPipeline(rate, false, a.an, a.mon)
			if a.record != "" {
				if rec != nil {
					rec.Close()
				}
				if rec, err = wav.Create(a.record, rate, 2); err != nil {
					a.log.Error("IQ recording", "err", err)
					rec = nil
				} else {
					a.log.Info("recording IQ", "file", a.record, "rate", rate)
				}
				p.rec = rec
			}
		}
		if !got {
			got = true
			a.log.Info("DAX IQ data arriving", "rate", rate, "samples_per_packet", len(pk.Payload)/8)
		}
		lost := 0
		if last >= 0 {
			lost = (int(pk.Count) - last - 1 + 16) % 16
		}
		last = int(pk.Count)
		iq = iq[:0]
		if lost > 0 && perPkt > 0 {
			iq = append(iq, make([]complex64, lost*perPkt)...)
			a.mon.set(func(s *Status) { s.Decoder.Lost += lost })
		}
		samples := flex.IQSamples(nil, pk.Payload, a.cfg.DAXIQ.SwapIQ)
		perPkt = len(samples)
		iq = append(iq, samples...)
		p.iq(iq)
	}
}

// openUDP binds the local VITA socket, starting at 4991 like FlexLib. When
// FlexWSPR runs on the same computer it has 4991, so this takes the next.
func openUDP() (*net.UDPConn, int, error) {
	var lastErr error
	for port := 4991; port <= 4991+40; port++ {
		if port == 4992 { // discovery broadcasts
			continue
		}
		c, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
		if err == nil {
			c.SetReadBuffer(1 << 21)
			return c, port, nil
		}
		lastErr = err
	}
	return nil, 0, fmt.Errorf("no free UDP port for the DAX IQ stream: %w", lastErr)
}

// transverterAnt picks the receive antenna when none is set: the radio
// keeps a panadapter on its HF antenna when it is tuned into a transverter
// band (a FLEX-6600M stayed on ANT1 at 144.390 and heard only a nearby
// tracker), so use the transverter port. "" leaves the radio's choice.
func transverterAnt(r *flex.Radio, panID uint32) string {
	p, ok := r.PanByID(panID)
	if !ok || p.Xvtr == "" {
		return ""
	}
	if strings.HasPrefix(p.RxAnt, "XVT") {
		return p.RxAnt
	}
	ants := p.AntList
	if len(ants) == 0 {
		ants = r.Antennas()
	}
	for _, a := range ants {
		if a == "XVTA" {
			return a
		}
	}
	for _, a := range ants {
		if strings.HasPrefix(a, "XVT") {
			return a
		}
	}
	if len(ants) == 0 {
		return "XVTA"
	}
	return ""
}
