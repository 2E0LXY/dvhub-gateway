// Package wav reads and writes 16-bit PCM mono WAV files.
package wav

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
)

// Read returns samples scaled to [-1,1) and the sample rate. Multi-channel
// input is down-mixed.
func Read(path string) ([]float64, int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, 0, errors.New("wav: not RIFF/WAVE")
	}
	var rate, ch, bps int
	var data []byte
	for p := 12; p+8 <= len(b); {
		id, n := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:p+8]))
		body := b[p+8 : min(len(b), p+8+n)]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, 0, errors.New("wav: short fmt")
			}
			if f := binary.LittleEndian.Uint16(body); f != 1 && f != 0xfffe {
				return nil, 0, errors.New("wav: not PCM")
			}
			ch = int(binary.LittleEndian.Uint16(body[2:]))
			rate = int(binary.LittleEndian.Uint32(body[4:]))
			bps = int(binary.LittleEndian.Uint16(body[14:]))
		case "data":
			data = body
		}
		p += 8 + n + n&1
	}
	if bps != 16 || ch < 1 || data == nil {
		return nil, 0, errors.New("wav: need 16-bit PCM with data chunk")
	}
	n := len(data) / (2 * ch)
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		var s float64
		for c := 0; c < ch; c++ {
			s += float64(int16(binary.LittleEndian.Uint16(data[2*(i*ch+c):])))
		}
		out[i] = s / float64(ch) / 32768
	}
	return out, rate, nil
}

// Write stores samples (clipped to [-1,1]) as 16-bit mono PCM.
func Write(path string, x []float64, rate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return Encode(f, x, rate)
}

// Encode writes a WAV stream to w.
func Encode(w io.Writer, x []float64, rate int) error {
	h := make([]byte, 44)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+2*len(x)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(2*rate))
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(2*len(x)))
	buf := make([]byte, 2*len(x))
	for i, v := range x {
		v = max(-1, min(v, 32767.0/32768))
		binary.LittleEndian.PutUint16(buf[2*i:], uint16(int16(v*32768)))
	}
	if _, err := w.Write(h); err != nil {
		return err
	}
	_, err := w.Write(buf)
	return err
}
