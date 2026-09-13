package cryptoutil

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
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

func TestVerifyPersonalSign(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	address := crypto.PubkeyToAddress(key.PublicKey)
	message := "approve shared-access action #42"

	hash := accounts.TextHash([]byte(message))
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	ok, err := VerifyPersonalSign(message, sig, address)
	if err != nil {
		t.Fatalf("VerifyPersonalSign returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected a valid signature to verify")
	}

	// A signature with V normalized to 27/28 (the format most wallet
	// libraries actually produce) should verify identically.
	sig27 := append([]byte{}, sig...)
	sig27[64] += 27
	ok27, err := VerifyPersonalSign(message, sig27, address)
	if err != nil {
		t.Fatalf("VerifyPersonalSign (v=27 form) returned error: %v", err)
	}
	if !ok27 {
		t.Fatal("expected a v=27-normalized signature to verify")
	}

	otherKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	wrongAddress := crypto.PubkeyToAddress(otherKey.PublicKey)
	ok, err = VerifyPersonalSign(message, sig, wrongAddress)
	if err != nil {
		t.Fatalf("VerifyPersonalSign returned error: %v", err)
	}
	if ok {
		t.Fatal("expected the signature not to verify against a different address")
	}

	ok, err = VerifyPersonalSign("a different message", sig, address)
	if err != nil {
		t.Fatalf("VerifyPersonalSign returned error: %v", err)
	}
	if ok {
		t.Fatal("expected the signature not to verify against a different message")
	}
}
