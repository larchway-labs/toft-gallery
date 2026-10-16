package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// At-rest protection for the estate's push credentials.
//
// Each target's deploy key is encrypted under TARGET_KEY_SECRET before the row
// is written. What that defends against is a database dump — a backup file, a
// replica, a misconfigured grant — which is a real exposure surface precisely
// because the database belongs to the operator rather than to this app. What it
// does **not** defend against is a compromise of this running process, which
// holds the secret in its own environment by construction. The asymmetry is the
// whole value of the mechanism and it is stated here rather than implied
// somewhere by its absence.
//
// AES-256-GCM with a random nonce per record, the key derived from the secret
// with SHA-256 so an operator may paste a passphrase rather than 32 exact
// bytes. Every ciphertext carries its version, which is what lets a second
// scheme be added later without a reader having to guess which one wrote a row.
const cipherVersion = "v1"

type sealer struct{ aead cipher.AEAD }

func newSealer(secret string) (*sealer, error) {
	if secret == "" {
		return nil, errors.New("no secret")
	}
	sum := sha256.Sum256([]byte("toft-migration-console\x00" + secret))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sealer{aead: aead}, nil
}

func (s *sealer) seal(plain string) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := s.aead.Seal(nil, nonce, []byte(plain), []byte(cipherVersion))
	return cipherVersion + "." +
		base64.RawURLEncoding.EncodeToString(nonce) + "." +
		base64.RawURLEncoding.EncodeToString(ct), nil
}

func (s *sealer) open(record string) (string, error) {
	parts := splitN(record, '.', 3)
	if len(parts) != 3 || parts[0] != cipherVersion {
		return "", fmt.Errorf("this record was not written by %s of this app", cipherVersion)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	ct, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", err
	}
	plain, err := s.aead.Open(nil, nonce, ct, []byte(cipherVersion))
	if err != nil {
		// Named rather than swallowed: the overwhelmingly likely cause is that
		// TARGET_KEY_SECRET was changed, and an administrator who reads "the
		// key could not be decrypted" fixes it in a minute where "push failed"
		// costs an afternoon.
		return "", errors.New("this target's key could not be decrypted — TARGET_KEY_SECRET is not the one it was written with")
	}
	return string(plain), nil
}

func splitN(s string, sep byte, n int) []string {
	out := make([]string, 0, n)
	start := 0
	for i := 0; i < len(s) && len(out) < n-1; i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
