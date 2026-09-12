package middleware

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"wallet-backend/internal/apperrors"
)

// CtxSubject is the gin context key JWTAuth stores the verified token
// subject under - a wallet address for a wallet session, an admin
// identifier for the admin surface.
const CtxSubject = "subject"

// Audiences distinguish the two trust domains that share this JWT
// mechanism, so a wallet-session token can never be replayed against an
// admin route (or vice versa) even though both use the same signing
// secret in this base template.
const (
	AudienceWalletSession = "wallet-session"
	AudienceAdmin         = "admin"
)

// IssueToken creates a short-lived HS256 JWT scoped to one audience. This is
// intentionally minimal: add refresh-token rotation, revocation lists, or an
// external IdP as the project matures.
func IssueToken(secret, subject, audience string, ttl time.Duration) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   subject,
		Audience:  jwt.ClaimStrings{audience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// JWTAuth guards a route with a Bearer JWT, requiring it to carry
// requiredAudience - use middleware.AudienceWalletSession for the primary
// API (issued after a successful SIWE verification, see internal/components/auth)
// and middleware.AudienceAdmin for staff-only routes.
func JWTAuth(secret, requiredAudience string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			apperrors.Abort(c, apperrors.Unauthorized("missing bearer token"))
			return
		}
		tokenString := strings.TrimPrefix(header, prefix)

		claims := &jwt.RegisteredClaims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(secret), nil
		}, jwt.WithAudience(requiredAudience))
		if err != nil || !token.Valid {
			apperrors.Abort(c, apperrors.Unauthorized("invalid or expired token"))
			return
		}

		c.Set(CtxSubject, claims.Subject)
		c.Next()
	}
}
