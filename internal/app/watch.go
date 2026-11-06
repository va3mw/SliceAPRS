package app

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/va3mw/SliceAPRS/internal/flex"
)

// watchEvery is how often the session checks that our panadapter and
// slice are still where we put them.
const watchEvery = 15 * time.Second

// settleTime after setup, differences are our own setup settling (late
// statuses from the radio), so they are put right without asking.
const settleTime = 45 * time.Second

// tuning is what the session set up on the radio, checked every cycle.
// Another program on the radio (SmartSDR on the same receiver, say) can
// move it. SliceAPRS then asks in the GUI before putting it back.
type tuning struct {
	r       *flex.Radio
	cmd     func(string) (string, error)
	pan     string
	panID   uint32
	freq    int64
	rxant   string // "" leaves the radio's choice
	channel int
	others  map[uint32]string // other programs last seen
	asked   string            // the changes last reported, to log each once
}

// change is one difference from our set-up and the commands that undo it.
type change struct {
	what string
	cmds []string
}

func clientName(c flex.Client) string {
	n := c.Program
	if n == "" {
		n = fmt.Sprintf("client 0x%08X", c.Handle)
	}
	if c.Station != "" {
		n += " (" + c.Station + ")"
	}
	return n
}

// programs logs other programs coming and going and reports whether one
// left since the last cycle.
func (a *radioRun) programs(t *tuning) (left bool) {
	now := map[uint32]string{}
	for _, c := range t.r.Clients() {
		now[c.Handle] = clientName(c)
	}
	for h, n := range now {
		if _, ok := t.others[h]; !ok {
			a.log.Info("another program connected to the radio", "program", n)
		}
	}
	for h, n := range t.others {
		if _, ok := now[h]; !ok {
			a.log.Info("another program left the radio", "program", n)
			left = true
		}
	}
	t.others = now
	names := make([]string, 0, len(now))
	for _, n := range now {
		names = append(names, n)
	}
	sort.Strings(names)
	a.mon.set(func(s *Status) { s.Others = names })
	return left
}

// changes compares the radio's state with our set-up. It returns an error
// when our panadapter is gone, which ends the session.
func (t *tuning) changes() ([]change, error) {
	p, ok := t.r.PanByID(t.panID)
	if !ok {
		return nil, fmt.Errorf("our panadapter %s was removed from the radio", t.pan)
	}
	mhz := float64(t.freq) / 1e6
	var out []change
	if d := p.CenterHz - t.freq; d > 1000 || d < -1000 {
		out = append(out, change{fmt.Sprintf("panadapter moved to %.3f MHz", float64(p.CenterHz)/1e6),
			[]string{fmt.Sprintf("display pan set %s center=%.6f", t.pan, mhz)}})
	}
	if p.DAXIQ != t.channel {
		out = append(out, change{fmt.Sprintf("DAX IQ channel changed to %d", p.DAXIQ),
			[]string{fmt.Sprintf("display pan set %s daxiq_channel=%d", t.pan, t.channel)}})
	}
	if t.rxant != "" && p.RxAnt != t.rxant {
		out = append(out, change{fmt.Sprintf("antenna changed to %s", p.RxAnt),
			[]string{fmt.Sprintf("display pan set %s rxant=%s", t.pan, t.rxant)}})
	}
	ant := ""
	if t.rxant != "" {
		ant = " rxant=" + t.rxant
	}
	sl := t.r.SlicesOn(t.panID)
	if len(sl) == 0 {
		out = append(out, change{"its slice was removed",
			[]string{fmt.Sprintf("slice create pan=%s freq=%.6f mode=FM%s", t.pan, mhz, ant)}})
		return out, nil
	}
	s := sl[0]
	if d := s.FreqHz - t.freq; d > 100 || d < -100 || s.Mode != "FM" {
		out = append(out, change{fmt.Sprintf("slice moved to %.3f MHz %s", float64(s.FreqHz)/1e6, s.Mode),
			[]string{fmt.Sprintf("slice tune %d %.6f", s.Index, mhz), fmt.Sprintf("slice set %d mode=FM%s", s.Index, ant)}})
	}
	return out, nil
}

// refresh sends our whole set-up again. The settings do not change, but
// the radio picks its filters and antenna for 2 m again.
func (t *tuning) refresh() {
	mhz := float64(t.freq) / 1e6
	t.cmd(fmt.Sprintf("display pan set %s center=%.6f", t.pan, mhz))
	if t.rxant != "" {
		t.cmd(fmt.Sprintf("display pan set %s rxant=%s", t.pan, t.rxant))
	}
	if sl := t.r.SlicesOn(t.panID); len(sl) > 0 {
		t.cmd(fmt.Sprintf("slice tune %d %.6f", sl[0].Index, mhz))
	}
}

// check runs one cycle. While settling it puts differences right; after
// that it reports them in the status for the GUI to ask about. A program
// leaving with nothing changed gets a refresh.
func (a *radioRun) check(t *tuning, settling bool) error {
	left := a.programs(t)
	ch, err := t.changes()
	if err != nil {
		return err
	}
	if settling || len(ch) == 0 {
		for _, c := range ch {
			for _, cmd := range c.cmds {
				t.cmd(cmd)
			}
		}
		if left && len(ch) == 0 {
			a.log.Info("refreshing our tuning after the other program left")
			t.refresh()
		}
		a.setChanges(t, nil)
		return nil
	}
	a.setChanges(t, ch)
	return nil
}

// restoreRadio puts back everything that changed, when the user asks.
func (a *radioRun) restoreRadio(t *tuning) error {
	ch, err := t.changes()
	if err != nil {
		return err
	}
	a.log.Info("restoring the radio at your request", "changes", len(ch))
	for _, c := range ch {
		for _, cmd := range c.cmds {
			t.cmd(cmd)
		}
	}
	t.refresh()
	a.setChanges(t, nil)
	return nil
}

func (a *radioRun) setChanges(t *tuning, ch []change) {
	what := make([]string, len(ch))
	for i, c := range ch {
		what[i] = c.what
	}
	if k := strings.Join(what, "; "); k != t.asked {
		t.asked = k
		if k != "" {
			a.log.Warn("the radio was changed under SliceAPRS; asking before restoring", "changes", k)
		}
	}
	a.mon.set(func(s *Status) { s.Changed = what })
}
