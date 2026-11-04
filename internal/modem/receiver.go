package modem

// Received is a frame with its signal level, when measured from IQ.
type Received struct {
	Frame
	LevelDB  float64 // channel power during the frame, dB full scale
	FloorDB  float64 // channel noise floor, dB full scale
	HasLevel bool
}

// SNR is the level above the noise floor in dB.
func (r Received) SNR() float64 { return r.LevelDB - r.FloorDB }

// Receiver is the whole chain from FM IQ at one rate to frames.
type Receiver struct {
	fm   *FMDemod
	lvl  *Level
	afsk *Demod
}

// NewReceiver returns a receiver for IQ at fs with the channel centred on
// 0 Hz.
func NewReceiver(fs int, opt Options) *Receiver {
	return &Receiver{fm: NewFMDemod(fs, 7500), lvl: NewLevel(fs), afsk: NewDemod(fs, opt)}
}

// Demod gives access to the AFSK demodulator's counters.
func (r *Receiver) Demod() *Demod { return r.afsk }

// IQ processes a block of IQ.
func (r *Receiver) IQ(iq []complex64, emit func(Received)) {
	audio, pow := r.fm.Process(iq)
	r.lvl.Add(pow)
	r.afsk.Process(audio, func(f Frame) {
		rx := Received{Frame: f}
		// Skip the preamble flags' start; the frame proper is what matters.
		if lv, ok := r.lvl.Mean(f.Start, f.End); ok {
			rx.LevelDB, rx.HasLevel = lv, true
			rx.FloorDB, _ = r.lvl.Floor()
		}
		emit(rx)
	})
}

// AudioReceiver decodes audio (discriminator or speaker output), with no
// signal level.
type AudioReceiver struct{ afsk *Demod }

// NewAudioReceiver returns a decoder for audio at fs.
func NewAudioReceiver(fs int, opt Options) *AudioReceiver {
	return &AudioReceiver{afsk: NewDemod(fs, opt)}
}

// Demod gives access to the AFSK demodulator's counters.
func (r *AudioReceiver) Demod() *Demod { return r.afsk }

// Audio processes a block of audio.
func (r *AudioReceiver) Audio(a []float32, emit func(Received)) {
	r.afsk.Process(a, func(f Frame) { emit(Received{Frame: f}) })
}
