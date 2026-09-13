package services

import (
	"errors"

	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	"wallet-backend/internal/components/kyc/models"
)

// CreateSumsubLevelInput is the admin-provided shape of a new catalog
// entry - see models.SumsubLevel's doc comment for why this catalog
// exists at all (Sumsub has no API to discover the levels configured in a
// project's dashboard, so an operator maintains this list to match it).
type CreateSumsubLevelInput struct {
	Name        string
	Description string
}

func (s *Service) CreateSumsubLevel(input CreateSumsubLevelInput) (*models.SumsubLevel, error) {
	if input.Name == "" {
		return nil, apperrors.BadRequest("name is required")
	}
	level := models.SumsubLevel{Name: input.Name, Description: input.Description}
	if err := s.DB.Create(&level).Error; err != nil {
		return nil, apperrors.Conflict("a Sumsub level with this name already exists")
	}
	return &level, nil
}

func (s *Service) UpdateSumsubLevel(id uint, description string) (*models.SumsubLevel, error) {
	var level models.SumsubLevel
	if err := s.DB.First(&level, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("Sumsub level not found")
		}
		return nil, apperrors.Internal("failed to load Sumsub level")
	}
	level.Description = description
	if err := s.DB.Save(&level).Error; err != nil {
		return nil, apperrors.Internal("failed to update Sumsub level")
	}
	return &level, nil
}

func (s *Service) DeleteSumsubLevel(id uint) error {
	res := s.DB.Delete(&models.SumsubLevel{}, id)
	if res.Error != nil {
		return apperrors.Internal("failed to delete Sumsub level")
	}
	if res.RowsAffected == 0 {
		return apperrors.NotFound("Sumsub level not found")
	}
	return nil
}

// CreateDojaWidgetInput is the admin-provided shape of a new widget
// mapping - see models.DojaWidget's doc comment: Dojah's webhook payload
// identifies a submission by widget ID only, so an operator maps each of
// their dashboard's widget IDs to the level/applicant-type it represents.
type CreateDojaWidgetInput struct {
	ID        string
	Level     int
	Corporate bool
}

func (s *Service) CreateDojaWidget(input CreateDojaWidgetInput) (*models.DojaWidget, error) {
	if input.ID == "" {
		return nil, apperrors.BadRequest("id is required")
	}
	widget := models.DojaWidget{ID: input.ID, Level: input.Level, Corporate: input.Corporate}
	if err := s.DB.Create(&widget).Error; err != nil {
		return nil, apperrors.Conflict("a Doja widget with this ID already exists")
	}
	return &widget, nil
}

func (s *Service) UpdateDojaWidget(id string, level int, corporate bool) (*models.DojaWidget, error) {
	var widget models.DojaWidget
	if err := s.DB.First(&widget, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NotFound("Doja widget not found")
		}
		return nil, apperrors.Internal("failed to load Doja widget")
	}
	widget.Level = level
	widget.Corporate = corporate
	if err := s.DB.Save(&widget).Error; err != nil {
		return nil, apperrors.Internal("failed to update Doja widget")
	}
	return &widget, nil
}

func (s *Service) DeleteDojaWidget(id string) error {
	res := s.DB.Delete(&models.DojaWidget{}, "id = ?", id)
	if res.Error != nil {
		return apperrors.Internal("failed to delete Doja widget")
	}
	if res.RowsAffected == 0 {
		return apperrors.NotFound("Doja widget not found")
	}
	return nil
}
