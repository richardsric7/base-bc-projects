package models

// The types below are Sumsub API request/response shapes, not persisted
// models - ported near-verbatim from the original since they mirror
// Sumsub's actual REST contract (https://docs.sumsub.com/reference).

type SumsubInfo struct {
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	Country   string `json:"country,omitempty"`
}

type SumsubApplicant struct {
	ID             string     `json:"id,omitempty"`
	ExternalUserID string     `json:"externalUserId,omitempty"`
	Info           SumsubInfo `json:"info,omitempty"`
	FixedInfo      SumsubInfo `json:"fixedInfo,omitempty"`
	Review         struct {
		ReviewStatus string `json:"reviewStatus,omitempty"`
		ReviewResult struct {
			ReviewAnswer string `json:"reviewAnswer,omitempty"`
		} `json:"reviewResult,omitempty"`
	} `json:"review,omitempty"`
}

type SumsubAccessToken struct {
	Token  string `json:"token"`
	UserID string `json:"userId"`
}

// SumsubReviewResult is the "reviewResult" object inside a Sumsub webhook
// payload - see ReviewAnswer's doc comment on SumsubWebhookInput.
type SumsubReviewResult struct {
	ModerationComment string   `json:"moderationComment"`
	ClientComment     string   `json:"clientComment"`
	ReviewAnswer      string   `json:"reviewAnswer"`
	RejectLabels      []string `json:"rejectLabels"`
	ReviewRejectType  string   `json:"reviewRejectType"`
}

// SumsubWebhookInput is the payload Sumsub posts to the review-result
// webhook. ReviewAnswer is "GREEN" (approved), "RED" (rejected), or absent
// for an intermediate status update.
type SumsubWebhookInput struct {
	ApplicantID    string             `json:"applicantId"`
	InspectionID   string             `json:"inspectionId"`
	CorrelationID  string             `json:"correlationId"`
	ExternalUserID string             `json:"externalUserId"`
	LevelName      string             `json:"levelName"`
	Type           string             `json:"type"`
	ReviewResult   SumsubReviewResult `json:"reviewResult"`
	ReviewStatus   string             `json:"reviewStatus"`
	CreatedAtMs    string             `json:"createdAtMs"`
}
