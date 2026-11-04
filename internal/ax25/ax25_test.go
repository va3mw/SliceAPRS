package ax25

import "testing"

func TestRoundTrip(t *testing.T) {
	for _, s := range []string{
		"VA3MW-9>APRS,WIDE1-1,WIDE2-2:!4500.00N/07530.00W>test",
		"VE3ABC>APN391,VE3XYZ-1,WIDE2*:>status",
		"N0CALL>APRS:hello",
	} {
		f, err := ParseTNC2(s)
		if err != nil {
			t.Fatal(err)
		}
		g, err := Decode(f.Encode())
		if err != nil {
			t.Fatal(err)
		}
		if got := g.String(); got != s {
			t.Errorf("got %q, want %q", got, s)
		}
	}
}

func TestHBits(t *testing.T) {
	f, _ := ParseTNC2("A>B,D1,D2*,WIDE2-1:x")
	g, _ := Decode(f.Encode())
	if !g.Path[0].H || !g.Path[1].H || g.Path[2].H {
		t.Fatalf("H bits %+v", g.Path)
	}
	if g.LastRepeated() != 1 {
		t.Fatalf("last %d", g.LastRepeated())
	}
}

func TestDecodeErrors(t *testing.T) {
	if _, err := Decode([]byte{1, 2, 3}); err == nil {
		t.Error("short frame accepted")
	}
	f, _ := ParseTNC2("A>B:x")
	b := f.Encode()
	b[14] = 0x3F // I frame control
	if _, err := Decode(b); err == nil {
		t.Error("non-UI accepted")
	}
}
