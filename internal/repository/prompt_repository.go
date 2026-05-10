package repository

import (
	"errors"
	"fmt"
	"time"

	"github.com/carreira-cloud/ai-microservice/internal/model"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("name already exists for this tenant")

// PromptRepository handles all DB operations for prompt templates and versions.
// Every method is tenant-scoped — cross-tenant access is not possible by design.
type PromptRepository struct{ db *gorm.DB }

func NewPromptRepository(db *gorm.DB) *PromptRepository {
	return &PromptRepository{db: db}
}

type CreateTemplateInput struct {
	TenantID     string
	Name         string
	Description  string
	SystemPrompt string
	Provider     string
	Model        string
	Temperature  float32
	MaxTokens    int
	CacheTTLSec  int
}

// Create creates a PromptTemplate and its initial PromptVersion in a transaction.
func (r *PromptRepository) Create(input CreateTemplateInput) (*model.PromptTemplate, error) {
	var tmpl *model.PromptTemplate
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// Check uniqueness (tenant_id, name)
		var count int64
		tx.Model(&model.PromptTemplate{}).
			Where("tenant_id = ? AND name = ?", input.TenantID, input.Name).
			Count(&count)
		if count > 0 {
			return ErrConflict
		}

		t := &model.PromptTemplate{
			ID:        uuid.NewString(),
			TenantID:  input.TenantID,
			Name:      input.Name,
			Description: input.Description,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := tx.Create(t).Error; err != nil {
			return err
		}

		v := &model.PromptVersion{
			ID:           uuid.NewString(),
			TemplateID:   t.ID,
			SystemPrompt: input.SystemPrompt,
			Provider:     orDefault(input.Provider, "copilot"),
			Model:        orDefault(input.Model, "gpt-4o"),
			Temperature:  orDefaultF32(input.Temperature, 0.3),
			MaxTokens:    orDefaultInt(input.MaxTokens, 1024),
			CacheTTLSec:  orDefaultInt(input.CacheTTLSec, 3600),
			Version:      1,
			CreatedAt:    time.Now().UTC(),
		}
		if err := tx.Create(v).Error; err != nil {
			return err
		}
		t.ActiveVersionID = v.ID
		t.UpdatedAt = time.Now().UTC()
		if err := tx.Save(t).Error; err != nil {
			return err
		}
		t.ActiveVersion = v
		tmpl = t
		return nil
	})
	return tmpl, err
}

// FindByID returns a template with its active version, scoped to tenantID.
func (r *PromptRepository) FindByID(tenantID, id string) (*model.PromptTemplate, error) {
	var t model.PromptTemplate
	err := r.db.Where("id = ? AND tenant_id = ?", id, tenantID).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// Load active version
	if t.ActiveVersionID != "" {
		var v model.PromptVersion
		if e := r.db.Where("id = ?", t.ActiveVersionID).First(&v).Error; e == nil {
			t.ActiveVersion = &v
		}
	}
	return &t, nil
}

// List returns all templates for the tenant.
func (r *PromptRepository) List(tenantID string) ([]model.PromptTemplate, error) {
	var list []model.PromptTemplate
	err := r.db.Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&list).Error
	return list, err
}

// CreateVersion adds a new version to an existing template and sets it as active.
func (r *PromptRepository) CreateVersion(tenantID, templateID string, input CreateTemplateInput) (*model.PromptVersion, error) {
	tmpl, err := r.FindByID(tenantID, templateID)
	if err != nil {
		return nil, err
	}

	var maxVersion int
	r.db.Model(&model.PromptVersion{}).
		Where("template_id = ?", templateID).
		Select("COALESCE(MAX(version), 0)").
		Scan(&maxVersion)

	v := &model.PromptVersion{
		ID:           uuid.NewString(),
		TemplateID:   templateID,
		SystemPrompt: input.SystemPrompt,
		Provider:     orDefault(input.Provider, tmpl.ActiveVersion.Provider),
		Model:        orDefault(input.Model, tmpl.ActiveVersion.Model),
		Temperature:  orDefaultF32(input.Temperature, tmpl.ActiveVersion.Temperature),
		MaxTokens:    orDefaultInt(input.MaxTokens, tmpl.ActiveVersion.MaxTokens),
		CacheTTLSec:  orDefaultInt(input.CacheTTLSec, tmpl.ActiveVersion.CacheTTLSec),
		Version:      maxVersion + 1,
		CreatedAt:    time.Now().UTC(),
	}

	return v, r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(v).Error; err != nil {
			return fmt.Errorf("create version: %w", err)
		}
		return tx.Model(&model.PromptTemplate{}).
			Where("id = ?", templateID).
			Updates(map[string]any{"active_version_id": v.ID, "updated_at": time.Now().UTC()}).Error
	})
}

// ListVersions returns all versions for a template, ordered by version DESC.
func (r *PromptRepository) ListVersions(tenantID, templateID string) ([]model.PromptVersion, error) {
	// Confirm template belongs to tenant
	if _, err := r.FindByID(tenantID, templateID); err != nil {
		return nil, err
	}
	var versions []model.PromptVersion
	err := r.db.Where("template_id = ?", templateID).Order("version DESC").Find(&versions).Error
	return versions, err
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
func orDefaultInt(n, def int) int {
	if n == 0 {
		return def
	}
	return n
}
func orDefaultF32(f, def float32) float32 {
	if f == 0 {
		return def
	}
	return f
}
