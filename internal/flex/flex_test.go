package flex

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestParseVITAIQ(t *testing.T) {
	// IF data with stream id, class id, timestamps and 2 IQ samples.
	payload := make([]byte, 16)
	le := binary.LittleEndian
	for i, v := range []float32{0.5, -0.25, 1, 2} {
		le.PutUint32(payload[i*4:], math.Float32bits(v))
	}
	words := 1 + 1 + 2 + 1 + 2 + len(payload)/4
	b := make([]byte, words*4)
	be := binary.BigEndian
	h := uint32(1)<<28 | 1<<27 | 1<<22 | 1<<20 | uint32(7)<<16 | uint32(words)
	be.PutUint32(b, h)
	be.PutUint32(b[4:], 0x20000001)
	be.PutUint32(b[8:], flexOUI)
	be.PutUint32(b[12:], classIQ24k)
	copy(b[28:], payload)

	p, err := ParseVITA(b)
	if err != nil {
		t.Fatal(err)
	}
	if p.StreamID != 0x20000001 || p.OUI != flexOUI || IQRate(p.PacketClass) != 24000 || p.Count != 7 {
		t.Fatalf("%+v", p)
	}
	iq := IQSamples(nil, p.Payload, false)
	if len(iq) != 2 || iq[0] != complex(0.5, -0.25) || iq[1] != complex(1, 2) {
		t.Fatalf("%v", iq)
	}
	if sw := IQSamples(nil, p.Payload, true); sw[0] != complex(-0.25, 0.5) {
		t.Fatalf("swap %v", sw)
	}
}

func TestFirstHex(t *testing.T) {
	for in, want := range map[string]uint32{
		"0x40000000,0x42000000": 0x40000000,
		"0x20000000":            0x20000000,
		"pan=0x40000001 wf=0x4": 0x40000001,
	} {
		if got, err := FirstHex(in); err != nil || got != want {
			t.Errorf("FirstHex(%q) = %x, %v", in, got, err)
		}
	}
	if _, err := FirstHex(""); err == nil {
		t.Error("empty reply should fail")
	}
}

func TestDAXIQUsers(t *testing.T) {
	r := &Radio{pans: map[uint32]*Pan{}, clients: map[uint32]Client{}, streams: map[uint32]Stream{}, slices: map[int]Slice{}, handle: 0x11}
	r.handleStatus("slice 0 in_use=1 RF_frequency=14.100000 client_handle=0x11 mode=USB pan=0x40000001 rxant=XVTA")
	r.handleStatus("slice 1 in_use=1 RF_frequency=7.1 client_handle=0x22 pan=0x40000000")
	if s := r.SlicesOn(0x40000001); len(s) != 1 || s[0].FreqHz != 14100000 || s[0].Mode != "USB" {
		t.Fatalf("slices %+v", s)
	}
	r.handleStatus("slice 0 in_use=0")
	if s := r.SlicesOn(0x40000001); len(s) != 0 {
		t.Fatalf("slice not removed %+v", s)
	}
	r.handleStatus("client 0x22 connected local_ptt=0 program=FlexWSPR station=VA3MW\x7fPC")
	r.handleStatus("display pan 0x40000000 client_handle=0x22 daxiq_channel=1 center=14.097")
	r.handleStatus("stream 0x20000005 type=dax_iq daxiq_channel=3 client_handle=0x33")
	r.handleStatus("display pan 0x40000001 client_handle=0x11 daxiq_channel=2 center=144.39")
	if u := r.DAXIQUsers(1); len(u) != 1 || u[0] != "FlexWSPR (VA3MW PC)" {
		t.Fatalf("channel 1: %v", u)
	}
	if u := r.DAXIQUsers(2); len(u) != 0 {
		t.Fatalf("channel 2 (ours): %v", u)
	}
	if u := r.DAXIQUsers(3); len(u) != 1 || u[0] != "client 0x00000033" {
		t.Fatalf("channel 3: %v", u)
	}
	r.handleStatus("stream 0x20000005 removed")
	r.handleStatus("client 0x22 disconnected forced=0")
	if u := r.DAXIQUsers(3); len(u) != 0 {
		t.Fatalf("after removal: %v", u)
	}
	if c := r.Clients(); len(c) != 0 {
		t.Fatalf("clients %v", c)
	}
}
