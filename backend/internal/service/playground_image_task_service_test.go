//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type playgroundImageTaskRepoStub struct {
	task *PlaygroundImageTask
}

func (r *playgroundImageTaskRepoStub) Create(_ context.Context, task *PlaygroundImageTask) error {
	r.task = task
	return nil
}

func (r *playgroundImageTaskRepoStub) GetByID(_ context.Context, id string) (*PlaygroundImageTask, error) {
	if r.task == nil || r.task.ID != id {
		return nil, ErrPlaygroundImageTaskNotFound
	}
	return r.task, nil
}

func (r *playgroundImageTaskRepoStub) UpdateStatus(_ context.Context, id string, status PlaygroundImageTaskStatus, errorMessage string, resultJSON []byte, startedAt, finishedAt *time.Time) error {
	if r.task == nil || r.task.ID != id {
		return ErrPlaygroundImageTaskNotFound
	}
	r.task.Status = status
	r.task.ErrorMessage = errorMessage
	r.task.ResultJSON = resultJSON
	r.task.StartedAt = startedAt
	r.task.FinishedAt = finishedAt
	return nil
}

type playgroundImageStoreStub struct {
	result *PlaygroundImageUploadResult
	keys   []string
}

func (s *playgroundImageStoreStub) Upload(_ context.Context, key string, body io.Reader, contentType string) (*PlaygroundImageUploadResult, error) {
	if _, err := io.Copy(io.Discard, body); err != nil {
		return nil, err
	}
	s.keys = append(s.keys, key+"|"+contentType)
	return s.result, nil
}

func TestPlaygroundImageTaskServiceCompleteTaskUsesStoreURL(t *testing.T) {
	repo := &playgroundImageTaskRepoStub{
		task: &PlaygroundImageTask{
			ID:        "task-1",
			UserID:    1,
			Status:    PlaygroundImageTaskStatusRunning,
			CreatedAt: time.Now(),
		},
	}
	settingRepo := newMockSettingRepo()
	cfgJSON, err := json.Marshal(PlaygroundImageStorageConfig{
		Provider:       "cloudfile",
		CloudFileHost:  "https://cloudfile.example.com",
		CloudFileAppID: "app-id",
		CloudFileToken: "secret",
	})
	require.NoError(t, err)
	require.NoError(t, settingRepo.Set(context.Background(), settingKeyPlaygroundImageStorage, string(cfgJSON)))

	store := &playgroundImageStoreStub{
		result: &PlaygroundImageUploadResult{
			SizeBytes: 4,
			URL:       "https://cdn.example.com/final.png",
		},
	}
	svc := NewPlaygroundImageTaskService(repo, settingRepo, func(ctx context.Context, cfg *PlaygroundImageStorageConfig) (PlaygroundImageObjectStore, error) {
		return store, nil
	})

	payload := []byte(`{"data":[{"b64_json":"AQIDBA==","revised_prompt":"done"}]}`)
	require.NoError(t, svc.CompleteTask(context.Background(), "task-1", payload))

	var result PlaygroundImageTaskResult
	require.NoError(t, json.Unmarshal(repo.task.ResultJSON, &result))
	require.Len(t, result.Data, 1)
	require.Equal(t, "https://cdn.example.com/final.png", result.Data[0].URL)
	require.Equal(t, "done", result.Data[0].RevisedPrompt)
	require.Equal(t, []string{"playground-images/task-1-01.png|image/png"}, store.keys)
}

func TestPlaygroundImageTaskServiceCreateTaskClonesHeaders(t *testing.T) {
	repo := &playgroundImageTaskRepoStub{}
	svc := NewPlaygroundImageTaskService(repo, nil, nil)

	headers := http.Header{
		"Accept-Language": []string{"zh-CN"},
		"User-Agent":      []string{"test-agent"},
	}
	task, err := svc.CreateTask(context.Background(), 7, "/v1/images/generations", "application/json", headers, []byte(`{"prompt":"猫"}`))
	require.NoError(t, err)
	require.NotNil(t, task)
	require.Equal(t, []string{"zh-CN"}, repo.task.RequestHeaders["Accept-Language"])
	require.Equal(t, []string{"test-agent"}, repo.task.RequestHeaders["User-Agent"])

	headers.Set("User-Agent", "mutated")
	require.Equal(t, []string{"test-agent"}, repo.task.RequestHeaders["User-Agent"])
}
