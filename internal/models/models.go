package models

import "time"

type Entry struct {
	ID        int64                  `json:"id"`
	Prompt    string                 `json:"prompt"`
	Response  string                 `json:"response"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at,omitempty"`
	UpdatedAt time.Time              `json:"updated_at,omitempty"`
}
