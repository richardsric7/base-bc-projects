// Package cryptoutil groups the cryptographic primitives the wallet backend
// needs outside of the on-chain transaction-signing path itself:
// deterministic key derivation for server-controlled signer roles,
// symmetric encryption for data at rest, and password hashing for
// non-wallet logins (e.g. an admin panel).
//
// Note on naming: the upstream project this template is derived from called
// this package "blockchainalgofuncs" and it was widely misread as
// "Algorand functions" - it has nothing to do with Algorand. "cryptoutil" is
// deliberately unambiguous.
package cryptoutil

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/crypto/bcrypt"
)

// DeriveKey deterministically derives a secp256k1 private key from
// arbitrary seed material (e.g. "<server salt>|<role>|<user id>"). Use this
// for server-controlled signer roles (a fee-sponsor account, an escrow
// signer, etc.) - never for a user's own wallet key, which must be
// generated client-side and never leave the client.
//
// Unlike ed25519 (where any 32-byte seed is a valid key), a secp256k1
// private key must be a scalar in [1, N-1] for the curve order N; a plain
// SHA-256 digest lands outside that range with negligible but nonzero
// probability. This uses "try-and-increment": hash the seed material with
// an appended counter and retry on the rare invalid output, rather than
// silently accepting whatever SHA-256 produces the way a naive port of the
// original ed25519 code would. For a production deployment that needs
// standard hardware-wallet-compatible derivation paths (m/44'/60'/...),
// upgrade to full BIP-32 HD derivation instead - this function only
// guarantees a valid, deterministic key, not a standard derivation path.
func DeriveKey(seedMaterial string) (*ecdsa.PrivateKey, error) {
	for counter := uint32(0); counter < 256; counter++ {
		var counterBytes [4]byte
		binary.BigEndian.PutUint32(counterBytes[:], counter)
		digest := sha256.Sum256(append([]byte(seedMaterial), counterBytes[:]...))
		key, err := crypto.ToECDSA(digest[:])
		if err == nil {
			return key, nil
		}
	}
	return nil, errors.New("cryptoutil: failed to derive a valid secp256k1 key after 256 attempts")
}

// VerifyPersonalSign reports whether signature is a valid EIP-191
// "personal_sign" signature of message by expectedAddress. Use this for
// off-chain approval flows (see PLAN.md §2's shared-access row) where a
// member proves authorization by signing a description of an action rather
// than a transaction itself. signature is the raw 65-byte
// r||s||v signature most wallet libraries produce (v as 27/28 or 0/1 -
// both are normalized).
func VerifyPersonalSign(message string, signature []byte, expectedAddress common.Address) (bool, error) {
	if len(signature) != 65 {
		return false, fmt.Errorf("signature must be 65 bytes, got %d", len(signature))
	}
	sig := make([]byte, 65)
	copy(sig, signature)
	if sig[64] >= 27 {
		sig[64] -= 27
	}

	hash := accounts.TextHash([]byte(message))
	pubKey, err := crypto.SigToPub(hash, sig)
	if err != nil {
		return false, fmt.Errorf("recover public key: %w", err)
	}
	return crypto.PubkeyToAddress(*pubKey) == expectedAddress, nil
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
		return nil, fmt.Errorf("ciphertext too short")
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
