package services

import (
	"log"
	"time"

	"wallet-backend/internal/components/patron/models"
)

// PromotePendingMemberships promotes subscription logs whose EffectiveDate
// has arrived into the active UserPatronMembership row. Two bugs found
// while porting upstream's UpdateUserPatronMemberships, fixed rather than
// reproduced (PLAN.md §4.10):
//
//  1. Upstream runs this once at process boot only - a subscription that
//     becomes effective while the process keeps running past that point is
//     never promoted until the next restart. This port calls it from a
//     proper recurring worker (main.go).
//  2. Upstream's query selects logs where effective_date >= now() -
//     backwards from what a "promote what's now due" sweep needs
//     (effective_date <= now()). As written, upstream promotes
//     future-dated upgrades immediately (defeating the deferred-upgrade
//     feature entirely) and stops selecting a log the moment its
//     effective date has actually passed. This port selects
//     effective_date <= now(), the correct direction for "this is now
//     due."
func (s *Service) PromotePendingMemberships() {
	var dueLogs []models.UserPatronSubscriptionLog
	if err := s.DB.Where("effective_date <= ?", time.Now()).Order("effective_date ASC").Find(&dueLogs).Error; err != nil {
		log.Printf("[patron] failed to query due subscription logs: %v", err)
		return
	}
	if len(dueLogs) == 0 {
		return
	}

	// Multiple due logs can exist for one user (e.g. a queued upgrade that
	// itself became due before being promoted) - apply only the most
	// recent by effective date per user.
	latestByUser := make(map[uint]models.UserPatronSubscriptionLog)
	for _, entry := range dueLogs {
		current, ok := latestByUser[entry.UserID]
		if !ok || entry.EffectiveDate.After(current.EffectiveDate) {
			latestByUser[entry.UserID] = entry
		}
	}

	for userID, entry := range latestByUser {
		var membership models.UserPatronMembership
		err := s.DB.Where("user_id = ?", userID).First(&membership).Error
		if err != nil {
			membership = models.UserPatronMembership{UserID: userID}
		}
		if membership.PatronPackageID == entry.PatronPackageID && membership.PatronTierID == entry.PatronTierID && membership.ValidTill.Equal(entry.ValidTill) {
			continue // already applied
		}
		membership.PatronPackageID = entry.PatronPackageID
		membership.PatronTierID = entry.PatronTierID
		membership.ValidTill = entry.ValidTill
		if err := s.DB.Save(&membership).Error; err != nil {
			log.Printf("[patron] failed to promote membership for user %d: %v", userID, err)
		}
	}
}
