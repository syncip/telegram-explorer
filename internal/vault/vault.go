// Package vault holds all cryptography of the application.
//
// Key hierarchy:
//
//	passphrase --Argon2id--> KEK --wraps--> master key --wraps--> per-file key
//
// File contents are split into segments of exactly 1 MiB ciphertext, because
// that is also the largest window Telegram serves in a single request. A random
// range of a file can therefore be decrypted with a single round trip.
package vault

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// SegmentCipher is the size of one encrypted segment.
	SegmentCipher = 1 << 20
	// Overhead is the AEAD tag added to every segment.
	Overhead = chacha20poly1305.Overhead
	// SegmentPlain is the amount of plaintext in one encrypted segment.
	SegmentPlain = SegmentCipher - Overhead
)

// ErrWrongPassphrase is returned when the passphrase cannot unwrap the master key.
var ErrWrongPassphrase = errors.New("falsche Passphrase")

// Header is the public, non-secret part of a vault.
type Header struct {
	Salt    []byte `json:"salt"`
	Time    uint32 `json:"time"`
	MemKiB  uint32 `json:"memKiB"`
	Threads uint8  `json:"threads"`
	Wrapped []byte `json:"wrapped"`
}

// Vault is an unlocked master key.
type Vault struct {
	aead cipher.AEAD
	key  []byte
}

// Create generates a new master key protected by passphrase.
func Create(passphrase string) (*Vault, []byte, error) {
	master := make([]byte, chacha20poly1305.KeySize)
	if _, err := rand.Read(master); err != nil {
		return nil, nil, err
	}
	v, err := fromKey(master)
	if err != nil {
		return nil, nil, err
	}
	header, err := v.Header(passphrase)
	if err != nil {
		return nil, nil, err
	}
	return v, header, nil
}

// Header wraps the master key with a freshly salted KEK. It is also used to
// change the passphrase, which does not touch any file.
func (v *Vault) Header(passphrase string) ([]byte, error) {
	h := Header{Salt: make([]byte, 16), Time: 3, MemKiB: 128 * 1024, Threads: 2}
	if _, err := rand.Read(h.Salt); err != nil {
		return nil, err
	}
	kek, err := fromKey(h.kek(passphrase))
	if err != nil {
		return nil, err
	}
	h.Wrapped, err = kek.Seal("master", v.key)
	if err != nil {
		return nil, err
	}
	return json.Marshal(h)
}

// Unlock opens a vault header with the passphrase.
func Unlock(header []byte, passphrase string) (*Vault, error) {
	var h Header
	if err := json.Unmarshal(header, &h); err != nil {
		return nil, fmt.Errorf("vault header: %w", err)
	}
	kek, err := fromKey(h.kek(passphrase))
	if err != nil {
		return nil, err
	}
	master, err := kek.Open("master", h.Wrapped)
	if err != nil {
		return nil, ErrWrongPassphrase
	}
	return fromKey(master)
}

func (h Header) kek(passphrase string) []byte {
	return argon2.IDKey([]byte(passphrase), h.Salt, h.Time, h.MemKiB, h.Threads, chacha20poly1305.KeySize)
}

func fromKey(key []byte) (*Vault, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead, key: key}, nil
}

// Seal encrypts small values. The purpose is authenticated, so a value sealed
// for one purpose cannot be swapped in for another.
func (v *Vault) Seal(purpose string, plain []byte) ([]byte, error) {
	nonce := make([]byte, v.aead.NonceSize(), v.aead.NonceSize()+len(plain)+Overhead)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return v.aead.Seal(nonce, nonce, plain, []byte(purpose)), nil
}

// Open reverses Seal.
func (v *Vault) Open(purpose string, sealed []byte) ([]byte, error) {
	if len(sealed) < v.aead.NonceSize() {
		return nil, errors.New("sealed value too short")
	}
	nonce, body := sealed[:v.aead.NonceSize()], sealed[v.aead.NonceSize():]
	return v.aead.Open(nil, nonce, body, []byte(purpose))
}

// NewFileKey returns a random file key together with its wrapped form.
func (v *Vault) NewFileKey() (wrapped []byte, err error) {
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return v.Seal("file", key)
}

// FileCipher encrypts the segments of a single file. The file key is unique, so
// the segment number is a safe nonce. Binding the total size stops truncation
// and appending; the segment number stops reordering, even across blocks.
type FileCipher struct {
	aead cipher.AEAD
	ad   [8]byte
}

// FileCipher unwraps a file key.
func (v *Vault) FileCipher(wrapped []byte, plainSize int64) (*FileCipher, error) {
	key, err := v.Open("file", wrapped)
	if err != nil {
		return nil, fmt.Errorf("file key: %w", err)
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	fc := &FileCipher{aead: aead}
	binary.BigEndian.PutUint64(fc.ad[:], uint64(plainSize))
	return fc, nil
}

func nonce(segment int64) []byte {
	n := make([]byte, chacha20poly1305.NonceSize)
	binary.BigEndian.PutUint64(n, uint64(segment))
	return n
}

// SealSegment appends the encrypted segment to dst.
func (c *FileCipher) SealSegment(dst []byte, segment int64, plain []byte) []byte {
	return c.aead.Seal(dst, nonce(segment), plain, c.ad[:])
}

// OpenSegment decrypts and authenticates one segment.
func (c *FileCipher) OpenSegment(segment int64, sealed []byte) ([]byte, error) {
	plain, err := c.aead.Open(nil, nonce(segment), sealed, c.ad[:])
	if err != nil {
		return nil, fmt.Errorf("segment %d: Daten beschädigt oder manipuliert", segment)
	}
	return plain, nil
}

const backupMagic = "TELEXBK1"

// SealBackup encrypts a database snapshot. The vault header is embedded, so a
// backup can be restored with nothing but the passphrase.
func (v *Vault) SealBackup(header, db []byte) ([]byte, error) {
	sealed, err := v.Seal("backup", db)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(backupMagic)+4+len(header)+len(sealed))
	out = append(out, backupMagic...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(header)))
	out = append(out, header...)
	return append(out, sealed...), nil
}

// OpenBackup decrypts a backup with the passphrase.
func OpenBackup(backup []byte, passphrase string) ([]byte, error) {
	if len(backup) < len(backupMagic)+4 || string(backup[:len(backupMagic)]) != backupMagic {
		return nil, errors.New("keine gültige Backup-Datei")
	}
	rest := backup[len(backupMagic):]
	n := binary.BigEndian.Uint32(rest)
	rest = rest[4:]
	if uint64(n) > uint64(len(rest)) {
		return nil, errors.New("Backup-Datei ist beschädigt")
	}
	v, err := Unlock(rest[:n], passphrase)
	if err != nil {
		return nil, err
	}
	return v.Open("backup", rest[n:])
}
