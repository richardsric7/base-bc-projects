package services

import (
	"context"
	"crypto/ecdsa"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/tokenization/models"
	"wallet-backend/internal/contracts"
	"wallet-backend/internal/cryptoutil"
)

// This file restores upstream's InternalBalanceTokenCode/InternalTokenIssuer
// restriction (PLAN.md §4.9's "Quote currency" note; models'
// reference_data.go doc comment) at the user's explicit request: the
// internal-balance/quote-currency asset is itself a restricted B20 asset,
// same as every tokenized asset (PLAN.md §22.1) - one deployed per
// country - and a wallet must be authorized on it before it can hold it,
// mirroring upstream's checkDistributionWalletHasQuoteCurrencyAuthorization
// gate. What's still NOT restored: Stellar's atomic multi-hop path-payment
// routing through this asset - Base's direct ERC-20 transfers make that
// unnecessary (see services.go's own package doc comment); only the
// restricted-holding requirement is restored. See PLAN.md §22.4.

func (s *Service) getCountryConfig(countryCode string) (*models.TokenizationCountryConfig, error) {
	var cfg models.TokenizationCountryConfig
	if err := s.DB.Where("country_code = ?", countryCode).First(&cfg).Error; err != nil {
		return nil, apperrors.NotFound("no tokenization config exists for this country")
	}
	return &cfg, nil
}

func (s *Service) deriveInternalBalanceIssuerKey(countryCode string) (*ecdsa.PrivateKey, error) {
	key, err := cryptoutil.DeriveKey(s.IssuerKeySalt + "|tokenization-internal-balance|" + countryCode)
	if err != nil {
		return nil, apperrors.Internal("failed to derive the internal balance asset's issuer key")
	}
	return key, nil
}

// DeployInternalBalanceAsset deploys countryCode's restricted internal-
// balance asset - idempotent: if one is already deployed for this
// country, that country config is returned unchanged rather than
// deploying a second contract. Uses the exact same TokenizedAsset.sol
// contract and deployment path tokenized assets use (TokenizedAssetDeployData),
// per the user's explicit direction that every restricted asset in this
// system, internal-balance included, is the same B20 primitive.
func (s *Service) DeployInternalBalanceAsset(ctx context.Context, countryCode, name, symbol string, decimals uint8) (*models.TokenizationCountryConfig, error) {
	cfg, err := s.getCountryConfig(countryCode)
	if err != nil {
		return nil, err
	}
	if cfg.InternalBalanceContractAddress != nil {
		return cfg, nil
	}
	issuerKey, err := s.deriveInternalBalanceIssuerKey(countryCode)
	if err != nil {
		return nil, err
	}
	issuerAddr := crypto.PubkeyToAddress(issuerKey.PublicKey).Hex()
	deployData, err := contracts.TokenizedAssetDeployData(name, symbol, decimals, issuerAddr)
	if err != nil {
		return nil, apperrors.Internal("failed to encode internal balance asset deployment")
	}
	contractAddr, _, err := s.Blockchain.DeployContract(ctx, issuerKey, deployData)
	if err != nil {
		return nil, apperrors.Internal("failed to deploy internal balance asset: " + err.Error())
	}
	cfg.InternalBalanceContractAddress = &contractAddr
	if err := s.DB.Save(cfg).Error; err != nil {
		return nil, apperrors.Internal("failed to record deployed internal balance asset")
	}
	return cfg, nil
}

// AuthorizeInternalBalanceHolder authorizes holderAddress to hold
// countryCode's internal-balance restricted asset - the same on-chain
// mechanism authorizeHolder uses for a tokenized asset
// (TokenizedAsset.authorize), scoped to the per-country contract instead
// of a per-asset one. A country with no deployed internal-balance asset
// yet carries no restriction to satisfy, so this is a no-op rather than
// an error in that case (mirrors authorizeHolder's own "not minted yet"
// posture, just inverted: here, "not deployed yet" means nothing to
// authorize against, not a hard failure).
func (s *Service) AuthorizeInternalBalanceHolder(ctx context.Context, countryCode, holderAddress string) error {
	cfg, err := s.getCountryConfig(countryCode)
	if err != nil {
		return err
	}
	if cfg.InternalBalanceContractAddress == nil {
		return nil
	}
	issuerKey, err := s.deriveInternalBalanceIssuerKey(countryCode)
	if err != nil {
		return err
	}
	data, err := contracts.EncodeAuthorize(holderAddress)
	if err != nil {
		return apperrors.Internal("failed to encode authorize call")
	}
	contractAddr := common.HexToAddress(*cfg.InternalBalanceContractAddress)
	if _, err := s.Blockchain.SignAndSubmitTx(ctx, issuerKey, &contractAddr, big.NewInt(0), data, nil); err != nil {
		return apperrors.Internal("failed to authorize wallet to hold the internal balance asset: " + err.Error())
	}
	return nil
}

// DeauthorizeInternalBalanceHolder revokes holderAddress's ability to
// further hold/send/receive countryCode's internal-balance asset (their
// existing balance is untouched - there is no seize/clawback, matching
// TokenizedAsset.sol's own deauthorize doc comment) - the "disabling it
// from being held" half of the user's request.
func (s *Service) DeauthorizeInternalBalanceHolder(ctx context.Context, countryCode, holderAddress string) error {
	cfg, err := s.getCountryConfig(countryCode)
	if err != nil {
		return err
	}
	if cfg.InternalBalanceContractAddress == nil {
		return apperrors.Conflict("this country has no internal balance asset deployed")
	}
	issuerKey, err := s.deriveInternalBalanceIssuerKey(countryCode)
	if err != nil {
		return err
	}
	data, err := contracts.EncodeDeauthorize(holderAddress)
	if err != nil {
		return apperrors.Internal("failed to encode deauthorize call")
	}
	contractAddr := common.HexToAddress(*cfg.InternalBalanceContractAddress)
	if _, err := s.Blockchain.SignAndSubmitTx(ctx, issuerKey, &contractAddr, big.NewInt(0), data, nil); err != nil {
		return apperrors.Internal("failed to deauthorize wallet from holding the internal balance asset: " + err.Error())
	}
	return nil
}
