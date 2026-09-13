package services

import (
	"context"
	"log"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/crypto/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/network"
)

// EnsureDepositAddresses returns the caller's deposit addresses for
// currency, requesting a new OneLiquidity subwallet if none exists yet.
// Ported from the original's CreateCryptoSubwalletRequest/GetCryptoSubwalletRequest
// pair - this port always tries create-or-fetch in one call rather than
// exposing both as separate client-visible steps, since a client never
// actually needs to distinguish "created new" from "already existed."
func (s *Service) EnsureDepositAddresses(address, currency string) ([]models.CryptoDepositAddress, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}

	var existing []models.CryptoDepositAddress
	if err := s.DB.Where("user_id = ? AND currency = ?", user.ID, currency).Find(&existing).Error; err != nil {
		return nil, apperrors.Internal("failed to load deposit addresses")
	}
	if len(existing) > 0 {
		return existing, nil
	}

	var resp models.SubwalletResponse
	req := models.SubwalletRequest{Currency: currency, UID: s.uidFor(user.Username)}
	if err := s.doRequest("POST", "/wallets/v1/sub", req, &resp); err != nil {
		return nil, err
	}

	addresses := make([]models.CryptoDepositAddress, 0, len(resp.Data.Addresses))
	for _, a := range resp.Data.Addresses {
		row := models.CryptoDepositAddress{
			UserID:         user.ID,
			Currency:       currency,
			Network:        a.Network,
			DepositAddress: a.Address,
		}
		if err := s.DB.Create(&row).Error; err != nil {
			return nil, apperrors.Internal("failed to save deposit address")
		}
		addresses = append(addresses, row)
	}
	return addresses, nil
}

// ListDeposits returns the caller's locally recorded deposits (crediting
// status included) - mirrors the original's deposit-history endpoint,
// backed by this port's own ledger rather than a live OneLiquidity call
// each time.
func (s *Service) ListDeposits(address string) ([]models.CryptoDeposit, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}
	var deposits []models.CryptoDeposit
	if err := s.DB.Where("user_id = ?", user.ID).Order("created_at DESC").Find(&deposits).Error; err != nil {
		return nil, apperrors.Internal("failed to load deposits")
	}
	return deposits, nil
}

// PollNewDeposits fetches OneLiquidity's recent deposit list and credits
// any completed deposit this backend hasn't recorded yet - meant to be
// called on an interval by a background goroutine (see main.go).
//
// Crediting here is a **transfer** from a derived treasury key holding a
// balance of the matching CuratedToken, not the original's Stellar
// **mint** from a Trovo-issuer key - see PLAN.md §4.7. Base's curated
// assets are ordinary already-deployed ERC-20 contracts this project
// doesn't control minting for (unlike a Stellar asset, where Trovo was
// always the issuer), so an operator funds the treasury address
// (Service.treasuryAddress()) with each curated token ahead of time,
// the same operational shape as the activation faucet. A currency with no
// matching CuratedToken is skipped (logged, Credited stays false) rather
// than failing the whole poll - the deposit is still recorded for a
// manual/future reconciliation once that token is curated.
func (s *Service) PollNewDeposits() {
	var resp models.DepositListResponse
	if err := s.doRequest("GET", "/wallets/v1/deposit?limit=50", nil, &resp); err != nil {
		log.Printf("[crypto] error polling deposits: %v", err)
		return
	}

	for _, item := range resp.Data {
		if !item.IsCompleted || !item.IsValid {
			continue
		}
		var count int64
		s.DB.Model(&models.CryptoDeposit{}).Where("deposit_id = ?", item.DepositID).Count(&count)
		if count > 0 {
			continue
		}
		if err := s.creditDeposit(item); err != nil {
			log.Printf("[crypto] error crediting deposit %s: %v", item.DepositID, err)
		}
	}
}

func (s *Service) creditDeposit(item models.DepositItem) error {
	var addr models.CryptoDepositAddress
	if err := s.DB.Where("deposit_address = ? AND currency = ?", item.ToAddress, item.Currency).First(&addr).Error; err != nil {
		return apperrors.Internal("deposit references an unrecognized address")
	}

	deposit := models.CryptoDeposit{
		UserID:      addr.UserID,
		DepositID:   item.DepositID,
		TxID:        item.TxID,
		Currency:    item.Currency,
		Amount:      item.Amount,
		FromAddress: item.FromAddress,
		ToAddress:   item.ToAddress,
	}
	if err := s.DB.Create(&deposit).Error; err != nil {
		return err
	}

	var user usersModels.User
	if err := s.DB.Select("address").Where("id = ?", addr.UserID).First(&user).Error; err != nil {
		return err
	}

	token := s.findCuratedToken(item.Currency)
	if token == nil {
		log.Printf("[crypto] no curated token for %s - deposit %s recorded but not credited", item.Currency, item.DepositID)
		return nil
	}

	amount, err := decimal.NewFromString(item.Amount)
	if err != nil {
		return apperrors.Internal("invalid deposit amount from OneLiquidity")
	}
	baseUnits := toBaseUnits(amount, token.Decimals)

	treasuryKey, err := s.deriveTreasuryKey()
	if err != nil {
		return err
	}
	data, err := network.EncodeERC20Transfer(user.Address, baseUnits)
	if err != nil {
		return err
	}
	tokenAddr := common.HexToAddress(token.ContractAddress)
	txHash, err := s.Blockchain.SignAndSubmitTx(context.Background(), treasuryKey, &tokenAddr, big.NewInt(0), data, nil)
	if err != nil {
		return apperrors.Internal("failed to credit deposit on-chain: " + err.Error())
	}

	return s.DB.Model(&deposit).Updates(map[string]interface{}{"credited": true, "credit_tx_hash": txHash}).Error
}

// toBaseUnits converts a decimal quantity into an integer amount in the
// given number of on-chain decimals - same helper as fiat's, duplicated
// rather than shared since these are two independent, small components
// and neither has a reason to depend on the other.
func toBaseUnits(amount decimal.Decimal, decimals uint8) *big.Int {
	scaled := amount.Shift(int32(decimals)).Truncate(0)
	result, ok := new(big.Int).SetString(scaled.String(), 10)
	if !ok {
		return big.NewInt(0)
	}
	return result
}
