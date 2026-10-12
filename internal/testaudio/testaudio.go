// SPDX-License-Identifier: Apache-2.0

// Package testaudio generates tiny, silent audio files for tests, so the
// repository needs no binary or copyrighted fixtures. It is imported only by
// tests and is not part of the Rewind binary.
//
// The files are structurally valid enough for TagLib to read their audio
// properties and to add tags with Tag; they are not meant to be playable.
package testaudio

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"go.senan.xyz/taglib"
)

// WAV returns a mono 16-bit PCM WAV file of the given length at 8 kHz.
func WAV(seconds int) []byte {
	const rate, bytesPerSample = 8000, 2
	data := make([]byte, rate*bytesPerSample*seconds)
	var b bytes.Buffer
	b.WriteString("RIFF")
	le32(&b, uint32(36+len(data)))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	le32(&b, 16)
	le16(&b, 1) // PCM
	le16(&b, 1) // mono
	le32(&b, rate)
	le32(&b, rate*bytesPerSample)
	le16(&b, bytesPerSample)
	le16(&b, 16)
	b.WriteString("data")
	le32(&b, uint32(len(data)))
	b.Write(data)
	return b.Bytes()
}

// MP3 returns silent MPEG-1 Layer III frames (32 kbit/s, 32 kHz, mono).
func MP3(seconds int) []byte {
	const frameLen = 144 // 144 * 32000 / 32000, no padding
	frames := seconds * 32000 / 1152
	var b bytes.Buffer
	for i := 0; i < frames; i++ {
		b.Write([]byte{0xFF, 0xFB, 0x18, 0xC0})
		b.Write(make([]byte, frameLen-4))
	}
	return b.Bytes()
}

// FLAC returns a FLAC stream with a STREAMINFO block describing the given
// length at 8 kHz mono 16-bit, followed by padding.
func FLAC(seconds int) []byte {
	var b bytes.Buffer
	b.WriteString("fLaC")
	b.Write([]byte{0x00, 0, 0, 34})   // STREAMINFO, not last, length 34
	be16(&b, 4096)                    // min block size
	be16(&b, 4096)                    // max block size
	b.Write([]byte{0, 0, 0, 0, 0, 0}) // min/max frame size unknown
	const rate = 8000
	total := uint64(rate * seconds)
	// 20 bits sample rate, 3 bits channels-1, 5 bits bps-1, 36 bits samples.
	packed := uint64(rate)<<44 | uint64(0)<<41 | uint64(15)<<36 | total
	var p [8]byte
	binary.BigEndian.PutUint64(p[:], packed)
	b.Write(p[:])
	b.Write(make([]byte, 16))            // MD5 unknown
	b.Write([]byte{0x81, 0, 0x04, 0x00}) // PADDING, last, length 1024
	b.Write(make([]byte, 1024))
	return b.Bytes()
}

// OggVorbis returns an Ogg Vorbis stream (44.1 kHz stereo headers, one tiny
// audio packet) whose final granule position encodes the given length.
func OggVorbis(seconds int) []byte {
	const rate = 44100
	var id bytes.Buffer
	id.WriteString("\x01vorbis")
	le32(&id, 0) // version
	id.WriteByte(2)
	le32(&id, rate)
	le32(&id, 0)
	le32(&id, 128000)
	le32(&id, 0)
	id.WriteByte(0xB8) // blocksizes 256 / 2048
	id.WriteByte(1)    // framing

	var comment bytes.Buffer
	comment.WriteString("\x03vorbis")
	vendor := "rewind-testaudio"
	le32(&comment, uint32(len(vendor)))
	comment.WriteString(vendor)
	le32(&comment, 0)
	comment.WriteByte(1)

	setup := append([]byte("\x05vorbis"), make([]byte, 32)...)

	const serial = 0x52455744
	var out bytes.Buffer
	out.Write(oggPage(0x02, 0, serial, 0, id.Bytes()))
	out.Write(oggPage(0x00, 0, serial, 1, comment.Bytes(), setup))
	out.Write(oggPage(0x04, uint64(rate*seconds), serial, 2, []byte{0}))
	return out.Bytes()
}

func oggPage(flags byte, granule uint64, serial, seq uint32, packets ...[]byte) []byte {
	var segs []byte
	var body bytes.Buffer
	for _, p := range packets {
		n := len(p)
		for n >= 255 {
			segs = append(segs, 255)
			n -= 255
		}
		segs = append(segs, byte(n))
		body.Write(p)
	}
	var h bytes.Buffer
	h.WriteString("OggS")
	h.WriteByte(0)
	h.WriteByte(flags)
	var g [8]byte
	binary.LittleEndian.PutUint64(g[:], granule)
	h.Write(g[:])
	le32(&h, serial)
	le32(&h, seq)
	le32(&h, 0) // CRC placeholder
	h.WriteByte(byte(len(segs)))
	h.Write(segs)
	page := append(h.Bytes(), body.Bytes()...)
	binary.LittleEndian.PutUint32(page[22:], oggChecksum(page))
	return page
}

// oggChecksum is CRC-32 with polynomial 0x04C11DB7, MSB-first, no
// reflection, zero initial value and no final XOR, as Ogg requires.
func oggChecksum(data []byte) uint32 {
	var crc uint32
	for _, b := range data {
		crc = (crc << 8) ^ oggTable[byte(crc>>24)^b]
	}
	return crc
}

var oggTable = func() (t [256]uint32) {
	for i := range t {
		r := uint32(i) << 24
		for j := 0; j < 8; j++ {
			if r&0x80000000 != 0 {
				r = r<<1 ^ 0x04C11DB7
			} else {
				r <<= 1
			}
		}
		t[i] = r
	}
	return
}()

// M4A returns an MPEG-4 audio file with an AAC sample description and the
// given duration, and no audio samples.
func M4A(seconds int) []byte {
	const rate = 44100
	ftyp := box("ftyp", []byte("M4A "), u32(0), []byte("M4A mp42isom"))

	matrix := concat(u32(0x00010000), u32(0), u32(0), u32(0), u32(0x00010000), u32(0), u32(0), u32(0), u32(0x40000000))
	mvhd := box("mvhd", u32(0), u32(0), u32(0), u32(1000), u32(uint32(seconds*1000)),
		u32(0x00010000), u16(0x0100), make([]byte, 10), matrix, make([]byte, 24), u32(2))
	tkhd := box("tkhd", u32(7), u32(0), u32(0), u32(1), u32(0), u32(uint32(seconds*1000)),
		make([]byte, 8), u16(0), u16(0), u16(0x0100), u16(0), matrix, u32(0), u32(0))
	mdhd := box("mdhd", u32(0), u32(0), u32(0), u32(rate), u32(uint32(rate*seconds)), u16(0x55C4), u16(0))
	hdlr := box("hdlr", u32(0), u32(0), []byte("soun"), make([]byte, 12), []byte{0})

	esds := box("esds", u32(0),
		[]byte{0x03, 25}, u16(1), []byte{0},
		[]byte{0x04, 17, 0x40, 0x15}, []byte{0, 0, 0}, u32(128000), u32(128000),
		[]byte{0x05, 2, 0x12, 0x10},
		[]byte{0x06, 1, 0x02})
	mp4a := box("mp4a", make([]byte, 6), u16(1), make([]byte, 8), u16(2), u16(16), u16(0), u16(0), u32(rate<<16), esds)
	stsd := box("stsd", u32(0), u32(1), mp4a)
	empty := func(t string) []byte { return box(t, u32(0), u32(0)) }
	stbl := box("stbl", stsd, empty("stts"), empty("stsc"), box("stsz", u32(0), u32(0), u32(0)), empty("stco"))
	dinf := box("dinf", box("dref", u32(0), u32(1), box("url ", u32(1))))
	minf := box("minf", box("smhd", u32(0), u32(0)), dinf, stbl)
	mdia := box("mdia", mdhd, hdlr, minf)
	moov := box("moov", mvhd, box("trak", tkhd, mdia))
	return concat(ftyp, moov, box("mdat", []byte{0}))
}

func box(typ string, parts ...[]byte) []byte {
	body := concat(parts...)
	return concat(u32(uint32(8+len(body))), []byte(typ), body)
}

func concat(parts ...[]byte) []byte {
	var b bytes.Buffer
	for _, p := range parts {
		b.Write(p)
	}
	return b.Bytes()
}

func u32(v uint32) []byte { var b [4]byte; binary.BigEndian.PutUint32(b[:], v); return b[:] }
func u16(v uint16) []byte { var b [2]byte; binary.BigEndian.PutUint16(b[:], v); return b[:] }

func le32(b *bytes.Buffer, v uint32) { binary.Write(b, binary.LittleEndian, v) }
func le16(b *bytes.Buffer, v uint16) { binary.Write(b, binary.LittleEndian, v) }
func be16(b *bytes.Buffer, v uint16) { binary.Write(b, binary.BigEndian, v) }

// Generators maps a file extension to its generator.
var Generators = map[string]func(seconds int) []byte{
	".wav":  WAV,
	".mp3":  MP3,
	".flac": FLAC,
	".ogg":  OggVorbis,
	".m4a":  M4A,
}

// Write creates dir/name (and parent directories) containing a generated file
// for name's extension, optionally tagged with tags. It returns the path.
func Write(t testing.TB, dir, name string, seconds int, tags map[string][]string) string {
	t.Helper()
	gen, ok := Generators[filepath.Ext(name)]
	if !ok {
		t.Fatalf("testaudio: no generator for %q", name)
	}
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, gen(seconds), 0o644); err != nil {
		t.Fatal(err)
	}
	if len(tags) > 0 {
		Tag(t, path, tags)
	}
	return path
}

// Tag writes tags into a generated test file using TagLib. Rewind itself
// never writes tags; this is test setup only.
func Tag(t testing.TB, path string, tags map[string][]string) {
	t.Helper()
	if err := taglib.WriteTags(path, tags, taglib.Clear); err != nil {
		t.Fatalf("testaudio: tagging %s: %v", path, err)
	}
}
