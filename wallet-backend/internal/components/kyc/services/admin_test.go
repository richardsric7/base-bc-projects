package services

import "testing"

func newAdminTestService(t *testing.T) *Service {
	t.Helper()
	db := newTestDB(t)
	return New(db, "https://api.sumsub.com", "token", "secret", "doja-secret")
}

func TestCreateSumsubLevel_DuplicateNameConflicts(t *testing.T) {
	svc := newAdminTestService(t)
	if _, err := svc.CreateSumsubLevel(CreateSumsubLevelInput{Name: "level-1"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := svc.CreateSumsubLevel(CreateSumsubLevelInput{Name: "level-1"}); err == nil {
		t.Fatalf("expected a conflict for a duplicate level name")
	}
}

func TestUpdateAndDeleteSumsubLevel(t *testing.T) {
	svc := newAdminTestService(t)
	level, err := svc.CreateSumsubLevel(CreateSumsubLevelInput{Name: "level-1", Description: "old"})
	if err != nil {
		t.Fatalf("CreateSumsubLevel: %v", err)
	}

	updated, err := svc.UpdateSumsubLevel(level.ID, "new description")
	if err != nil {
		t.Fatalf("UpdateSumsubLevel: %v", err)
	}
	if updated.Description != "new description" {
		t.Fatalf("expected description to be updated, got %q", updated.Description)
	}

	if err := svc.DeleteSumsubLevel(level.ID); err != nil {
		t.Fatalf("DeleteSumsubLevel: %v", err)
	}
	if _, err := svc.UpdateSumsubLevel(level.ID, "x"); err == nil {
		t.Fatalf("expected the level to be gone after deletion")
	}
}

func TestCreateDojaWidget_DuplicateIDConflicts(t *testing.T) {
	svc := newAdminTestService(t)
	if _, err := svc.CreateDojaWidget(CreateDojaWidgetInput{ID: "widget-1", Level: 1}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := svc.CreateDojaWidget(CreateDojaWidgetInput{ID: "widget-1", Level: 2}); err == nil {
		t.Fatalf("expected a conflict for a duplicate widget ID")
	}
}

func TestUpdateAndDeleteDojaWidget(t *testing.T) {
	svc := newAdminTestService(t)
	widget, err := svc.CreateDojaWidget(CreateDojaWidgetInput{ID: "widget-1", Level: 1, Corporate: false})
	if err != nil {
		t.Fatalf("CreateDojaWidget: %v", err)
	}

	updated, err := svc.UpdateDojaWidget(widget.ID, 2, true)
	if err != nil {
		t.Fatalf("UpdateDojaWidget: %v", err)
	}
	if updated.Level != 2 || !updated.Corporate {
		t.Fatalf("expected the widget to be updated, got %+v", updated)
	}

	if err := svc.DeleteDojaWidget(widget.ID); err != nil {
		t.Fatalf("DeleteDojaWidget: %v", err)
	}
	if _, err := svc.UpdateDojaWidget(widget.ID, 3, false); err == nil {
		t.Fatalf("expected the widget to be gone after deletion")
	}
}
