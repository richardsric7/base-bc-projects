package middleware

import "github.com/gin-gonic/gin"

// CORS is a permissive default suitable for a base template; tighten
// Access-Control-Allow-Origin to a real allowlist before shipping to
// production.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Signer-Address, X-Wallet-Address, X-Timestamp, X-Signature, X-Api-Key")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}
