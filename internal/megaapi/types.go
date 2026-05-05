package megaapi

// SendTextRequest matches POST {host}/rest/sendMessage/{instance_key}/text
type SendTextRequest struct {
	MessageData SendTextData `json:"messageData"`
}

type SendTextData struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

// ConfigWebhookRequest matches POST {host}/rest/webhook/{instance_key}/configWebhook
type ConfigWebhookRequest struct {
	MessageData ConfigWebhookData `json:"messageData"`
}

type ConfigWebhookData struct {
	WebhookURL     string `json:"webhookUrl"`
	WebhookEnabled bool   `json:"webhookEnabled"`
}

// Status is the parsed response of GET {host}/rest/instance/{instance_key}/me
type Status struct {
	Connected bool   `json:"connected"`
	Me        string `json:"me,omitempty"`
	Raw       []byte `json:"-"`
}

// Inbound webhook payload from megaAPI (parser is tolerant — ignores unknown fields).

type WebhookPayload struct {
	InstanceKey string           `json:"instance_key,omitempty"`
	Messages    []WebhookMessage `json:"messages"`
}

type WebhookMessage struct {
	Key              WebhookKey         `json:"key"`
	Message          WebhookMessageBody `json:"message"`
	MessageTimestamp int64              `json:"messageTimestamp"`
	PushName         string             `json:"pushName,omitempty"`
}

type WebhookKey struct {
	ID        string `json:"id"`
	RemoteJID string `json:"remoteJid"`
	FromMe    bool   `json:"fromMe"`
}

type WebhookMessageBody struct {
	Conversation string `json:"conversation,omitempty"`
}
