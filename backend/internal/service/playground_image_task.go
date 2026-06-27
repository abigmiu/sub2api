package service

import (
	"context"
	"encoding/json"
	"io"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const settingKeyPlaygroundImageStorage = "playground_image_storage"

var ErrPlaygroundImageTaskNotFound = infraerrors.NotFound("PLAYGROUND_IMAGE_TASK_NOT_FOUND", "playground image task not found")

type PlaygroundImageTaskStatus string

const (
	PlaygroundImageTaskStatusPending   PlaygroundImageTaskStatus = "pending"
	PlaygroundImageTaskStatusRunning   PlaygroundImageTaskStatus = "running"
	PlaygroundImageTaskStatusSucceeded PlaygroundImageTaskStatus = "succeeded"
	PlaygroundImageTaskStatusFailed    PlaygroundImageTaskStatus = "failed"
)

type PlaygroundImageTask struct {
	ID                 string
	UserID             int64
	Status             PlaygroundImageTaskStatus
	RequestPath        string
	RequestContentType string
	RequestBody        []byte
	ErrorMessage       string
	ResultJSON         []byte
	CreatedAt          time.Time
	StartedAt          *time.Time
	FinishedAt         *time.Time
}

type PlaygroundImageTaskRepository interface {
	Create(ctx context.Context, task *PlaygroundImageTask) error
	GetByID(ctx context.Context, id string) (*PlaygroundImageTask, error)
	UpdateStatus(ctx context.Context, id string, status PlaygroundImageTaskStatus, errorMessage string, resultJSON []byte, startedAt, finishedAt *time.Time) error
}

type PlaygroundImageObjectStore interface {
	Upload(ctx context.Context, key string, body io.Reader, contentType string) (sizeBytes int64, err error)
}

type PlaygroundImageObjectStoreFactory func(ctx context.Context, cfg *PlaygroundImageStorageConfig) (PlaygroundImageObjectStore, error)

type PlaygroundImageStorageConfig struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key,omitempty"` //nolint:revive
	Prefix          string `json:"prefix"`
	ForcePathStyle  bool   `json:"force_path_style"`
	PublicBaseURL   string `json:"public_base_url"`
}

func (c *PlaygroundImageStorageConfig) IsConfigured() bool {
	return c != nil &&
		c.Bucket != "" &&
		c.AccessKeyID != "" &&
		c.SecretAccessKey != "" &&
		c.PublicBaseURL != ""
}

type PlaygroundImageTaskResult struct {
	Data []ImageResponseItem `json:"data"`
}

type ImageResponseItem struct {
	URL           string `json:"url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

var ErrPlaygroundImageStorageNotConfigured = infraerrors.BadRequest("PLAYGROUND_IMAGE_STORAGE_NOT_CONFIGURED", "playground image storage is not configured")

type PlaygroundImageTaskView struct {
	ID           string                    `json:"id"`
	Status       PlaygroundImageTaskStatus `json:"status"`
	ErrorMessage string                    `json:"error_message,omitempty"`
	Data         []ImageResponseItem       `json:"data,omitempty"`
	CreatedAt    int64                     `json:"created_at"`
	StartedAt    *int64                    `json:"started_at,omitempty"`
	FinishedAt   *int64                    `json:"finished_at,omitempty"`
}

func (t *PlaygroundImageTask) ToView() PlaygroundImageTaskView {
	view := PlaygroundImageTaskView{
		ID:           t.ID,
		Status:       t.Status,
		ErrorMessage: t.ErrorMessage,
		CreatedAt:    t.CreatedAt.UnixMilli(),
	}
	if t.StartedAt != nil {
		ms := t.StartedAt.UnixMilli()
		view.StartedAt = &ms
	}
	if t.FinishedAt != nil {
		ms := t.FinishedAt.UnixMilli()
		view.FinishedAt = &ms
	}
	if len(t.ResultJSON) > 0 {
		var result PlaygroundImageTaskResult
		if err := json.Unmarshal(t.ResultJSON, &result); err == nil {
			view.Data = result.Data
		}
	}
	return view
}
