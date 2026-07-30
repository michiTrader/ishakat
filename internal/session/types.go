package session

import "time"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type TokenUsage struct {
	PromptTokens     int     `json:"prompt_tokens,omitempty"`
	CompletionTokens int     `json:"completion_tokens,omitempty"`
	TotalTokens      int     `json:"total_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
}

type Message struct {
	ID        string     `json:"id"`
	Role      Role       `json:"role"`
	Content   string     `json:"content"`
	Reasoning string     `json:"reasoning,omitempty"`
	Model     string     `json:"model,omitempty"`
	Timestamp time.Time  `json:"timestamp"`
	Usage     TokenUsage `json:"usage,omitempty"`
}

type Header struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Model     string    `json:"model"`
}

type Session struct {
	Header
	Messages []Message `json:"messages,omitempty"`
}

type RecordType string

const (
	RecordHeader  RecordType = "header"
	RecordMessage RecordType = "message"
)

type LineRecord struct {
	Type    RecordType `json:"type"`
	Header  *Header    `json:"header,omitempty"`
	Message *Message   `json:"message,omitempty"`
}
