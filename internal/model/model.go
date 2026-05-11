package model

import "time"

// PromptTemplate is the top-level prompt entity, owned by a tenant.
// Uniqueness is enforced at DB level AND via GORM AutoMigrate as (tenant_id, name).
type PromptTemplate struct {
	ID              string    `gorm:"primaryKey;type:varchar(36)"                              json:"id"`
	Name            string    `gorm:"not null;uniqueIndex:idx_template_tenant_name"            json:"name"`
	Description     string    `gorm:"type:text"                                                json:"description,omitempty"`
	ActiveVersionID string    `gorm:"type:varchar(36)"                                         json:"active_version_id,omitempty"`
	TenantID        string    `gorm:"type:varchar(64);not null;index;uniqueIndex:idx_template_tenant_name" json:"tenant_id"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	// Preloaded by FindByID.
	ActiveVersion *PromptVersion `gorm:"foreignKey:ID;references:ActiveVersionID" json:"active_version,omitempty"`
}

// PromptVersion holds a versioned snapshot of a prompt template's configuration.
type PromptVersion struct {
	ID           string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	TemplateID   string    `gorm:"type:varchar(36);not null;index" json:"template_id"`
	SystemPrompt string    `gorm:"type:text;not null"          json:"system_prompt"`
	Provider     string    `gorm:"type:varchar(32);default:'copilot'" json:"provider"`
	Model        string    `gorm:"type:varchar(64);default:'gpt-4o'"  json:"model"`
	Temperature  float32   `gorm:"default:0.3"                 json:"temperature"`
	MaxTokens    int       `gorm:"default:1024"                json:"max_tokens"`
	CacheTTLSec  int       `gorm:"default:3600"                json:"cache_ttl_sec"`
	Version      int       `gorm:"not null"                    json:"version"`
	CreatedAt    time.Time `json:"created_at"`
}

// AuditLog records every LLM call for observability and compliance.
// Per-tenant detail is queried here, not via Prometheus labels.
type AuditLog struct {
	ID           string    `gorm:"primaryKey;type:varchar(36)" json:"id"`
	TenantID     string    `gorm:"type:varchar(64);not null;index:idx_audit_tenant_time" json:"tenant_id"`
	TemplateID   string    `gorm:"type:varchar(36);index"      json:"template_id,omitempty"`
	Provider     string    `gorm:"type:varchar(32)"            json:"provider"`
	Model        string    `gorm:"type:varchar(64)"            json:"model"`
	PromptHash   string    `gorm:"type:varchar(64)"            json:"prompt_hash"`
	ResponseHash string    `gorm:"type:varchar(64)"            json:"response_hash,omitempty"`
	LatencyMs    int64     `json:"latency_ms"`
	Cached       bool      `json:"cached"`
	Idempotent   bool      `json:"idempotent"`
	Error        string    `gorm:"type:text"                   json:"error,omitempty"`
	CreatedAt    time.Time `gorm:"index:idx_audit_tenant_time" json:"created_at"`
}

func (AuditLog) TableName() string { return "ai_audit_logs" }
