package controllers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"wallet-backend/internal/apperrors"
)

// uintParam parses a positive-integer path parameter, aborting the request
// and returning ok=false on failure.
func uintParam(c *gin.Context, name string) (uint, bool) {
	value, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil {
		apperrors.Abort(c, apperrors.BadRequest(name+" must be a positive integer"))
		return 0, false
	}
	return uint(value), true
}

func writeError(c *gin.Context, err error) {
	apperrors.AbortAny(c, err)
}

// noContent is a small readability helper for the common "succeeded, no
// body to return" response.
func noContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}
