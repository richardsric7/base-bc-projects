package cryptoutil

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

func TestDeriveKey_Deterministic(t *testing.T) {
	key1, err := DeriveKey("server-salt|recovery|user-42")
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	key2, err := DeriveKey("server-salt|recovery|user-42")
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	if !bytes.Equal(crypto.FromECDSA(key1), crypto.FromECDSA(key2)) {
		t.Fatal("expected DeriveKey to be deterministic for the same seed material")
	}

	addr1 := crypto.PubkeyToAddress(key1.PublicKey)
	addr2 := crypto.PubkeyToAddress(key2.PublicKey)
	if addr1 != addr2 {
		t.Fatalf("derived addresses differ: %s vs %s", addr1.Hex(), addr2.Hex())
	}
}

func TestDeriveKey_DifferentSeedsDiffer(t *testing.T) {
	key1, err := DeriveKey("role-a")
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	key2, err := DeriveKey("role-b")
	if err != nil {
		t.Fatalf("DeriveKey returned error: %v", err)
	}
	if bytes.Equal(crypto.FromECDSA(key1), crypto.FromECDSA(key2)) {
		t.Fatal("expected different seed material to derive different keys")
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes
	plaintext := []byte("a message worth encrypting")

	ciphertext, err := Encrypt(key[:32], plaintext)
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}
	decrypted, err := Decrypt(key[:32], ciphertext)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}
	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("round trip mismatch: got %q, want %q", decrypted, plaintext)
	}
}

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if !CheckPasswordHash("correct horse battery staple", hash) {
		t.Fatal("expected the correct password to verify")
	}
	if CheckPasswordHash("wrong password", hash) {
		t.Fatal("expected an incorrect password not to verify")
	}
}
