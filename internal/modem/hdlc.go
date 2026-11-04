// Package modem turns 2 m FM IQ into AX.25 frames: FM demodulation, 1200
// baud AFSK (Bell 202) demodulation with a digital PLL for the bit clock,
// NRZI and HDLC framing with bit unstuffing and the FCS check. It also has
// the matching modulators, used by the tests and the signal generator.
package modem

// FCS is the AX.25 frame check sequence: CRC-16-CCITT, reflected, preset to
// all ones and complemented.
func FCS(b []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, c := range b {
		crc ^= uint16(c)
		for k := 0; k < 8; k++ {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

// Smallest frame: two addresses, control, PID and the FCS.
const (
	minFrame = 7*2 + 2 + 2
	maxFrame = 7*10 + 2 + 256 + 2 + 16 // a little over the AX.25 limit
)

// hdlc collects bits into frames: flag detection, bit unstuffing and the
// FCS check. It is fed data bits after NRZI decoding.
type hdlc struct {
	pat     uint8 // last 8 bits, newest in bit 7
	acc     uint8
	nbits   int
	buf     []byte
	active  bool  // between flags
	start   int64 // sample index of the opening flag
	lastFlg int64 // sample index of the last flag seen
	got     int64 // start of the frame bit last returned
}

// bit adds one data bit at sample index at. It returns a frame (without
// FCS) when a closing flag ends a frame whose FCS checks.
func (h *hdlc) bit(b int, at int64) []byte {
	h.pat = h.pat>>1 | uint8(b)<<7
	if h.pat == 0x7E {
		var out []byte
		// The flag's first seven bits went into acc; a whole number of bytes
		// before them means nbits is 7.
		if h.active && h.nbits == 7 && len(h.buf) >= minFrame {
			n := len(h.buf)
			if FCS(h.buf[:n-2]) == uint16(h.buf[n-2])|uint16(h.buf[n-1])<<8 {
				out = append([]byte(nil), h.buf[:n-2]...)
				h.got = h.lastFlg
			}
		}
		h.active, h.buf, h.acc, h.nbits = true, h.buf[:0], 0, 0
		h.start, h.lastFlg = at, at
		return out
	}
	if h.pat&0xFE == 0xFE { // seven ones: abort, or idle noise
		h.active = false
		return nil
	}
	if !h.active {
		return nil
	}
	if h.pat&0xFC == 0x7C { // five ones then a zero: a stuffed bit
		return nil
	}
	h.acc = h.acc>>1 | uint8(b)<<7
	h.nbits++
	if h.nbits == 8 {
		if len(h.buf) >= maxFrame {
			h.active = false
			return nil
		}
		h.buf = append(h.buf, h.acc)
		h.acc, h.nbits = 0, 0
	}
	return nil
}

// inFrame reports whether a frame looks to be in progress: a flag in the
// last few bytes or data being collected. The PLL holds its phase harder
// then.
func (h *hdlc) inFrame() bool { return h.active }

// HDLCBits frames data (no FCS) for sending: preamble flags, the data and
// FCS with bit stuffing, LSB first, then closing flags. The result is data
// bits before NRZI.
func HDLCBits(data []byte, preamble, postamble int) []int {
	var bits []int
	flag := func() {
		for k := 0; k < 8; k++ {
			bits = append(bits, int(0x7E>>k&1))
		}
	}
	for i := 0; i < preamble; i++ {
		flag()
	}
	fcs := FCS(data)
	frame := append(append([]byte(nil), data...), byte(fcs), byte(fcs>>8))
	ones := 0
	for _, c := range frame {
		for k := 0; k < 8; k++ {
			b := int(c >> k & 1)
			bits = append(bits, b)
			if b == 1 {
				ones++
				if ones == 5 {
					bits = append(bits, 0)
					ones = 0
				}
			} else {
				ones = 0
			}
		}
	}
	for i := 0; i < postamble; i++ {
		flag()
	}
	return bits
}

// NRZI encodes data bits as line levels: a 0 changes the level, a 1 keeps
// it. level is the starting level and is updated.
func NRZI(bits []int, level *int) []int {
	out := make([]int, len(bits))
	for i, b := range bits {
		if b == 0 {
			*level ^= 1
		}
		out[i] = *level
	}
	return out
}
