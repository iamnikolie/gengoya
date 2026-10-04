package provider

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"strings"
)

// WrapPCMAsWAV prepends a canonical 44-byte RIFF/WAVE header to headerless
// little-endian signed PCM (pure Go, no ffmpeg). bits is normally 16.
func WrapPCMAsWAV(pcm []byte, sampleRate, channels, bits int) []byte {
	// Values come from the response mime ("audio/L16;rate=24000"); keep them in
	// the ranges a WAV header can hold and players accept.
	if channels < 1 || channels > 8 {
		channels = 1
	}
	if bits != 8 && bits != 16 && bits != 24 && bits != 32 {
		bits = 16
	}
	if sampleRate < 1 || sampleRate > 384000 {
		sampleRate = 24000
	}
	blockAlign := channels * bits / 8
	byteRate := sampleRate * blockAlign
	var buf bytes.Buffer
	buf.Grow(44 + len(pcm))
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+len(pcm)))
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16)) // fmt chunk size
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))  // PCM
	_ = binary.Write(&buf, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(bits))
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)
	return buf.Bytes()
}

// IsWAV reports whether b starts with a RIFF/WAVE header.
func IsWAV(b []byte) bool {
	return len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WAVE"
}

// WAVSeconds returns the playback length of a PCM WAV from its fmt chunk and
// data size, or 0 when the header cannot be parsed. A data size of 0 or
// 0xFFFFFFFF (streaming writers) is treated as "rest of the file".
func WAVSeconds(b []byte) float64 {
	if !IsWAV(b) {
		return 0
	}
	var byteRate uint32
	pos := 12
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		size := binary.LittleEndian.Uint32(b[pos+4 : pos+8])
		body := pos + 8
		switch id {
		case "fmt ":
			if body+12 > len(b) {
				return 0
			}
			byteRate = binary.LittleEndian.Uint32(b[body+8 : body+12])
		case "data":
			n := int(size)
			if size == 0 || size == 0xFFFFFFFF || body+n > len(b) {
				n = len(b) - body
			}
			if byteRate == 0 {
				return 0
			}
			return float64(n) / float64(byteRate)
		}
		next := body + int(size)
		if size%2 == 1 {
			next++
		}
		if next <= pos {
			return 0
		}
		pos = next
	}
	return 0
}

func isPCMMime(mime string) bool {
	m := strings.ToLower(mime)
	return strings.HasPrefix(m, "audio/l16") || strings.HasPrefix(m, "audio/pcm")
}

// ParsePCMMime reads rate / channels from "audio/L16;codec=pcm;rate=24000" or
// "audio/l16; rate=24000; channels=1". Missing values return rate 0 / 1 channel.
func ParsePCMMime(mime string) (rate, channels int) {
	channels = 1
	for _, p := range strings.Split(mime, ";")[1:] {
		kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
		if len(kv) != 2 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(kv[1]))
		if err != nil || n <= 0 {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(kv[0])) {
		case "rate":
			rate = n
		case "channels":
			channels = n
		}
	}
	return rate, channels
}
