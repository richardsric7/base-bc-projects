package services

import (
	"context"
	"testing"

	"wallet-backend/internal/components/tokenization/models"
)

func TestDeployInternalBalanceAsset_DeploysAndRecordsContract(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, db := newTestService(t, blockchain)
	seedCountryAndCurrencies(t, db)

	cfg, err := svc.DeployInternalBalanceAsset(context.Background(), "NG", "Internal NGN Balance", "iNGN", 6)
	if err != nil {
		t.Fatalf("DeployInternalBalanceAsset: %v", err)
	}
	if cfg.InternalBalanceContractAddress == nil {
		t.Fatal("expected InternalBalanceContractAddress to be set")
	}
	if blockchain.deployCount != 1 {
		t.Fatalf("expected exactly 1 contract deployment, got %d", blockchain.deployCount)
	}

	var stored models.TokenizationCountryConfig
	if err := db.Where("country_code = ?", "NG").First(&stored).Error; err != nil {
		t.Fatalf("reload country config: %v", err)
	}
	if stored.InternalBalanceContractAddress == nil || *stored.InternalBalanceContractAddress != *cfg.InternalBalanceContractAddress {
		t.Fatal("expected the deployed contract address to be persisted")
	}
}

func TestDeployInternalBalanceAsset_IdempotentOnceDeployed(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, db := newTestService(t, blockchain)
	seedCountryAndCurrencies(t, db)

	first, err := svc.DeployInternalBalanceAsset(context.Background(), "NG", "Internal NGN Balance", "iNGN", 6)
	if err != nil {
		t.Fatalf("first deploy: %v", err)
	}
	second, err := svc.DeployInternalBalanceAsset(context.Background(), "NG", "Internal NGN Balance", "iNGN", 6)
	if err != nil {
		t.Fatalf("second deploy: %v", err)
	}
	if blockchain.deployCount != 1 {
		t.Fatalf("expected the second call to be a no-op, but deployCount is %d", blockchain.deployCount)
	}
	if *first.InternalBalanceContractAddress != *second.InternalBalanceContractAddress {
		t.Fatal("expected the same contract address to be returned")
	}
}

func TestDeployInternalBalanceAsset_UnknownCountry(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)

	if _, err := svc.DeployInternalBalanceAsset(context.Background(), "ZZ", "Internal Balance", "iZZ", 6); err == nil {
		t.Fatal("expected an error for a country with no tokenization config")
	}
}

func TestAuthorizeInternalBalanceHolder_NoOpWhenNotDeployed(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, db := newTestService(t, blockchain)
	seedCountryAndCurrencies(t, db)

	if err := svc.AuthorizeInternalBalanceHolder(context.Background(), "NG", "0xholder0000000000000000000000000000000000"); err != nil {
		t.Fatalf("expected no error for an undeployed internal balance asset, got %v", err)
	}
	if len(blockchain.signedData) != 0 {
		t.Fatal("expected no on-chain call when no internal balance asset is deployed")
	}
}

func TestAuthorizeInternalBalanceHolder_SendsAuthorizeCall(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, db := newTestService(t, blockchain)
	seedCountryAndCurrencies(t, db)
	if _, err := svc.DeployInternalBalanceAsset(context.Background(), "NG", "Internal NGN Balance", "iNGN", 6); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	holder := "0xholder0000000000000000000000000000000000"
	if err := svc.AuthorizeInternalBalanceHolder(context.Background(), "NG", holder); err != nil {
		t.Fatalf("AuthorizeInternalBalanceHolder: %v", err)
	}
	if len(blockchain.signedData) != 1 {
		t.Fatalf("expected exactly 1 authorize call, got %d", len(blockchain.signedData))
	}
	if !authorizeCallFor(t, blockchain, holder) {
		t.Fatal("expected an authorize(holder) call to have been sent")
	}
}

func TestDeauthorizeInternalBalanceHolder_RequiresDeployedAsset(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	seedCountryAndCurrencies(t, db)

	err := svc.DeauthorizeInternalBalanceHolder(context.Background(), "NG", "0xholder0000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected an error deauthorizing against an undeployed internal balance asset")
	}
	if status := appErrStatus(t, err); status != 409 {
		t.Fatalf("expected 409 Conflict, got %d", status)
	}
}

func TestDeauthorizeInternalBalanceHolder_SendsDeauthorizeCall(t *testing.T) {
	blockchain := &fakeBlockchain{}
	svc, db := newTestService(t, blockchain)
	seedCountryAndCurrencies(t, db)
	if _, err := svc.DeployInternalBalanceAsset(context.Background(), "NG", "Internal NGN Balance", "iNGN", 6); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	holder := "0xholder0000000000000000000000000000000000"
	if err := svc.DeauthorizeInternalBalanceHolder(context.Background(), "NG", holder); err != nil {
		t.Fatalf("DeauthorizeInternalBalanceHolder: %v", err)
	}
	if len(blockchain.signedData) != 1 {
		t.Fatalf("expected exactly 1 deauthorize call, got %d", len(blockchain.signedData))
	}
}
