package middleware

import (
	"encoding/base64"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stellar/go-stellar-sdk/keypair"

	"wallet-backend/internal/apperrors"
)

// CtxPublicKey is the gin context key the signature-auth middleware stores
// the caller's verified public key under.
const CtxPublicKey = "publicKey"

// StellarSignatureAuth authenticates a request using the caller's Stellar
// wallet key instead of a username/password: since wallets in this template
// are non-custodial, the keypair the user already controls doubles as their
// login credential.
//
// The client sends three headers:
//
//	X-Public-Key: the caller's Stellar public address (G...)
//	X-Timestamp:  unix seconds when the request was signed
//	X-Signature:  base64 ed25519 signature of "<publicKey><timestamp>"
//
// window bounds how far the timestamp may drift from server time, guarding
// against replay of an old signed request.
func StellarSignatureAuth(window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		publicKey := c.GetHeader("X-Public-Key")
		timestamp := c.GetHeader("X-Timestamp")
		signature := c.GetHeader("X-Signature")
		if publicKey == "" || timestamp == "" || signature == "" {
			apperrors.Abort(c, apperrors.Unauthorized("missing X-Public-Key, X-Timestamp or X-Signature header"))
			return
		}

		ts, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			apperrors.Abort(c, apperrors.Unauthorized("invalid timestamp"))
			return
		}
		age := time.Since(time.Unix(ts, 0))
		if age > window || age < -window {
			apperrors.Abort(c, apperrors.Unauthorized("stale or future-dated timestamp"))
			return
		}

		kp, err := keypair.ParseAddress(publicKey)
		if err != nil {
			apperrors.Abort(c, apperrors.Unauthorized("invalid Stellar public key"))
			return
		}

		sigBytes, err := base64.StdEncoding.DecodeString(signature)
		if err != nil {
			apperrors.Abort(c, apperrors.Unauthorized("invalid signature encoding"))
			return
		}

		payload := []byte(publicKey + timestamp)
		if err := kp.Verify(payload, sigBytes); err != nil {
			apperrors.Abort(c, apperrors.Unauthorized("signature verification failed"))
			return
		}

		c.Set(CtxPublicKey, publicKey)
		c.Next()
	}
}
