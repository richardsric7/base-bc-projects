// Package services implements the shortlink/deep-link component - a
// self-hosted table plus a Go QR-code library replacing the original's
// Firebase Dynamic Links dependency, so referrals, servicelinks
// login/authorize/event deep links, and payment-request links all work
// with one fewer required third-party account, consistent with this
// port's "free/local default, real vendor optional" pattern for every
// other integration. See PLAN.md §4.13.
package services

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strings"

	"github.com/skip2/go-qrcode"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/shortlink/models"
)

type Service struct {
	DB      *gorm.DB
	BaseURL string // prefixed to a ShortCode when building the public short URL, e.g. "https://trov.to"
}

func New(db *gorm.DB, baseURL string) *Service {
	return &Service{DB: db, BaseURL: strings.TrimSuffix(baseURL, "/")}
}

// shortCodeAlphabet is Crockford base32 (no padding, unambiguous
// characters) - short, URL-safe, and easy to read aloud or retype from a
// printed QR code if scanning fails.
var shortCodeEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

func generateShortCode() (string, error) {
	buf := make([]byte, 5) // 8 base32 characters
	if _, err := rand.Read(buf); err != nil {
		return "", apperrors.Internal("failed to generate short code")
	}
	return shortCodeEncoding.EncodeToString(buf), nil
}

// CreateLink mints a new short link for targetURL, retrying on the
// astronomically unlikely event of a short-code collision.
func (s *Service) CreateLink(targetURL, metadata string) (*models.DynamicLink, error) {
	if targetURL == "" {
		return nil, apperrors.BadRequest("targetUrl is required")
	}
	for attempt := 0; attempt < 5; attempt++ {
		code, err := generateShortCode()
		if err != nil {
			return nil, err
		}
		link := models.DynamicLink{ShortCode: code, TargetURL: targetURL, Metadata: metadata}
		err = s.DB.Create(&link).Error
		if err == nil {
			return &link, nil
		}
		if !isUniqueConstraintErr(err) {
			return nil, apperrors.Internal("failed to create short link")
		}
		// Collision on ShortCode - retry with a freshly generated one.
	}
	return nil, apperrors.Internal("failed to generate a unique short code after several attempts")
}

// isUniqueConstraintErr is a best-effort check across SQLite/Postgres
// error message shapes - good enough to distinguish "code collision,
// retry" from every other failure, without importing either driver's
// error type here.
func isUniqueConstraintErr(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate")
}

// GetLink looks up a link by its short code without recording a click -
// used wherever a caller needs to know a code is valid (e.g. serving its
// QR code) without counting that as a visit.
func (s *Service) GetLink(shortCode string) (*models.DynamicLink, error) {
	var link models.DynamicLink
	if err := s.DB.Where("short_code = ?", shortCode).First(&link).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("short link not found")
		}
		return nil, apperrors.Internal("failed to load short link")
	}
	return &link, nil
}

// Resolve looks up a link by its short code and records a click - the
// redirect handler's core lookup. The returned ClickCount reflects this
// visit (an atomic SQL increment, then a reload - GORM's UpdateColumn
// with a raw expression doesn't write the resulting value back into the
// Go struct on its own).
func (s *Service) Resolve(shortCode string) (*models.DynamicLink, error) {
	link, err := s.GetLink(shortCode)
	if err != nil {
		return nil, err
	}
	if err := s.DB.Model(link).UpdateColumn("click_count", gorm.Expr("click_count + 1")).Error; err != nil {
		return nil, apperrors.Internal("failed to record click")
	}
	return s.GetLink(shortCode)
}

// ShortURL builds the public short URL for a code (BaseURL + "/s/" + code) -
// what a caller actually hands out (in a QR code, an SMS, a referral
// message), as opposed to the raw TargetURL it resolves to.
func (s *Service) ShortURL(shortCode string) string {
	return s.BaseURL + "/s/" + shortCode
}

// QRCodePNG renders a short URL as a PNG QR code at the given pixel size
// (a square image, size x size). 256 is a reasonable default for
// on-screen display; use a larger size for print.
func (s *Service) QRCodePNG(shortCode string, size int) ([]byte, error) {
	if size <= 0 {
		size = 256
	}
	png, err := qrcode.Encode(s.ShortURL(shortCode), qrcode.Medium, size)
	if err != nil {
		return nil, apperrors.Internal("failed to generate QR code")
	}
	return png, nil
}
