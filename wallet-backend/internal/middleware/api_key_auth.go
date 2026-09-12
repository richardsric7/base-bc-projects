package middleware

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	servicelinksModels "wallet-backend/internal/components/servicelinks/models"
)

// CtxServiceLink is the gin context key APIKeyAuth stores the resolved
// *servicelinksModels.ServiceLink under, once per request - handlers read
// it from here instead of each independently re-querying the database and
// re-implementing their own status checks, which is what the original did
// (and did inconsistently - see PLAN.md §4.11 finding 2).
const CtxServiceLink = "service_link"

// APIKeyHeader is the header a partner presents its API key in.
const APIKeyHeader = "X-API-Key"

// HashAPIKey returns the hex-encoded SHA-256 hash of a raw API key -
// what's actually stored in ServiceLink.APIKeyHash. The raw key itself is
// never persisted (PLAN.md §4.11 finding 1).
func HashAPIKey(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}

// APIKeyAuth resolves the calling ServiceLink and rejects the request
// outright if it's missing, unknown, inactive, suspended, or unverified -
// deny-by-default for every route this guards, fixing the original's gap
// where only 3 of its ~40 servicelinks routes enforced that trio at all
// (PLAN.md §4.11 finding 3). On success the resolved ServiceLink is
// attached to gin.Context under CtxServiceLink; route-specific capability
// checks (CanSendPayments, CanManageTokenization, ...) are the caller's
// responsibility, checked against that attached value.
func APIKeyAuth(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawKey := c.GetHeader(APIKeyHeader)
		if rawKey == "" {
			apperrors.Abort(c, apperrors.Unauthorized("missing API key"))
			return
		}

		var link servicelinksModels.ServiceLink
		err := db.Where("api_key_hash = ?", HashAPIKey(rawKey)).First(&link).Error
		if err != nil {
			apperrors.Abort(c, apperrors.Unauthorized("invalid API key"))
			return
		}
		if link.Inactive {
			apperrors.Abort(c, apperrors.Unauthorized("this service link has been deactivated"))
			return
		}
		if link.Suspended {
			apperrors.Abort(c, apperrors.Unauthorized("this service link has been suspended"))
			return
		}
		if !link.Verified {
			apperrors.Abort(c, apperrors.Unauthorized("this service link has not been verified"))
			return
		}

		c.Set(CtxServiceLink, &link)
		c.Next()
	}
}
