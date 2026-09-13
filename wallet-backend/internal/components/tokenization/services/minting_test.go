package services

import (
	"context"
	"crypto/ecdsa"
	"net/http"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"wallet-backend/internal/components/tokenization/models"
)

func randomAddress(t *testing.T) (string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return crypto.PubkeyToAddress(key.PublicKey).Hex(), key
}

func sign(t *testing.T, key *ecdsa.PrivateKey, message string) string {
	t.Helper()
	hash := accounts.TextHash([]byte(message))
	sig, err := crypto.Sign(hash, key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return "0x" + common.Bytes2Hex(sig)
}

func TestRequestMint_RequiresGlobalAuthorization(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	user := createTestUser(t, db, "alice", true)
	asset, err := svc.SubmitApplication(user.ID, validDraftAsset())
	if err != nil {
		t.Fatalf("SubmitApplication returned error: %v", err)
	}
	asset.Status = models.StatusFeeAcknowledged
	if err := db.Save(asset).Error; err != nil {
		t.Fatalf("advance status: %v", err)
	}

	_, err = svc.RequestMint(user.ID, asset.ID)
	if err == nil {
		t.Fatal("expected an error for a caller not on the global minting allow-list")
	}
	if status := appErrStatus(t, err); status != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", status)
	}
}

func TestRequestMint_RequiresFourApprovers(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	user := createTestUser(t, db, "alice", true)
	if err := db.Create(&models.TokenizationMintingInitiator{UserID: user.ID}).Error; err != nil {
		t.Fatalf("seed minting initiator: %v", err)
	}
	asset, err := svc.SubmitApplication(user.ID, validDraftAsset())
	if err != nil {
		t.Fatalf("SubmitApplication returned error: %v", err)
	}
	asset.Status = models.StatusFeeAcknowledged
	a1, _ := randomAddress(t)
	a2, _ := randomAddress(t)
	asset.MintingApprovers = a1 + "," + a2
	if err := db.Save(asset).Error; err != nil {
		t.Fatalf("advance status: %v", err)
	}

	_, err = svc.RequestMint(user.ID, asset.ID)
	if err == nil {
		t.Fatal("expected an error for fewer than 4 approvers")
	}
	if status := appErrStatus(t, err); status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", status)
	}
}

func TestMintFlow_ExecutesOnceThresholdReached(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, db := newTestService(t, blockchain)
	seedCountryAndCurrencies(t, db)
	user := createTestUser(t, db, "alice", true)
	if err := db.Create(&models.TokenizationMintingInitiator{UserID: user.ID}).Error; err != nil {
		t.Fatalf("seed minting initiator: %v", err)
	}
	asset, err := svc.SubmitApplication(user.ID, validDraftAsset())
	if err != nil {
		t.Fatalf("SubmitApplication returned error: %v", err)
	}

	addr1, key1 := randomAddress(t)
	addr2, key2 := randomAddress(t)
	addr3, _ := randomAddress(t)
	addr4, _ := randomAddress(t)
	asset.Status = models.StatusFeeAcknowledged
	asset.MintingApprovers = addr1 + "," + addr2 + "," + addr3 + "," + addr4
	if err := db.Save(asset).Error; err != nil {
		t.Fatalf("advance status: %v", err)
	}

	approval, err := svc.RequestMint(user.ID, asset.ID)
	if err != nil {
		t.Fatalf("RequestMint returned error: %v", err)
	}
	if approval.RequiredApprovals != 2 {
		t.Fatalf("expected RequiredApprovals=2 (4-2), got %d", approval.RequiredApprovals)
	}

	message, err := svc.CanonicalMintMessage(approval.ID)
	if err != nil {
		t.Fatalf("CanonicalMintMessage returned error: %v", err)
	}

	ctx := context.Background()
	if _, err := svc.SignMintApproval(ctx, approval.ID, addr1, sign(t, key1, message)); err != nil {
		t.Fatalf("first signoff returned error: %v", err)
	}
	updated, err := svc.SignMintApproval(ctx, approval.ID, addr2, sign(t, key2, message))
	if err != nil {
		t.Fatalf("second signoff returned error: %v", err)
	}
	if updated.Status != models.MintApprovalExecuted {
		t.Fatalf("expected mint to execute once threshold reached, got status %s", updated.Status)
	}

	var mintedAsset models.TokenizedAsset
	if err := db.First(&mintedAsset, asset.ID).Error; err != nil {
		t.Fatalf("reload asset: %v", err)
	}
	if mintedAsset.Status != models.StatusMinted {
		t.Fatalf("expected asset status MINTED, got %s", mintedAsset.Status)
	}
	if mintedAsset.IssuerContractAddress == nil || mintedAsset.SaleContractAddress == nil || mintedAsset.DistributionAddress == nil {
		t.Fatal("expected issuer/sale/distribution addresses to be set")
	}
	if blockchain.deployCount != 2 {
		t.Fatalf("expected 2 contract deployments (asset + sale), got %d", blockchain.deployCount)
	}

	var count int64
	db.Table("curated_tokens").Where("symbol = ?", asset.AssetCode).Count(&count)
	if count != 1 {
		t.Fatalf("expected the minted asset to be added as a curated token, got count=%d", count)
	}

	// A third, unauthorized signer must be rejected even after execution.
	_, unauthorizedKey := randomAddress(t)
	if _, err := svc.SignMintApproval(ctx, approval.ID, addr3, sign(t, unauthorizedKey, message)); err == nil {
		t.Fatal("expected an error signing an already-executed approval")
	}
}

func TestSignMintApproval_RejectsUnauthorizedSigner(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)
	user := createTestUser(t, db, "alice", true)
	if err := db.Create(&models.TokenizationMintingInitiator{UserID: user.ID}).Error; err != nil {
		t.Fatalf("seed minting initiator: %v", err)
	}
	asset, err := svc.SubmitApplication(user.ID, validDraftAsset())
	if err != nil {
		t.Fatalf("SubmitApplication returned error: %v", err)
	}
	a1, _ := randomAddress(t)
	a2, _ := randomAddress(t)
	a3, _ := randomAddress(t)
	a4, _ := randomAddress(t)
	asset.Status = models.StatusFeeAcknowledged
	asset.MintingApprovers = a1 + "," + a2 + "," + a3 + "," + a4
	if err := db.Save(asset).Error; err != nil {
		t.Fatalf("advance status: %v", err)
	}
	approval, err := svc.RequestMint(user.ID, asset.ID)
	if err != nil {
		t.Fatalf("RequestMint returned error: %v", err)
	}

	impostorAddr, impostorKey := randomAddress(t)
	message, _ := svc.CanonicalMintMessage(approval.ID)
	_, err = svc.SignMintApproval(context.Background(), approval.ID, impostorAddr, sign(t, impostorKey, message))
	if err == nil {
		t.Fatal("expected an error for a signer not on the asset's MintingApprovers list")
	}
	if status := appErrStatus(t, err); status != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", status)
	}
}
