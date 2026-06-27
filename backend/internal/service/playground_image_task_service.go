package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

type PlaygroundImageTaskService struct {
	repo         PlaygroundImageTaskRepository
	settingRepo  SettingRepository
	storeFactory PlaygroundImageObjectStoreFactory
}

func NewPlaygroundImageTaskService(
	repo PlaygroundImageTaskRepository,
	settingRepo SettingRepository,
	storeFactory PlaygroundImageObjectStoreFactory,
) *PlaygroundImageTaskService {
	return &PlaygroundImageTaskService{
		repo:         repo,
		settingRepo:  settingRepo,
		storeFactory: storeFactory,
	}
}

func (s *PlaygroundImageTaskService) CreateTask(ctx context.Context, userID int64, requestPath, contentType string, body []byte) (*PlaygroundImageTask, error) {
	task := &PlaygroundImageTask{
		ID:                 uuid.NewString(),
		UserID:             userID,
		Status:             PlaygroundImageTaskStatusPending,
		RequestPath:        requestPath,
		RequestContentType: contentType,
		RequestBody:        append([]byte(nil), body...),
		CreatedAt:          time.Now(),
	}
	if err := s.repo.Create(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *PlaygroundImageTaskService) GetTask(ctx context.Context, userID int64, taskID string) (*PlaygroundImageTask, error) {
	task, err := s.repo.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if task.UserID != userID {
		return nil, ErrPlaygroundImageTaskNotFound
	}
	return task, nil
}

func (s *PlaygroundImageTaskService) MarkTaskRunning(ctx context.Context, taskID string) (*PlaygroundImageTask, error) {
	task, err := s.repo.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	if err := s.repo.UpdateStatus(ctx, task.ID, PlaygroundImageTaskStatusRunning, "", nil, &startedAt, nil); err != nil {
		return nil, err
	}
	task.Status = PlaygroundImageTaskStatusRunning
	task.StartedAt = &startedAt
	return task, nil
}

func (s *PlaygroundImageTaskService) CompleteTask(ctx context.Context, taskID string, payload []byte) error {
	resultJSON, err := s.persistTaskResult(ctx, taskID, payload)
	if err != nil {
		return err
	}
	finishedAt := time.Now()
	return s.repo.UpdateStatus(ctx, taskID, PlaygroundImageTaskStatusSucceeded, "", resultJSON, nil, &finishedAt)
}

func (s *PlaygroundImageTaskService) failTask(ctx context.Context, taskID string, message string) {
	finishedAt := time.Now()
	_ = s.repo.UpdateStatus(ctx, taskID, PlaygroundImageTaskStatusFailed, strings.TrimSpace(message), nil, nil, &finishedAt)
}

func (s *PlaygroundImageTaskService) FailTask(ctx context.Context, taskID string, message string) {
	s.failTask(ctx, taskID, message)
}

func (s *PlaygroundImageTaskService) persistTaskResult(ctx context.Context, taskID string, payload []byte) ([]byte, error) {
	storageCfg, err := s.loadStorageConfig(ctx)
	if err != nil {
		return nil, err
	}
	store, err := s.storeFactory(ctx, storageCfg)
	if err != nil {
		return nil, err
	}

	type rawItem struct {
		B64JSON       string `json:"b64_json"`
		RevisedPrompt string `json:"revised_prompt"`
	}
	var response struct {
		Data []rawItem `json:"data"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, fmt.Errorf("parse image response: %w", err)
	}
	if len(response.Data) == 0 {
		return nil, fmt.Errorf("image response returned no data")
	}

	result := PlaygroundImageTaskResult{
		Data: make([]ImageResponseItem, 0, len(response.Data)),
	}
	for index, item := range response.Data {
		if strings.TrimSpace(item.B64JSON) == "" {
			return nil, fmt.Errorf("image response returned empty b64_json")
		}
		reader, contentType, err := decodeImageData(item.B64JSON)
		if err != nil {
			return nil, err
		}
		ext := imageExtensionFromContentType(contentType)
		objectKey := path.Join(strings.Trim(storageCfg.Prefix, "/"), "playground-images", taskID, fmt.Sprintf("%02d.%s", index+1, ext))
		if _, err := store.Upload(ctx, objectKey, reader, contentType); err != nil {
			return nil, err
		}
		result.Data = append(result.Data, ImageResponseItem{
			URL:           strings.TrimRight(storageCfg.PublicBaseURL, "/") + "/" + objectKey,
			RevisedPrompt: item.RevisedPrompt,
		})
	}
	return json.Marshal(result)
}

func (s *PlaygroundImageTaskService) loadStorageConfig(ctx context.Context) (*PlaygroundImageStorageConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyPlaygroundImageStorage)
	if err != nil {
		if err == ErrSettingNotFound {
			return nil, ErrPlaygroundImageStorageNotConfigured
		}
		return nil, err
	}
	var cfg PlaygroundImageStorageConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	if !cfg.IsConfigured() {
		return nil, ErrPlaygroundImageStorageNotConfigured
	}
	return &cfg, nil
}

func decodeImageData(b64 string) (*bytes.Reader, string, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, "", fmt.Errorf("decode image data: %w", err)
	}
	contentType := http.DetectContentType(data)
	if !strings.HasPrefix(contentType, "image/") {
		contentType = "image/png"
	}
	return bytes.NewReader(data), contentType, nil
}

func imageExtensionFromContentType(contentType string) string {
	switch strings.TrimSpace(strings.ToLower(contentType)) {
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

func extractPlaygroundTaskError(body []byte, statusCode int) string {
	if len(body) == 0 {
		return fmt.Sprintf("image task failed with status %d", statusCode)
	}
	message := strings.TrimSpace(gjson.GetBytes(body, "error.message").String())
	if message != "" {
		return message
	}
	message = strings.TrimSpace(gjson.GetBytes(body, "message").String())
	if message != "" {
		return message
	}
	return strings.TrimSpace(string(body))
}
