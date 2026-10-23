// Package pipeline turns a raw byte stream into a compressed, encrypted one
// and back again, hashing and counting bytes on the way through.
//
// Encoding order is raw -> zstd -> age -> stored. Decoding is the reverse.
// Nothing is buffered beyond the fixed-size windows of the codecs, so memory
// use does not depend on how much data flows through.
package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
)

// ErrChecksum is returned when the stored bytes do not hash to the expected value.
var ErrChecksum = errors.New("checksum mismatch")

// Stats describes a finished encode.
type Stats struct {
	RawBytes     int64
	RawSHA256    string
	StoredBytes  int64
	StoredSHA256 string
}

// tally counts and hashes everything that passes through it.
type tally struct {
	n int64
	h hash.Hash
}

func newTally() *tally { return &tally{h: sha256.New()} }

func (t *tally) add(p []byte) {
	t.n += int64(len(p))
	t.h.Write(p)
}

func (t *tally) sum() string { return hex.EncodeToString(t.h.Sum(nil)) }

type tallyWriter struct {
	t   *tally
	dst io.Writer
}

func (w *tallyWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	w.t.add(p[:n])
	return n, err
}

type tallyReader struct {
	t   *tally
	src io.Reader
}

func (r *tallyReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	r.t.add(p[:n])
	return n, err
}

// Encoder compresses and encrypts what is written to it.
type Encoder struct {
	raw    *tally
	stored *tally
	rawW   io.Writer
	zw     *zstd.Encoder
	aw     io.WriteCloser
}

// NewEncoder writes zstd-compressed, age-encrypted data to dst.
func NewEncoder(dst io.Writer, recipients ...age.Recipient) (*Encoder, error) {
	if len(recipients) == 0 {
		return nil, errors.New("no age recipient given")
	}
	e := &Encoder{raw: newTally(), stored: newTally()}
	aw, err := age.Encrypt(&tallyWriter{t: e.stored, dst: dst}, recipients...)
	if err != nil {
		return nil, fmt.Errorf("start age encryption: %w", err)
	}
	zw, err := zstd.NewWriter(aw,
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(2),
		zstd.WithWindowSize(4<<20),
	)
	if err != nil {
		return nil, fmt.Errorf("start zstd: %w", err)
	}
	e.aw, e.zw = aw, zw
	e.rawW = &tallyWriter{t: e.raw, dst: zw}
	return e, nil
}

// Write accepts raw bytes.
func (e *Encoder) Write(p []byte) (int, error) { return e.rawW.Write(p) }

// Close flushes the compressor, then the encryptor. It does not close dst.
func (e *Encoder) Close() error {
	zerr := e.zw.Close()
	aerr := e.aw.Close()
	if zerr != nil {
		return fmt.Errorf("finish zstd: %w", zerr)
	}
	if aerr != nil {
		return fmt.Errorf("finish age: %w", aerr)
	}
	return nil
}

// Stats is only complete after Close.
func (e *Encoder) Stats() Stats {
	return Stats{
		RawBytes:     e.raw.n,
		RawSHA256:    e.raw.sum(),
		StoredBytes:  e.stored.n,
		StoredSHA256: e.stored.sum(),
	}
}

// Decoder reverses an Encoder. Read returns the original raw bytes.
type Decoder struct {
	src    io.Reader
	stored *tally
	raw    *tally
	ar     io.Reader
	zr     *zstd.Decoder
	done   bool
}

// NewDecoder decrypts and decompresses src.
func NewDecoder(src io.Reader, identities ...age.Identity) (*Decoder, error) {
	if len(identities) == 0 {
		return nil, errors.New("no age identity given")
	}
	d := &Decoder{stored: newTally(), raw: newTally()}
	d.src = &tallyReader{t: d.stored, src: src}
	ar, err := age.Decrypt(d.src, identities...)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	zr, err := zstd.NewReader(ar, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(64<<20))
	if err != nil {
		return nil, fmt.Errorf("start zstd: %w", err)
	}
	d.ar, d.zr = ar, zr
	return d, nil
}

// Read returns decompressed plaintext. At the end of the stream it also reads
// the encrypted stream to its end, so a truncated or altered tail is an error
// and not a silent short read.
func (d *Decoder) Read(p []byte) (int, error) {
	n, err := d.zr.Read(p)
	d.raw.add(p[:n])
	if err == io.EOF && !d.done {
		d.done = true
		if _, derr := io.Copy(io.Discard, d.ar); derr != nil {
			return n, fmt.Errorf("decrypt: %w", derr)
		}
	}
	return n, err
}

// Close releases decoder goroutines. It does not close the source.
func (d *Decoder) Close() { d.zr.Close() }

// RawBytes is the number of plaintext bytes read so far.
func (d *Decoder) RawBytes() int64 { return d.raw.n }

// VerifyStored reads whatever is left of the source and compares the SHA-256
// of the whole stored stream with want. Call it after the data has been used
// (or after a failure, to tell corruption apart from other errors).
func (d *Decoder) VerifyStored(want string) error {
	if _, err := io.Copy(io.Discard, d.src); err != nil {
		return fmt.Errorf("read rest of stored stream: %w", err)
	}
	if got := d.stored.sum(); got != want {
		return fmt.Errorf("%w: stored sha256 is %s, manifest says %s", ErrChecksum, got, want)
	}
	return nil
}
