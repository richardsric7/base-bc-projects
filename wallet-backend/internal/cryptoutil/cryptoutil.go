// Package cryptoutil groups the cryptographic primitives the wallet backend
// needs outside of the Stellar transaction-signing path itself: deterministic
// keypair derivation for server-controlled signer roles, symmetric
// encryption for data at rest, and password hashing for non-wallet logins
// (e.g. an admin panel).
//
// Note on naming: the upstream project this template is derived from called
// this package "blockchainalgofuncs" and it was widely misread as
// "Algorand functions" - it has nothing to do with Algorand. "cryptoutil" is
// deliberately unambiguous.
package cryptoutil

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	"github.com/stellar/go-stellar-sdk/keypair"
	"golang.org/x/crypto/bcrypt"
)

// DeriveKeypair deterministically derives a Stellar keypair from arbitrary
// seed material (e.g. "<server salt>|<role>|<user id>"). Use this for
// server-controlled signer roles (fee sponsor accounts, an escrow signer,
// etc.) - never for a user's own wallet key, which must be generated
// client-side and never leave the client.
func DeriveKeypair(seedMaterial string) (*keypair.Full, error) {
	seed := sha256.Sum256([]byte(seedMaterial))
	return keypair.FromRawSeed(seed)
}

// HashSHA256Hex returns the hex-encoded SHA-256 digest of s.
func HashSHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// HashPassword bcrypt-hashes a plaintext password for storage.
func HashPassword(password string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hashed), err
}

// CheckPasswordHash reports whether password matches the given bcrypt hash.
func CheckPasswordHash(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Encrypt AES-256-GCM encrypts plaintext with key (must be 32 bytes),
// returning nonce||ciphertext.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt.
func Decrypt(key, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, data := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, data, nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
