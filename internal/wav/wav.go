// Package wav reads and writes WAV files: audio recordings to decode, and
// IQ recordings (I left, Q right) to replay.
package wav

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// Reader streams samples from a WAV file as float32 in -1..1.
type Reader struct {
	f        *os.File
	r        *bufio.Reader
	Rate     int
	Channels int
	bits     int
	float    bool
	left     int64 // data bytes left
}

// Open opens a PCM (8, 16, 24 or 32 bit) or float WAV file.
func Open(name string) (*Reader, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	w := &Reader{f: f, r: bufio.NewReaderSize(f, 1<<16)}
	if err := w.header(); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return w, nil
}

func (w *Reader) header() error {
	var riff [12]byte
	if _, err := io.ReadFull(w.r, riff[:]); err != nil {
		return err
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return errors.New("not a WAV file")
	}
	le := binary.LittleEndian
	gotFmt := false
	for {
		var ch [8]byte
		if _, err := io.ReadFull(w.r, ch[:]); err != nil {
			return errors.New("no data chunk")
		}
		size := int64(le.Uint32(ch[4:]))
		switch string(ch[0:4]) {
		case "fmt ":
			b := make([]byte, size)
			if _, err := io.ReadFull(w.r, b); err != nil {
				return err
			}
			if len(b) < 16 {
				return errors.New("short fmt chunk")
			}
			tag := le.Uint16(b[0:])
			w.Channels = int(le.Uint16(b[2:]))
			w.Rate = int(le.Uint32(b[4:]))
			w.bits = int(le.Uint16(b[14:]))
			if tag == 0xFFFE && len(b) >= 26 { // extensible: the real tag is in the GUID
				tag = le.Uint16(b[24:])
			}
			switch tag {
			case 1:
			case 3:
				w.float = true
			default:
				return fmt.Errorf("unsupported WAV format %d", tag)
			}
			if size%2 == 1 {
				w.r.ReadByte()
			}
			gotFmt = true
		case "data":
			if !gotFmt {
				return errors.New("data before fmt")
			}
			w.left = size
			if size == 0 || size == 0xFFFFFFFF {
				w.left = math.MaxInt64 // streaming writers leave it unset
			}
			if w.Channels < 1 || w.Rate < 1000 {
				return errors.New("bad WAV header")
			}
			switch {
			case w.float && (w.bits == 32 || w.bits == 64):
			case !w.float && (w.bits == 8 || w.bits == 16 || w.bits == 24 || w.bits == 32):
			default:
				return fmt.Errorf("unsupported %d-bit samples", w.bits)
			}
			return nil
		default:
			if _, err := io.CopyN(io.Discard, w.r, size+size%2); err != nil {
				return err
			}
		}
	}
}

// Read fills frames with up to len(frames[0]) sample frames, one slice per
// channel, and returns how many it read; io.EOF at the end.
func (w *Reader) Read(frames [][]float32) (int, error) {
	bps := w.bits / 8
	n := len(frames[0])
	buf := make([]byte, bps*w.Channels)
	le := binary.LittleEndian
	for i := 0; i < n; i++ {
		if w.left < int64(len(buf)) {
			if i == 0 {
				return 0, io.EOF
			}
			return i, nil
		}
		if _, err := io.ReadFull(w.r, buf); err != nil {
			if i == 0 {
				return 0, io.EOF
			}
			return i, nil
		}
		w.left -= int64(len(buf))
		for c := 0; c < w.Channels; c++ {
			b := buf[c*bps:]
			var v float32
			switch {
			case w.float && bps == 4:
				v = math.Float32frombits(le.Uint32(b))
			case w.float:
				v = float32(math.Float64frombits(le.Uint64(b)))
			case bps == 1:
				v = (float32(b[0]) - 128) / 128
			case bps == 2:
				v = float32(int16(le.Uint16(b))) / 32768
			case bps == 3:
				v = float32(int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24)) / 2147483648
			default:
				v = float32(int32(le.Uint32(b))) / 2147483648
			}
			if c < len(frames) {
				frames[c][i] = v
			}
		}
	}
	return n, nil
}

// Close closes the file.
func (w *Reader) Close() error { return w.f.Close() }

// Writer writes 32-bit float WAV files.
type Writer struct {
	f        *os.File
	w        *bufio.Writer
	channels int
	n        int64
}

// Create starts a float WAV file.
func Create(name string, rate, channels int) (*Writer, error) {
	f, err := os.Create(name)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, w: bufio.NewWriterSize(f, 1<<16), channels: channels}
	w.header(rate)
	return w, nil
}

func (w *Writer) header(rate int) {
	le := binary.LittleEndian
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	le.PutUint32(h[4:], uint32(36+w.n))
	copy(h[8:], "WAVEfmt ")
	le.PutUint32(h[16:], 16)
	le.PutUint16(h[20:], 3)
	le.PutUint16(h[22:], uint16(w.channels))
	le.PutUint32(h[24:], uint32(rate))
	le.PutUint32(h[28:], uint32(rate*w.channels*4))
	le.PutUint16(h[32:], uint16(w.channels*4))
	le.PutUint16(h[34:], 32)
	copy(h[36:], "data")
	le.PutUint32(h[40:], uint32(w.n))
	w.w.Write(h)
}

// WriteIQ appends IQ samples as a stereo pair (needs 2 channels).
func (w *Writer) WriteIQ(iq []complex64) error {
	var b [8]byte
	for _, s := range iq {
		binary.LittleEndian.PutUint32(b[0:], math.Float32bits(real(s)))
		binary.LittleEndian.PutUint32(b[4:], math.Float32bits(imag(s)))
		if _, err := w.w.Write(b[:]); err != nil {
			return err
		}
		w.n += 8
	}
	return nil
}

// WriteMono appends audio samples (needs 1 channel).
func (w *Writer) WriteMono(a []float32) error {
	var b [4]byte
	for _, s := range a {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(s))
		if _, err := w.w.Write(b[:]); err != nil {
			return err
		}
		w.n += 4
	}
	return nil
}

// Close finishes the header and closes the file.
func (w *Writer) Close() error {
	if err := w.w.Flush(); err != nil {
		w.f.Close()
		return err
	}
	le := binary.LittleEndian
	var b [4]byte
	le.PutUint32(b[:], uint32(36+w.n))
	w.f.WriteAt(b[:], 4)
	le.PutUint32(b[:], uint32(w.n))
	w.f.WriteAt(b[:], 40)
	return w.f.Close()
}
