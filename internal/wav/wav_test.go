package wav

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestIQRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "iq.wav")
	w, err := Create(p, 24000, 2)
	if err != nil {
		t.Fatal(err)
	}
	w.WriteIQ([]complex64{complex(0.5, -0.25), complex(1, 0)})
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Rate != 24000 || r.Channels != 2 {
		t.Fatalf("%d Hz %d ch", r.Rate, r.Channels)
	}
	fr := [][]float32{make([]float32, 4), make([]float32, 4)}
	n, _ := r.Read(fr)
	if n != 2 || fr[0][0] != 0.5 || fr[1][0] != -0.25 || fr[0][1] != 1 {
		t.Fatalf("%d %v", n, fr)
	}
	if _, err := r.Read(fr); err != io.EOF {
		t.Fatal("no EOF")
	}
}

func TestPCM16(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.wav")
	le := binary.LittleEndian
	h := make([]byte, 44+4)
	copy(h, "RIFF")
	le.PutUint32(h[4:], 40)
	copy(h[8:], "WAVEfmt ")
	le.PutUint32(h[16:], 16)
	le.PutUint16(h[20:], 1)
	le.PutUint16(h[22:], 1)
	le.PutUint32(h[24:], 44100)
	le.PutUint32(h[28:], 88200)
	le.PutUint16(h[32:], 2)
	le.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	le.PutUint32(h[40:], 4)
	le.PutUint16(h[44:], uint16(16384))
	v := int16(-32768)
	le.PutUint16(h[46:], uint16(v))
	os.WriteFile(p, h, 0o644)
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fr := [][]float32{make([]float32, 8)}
	n, _ := r.Read(fr)
	if n != 2 || fr[0][0] != 0.5 || fr[0][1] != -1 {
		t.Fatalf("%d %v", n, fr[0][:2])
	}
}
