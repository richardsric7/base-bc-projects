package services

import "testing"

func TestCreatePackage_DuplicateIDConflicts(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.CreatePackage(CreatePackageInput{ID: "SILVER", PriorityOrder: 4}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := svc.CreatePackage(CreatePackageInput{ID: "SILVER", PriorityOrder: 4}); err == nil {
		t.Fatalf("expected a conflict for a duplicate package id")
	}
}

func TestSetPackageInactiveAndDelete(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.CreatePackage(CreatePackageInput{ID: "SILVER", PriorityOrder: 4}); err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}

	updated, err := svc.SetPackageInactive("SILVER", true)
	if err != nil {
		t.Fatalf("SetPackageInactive: %v", err)
	}
	if !updated.Inactive {
		t.Fatalf("expected the package to be marked inactive")
	}

	if err := svc.DeletePackage("SILVER"); err != nil {
		t.Fatalf("DeletePackage: %v", err)
	}
	if _, err := svc.SetPackageInactive("SILVER", false); err == nil {
		t.Fatalf("expected the package to be gone after deletion")
	}
}

func TestCreateTier_DuplicateIDConflicts(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.CreateTier(CreateTierInput{ID: "WEEKLY", CanExpire: true, PriorityOrder: 4}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := svc.CreateTier(CreateTierInput{ID: "WEEKLY", CanExpire: true, PriorityOrder: 4}); err == nil {
		t.Fatalf("expected a conflict for a duplicate tier id")
	}
}

func TestUpsertMembershipGrade_RejectsUnknownPackageOrTier(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.UpsertMembershipGrade("NOSUCHPACKAGE", "MONTHLY", 10); err == nil {
		t.Fatalf("expected an unknown package to be rejected")
	}
}

func TestUpsertMembershipGrade_CreateThenReprice(t *testing.T) {
	svc, _ := newTestService(t)
	if _, err := svc.CreatePackage(CreatePackageInput{ID: "SILVER", PriorityOrder: 4}); err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	if _, err := svc.CreateTier(CreateTierInput{ID: "WEEKLY", CanExpire: true, PriorityOrder: 4}); err != nil {
		t.Fatalf("CreateTier: %v", err)
	}

	grade, err := svc.UpsertMembershipGrade("SILVER", "WEEKLY", 5)
	if err != nil {
		t.Fatalf("UpsertMembershipGrade (create): %v", err)
	}
	if grade.PriceUSD != 5 {
		t.Fatalf("expected price 5, got %v", grade.PriceUSD)
	}

	repriced, err := svc.UpsertMembershipGrade("SILVER", "WEEKLY", 7.5)
	if err != nil {
		t.Fatalf("UpsertMembershipGrade (reprice): %v", err)
	}
	if repriced.ID != grade.ID {
		t.Fatalf("expected the same grade row to be updated in place, got a new ID")
	}
	if repriced.PriceUSD != 7.5 {
		t.Fatalf("expected price 7.5, got %v", repriced.PriceUSD)
	}
}

func TestSetPaymentAssetAllowed_CreateThenToggle(t *testing.T) {
	svc, _ := newTestService(t)
	asset, err := svc.SetPaymentAssetAllowed("DAI", false)
	if err != nil {
		t.Fatalf("SetPaymentAssetAllowed (create): %v", err)
	}
	if asset.Inactive {
		t.Fatalf("expected a newly allowed asset to not be inactive")
	}

	disabled, err := svc.SetPaymentAssetAllowed("DAI", true)
	if err != nil {
		t.Fatalf("SetPaymentAssetAllowed (disable): %v", err)
	}
	if !disabled.Inactive {
		t.Fatalf("expected the asset to be marked inactive")
	}
}
