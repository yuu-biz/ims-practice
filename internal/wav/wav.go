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

// Read reads a RIFF WAVE file of 16-bit PCM, mono, 8 kHz.
func Read(r io.Reader) ([]int16, error) {
	var riff [12]byte
	if _, err := io.ReadFull(r, riff[:]); err != nil {
		return nil, err
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return nil, errors.New("wav: not a RIFF WAVE file")
	}
	formatSeen := false
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, errors.New("wav: no data chunk")
		}
		size := binary.LittleEndian.Uint32(hdr[4:])
		switch string(hdr[0:4]) {
		case "fmt ":
			f := make([]byte, size)
			if _, err := io.ReadFull(r, f); err != nil {
				return nil, err
			}
			if len(f) < 16 {
				return nil, errors.New("wav: short fmt chunk")
			}
			format, channels := binary.LittleEndian.Uint16(f[0:]), binary.LittleEndian.Uint16(f[2:])
			rate, bits := binary.LittleEndian.Uint32(f[4:]), binary.LittleEndian.Uint16(f[14:])
			if format != 1 || channels != 1 || rate != SampleRate || bits != 16 {
				return nil, fmt.Errorf("wav: format %d, %d channels, %d Hz, %d bits; want PCM, 1, %d, 16",
					format, channels, rate, bits, SampleRate)
			}
			formatSeen = true
		case "data":
			if !formatSeen {
				return nil, errors.New("wav: data before fmt")
			}
			samples := make([]int16, size/2)
			if err := binary.Read(r, binary.LittleEndian, samples); err != nil {
				return nil, err
			}
			return samples, nil
		default:
			// Chunks are padded to an even size.
			if _, err := io.CopyN(io.Discard, r, int64(size+size%2)); err != nil {
				return nil, err
			}
		}
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
