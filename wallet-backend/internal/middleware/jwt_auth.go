package middleware

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"wallet-backend/internal/apperrors"
)

// CtxAdminSubject is the gin context key JWTAuth stores the token subject under.
const CtxAdminSubject = "adminSubject"

// IssueAdminToken creates a short-lived HS256 JWT for the staff/admin
// surface (announcements management, etc.). This is intentionally minimal:
// add refresh-token rotation, revocation lists, or an external IdP as the
// project matures.
func IssueAdminToken(secret, subject string, ttl time.Duration) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   subject,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// JWTAuth guards admin-only routes with a Bearer JWT.
func JWTAuth(secret string) gin.HandlerFunc {
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
		})
		if err != nil || !token.Valid {
			apperrors.Abort(c, apperrors.Unauthorized("invalid or expired token"))
			return
		}

		c.Set(CtxAdminSubject, claims.Subject)
		c.Next()
	}
}
