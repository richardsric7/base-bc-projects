// Package services implements Sign-In With Ethereum (SIWE, EIP-4361): the
// primary API's wallet-signature auth. A client requests a nonce, signs a
// SIWE message containing it with their wallet, and exchanges the signed
// message for a short-lived session JWT (audience
// middleware.AudienceWalletSession) that authenticates subsequent calls.
//
// This replaces the earlier Stellar-specific per-request header signature
// scheme with the EVM ecosystem's standard, wallet-interoperable flow - see
// PLAN.md §2. It also fixes a real gap in that earlier scheme: a signature
// here is bound to a single-use, server-issued nonce instead of just a
// timestamp, so a captured signed message can't be replayed against a
// different session once its nonce is consumed.
package services

import (
	"time"

	"github.com/spruceid/siwe-go"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/cache"
	"wallet-backend/internal/middleware"
)

const nonceCachePrefix = "siwe:nonce:"
const nonceTTL = 5 * time.Minute

type Service struct {
	Cache      cache.Cache
	Domain     string // the "domain" every SIWE message must declare, e.g. "wallet.example.com"
	ChainID    int64
	JWTSecret  string
	SessionTTL time.Duration
}

func New(c cache.Cache, domain string, chainID int64, jwtSecret string, sessionTTL time.Duration) *Service {
	return &Service{Cache: c, Domain: domain, ChainID: chainID, JWTSecret: jwtSecret, SessionTTL: sessionTTL}
}

// GenerateNonce issues a fresh, single-use nonce and remembers it for
// nonceTTL so a later Verify call can confirm it hasn't been used before
// and hasn't expired.
func (s *Service) GenerateNonce() string {
	nonce := siwe.GenerateNonce()
	s.Cache.Set(nonceCachePrefix+nonce, "1", nonceTTL)
	return nonce
}

// Verify checks a signed SIWE message and, if valid, consumes its nonce and
// returns a session JWT for the signing address.
func (s *Service) Verify(rawMessage, signature string) (address string, token string, err error) {
	msg, parseErr := siwe.ParseMessage(rawMessage)
	if parseErr != nil {
		return "", "", apperrors.BadRequest("invalid SIWE message: " + parseErr.Error())
	}

	nonce := msg.GetNonce()
	if _, ok := s.Cache.Get(nonceCachePrefix + nonce); !ok {
		return "", "", apperrors.Unauthorized("unknown or expired nonce")
	}

	if msg.GetDomain() != s.Domain {
		return "", "", apperrors.Unauthorized("unexpected SIWE domain")
	}
	if int64(msg.GetChainID()) != s.ChainID {
		return "", "", apperrors.Unauthorized("unexpected chain id")
	}

	domain := s.Domain
	if _, verifyErr := msg.Verify(signature, &domain, &nonce, nil); verifyErr != nil {
		return "", "", apperrors.Unauthorized("signature verification failed: " + verifyErr.Error())
	}

	// The nonce is single-use: consume it now so this exact signed message
	// can never be replayed into a second session.
	s.Cache.Delete(nonceCachePrefix + nonce)

	address = msg.GetAddress().Hex()
	token, tokenErr := middleware.IssueToken(s.JWTSecret, address, middleware.AudienceWalletSession, s.SessionTTL)
	if tokenErr != nil {
		return "", "", apperrors.Internal("failed to issue session token")
	}
	return address, token, nil
}
