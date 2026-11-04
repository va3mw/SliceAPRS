// Package ax25 decodes and encodes AX.25 UI frames (without the HDLC flags
// and FCS), including the digipeater path and its has-been-repeated bits.
package ax25

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Address is one AX.25 address. H is the has-been-repeated bit for a
// digipeater (the command/response bit for source and destination).
type Address struct {
	Call string
	SSID int
	H    bool
}

// String is CALL or CALL-SSID.
func (a Address) String() string {
	if a.SSID == 0 {
		return a.Call
	}
	return a.Call + "-" + strconv.Itoa(a.SSID)
}

// Frame is a decoded UI frame.
type Frame struct {
	Dest    Address
	Src     Address
	Path    []Address // digipeaters, in order
	Control byte
	PID     byte
	Info    []byte
}

// MaxDigis is the most digipeaters AX.25 allows in a path.
const MaxDigis = 8

// Decode parses a frame's bytes (addresses through info, no FCS).
func Decode(b []byte) (*Frame, error) {
	if len(b) < 16 {
		return nil, errors.New("frame too short")
	}
	var addrs []Address
	i := 0
	for {
		if i+7 > len(b) {
			return nil, errors.New("address field runs past the frame")
		}
		a, err := decodeAddr(b[i : i+7])
		if err != nil {
			return nil, err
		}
		addrs = append(addrs, a)
		last := b[i+6]&1 != 0
		i += 7
		if last {
			break
		}
		if len(addrs) == 2+MaxDigis {
			return nil, errors.New("too many digipeaters")
		}
	}
	if len(addrs) < 2 {
		return nil, errors.New("missing source address")
	}
	if i+2 > len(b) {
		return nil, errors.New("no control/PID")
	}
	f := &Frame{Dest: addrs[0], Src: addrs[1], Path: addrs[2:], Control: b[i]}
	if f.Control&^0x10 != 0x03 { // UI, poll/final either way
		return nil, fmt.Errorf("not a UI frame (control 0x%02X)", f.Control)
	}
	f.PID = b[i+1]
	f.Info = append([]byte(nil), b[i+2:]...)
	return f, nil
}

func decodeAddr(b []byte) (Address, error) {
	var sb strings.Builder
	for k := 0; k < 6; k++ {
		if b[k]&1 != 0 {
			return Address{}, errors.New("address extension bit set inside a callsign")
		}
		c := b[k] >> 1
		if c == ' ' {
			continue
		}
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return Address{}, fmt.Errorf("bad callsign character 0x%02X", c)
		}
		sb.WriteByte(c)
	}
	if sb.Len() == 0 {
		return Address{}, errors.New("empty callsign")
	}
	return Address{Call: sb.String(), SSID: int(b[6]>>1) & 0x0F, H: b[6]&0x80 != 0}, nil
}

// Encode builds the frame's bytes (no FCS).
func (f *Frame) Encode() []byte {
	addrs := append([]Address{f.Dest, f.Src}, f.Path...)
	var out []byte
	for k, a := range addrs {
		call := (a.Call + "      ")[:6]
		for j := 0; j < 6; j++ {
			out = append(out, call[j]<<1)
		}
		ssid := byte(0x60) | byte(a.SSID&0x0F)<<1
		if a.H {
			ssid |= 0x80
		}
		if k == len(addrs)-1 {
			ssid |= 1
		}
		out = append(out, ssid)
	}
	ctl, pid := f.Control, f.PID
	if ctl == 0 {
		ctl = 0x03
	}
	if pid == 0 {
		pid = 0xF0
	}
	out = append(out, ctl, pid)
	return append(out, f.Info...)
}

// String is the frame in TNC2 monitor format: SRC>DEST,DIGI1,DIGI2*:info.
// The star follows the last digipeater that has repeated the frame.
func (f *Frame) String() string {
	var sb strings.Builder
	sb.WriteString(f.Src.String())
	sb.WriteByte('>')
	sb.WriteString(f.Dest.String())
	last := f.LastRepeated()
	for k, d := range f.Path {
		sb.WriteByte(',')
		sb.WriteString(d.String())
		if k == last {
			sb.WriteByte('*')
		}
	}
	sb.WriteByte(':')
	for _, c := range f.Info {
		if c < 0x20 || c == 0x7F {
			fmt.Fprintf(&sb, "<0x%02x>", c)
		} else {
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// LastRepeated is the index in Path of the last digipeater with its H bit
// set, or -1 when no digipeater has repeated the frame.
func (f *Frame) LastRepeated() int {
	last := -1
	for k, d := range f.Path {
		if d.H {
			last = k
		}
	}
	return last
}

// ParseAddress parses "CALL", "CALL-7" or "CALL-7*" (the star sets H).
func ParseAddress(s string) (Address, error) {
	var a Address
	if strings.HasSuffix(s, "*") {
		a.H = true
		s = s[:len(s)-1]
	}
	call, ssid, has := strings.Cut(strings.ToUpper(s), "-")
	if call == "" || len(call) > 6 {
		return a, fmt.Errorf("bad callsign %q", s)
	}
	for _, c := range call {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return a, fmt.Errorf("bad callsign %q", s)
		}
	}
	a.Call = call
	if has {
		n, err := strconv.Atoi(ssid)
		if err != nil || n < 0 || n > 15 {
			return a, fmt.Errorf("bad SSID in %q", s)
		}
		a.SSID = n
	}
	return a, nil
}

// ParseTNC2 builds a frame from monitor format, as in String. A star on a
// digipeater sets H on it and on every digipeater before it. For tests and
// the generator.
func ParseTNC2(s string) (*Frame, error) {
	hdr, info, ok := strings.Cut(s, ":")
	if !ok {
		return nil, errors.New("no ':' in packet")
	}
	src, rest, ok := strings.Cut(hdr, ">")
	if !ok {
		return nil, errors.New("no '>' in packet")
	}
	parts := strings.Split(rest, ",")
	f := &Frame{Control: 0x03, PID: 0xF0, Info: []byte(info)}
	var err error
	if f.Src, err = ParseAddress(src); err != nil {
		return nil, err
	}
	if f.Dest, err = ParseAddress(parts[0]); err != nil {
		return nil, err
	}
	star := -1
	for k, p := range parts[1:] {
		a, err := ParseAddress(p)
		if err != nil {
			return nil, err
		}
		if a.H {
			star = k
		}
		f.Path = append(f.Path, a)
	}
	for k := 0; k <= star; k++ {
		f.Path[k].H = true
	}
	if len(f.Path) > MaxDigis {
		return nil, errors.New("too many digipeaters")
	}
	return f, nil
}
