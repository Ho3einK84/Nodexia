package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/scrypt"
)

// Optional passphrase encryption wraps the plaintext backup JSON in an
// AES-256-GCM ciphertext, with the key derived from the passphrase via scrypt.
// This is the safe way to carry a secrets-bearing backup: the file is useless
// without the passphrase, and GCM authentication detects tampering or a wrong
// passphrase on decrypt. x/crypto is already a pinned dependency, so this adds
// no new module.
const (
	kdfScrypt = "scrypt"
	// scrypt cost parameters. N must be a power of two; these are the widely
	// used interactive-login defaults (~16 MB, tens of ms).
	scryptN      = 1 << 15
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32 // AES-256
	saltLen      = 16
)

// encryptedEnvelope is the on-disk shape of a passphrase-encrypted backup.
type encryptedEnvelope struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	N          int    `json:"n"`
	R          int    `json:"r"`
	P          int    `json:"p"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// marshalArchive serializes an Archive to indented JSON, encrypting it when a
// passphrase is supplied.
func marshalArchive(archive Archive, passphrase string) ([]byte, error) {
	plaintext, err := json.MarshalIndent(archive, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("backup: encode archive: %w", err)
	}
	if passphrase == "" {
		return plaintext, nil
	}
	return seal(plaintext, passphrase)
}

// unmarshalArchive decodes plaintext backup JSON into an Archive.
func unmarshalArchive(plaintext []byte) (Archive, error) {
	var archive Archive
	if err := json.Unmarshal(plaintext, &archive); err != nil {
		return Archive{}, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return archive, nil
}

// decodeMaybeEncrypted returns the plaintext backup JSON from raw bytes that may
// be either a plain or an encrypted document. It inspects the format marker to
// decide which path to take.
func decodeMaybeEncrypted(raw []byte, passphrase string) ([]byte, error) {
	format, err := peekFormat(raw)
	if err != nil {
		return nil, err
	}
	switch format {
	case FormatEncrypted:
		if passphrase == "" {
			return nil, ErrPassphraseRequired
		}
		return open(raw, passphrase)
	case FormatPlain:
		return raw, nil
	default:
		return nil, ErrBadFormat
	}
}

// peekFormat reads just the "format" field to classify a document without fully
// decoding it.
func peekFormat(raw []byte) (string, error) {
	var probe struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if probe.Format == "" {
		return "", ErrBadFormat
	}
	return probe.Format, nil
}

// seal encrypts plaintext under a passphrase and returns the encrypted envelope
// as indented JSON.
func seal(plaintext []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("backup: read salt: %w", err)
	}
	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return nil, fmt.Errorf("backup: derive key: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("backup: read nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	envelope := encryptedEnvelope{
		Format:     FormatEncrypted,
		Version:    SchemaVersion,
		KDF:        kdfScrypt,
		N:          scryptN,
		R:          scryptR,
		P:          scryptP,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	out, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("backup: encode envelope: %w", err)
	}
	return out, nil
}

// open decrypts an encrypted envelope with the passphrase, returning the inner
// plaintext JSON. A wrong passphrase or tampered file surfaces as
// ErrWrongPassphrase.
func open(raw []byte, passphrase string) ([]byte, error) {
	var envelope encryptedEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if envelope.KDF != kdfScrypt {
		return nil, fmt.Errorf("%w: unknown kdf %q", ErrBadFormat, envelope.KDF)
	}
	if envelope.Version != SchemaVersion {
		return nil, fmt.Errorf("%w: found %d", ErrUnsupportedVersion, envelope.Version)
	}
	salt, err := base64.StdEncoding.DecodeString(envelope.Salt)
	if err != nil {
		return nil, fmt.Errorf("%w: salt: %v", ErrMalformed, err)
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, fmt.Errorf("%w: nonce: %v", ErrMalformed, err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("%w: ciphertext: %v", ErrMalformed, err)
	}
	n, r, p := envelope.N, envelope.R, envelope.P
	if n == 0 {
		n, r, p = scryptN, scryptR, scryptP
	}
	if err := validateScryptParams(n, r, p); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	key, err := scrypt.Key([]byte(passphrase), salt, n, r, p, scryptKeyLen)
	if err != nil {
		return nil, fmt.Errorf("backup: derive key: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, ErrMalformed
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// GCM authentication failure means a wrong passphrase or a corrupt file.
		return nil, ErrWrongPassphrase
	}
	if !bytes.HasPrefix(bytes.TrimSpace(plaintext), []byte("{")) {
		return nil, ErrMalformed
	}
	return plaintext, nil
}

// validateScryptParams rejects scrypt cost parameters that could exhaust
// memory or CPU when decrypting a crafted backup envelope.
func validateScryptParams(n, r, p int) error {
	if n <= 0 || (n&(n-1)) != 0 {
		return errors.New("scrypt N must be a power of 2")
	}
	if n > 1<<20 {
		return errors.New("scrypt N is too large")
	}
	if r < 1 || r > 32 {
		return errors.New("scrypt r must be between 1 and 32")
	}
	if p < 1 || p > 32 {
		return errors.New("scrypt p must be between 1 and 32")
	}
	return nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("backup: init cipher")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("backup: init gcm")
	}
	return gcm, nil
}
