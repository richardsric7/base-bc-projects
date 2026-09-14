package middleware

import (
	"crypto/ecdsa"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	usersModels "wallet-backend/internal/components/users/models"
)

func newSignatureAuthTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(sharedaccessModels.Models...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.AutoMigrate(usersModels.Models...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// signRequest signs method+path+signer+timestamp the same way
// SignatureAuth verifies it, returning the 0x-prefixed hex signature.
func signRequest(t *testing.T, key *ecdsa.PrivateKey, path, signer string, timestamp int64) string {
	t.Helper()
	message := path + signer + fmt.Sprintf("%d", timestamp)
	hash := accounts.TextHash([]byte(message))
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sig[64] += 27
	return "0x" + common.Bytes2Hex(sig)
}

func doSignedRequest(db *gorm.DB, signerAddr, walletAddr, signatureHex string, timestamp int64) *httptest.ResponseRecorder {
	router := gin.New()
	router.GET("/protected", SignatureAuth(db, 300), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"subject": c.MustGet(CtxSubject),
			"signer":  c.MustGet(CtxSigner),
		})
	})
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if signerAddr != "" {
		req.Header.Set(headerSignerAddress, signerAddr)
	}
	if walletAddr != "" {
		req.Header.Set(headerWalletAddress, walletAddr)
	}
	if signatureHex != "" {
		req.Header.Set(headerSignature, signatureHex)
	}
	if timestamp != 0 {
		req.Header.Set(headerTimestamp, fmt.Sprintf("%d", timestamp))
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestSignatureAuth_MissingHeaders(t *testing.T) {
	db := newSignatureAuthTestDB(t)
	rec := doSignedRequest(db, "", "", "", 0)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestSignatureAuth_SelfServiceAcceptsValidSignature(t *testing.T) {
	db := newSignatureAuthTestDB(t)
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	ts := time.Now().Unix()
	sig := signRequest(t, key, "/protected", addr, ts)

	rec := doSignedRequest(db, addr, addr, sig, ts)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSignatureAuth_RejectsTamperedSignature(t *testing.T) {
	db := newSignatureAuthTestDB(t)
	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	ts := time.Now().Unix()
	sig := signRequest(t, key, "/protected", addr, ts)
	// Flip a hex character to invalidate the signature without changing its length.
	tampered := sig[:len(sig)-1] + "0"
	if tampered == sig {
		tampered = sig[:len(sig)-1] + "1"
	}

	rec := doSignedRequest(db, addr, addr, tampered, ts)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a tampered signature, got %d", rec.Code)
	}
}

func TestSignatureAuth_RejectsSignatureFromWrongKey(t *testing.T) {
	db := newSignatureAuthTestDB(t)
	key, _ := crypto.GenerateKey()
	otherKey, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	ts := time.Now().Unix()
	// Signed by a different key than the one named in X-Signer-Address.
	sig := signRequest(t, otherKey, "/protected", addr, ts)

	rec := doSignedRequest(db, addr, addr, sig, ts)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when the signature doesn't match the claimed signer, got %d", rec.Code)
	}
}

func TestSignatureAuth_RejectsStaleTimestamp(t *testing.T) {
	db := newSignatureAuthTestDB(t)
	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	ts := time.Now().Add(-10 * time.Minute).Unix() // well outside the 300s tolerance
	sig := signRequest(t, key, "/protected", addr, ts)

	rec := doSignedRequest(db, addr, addr, sig, ts)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a stale timestamp, got %d", rec.Code)
	}
}

func TestSignatureAuth_RejectsFutureTimestampBeyondTolerance(t *testing.T) {
	db := newSignatureAuthTestDB(t)
	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	ts := time.Now().Add(10 * time.Minute).Unix()
	sig := signRequest(t, key, "/protected", addr, ts)

	rec := doSignedRequest(db, addr, addr, sig, ts)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a future timestamp beyond tolerance, got %d", rec.Code)
	}
}

func TestSignatureAuth_DelegatedSignerWithGroupMembershipIsAuthorized(t *testing.T) {
	db := newSignatureAuthTestDB(t)
	signerKey, _ := crypto.GenerateKey()
	signerAddr := crypto.PubkeyToAddress(signerKey.PublicKey).Hex()

	walletKey, _ := crypto.GenerateKey()
	walletAddr := crypto.PubkeyToAddress(walletKey.PublicKey).Hex()
	group := sharedaccessModels.ClosedGroup{Name: "shared wallet", Address: &walletAddr, Threshold: 1}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("seed group: %v", err)
	}
	member := sharedaccessModels.GroupMember{GroupID: group.ID, MemberAddress: signerAddr, Role: sharedaccessModels.RoleApprover}
	if err := db.Create(&member).Error; err != nil {
		t.Fatalf("seed member: %v", err)
	}

	ts := time.Now().Unix()
	sig := signRequest(t, signerKey, "/protected", signerAddr, ts)
	rec := doSignedRequest(db, signerAddr, walletAddr, sig, ts)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a signer with group standing on the wallet, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSignatureAuth_SignerOwningPrimaryWalletIsAuthorized(t *testing.T) {
	db := newSignatureAuthTestDB(t)
	signerKey, _ := crypto.GenerateKey()
	signerAddr := crypto.PubkeyToAddress(signerKey.PublicKey).Hex()

	walletKey, _ := crypto.GenerateKey()
	walletAddr := crypto.PubkeyToAddress(walletKey.PublicKey).Hex() // a Safe address, distinct from signerAddr
	user := usersModels.User{Username: "alice", Email: "alice@example.com", Address: walletAddr, SignerAddress: signerAddr}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	ts := time.Now().Unix()
	sig := signRequest(t, signerKey, "/protected", signerAddr, ts)
	rec := doSignedRequest(db, signerAddr, walletAddr, sig, ts)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for a signer who owns the named primary wallet, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSignatureAuth_RejectsSignerWithNoStandingOnWallet(t *testing.T) {
	db := newSignatureAuthTestDB(t)
	signerKey, _ := crypto.GenerateKey()
	signerAddr := crypto.PubkeyToAddress(signerKey.PublicKey).Hex()
	unownedWalletKey, _ := crypto.GenerateKey()
	walletAddr := crypto.PubkeyToAddress(unownedWalletKey.PublicKey).Hex() // no group, no membership

	ts := time.Now().Unix()
	sig := signRequest(t, signerKey, "/protected", signerAddr, ts)
	rec := doSignedRequest(db, signerAddr, walletAddr, sig, ts)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a signer with no standing on the wallet, got %d", rec.Code)
	}
}
