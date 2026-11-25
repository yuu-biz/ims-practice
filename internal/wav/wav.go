// Package wav reads and writes the WAV files the UE simulator plays and
// records: 16-bit PCM, mono, 8000 samples per second, which is what
// G.711 carries.
package wav

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// SampleRate is the G.711 sampling rate (RFC 3551 4.5.14).
const SampleRate = 8000

// Read reads a RIFF WAVE file of 16-bit PCM, mono, 8 kHz. It refuses a
// chunk whose size claims more bytes than the file has left, so that a
// broken file cannot make it reserve memory the file does not hold.
func Read(r io.Reader) ([]int16, error) {
	file, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(file) < 12 || string(file[0:4]) != "RIFF" || string(file[8:12]) != "WAVE" {
		return nil, errors.New("wav: not a RIFF WAVE file")
	}
	formatSeen := false
	for rest := file[12:]; ; {
		if len(rest) < 8 {
			return nil, errors.New("wav: no data chunk")
		}
		id, size := string(rest[0:4]), binary.LittleEndian.Uint32(rest[4:])
		rest = rest[8:]
		if uint64(size) > uint64(len(rest)) {
			return nil, fmt.Errorf("wav: %q chunk of %d bytes with %d bytes left in the file", id, size, len(rest))
		}
		body := rest[:size]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, errors.New("wav: short fmt chunk")
			}
			format, channels := binary.LittleEndian.Uint16(body[0:]), binary.LittleEndian.Uint16(body[2:])
			rate, bits := binary.LittleEndian.Uint32(body[4:]), binary.LittleEndian.Uint16(body[14:])
			if format != 1 || channels != 1 || rate != SampleRate || bits != 16 {
				return nil, fmt.Errorf("wav: format %d, %d channels, %d Hz, %d bits; want PCM, 1, %d, 16",
					format, channels, rate, bits, SampleRate)
			}
			formatSeen = true
		case "data":
			if !formatSeen {
				return nil, errors.New("wav: data before fmt")
			}
			samples := make([]int16, len(body)/2)
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(body[2*i:]))
			}
			return samples, nil
		}
		// Chunks are padded to an even size; the last one may end without
		// its pad byte.
		rest = rest[min(uint64(size)+uint64(size%2), uint64(len(rest))):]
	}
}

// Write writes samples as a RIFF WAVE file of 16-bit PCM, mono, 8 kHz.
func Write(w io.Writer, samples []int16) error {
	data := uint32(2 * len(samples))
	hdr := make([]byte, 44)
	copy(hdr[0:], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], 36+data)
	copy(hdr[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(hdr[16:], 16)
	binary.LittleEndian.PutUint16(hdr[20:], 1) // PCM
	binary.LittleEndian.PutUint16(hdr[22:], 1) // mono
	binary.LittleEndian.PutUint32(hdr[24:], SampleRate)
	binary.LittleEndian.PutUint32(hdr[28:], 2*SampleRate) // bytes per second
	binary.LittleEndian.PutUint16(hdr[32:], 2)            // bytes per frame
	binary.LittleEndian.PutUint16(hdr[34:], 16)
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], data)
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	return binary.Write(w, binary.LittleEndian, samples)
}
