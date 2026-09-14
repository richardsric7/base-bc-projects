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

// Audiences distinguish the trust domains that share this JWT mechanism,
// so a token issued for one purpose can never be replayed against a
// route guarding another, even though all of them use the same signing
// secret in this base template. There is no AudienceWalletSession
// anymore - user operations authenticate via SignatureAuth (PLAN.md
// §12), a stateless per-request scheme with no session token to issue at
// all - only staff/admin routes and the servicelinks partner-login
// token (PLAN.md §12.5) still mint a JWT.
const (
	AudienceAdmin = "admin"
	// AudienceServiceLinkSession scopes the token servicelinks'
	// LOGIN-kind approval verify issues to a redeeming partner (PLAN.md
	// §14.1.1) - it authenticates a partner, not a user's own wallet
	// operations, so it keeps its own audience rather than inheriting
	// the now-removed AudienceWalletSession's name.
	AudienceServiceLinkSession = "servicelink-session"
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
// requiredAudience - middleware.AudienceAdmin for staff-only routes, or
// middleware.AudienceServiceLinkSession for a servicelinks partner
// redeeming a LOGIN approval. User operations use middleware.SignatureAuth
// instead (PLAN.md §12), not this function at all.
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
