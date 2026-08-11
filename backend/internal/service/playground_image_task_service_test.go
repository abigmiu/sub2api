//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
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
	bodies [][]byte
}

func (s *playgroundImageStoreStub) Upload(_ context.Context, key string, body io.Reader, contentType string) (*PlaygroundImageUploadResult, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	s.keys = append(s.keys, key+"|"+contentType)
	s.bodies = append(s.bodies, data)
	return s.result, nil
}

type playgroundImageRoundTripFunc func(*http.Request) (*http.Response, error)

func (f playgroundImageRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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
	}, nil)

	payload := []byte(`{"data":[{"b64_json":"AQIDBA==","revised_prompt":"done"}]}`)
	require.NoError(t, svc.CompleteTask(context.Background(), "task-1", payload))

	var result PlaygroundImageTaskResult
	require.NoError(t, json.Unmarshal(repo.task.ResultJSON, &result))
	require.Len(t, result.Data, 1)
	require.Equal(t, "https://cdn.example.com/final.png", result.Data[0].URL)
	require.Equal(t, "done", result.Data[0].RevisedPrompt)
	require.Equal(t, []string{"playground-images/task-1-01.png|image/png"}, store.keys)
	require.Equal(t, [][]byte{{1, 2, 3, 4}}, store.bodies)
}

func TestPlaygroundImageTaskServiceCompleteTaskDownloadsURL(t *testing.T) {
	repo := &playgroundImageTaskRepoStub{
		task: &PlaygroundImageTask{
			ID:        "task-2",
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
		result: &PlaygroundImageUploadResult{URL: "https://cdn.example.com/final.png"},
	}
	svc := NewPlaygroundImageTaskService(repo, settingRepo, func(context.Context, *PlaygroundImageStorageConfig) (PlaygroundImageObjectStore, error) {
		return store, nil
	}, nil)
	png := []byte("\x89PNG\r\n\x1a\nimage-data")
	svc.downloadClient = &http.Client{Transport: playgroundImageRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "https://images.example.com/generated/image.png", req.URL.String())
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(png)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	payload := []byte(`{"data":[{"url":"https://images.example.com/generated/image.png","revised_prompt":"done"}]}`)
	require.NoError(t, svc.CompleteTask(context.Background(), "task-2", payload))

	var result PlaygroundImageTaskResult
	require.NoError(t, json.Unmarshal(repo.task.ResultJSON, &result))
	require.Equal(t, "https://cdn.example.com/final.png", result.Data[0].URL)
	require.Equal(t, []string{"playground-images/task-2-01.png|image/png"}, store.keys)
	require.Equal(t, [][]byte{png}, store.bodies)
}

func TestPlaygroundImageTaskServiceCompleteTaskRejectsMissingImageData(t *testing.T) {
	repo := &playgroundImageTaskRepoStub{
		task: &PlaygroundImageTask{ID: "task-3", Status: PlaygroundImageTaskStatusRunning},
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
	svc := NewPlaygroundImageTaskService(repo, settingRepo, func(context.Context, *PlaygroundImageStorageConfig) (PlaygroundImageObjectStore, error) {
		return &playgroundImageStoreStub{}, nil
	}, nil)

	err = svc.CompleteTask(context.Background(), "task-3", []byte(`{"data":[{}]}`))
	require.EqualError(t, err, "image response item has neither b64_json nor url")
}

func TestPlaygroundImageTaskServiceDownloadRejectsNonImage(t *testing.T) {
	svc := &PlaygroundImageTaskService{
		downloadClient: &http.Client{Transport: playgroundImageRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("not an image")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		})},
	}

	_, _, err := svc.downloadImage(context.Background(), "https://images.example.com/not-image")
	require.EqualError(t, err, "downloaded content is not an image")
}

func TestValidatePlaygroundImageURL(t *testing.T) {
	rawURL := "https://images.example.com/generated%2Fimage.png/?token=a%2Fb"
	normalizedURL, err := validatePlaygroundImageURL(rawURL)
	require.NoError(t, err)
	require.Equal(t, rawURL, normalizedURL)

	_, err = validatePlaygroundImageURL("http://127.0.0.1/image.png")
	require.EqualError(t, err, "image host is not allowed")
}

func TestPlaygroundImageTaskServiceCreateTaskClonesHeaders(t *testing.T) {
	repo := &playgroundImageTaskRepoStub{}
	svc := NewPlaygroundImageTaskService(repo, nil, nil, nil)

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
