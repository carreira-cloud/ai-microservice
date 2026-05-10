package audit

import (
	"context"
	"time"

	"github.com/carreira-cloud/ai-microservice/internal/model"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// Entry is a single audit record enqueued for async persistence.
type Entry struct {
	TenantID     string
	TemplateID   string
	Provider     string
	Model        string
	PromptHash   string
	ResponseHash string
	LatencyMs    int64
	Cached       bool
	Idempotent   bool
	Error        string
}

// Worker drains audit entries from a buffered channel into the DB.
// Call Start() to begin processing, Drain(ctx) on shutdown.
type Worker struct {
	ch   chan Entry
	db   *gorm.DB
	done chan struct{}
}

// NewWorker creates a Worker with a buffered channel of bufSize entries.
func NewWorker(db *gorm.DB, bufSize int) *Worker {
	return &Worker{
		ch:   make(chan Entry, bufSize),
		db:   db,
		done: make(chan struct{}),
	}
}

// Start launches the background goroutine. Call once on startup.
func (w *Worker) Start() {
	go func() {
		defer close(w.done)
		for e := range w.ch {
			w.persist(e)
		}
	}()
}

// Enqueue adds an entry to the buffer. Non-blocking: drops and logs if full.
func (w *Worker) Enqueue(e Entry) {
	select {
	case w.ch <- e:
	default:
		logrus.Warn("audit: channel full — dropping entry")
	}
}

// Drain closes the channel and waits for all pending entries to be persisted.
// Respects ctx deadline; logs a warning if entries are lost on timeout.
func (w *Worker) Drain(ctx context.Context) {
	close(w.ch)
	select {
	case <-w.done:
	case <-ctx.Done():
		logrus.Warn("audit: drain timeout — some entries may be lost")
	}
}

func (w *Worker) persist(e Entry) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	record := model.AuditLog{
		ID:           uuid.NewString(),
		TenantID:     e.TenantID,
		TemplateID:   e.TemplateID,
		Provider:     e.Provider,
		Model:        e.Model,
		PromptHash:   e.PromptHash,
		ResponseHash: e.ResponseHash,
		LatencyMs:    e.LatencyMs,
		Cached:       e.Cached,
		Idempotent:   e.Idempotent,
		Error:        e.Error,
		CreatedAt:    time.Now().UTC(),
	}
	if err := w.db.WithContext(ctx).Create(&record).Error; err != nil {
		logrus.WithError(err).Warn("audit: insert failed")
	}
}
