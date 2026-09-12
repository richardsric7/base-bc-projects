package services

import (
	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/market/models"
)

// CancelOffer cancels the caller's own OPEN or PARTIALLY_FILLED offer -
// already-FILLED or already-CANCELED offers can't be canceled again.
func (s *Service) CancelOffer(address, offerID string) (*models.MarketOffer, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}

	var offer models.MarketOffer
	if err := s.DB.Where("id = ?", offerID).First(&offer).Error; err != nil {
		return nil, apperrors.NotFound("offer not found")
	}
	if offer.UserID != user.ID {
		return nil, apperrors.Forbidden("you may only cancel your own offers")
	}
	if offer.Status != models.OfferOpen && offer.Status != models.OfferPartiallyFilled {
		return nil, apperrors.Conflict("offer is already " + string(offer.Status))
	}

	if err := s.DB.Model(&offer).Update("status", models.OfferCanceled).Error; err != nil {
		return nil, apperrors.Internal("failed to cancel offer")
	}
	offer.Status = models.OfferCanceled
	return &offer, nil
}

// ListOrderBook returns every open (OPEN or PARTIALLY_FILLED) offer for a
// trading pair, best price first on each side - the public order-book
// view a client renders directly.
func (s *Service) ListOrderBook(baseToken, quoteToken string) (buys, sells []models.MarketOffer, err error) {
	statuses := []models.OfferStatus{models.OfferOpen, models.OfferPartiallyFilled}
	if dbErr := s.DB.Where("base_token_address = ? AND quote_token_address = ? AND offer_type = ? AND status IN ?", baseToken, quoteToken, models.OfferBuy, statuses).
		Order("CAST(price_per_unit AS DECIMAL) DESC, created_at ASC").Find(&buys).Error; dbErr != nil {
		return nil, nil, apperrors.Internal("failed to load buy offers")
	}
	if dbErr := s.DB.Where("base_token_address = ? AND quote_token_address = ? AND offer_type = ? AND status IN ?", baseToken, quoteToken, models.OfferSell, statuses).
		Order("CAST(price_per_unit AS DECIMAL) ASC, created_at ASC").Find(&sells).Error; dbErr != nil {
		return nil, nil, apperrors.Internal("failed to load sell offers")
	}
	return buys, sells, nil
}

// ListMyOffers returns every offer the caller has ever placed.
func (s *Service) ListMyOffers(address string) ([]models.MarketOffer, error) {
	user, err := s.getUserByAddress(address)
	if err != nil {
		return nil, err
	}
	var offers []models.MarketOffer
	if err := s.DB.Where("user_id = ?", user.ID).Order("created_at DESC").Find(&offers).Error; err != nil {
		return nil, apperrors.Internal("failed to load offers")
	}
	return offers, nil
}

// ListTrades returns every trade a pair has executed, most recent first.
func (s *Service) ListTrades(baseToken, quoteToken string) ([]models.MarketTrade, error) {
	var trades []models.MarketTrade
	if err := s.DB.Where("base_token_address = ? AND quote_token_address = ?", baseToken, quoteToken).
		Order("created_at DESC").Find(&trades).Error; err != nil {
		return nil, apperrors.Internal("failed to load trades")
	}
	return trades, nil
}
