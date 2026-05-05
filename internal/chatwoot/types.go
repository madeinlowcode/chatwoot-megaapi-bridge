package chatwoot

// Config carries everything the client needs to call a Chatwoot account/inbox.
type Config struct {
	BaseURL         string
	APIToken        string // plaintext (decrypted by caller)
	AccountID       int32
	InboxID         int32
	InboxIdentifier string // optional: API channel identifier
}

type Contact struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	PhoneNumber string `json:"phone_number,omitempty"`
	Identifier  string `json:"identifier,omitempty"`
	Email       string `json:"email,omitempty"`
}

type SearchContactsResponse struct {
	Payload []Contact `json:"payload"`
	Meta    struct {
		Count       int `json:"count"`
		CurrentPage int `json:"current_page"`
	} `json:"meta"`
}

type CreateContactRequest struct {
	InboxID     int32  `json:"inbox_id"`
	Name        string `json:"name"`
	PhoneNumber string `json:"phone_number,omitempty"`
	Identifier  string `json:"identifier,omitempty"`
}

type CreateContactResponse struct {
	Payload struct {
		Contact Contact `json:"contact"`
	} `json:"payload"`
}

type Conversation struct {
	ID        int64  `json:"id"`
	AccountID int32  `json:"account_id,omitempty"`
	InboxID   int32  `json:"inbox_id,omitempty"`
	Status    string `json:"status,omitempty"`
}

type CreateConversationRequest struct {
	SourceID  string `json:"source_id"`
	InboxID   int32  `json:"inbox_id"`
	ContactID int64  `json:"contact_id"`
	Status    string `json:"status,omitempty"`
}

type CreateMessageRequest struct {
	Content           string         `json:"content"`
	MessageType       string         `json:"message_type"`
	Private           bool           `json:"private"`
	ContentAttributes map[string]any `json:"content_attributes,omitempty"`
}

type Message struct {
	ID             int64  `json:"id"`
	Content        string `json:"content"`
	MessageType    any    `json:"message_type"` // chatwoot returns int OR string depending on version
	ConversationID int64  `json:"conversation_id"`
}
