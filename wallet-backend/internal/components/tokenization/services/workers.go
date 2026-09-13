package services

import (
	"context"
	"log"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"wallet-backend/internal/components/tokenization/models"
	"wallet-backend/internal/contracts"
)

// ActivatePrimarySales sweeps Minted assets whose SalesStart has arrived
// and flips them to PrimarySaleActive, notifying anyone who expressed
// interest. Ported from upstream's ActivatePrimarySalesRoutine/
// SendPNToSuscribersForPrimarySales, with two differences (PLAN.md §4.9):
// a single clean poll interval (main.go) instead of reproducing upstream's
// own accidental 15-minute-sleep-inside-a-5-second-loop stacking, and a
// synchronous notify-here instead of a channel handoff to a separate
// worker - one fewer moving part for the same outcome. Notification itself
// is a log line, not a real push: no device-token subsystem exists in
// this port yet (the same gap already documented for Stablerail, §4.6) -
// ExpressionOfInterest.Notified still flips so the intent is recorded
// even though delivery isn't.
func (s *Service) ActivatePrimarySales() {
	var assets []models.TokenizedAsset
	if err := s.DB.Where("status = ? AND sales_start IS NOT NULL AND sales_start <= ?", models.StatusMinted, time.Now()).Find(&assets).Error; err != nil {
		log.Printf("[tokenization] failed to query assets ready for primary sale: %v", err)
		return
	}
	for i := range assets {
		asset := &assets[i]
		asset.Status = models.StatusPrimarySaleActive
		if err := s.DB.Save(asset).Error; err != nil {
			log.Printf("[tokenization] failed to activate primary sale for asset %d: %v", asset.ID, err)
			continue
		}

		var interests []models.ExpressionOfInterest
		s.DB.Where("tokenized_asset_id = ? AND notified = ?", asset.ID, false).Find(&interests)
		for j := range interests {
			log.Printf("[tokenization] notify user %d: primary sale for %s is now live", interests[j].UserID, asset.AssetCode)
			interests[j].Notified = true
			s.DB.Save(&interests[j])
		}
	}
}

// ActivateSecondarySales sweeps PrimarySaleActive assets whose SalesEnd has
// passed, pauses their Sale contract (Phase 8's EncodeSetPaused - the
// direct functional equivalent of upstream's primary window closing), and
// flips them to SecondarySaleActive. From this point the asset trades only
// through Phase 7's market component (already possible the moment it was
// curated at mint time - see minting.go).
func (s *Service) ActivateSecondarySales(ctx context.Context) {
	var assets []models.TokenizedAsset
	if err := s.DB.Where("status = ? AND sales_end IS NOT NULL AND sales_end <= ?", models.StatusPrimarySaleActive, time.Now()).Find(&assets).Error; err != nil {
		log.Printf("[tokenization] failed to query assets ready for secondary sale: %v", err)
		return
	}
	for i := range assets {
		asset := &assets[i]
		if asset.SaleContractAddress != nil {
			distributionKey, err := s.deriveDistributionKey(asset.ID)
			if err != nil {
				log.Printf("[tokenization] failed to derive distribution key for asset %d: %v", asset.ID, err)
				continue
			}
			data, err := contracts.EncodeSetPaused(true)
			if err != nil {
				log.Printf("[tokenization] failed to encode setPaused for asset %d: %v", asset.ID, err)
				continue
			}
			addr := common.HexToAddress(*asset.SaleContractAddress)
			if _, err := s.Blockchain.SignAndSubmitTx(ctx, distributionKey, &addr, big.NewInt(0), data, nil); err != nil {
				log.Printf("[tokenization] failed to pause sale contract for asset %d: %v", asset.ID, err)
				continue
			}
		}
		asset.Status = models.StatusSecondarySaleActive
		if err := s.DB.Save(asset).Error; err != nil {
			log.Printf("[tokenization] failed to activate secondary sale for asset %d: %v", asset.ID, err)
		}
	}
}
