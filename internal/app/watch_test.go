package app

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/va3mw/SliceAPRS/internal/flex"
)

func TestCheckTuning(t *testing.T) {
	r := flex.Offline(0x11)
	r.Status("display pan 0x40000000 client_handle=0x11 daxiq_channel=1 center=144.390000 rxant=XVTA")
	r.Status("slice 0 in_use=1 RF_frequency=144.390000 client_handle=0x11 mode=FM pan=0x40000000 rxant=XVTA")
	var sent []string
	tn := &tuning{r: r, pan: "0x40000000", panID: 0x40000000, freq: 144390000, rxant: "XVTA", channel: 1,
		others: map[uint32]string{},
		cmd:    func(c string) (string, error) { sent = append(sent, c); return "", nil }}
	a := &radioRun{log: slog.New(slog.NewTextHandler(io.Discard, nil)), mon: &monitor{}}
	changed := func() []string { return a.mon.snapshot().Changed }

	// All as we left it: nothing is sent, nothing to ask.
	if err := a.check(tn, false); err != nil || len(sent) != 0 || len(changed()) != 0 {
		t.Fatalf("steady: %v %v %v", err, sent, changed())
	}

	// While settling after setup, a late status is put right silently.
	r.Status("display pan 0x40000000 center=14.100000")
	if a.check(tn, true); len(sent) != 1 || sent[0] != "display pan set 0x40000000 center=144.390000" || len(changed()) != 0 {
		t.Fatalf("settling: %v %v", sent, changed())
	}
	r.Status("display pan 0x40000000 center=144.390000")
	sent = nil

	// SmartSDR connects and moves the receiver: ask, send nothing.
	r.Status("client 0x22 connected program=SmartSDR-Win station=VA3MW\x7fPC")
	r.Status("display pan 0x40000000 center=14.100000 rxant=ANT1")
	r.Status("slice 0 RF_frequency=14.100000 mode=CW")
	if err := a.check(tn, false); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 0 {
		t.Errorf("sent without asking: %v", sent)
	}
	if got := strings.Join(changed(), "; "); got != "panadapter moved to 14.100 MHz; antenna changed to ANT1; slice moved to 14.100 MHz CW" {
		t.Errorf("changed %q", got)
	}
	if o := a.mon.snapshot().Others; len(o) != 1 || o[0] != "SmartSDR-Win (VA3MW PC)" {
		t.Errorf("others %v", o)
	}

	// The user says restore.
	if err := a.restoreRadio(tn); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(sent, "\n")
	for _, want := range []string{"display pan set 0x40000000 center=144.390000", "display pan set 0x40000000 rxant=XVTA",
		"slice tune 0 144.390000", "slice set 0 mode=FM rxant=XVTA"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if len(changed()) != 0 {
		t.Errorf("still asking: %v", changed())
	}

	// Back on 2 m; SmartSDR leaves: a refresh with the same settings.
	r.Status("display pan 0x40000000 center=144.390000 rxant=XVTA")
	r.Status("slice 0 RF_frequency=144.390000 mode=FM")
	sent = nil
	r.Status("client 0x22 disconnected forced=0")
	if a.check(tn, false); len(sent) != 3 || len(changed()) != 0 {
		t.Errorf("after SmartSDR left: %v %v", sent, changed())
	}
	sent = nil
	if a.check(tn, false); len(sent) != 0 {
		t.Errorf("next cycle: %v", sent)
	}

	// Our slice removed: ask; our panadapter removed: the session ends.
	r.Status("slice 0 in_use=0")
	if a.check(tn, false); len(sent) != 0 || len(changed()) != 1 || changed()[0] != "its slice was removed" {
		t.Errorf("slice gone: %v %v", sent, changed())
	}
	r.Status("display pan 0x40000000 removed")
	if err := a.check(tn, false); err == nil {
		t.Error("panadapter gone: no error")
	}
}
