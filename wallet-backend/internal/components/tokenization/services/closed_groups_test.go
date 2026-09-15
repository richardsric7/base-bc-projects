package services

import (
	"testing"

	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	"wallet-backend/internal/components/tokenization/models"
)

func TestCreatePrivateOfferingGroup_OnlyInitiatorMayCreate(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	initiator := createTestUser(t, db, "initiator", true)
	other := createTestUser(t, db, "other", true)

	asset := models.TokenizedAsset{
		InitiatorUserID: initiator.ID,
		OfferingType:    models.OfferingPrivate,
	}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	if _, err := svc.CreatePrivateOfferingGroup(other.ID, asset.ID, "my investors"); err == nil {
		t.Fatal("expected an error when a non-initiator tries to create the access group")
	}

	group, err := svc.CreatePrivateOfferingGroup(initiator.ID, asset.ID, "my investors")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if group.Purpose != sharedaccessModels.PurposePrivateOffering {
		t.Fatalf("expected PurposePrivateOffering, got %v", group.Purpose)
	}
	if group.Address != nil {
		t.Fatalf("expected a private-offering group to have no on-chain address, got %v", *group.Address)
	}

	var reloaded models.TokenizedAsset
	if err := db.First(&reloaded, asset.ID).Error; err != nil {
		t.Fatalf("reload asset: %v", err)
	}
	if reloaded.ClosedGroupID == nil || *reloaded.ClosedGroupID != group.ID {
		t.Fatalf("expected asset.ClosedGroupID to be set to the new group, got %v", reloaded.ClosedGroupID)
	}

	if _, err := svc.CreatePrivateOfferingGroup(initiator.ID, asset.ID, "second group"); err == nil {
		t.Fatal("expected an error creating a second access group for the same offering")
	}
}

func TestCreatePrivateOfferingGroup_RejectsPublicOffering(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	initiator := createTestUser(t, db, "initiator", true)
	asset := models.TokenizedAsset{InitiatorUserID: initiator.ID, OfferingType: models.OfferingPublic}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatalf("seed asset: %v", err)
	}
	if _, err := svc.CreatePrivateOfferingGroup(initiator.ID, asset.ID, "my investors"); err == nil {
		t.Fatal("expected an error creating an access group for a public offering")
	}
}

func TestAddRemoveListPrivateOfferingMember(t *testing.T) {
	svc, db := newTestService(t, &fakeBlockchain{})
	initiator := createTestUser(t, db, "initiator", true)
	investor := createTestUser(t, db, "investor", true)
	other := createTestUser(t, db, "other", true)

	asset := models.TokenizedAsset{InitiatorUserID: initiator.ID, OfferingType: models.OfferingPrivate}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatalf("seed asset: %v", err)
	}

	if err := svc.AddPrivateOfferingMember(initiator.ID, asset.ID, "investor"); err == nil {
		t.Fatal("expected an error adding a member before the group exists")
	}

	if _, err := svc.CreatePrivateOfferingGroup(initiator.ID, asset.ID, "my investors"); err != nil {
		t.Fatalf("CreatePrivateOfferingGroup: %v", err)
	}

	if err := svc.AddPrivateOfferingMember(other.ID, asset.ID, "investor"); err == nil {
		t.Fatal("expected an error when a non-initiator tries to add a member")
	}
	if err := svc.AddPrivateOfferingMember(initiator.ID, asset.ID, "nobody"); err == nil {
		t.Fatal("expected an error adding an unregistered username")
	}
	if err := svc.AddPrivateOfferingMember(initiator.ID, asset.ID, "investor"); err != nil {
		t.Fatalf("AddPrivateOfferingMember: %v", err)
	}
	if err := svc.AddPrivateOfferingMember(initiator.ID, asset.ID, "investor"); err == nil {
		t.Fatal("expected an error adding the same member twice")
	}

	members, err := svc.ListPrivateOfferingMembers(asset.ID)
	if err != nil {
		t.Fatalf("ListPrivateOfferingMembers: %v", err)
	}
	if len(members) != 1 || members[0] != "investor" {
		t.Fatalf("unexpected members: %+v", members)
	}

	var reloaded models.TokenizedAsset
	if err := db.First(&reloaded, asset.ID).Error; err != nil {
		t.Fatalf("reload asset: %v", err)
	}
	if err := svc.enforceOfferingAccess(&reloaded, investor.Address); err != nil {
		t.Fatalf("expected the added member to pass enforceOfferingAccess: %v", err)
	}
	if err := svc.enforceOfferingAccess(&reloaded, other.Address); err == nil {
		t.Fatal("expected a non-member to fail enforceOfferingAccess")
	}

	if err := svc.RemovePrivateOfferingMember(other.ID, asset.ID, "investor"); err == nil {
		t.Fatal("expected an error when a non-initiator tries to remove a member")
	}
	if err := svc.RemovePrivateOfferingMember(initiator.ID, asset.ID, "investor"); err != nil {
		t.Fatalf("RemovePrivateOfferingMember: %v", err)
	}
	if err := svc.RemovePrivateOfferingMember(initiator.ID, asset.ID, "investor"); err == nil {
		t.Fatal("expected an error removing a member who is no longer in the group")
	}

	members, err = svc.ListPrivateOfferingMembers(asset.ID)
	if err != nil {
		t.Fatalf("ListPrivateOfferingMembers: %v", err)
	}
	if len(members) != 0 {
		t.Fatalf("expected no members after removal, got %+v", members)
	}
}
