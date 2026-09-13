package services

import (
	"io"
	"strings"
	"testing"
)

func TestUploadStakeholderDocument_RoundTrip(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme"})

	doc, err := svc.UploadStakeholderDocument(link.ID, "director-id.pdf", "application/pdf", strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("UploadStakeholderDocument: %v", err)
	}
	if doc.SHA256 == "" {
		t.Fatalf("expected a content hash to be recorded")
	}

	_, content, err := svc.GetStakeholderDocument(link.ID, doc.ID)
	if err != nil {
		t.Fatalf("GetStakeholderDocument: %v", err)
	}
	defer content.Close()
	body, err := io.ReadAll(content)
	if err != nil {
		t.Fatalf("read document content: %v", err)
	}
	if string(body) != "hello world" {
		t.Fatalf("expected round-tripped content to match, got %q", string(body))
	}
}

// This is the fix for the original's IDOR: the document store had no
// per-tenant ownership record at all, so any service link could fetch or
// delete any other tenant's documents by guessing an ID (PLAN.md §4.11
// finding 8).
func TestGetStakeholderDocument_RejectsOtherServiceLink(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	linkA, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme"})
	linkB, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "globex"})

	doc, err := svc.UploadStakeholderDocument(linkA.ID, "director-id.pdf", "application/pdf", strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("UploadStakeholderDocument: %v", err)
	}

	_, _, err = svc.GetStakeholderDocument(linkB.ID, doc.ID)
	if err == nil {
		t.Fatalf("expected a different service link to be denied access to this document")
	}
	if got := statusOf(t, err); got != 403 {
		t.Fatalf("expected 403, got %d", got)
	}
}

func TestDeleteStakeholderDocument_RejectsOtherServiceLink(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	linkA, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme"})
	linkB, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "globex"})

	doc, err := svc.UploadStakeholderDocument(linkA.ID, "director-id.pdf", "application/pdf", strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("UploadStakeholderDocument: %v", err)
	}

	if err := svc.DeleteStakeholderDocument(linkB.ID, doc.ID); err == nil {
		t.Fatalf("expected a different service link to be denied deletion of this document")
	}

	// The document must still be retrievable by its actual owner afterward.
	if _, content, err := svc.GetStakeholderDocument(linkA.ID, doc.ID); err != nil {
		t.Fatalf("expected the owning service link to still retrieve the document: %v", err)
	} else {
		content.Close()
	}
}

func TestDeleteStakeholderDocument_OwnerCanDelete(t *testing.T) {
	svc, db := newTestService(t)
	owner := createTestUser(t, db, "owner1", "0x1111111111111111111111111111111111111111", nil)
	link, _, _ := svc.CreateServiceLink(CreateServiceLinkInput{OwnerUserID: owner.ID, ShortName: "acme"})

	doc, err := svc.UploadStakeholderDocument(link.ID, "director-id.pdf", "application/pdf", strings.NewReader("hello world"))
	if err != nil {
		t.Fatalf("UploadStakeholderDocument: %v", err)
	}

	if err := svc.DeleteStakeholderDocument(link.ID, doc.ID); err != nil {
		t.Fatalf("DeleteStakeholderDocument: %v", err)
	}
	if _, _, err := svc.GetStakeholderDocument(link.ID, doc.ID); err == nil {
		t.Fatalf("expected the document to be gone after deletion")
	}
}
