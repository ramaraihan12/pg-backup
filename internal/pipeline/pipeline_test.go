package pipeline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/rand"
	"testing"

	"filippo.io/age"
)

func testData(n int) []byte {
	// Half random, half repetitive, so it compresses but not to nothing, and
	// spans many age chunks (64 KiB each).
	rng := rand.New(rand.NewSource(1))
	b := make([]byte, n)
	rng.Read(b[:n/2])
	for i := n / 2; i < n; i++ {
		b[i] = byte('a' + i%7)
	}
	return b
}

func encode(t *testing.T, data []byte, rec age.Recipient) ([]byte, Stats) {
	t.Helper()
	var out bytes.Buffer
	enc, err := NewEncoder(&out, rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(enc, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes(), enc.Stats()
}

func newKey(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRoundTrip(t *testing.T) {
	for _, size := range []int{0, 1, 65535, 65536, 65537, 5 << 20} {
		id := newKey(t)
		data := testData(size)
		stored, stats := encode(t, data, id.Recipient())

		if stats.RawBytes != int64(size) {
			t.Errorf("size %d: raw bytes = %d", size, stats.RawBytes)
		}
		if stats.StoredBytes != int64(len(stored)) {
			t.Errorf("size %d: stored bytes = %d, want %d", size, stats.StoredBytes, len(stored))
		}
		wantRaw := sha256.Sum256(data)
		if stats.RawSHA256 != hex.EncodeToString(wantRaw[:]) {
			t.Errorf("size %d: raw sha256 mismatch", size)
		}
		wantStored := sha256.Sum256(stored)
		if stats.StoredSHA256 != hex.EncodeToString(wantStored[:]) {
			t.Errorf("size %d: stored sha256 mismatch", size)
		}

		dec, err := NewDecoder(bytes.NewReader(stored), id)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(dec)
		dec.Close()
		if err != nil {
			t.Fatalf("size %d: decode: %v", size, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("size %d: round trip changed the bytes", size)
		}
		if err := dec.VerifyStored(stats.StoredSHA256); err != nil {
			t.Fatalf("size %d: verify: %v", size, err)
		}
	}
}

func TestCompressesAndEncrypts(t *testing.T) {
	id := newKey(t)
	data := bytes.Repeat([]byte("toko maju, jalan merdeka no. 1\n"), 50000)
	stored, _ := encode(t, data, id.Recipient())
	if len(stored) > len(data)/10 {
		t.Errorf("stored %d bytes for %d repetitive bytes, expected much less", len(stored), len(data))
	}
	if bytes.Contains(stored, []byte("toko maju")) {
		t.Error("plaintext found in stored bytes")
	}
}

func TestChecksumMismatchDetected(t *testing.T) {
	id := newKey(t)
	data := testData(1 << 20)
	stored, stats := encode(t, data, id.Recipient())

	// Flip one bit in the middle of the ciphertext.
	bad := append([]byte(nil), stored...)
	bad[len(bad)/2] ^= 0x01

	dec, err := NewDecoder(bytes.NewReader(bad), id)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(dec)
	dec.Close()
	if readErr == nil {
		t.Fatal("expected a decode error for a flipped bit")
	}
	err = dec.VerifyStored(stats.StoredSHA256)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("VerifyStored = %v, want ErrChecksum", err)
	}
}

func TestTruncatedStreamIsAnError(t *testing.T) {
	id := newKey(t)
	stored, stats := encode(t, testData(1<<20), id.Recipient())
	cut := stored[:len(stored)-100]

	dec, err := NewDecoder(bytes.NewReader(cut), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(dec); err == nil {
		t.Fatal("expected an error for a truncated stream")
	}
	dec.Close()
	if err := dec.VerifyStored(stats.StoredSHA256); !errors.Is(err, ErrChecksum) {
		t.Fatalf("VerifyStored = %v, want ErrChecksum", err)
	}
}

func TestWrongIdentityFails(t *testing.T) {
	stored, _ := encode(t, testData(1000), newKey(t).Recipient())
	if _, err := NewDecoder(bytes.NewReader(stored), newKey(t)); err == nil {
		t.Fatal("expected an error with the wrong identity")
	}
}

func TestEncoderNeedsRecipient(t *testing.T) {
	if _, err := NewEncoder(io.Discard); err == nil {
		t.Fatal("expected an error without recipients")
	}
}

func TestNoIdentityDoesNotPanicOnVerify(t *testing.T) {
	stored, stats := encode(t, testData(1000), newKey(t).Recipient())
	dec, err := NewDecoder(bytes.NewReader(stored))
	if err == nil {
		t.Fatal("expected an error without identities")
	}
	if err := dec.VerifyStored(stats.StoredSHA256); err != nil {
		t.Fatalf("VerifyStored = %v, want the checksum to match", err)
	}
}
