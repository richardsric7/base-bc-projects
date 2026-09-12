package models

// FlutterwaveWebhookPayload is the payload Flutterwave posts to the
// payment webhook, trimmed to the fields this component acts on. Ported
// from the original's FlutterwaveWebhook.
type FlutterwaveWebhookPayload struct {
	Event string `json:"event"`
	Data  struct {
		TxRef    string  `json:"tx_ref"`
		Amount   float64 `json:"amount"`
		Currency string  `json:"currency"`
		Status   string  `json:"status"` // "successful" on a completed charge
	} `json:"data"`
	MetaData struct {
		UserID  string `json:"user_id"`
		Product string `json:"product"` // "activation" or "asset purchase"
	} `json:"meta_data"`
}
