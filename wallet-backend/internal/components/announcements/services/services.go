package services

import (
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/announcements/models"
)

type Service struct {
	DB *gorm.DB
}

func New(db *gorm.DB) *Service {
	return &Service{DB: db}
}

// ListActive returns every active announcement, most recent first.
func (s *Service) ListActive() ([]models.Announcement, error) {
	var announcements []models.Announcement
	err := s.DB.Where("is_active = ?", true).Order("created_at DESC").Find(&announcements).Error
	if err != nil {
		return nil, apperrors.Internal("failed to load announcements")
	}
	return announcements, nil
}

// Create adds a new announcement. Only reachable via the JWT-gated admin route.
func (s *Service) Create(title, body string) (*models.Announcement, error) {
	if title == "" || body == "" {
		return nil, apperrors.BadRequest("title and body are required")
	}
	announcement := models.Announcement{Title: title, Body: body, IsActive: true}
	if err := s.DB.Create(&announcement).Error; err != nil {
		return nil, apperrors.Internal("failed to create announcement")
	}
	return &announcement, nil
}
