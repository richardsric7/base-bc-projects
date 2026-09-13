// Package apperrors defines a small, consistent error shape for API responses.
//
// Every handler-facing error implements GenericError so controllers can do
// exactly one thing on failure: apperrors.Abort(c, err) or, for a plain
// `error` return from a service, apperrors.AbortAny(c, err). Add new domain
// errors as functions returning *AppError (see the constructors below)
// rather than inventing new shapes.
package apperrors

import "github.com/gin-gonic/gin"

// GenericError is implemented by every error this API returns to clients.
type GenericError interface {
	error
	StatusCode() int
	JSON() gin.H
}

// AppError is the concrete error type used across the codebase.
type AppError struct {
	Code    string `json:"error"`
	Message string `json:"message"`
	Status  int    `json:"-"`
}

func (e *AppError) Error() string { return e.Message }

// StatusCode returns the HTTP status code that should be sent with this error.
func (e *AppError) StatusCode() int { return e.Status }

// JSON returns the response body shape documented in the project README.
func (e *AppError) JSON() gin.H {
	return gin.H{"error": e.Code, "message": e.Message}
}

// New builds an AppError with an explicit status/code/message.
func New(status int, code, message string) *AppError {
	return &AppError{Code: code, Message: message, Status: status}
}

// Common constructors covering the vast majority of API failure modes.
func BadRequest(message string) *AppError   { return New(400, "bad_request", message) }
func Unauthorized(message string) *AppError { return New(401, "unauthorized", message) }
func Forbidden(message string) *AppError    { return New(403, "forbidden", message) }
func NotFound(message string) *AppError     { return New(404, "not_found", message) }
func Conflict(message string) *AppError     { return New(409, "conflict", message) }
func Internal(message string) *AppError     { return New(500, "internal_error", message) }

// Abort writes the error as the JSON response and stops the middleware chain.
func Abort(c *gin.Context, err GenericError) {
	c.AbortWithStatusJSON(err.StatusCode(), err.JSON())
}

// AbortAny is Abort for a plain `error` return value: if err is already a
// GenericError (typically because a services method returned one of the
// constructors above), its status/code/message are preserved; any other
// error is reported as an opaque 500 so internal details never leak to
// clients. Controllers should route every service error through this.
func AbortAny(c *gin.Context, err error) {
	if genErr, ok := err.(GenericError); ok {
		Abort(c, genErr)
		return
	}
	Abort(c, Internal("unexpected error"))
}
