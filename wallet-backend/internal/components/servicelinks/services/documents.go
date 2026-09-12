package services

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/servicelinks/models"
)

// UploadStakeholderDocument stores a document a partner submits about one
// of its own stakeholders (e.g. beneficial-owner or director paperwork
// collected during onboarding) under this service link's own tenant, with
// a recorded owner and content hash. The original had no per-tenant
// ownership record on this store at all, letting any service link fetch
// or delete any other tenant's documents by guessing an ID (an IDOR -
// PLAN.md §4.11 finding 8); ServiceLinkID here is what closes it, checked
// by every read below.
func (s *Service) UploadStakeholderDocument(serviceLinkID uint, originalFilename, contentType string, file io.Reader) (*models.StakeholderDocument, error) {
	hasher := sha256.New()
	key := fmt.Sprintf("servicelinks/%d/documents/%d-%s", serviceLinkID, time.Now().UnixNano(), originalFilename)
	if _, err := s.Storage.Put(key, io.TeeReader(file, hasher)); err != nil {
		return nil, apperrors.Internal("failed to store document")
	}

	id, err := generateApprovalID() // reuse the same random-hex ID generator
	if err != nil {
		return nil, err
	}
	doc := models.StakeholderDocument{
		ID:               id,
		ServiceLinkID:    serviceLinkID,
		OriginalFilename: originalFilename,
		ContentType:      contentType,
		SHA256:           hex.EncodeToString(hasher.Sum(nil)),
		StorageKey:       key,
	}
	if err := s.DB.Create(&doc).Error; err != nil {
		return nil, apperrors.Internal("failed to save document")
	}
	return &doc, nil
}

func (s *Service) getOwnedStakeholderDocument(serviceLinkID uint, documentID string) (*models.StakeholderDocument, error) {
	var doc models.StakeholderDocument
	if err := s.DB.First(&doc, "id = ?", documentID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("document not found")
		}
		return nil, apperrors.Internal("failed to load document")
	}
	if doc.ServiceLinkID != serviceLinkID {
		return nil, apperrors.Forbidden("this document belongs to a different service link")
	}
	return &doc, nil
}

// GetStakeholderDocument returns a document's metadata and content,
// scoped to the calling service link.
func (s *Service) GetStakeholderDocument(serviceLinkID uint, documentID string) (*models.StakeholderDocument, io.ReadCloser, error) {
	doc, err := s.getOwnedStakeholderDocument(serviceLinkID, documentID)
	if err != nil {
		return nil, nil, err
	}
	content, err := s.Storage.Get(doc.StorageKey)
	if err != nil {
		return nil, nil, apperrors.Internal("failed to read document")
	}
	return doc, content, nil
}

// DeleteStakeholderDocument removes a document's stored content and
// record, scoped to the calling service link.
func (s *Service) DeleteStakeholderDocument(serviceLinkID uint, documentID string) error {
	doc, err := s.getOwnedStakeholderDocument(serviceLinkID, documentID)
	if err != nil {
		return err
	}
	if err := s.Storage.Delete(doc.StorageKey); err != nil {
		return apperrors.Internal("failed to delete document content")
	}
	if err := s.DB.Delete(doc).Error; err != nil {
		return apperrors.Internal("failed to delete document record")
	}
	return nil
}
