//go:build unit

package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestCloudFilePlaygroundImageStoreUpload(t *testing.T) {
	t.Helper()
	var uploadBody []byte
	var presignAuth [2]string
	var completeAuth [2]string
	var server *httptest.Server

	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/open/uploads/presign":
			presignAuth = [2]string{r.Header.Get("X-App-Id"), r.Header.Get("X-App-Token")}
			require.Equal(t, http.MethodPost, r.Method)
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, "image/png", payload["mime"])
			require.Equal(t, "playground-images/task-1-01.png", payload["originName"])
			require.Equal(t, float64(4), payload["size"])
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"uploadId":12,"fileUrl":"https://cdn.example.com/tmp.png","uploadTarget":{"method":"PUT","uploadUrl":"` + server.URL + `/upload-target","headers":[{"name":"Content-Type","value":"image/png"}]}}}`))
		case "/upload-target":
			require.Equal(t, http.MethodPut, r.Method)
			data, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			uploadBody = data
			require.Equal(t, "image/png", r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusOK)
		case "/api/open/uploads/complete":
			completeAuth = [2]string{r.Header.Get("X-App-Id"), r.Header.Get("X-App-Token")}
			require.Equal(t, http.MethodPost, r.Method)
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, float64(12), payload["uploadId"])
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"url":"https://cdn.example.com/final.png"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	store := NewCloudFilePlaygroundImageStore(&service.PlaygroundImageStorageConfig{
		Provider:            "cloudfile",
		CloudFileHost:       server.URL,
		CloudFileAppID:      "app-id",
		CloudFileToken:      "app-token",
		CloudFileProviderID: 7,
	})

	result, err := store.Upload(context.Background(), "playground-images/task-1-01.png", bytes.NewReader([]byte{1, 2, 3, 4}), "image/png")
	require.NoError(t, err)
	require.Equal(t, int64(4), result.SizeBytes)
	require.Equal(t, "https://cdn.example.com/final.png", result.URL)
	require.Equal(t, []byte{1, 2, 3, 4}, uploadBody)
	require.Equal(t, [2]string{"app-id", "app-token"}, presignAuth)
	require.Equal(t, [2]string{"app-id", "app-token"}, completeAuth)
}

func TestNewCloudFilePlaygroundImageStoreDisablesEnvironmentProxy(t *testing.T) {
	t.Helper()

	store := NewCloudFilePlaygroundImageStore(&service.PlaygroundImageStorageConfig{
		Provider:       "cloudfile",
		CloudFileHost:  "https://cloudfile.example.com",
		CloudFileAppID: "app-id",
		CloudFileToken: "app-token",
	})

	require.NotNil(t, store.httpClient)
	require.NotSame(t, http.DefaultClient, store.httpClient)

	transport, ok := store.httpClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport)
	require.Nil(t, transport.Proxy)
}

func TestNewCloudFilePlaygroundImageStoreUsesDedicatedUploadClient(t *testing.T) {
	t.Helper()

	store := NewCloudFilePlaygroundImageStore(&service.PlaygroundImageStorageConfig{
		Provider:       "cloudfile",
		CloudFileHost:  "https://cloudfile.example.com",
		CloudFileAppID: "app-id",
		CloudFileToken: "app-token",
	})

	require.NotNil(t, store.uploadHTTPClient)

	transport, ok := store.uploadHTTPClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport)
	require.Nil(t, transport.Proxy)
	require.True(t, transport.DisableKeepAlives)
	require.False(t, transport.ForceAttemptHTTP2)
	require.Equal(t, 1, transport.MaxConnsPerHost)
}

type cloudFileRoundTripFunc func(*http.Request) (*http.Response, error)

func (f cloudFileRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestCloudFilePlaygroundImageStoreUploadRetriesBrokenPipe(t *testing.T) {
	t.Helper()

	var uploadAttempts = 0
	var store = NewCloudFilePlaygroundImageStore(&service.PlaygroundImageStorageConfig{
		Provider:       "cloudfile",
		CloudFileHost:  "https://cloudfile.example.com",
		CloudFileAppID: "app-id",
		CloudFileToken: "app-token",
	})
	store.uploadHTTPClient = &http.Client{
		Transport: cloudFileRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			uploadAttempts++
			if uploadAttempts == 1 {
				return nil, syscall.EPIPE
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(nil)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	var uploadError = store.uploadObject(context.Background(), cloudFileUploadTarget{
		Method:    "PUT",
		UploadURL: "https://example.com/upload",
	}, []byte{1, 2, 3}, "image/png")

	require.NoError(t, uploadError)
	require.Equal(t, 2, uploadAttempts)
}

func TestCloudFilePlaygroundImageStoreUploadDoesNotRetryNonRetryableError(t *testing.T) {
	t.Helper()

	var uploadAttempts = 0
	var expectedError = errors.New("permission denied")
	var store = NewCloudFilePlaygroundImageStore(&service.PlaygroundImageStorageConfig{
		Provider:       "cloudfile",
		CloudFileHost:  "https://cloudfile.example.com",
		CloudFileAppID: "app-id",
		CloudFileToken: "app-token",
	})
	store.uploadHTTPClient = &http.Client{
		Transport: cloudFileRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			uploadAttempts++
			return nil, expectedError
		}),
	}

	var uploadError = store.uploadObject(context.Background(), cloudFileUploadTarget{
		Method:    "PUT",
		UploadURL: "https://example.com/upload",
	}, []byte{1, 2, 3}, "image/png")

	require.Error(t, uploadError)
	require.ErrorContains(t, uploadError, "permission denied")
	require.Equal(t, 1, uploadAttempts)
}
