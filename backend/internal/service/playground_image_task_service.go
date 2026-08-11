package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const playgroundImageMaxDownloadBytes int64 = 32 << 20

type PlaygroundImageTaskService struct {
	repo           PlaygroundImageTaskRepository
	settingRepo    SettingRepository
	storeFactory   PlaygroundImageObjectStoreFactory
	signerFactory  PlaygroundUploadSignerFactory
	downloadClient *http.Client
}

func NewPlaygroundImageTaskService(
	repo PlaygroundImageTaskRepository,
	settingRepo SettingRepository,
	storeFactory PlaygroundImageObjectStoreFactory,
	signerFactory PlaygroundUploadSignerFactory,
) *PlaygroundImageTaskService {
	return &PlaygroundImageTaskService{
		repo:           repo,
		settingRepo:    settingRepo,
		storeFactory:   storeFactory,
		signerFactory:  signerFactory,
		downloadClient: newPlaygroundImageDownloadClient(),
	}
}

func (s *PlaygroundImageTaskService) CreateTask(ctx context.Context, userID int64, requestPath, contentType string, headers http.Header, body []byte) (*PlaygroundImageTask, error) {
	task := &PlaygroundImageTask{
		ID:                 uuid.NewString(),
		UserID:             userID,
		Status:             PlaygroundImageTaskStatusPending,
		RequestPath:        requestPath,
		RequestContentType: contentType,
		RequestHeaders:     ClonePlaygroundTaskHeaders(headers),
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
		URL           string `json:"url"`
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
		var reader *bytes.Reader
		var contentType string
		if strings.TrimSpace(item.B64JSON) != "" {
			reader, contentType, err = decodeImageData(item.B64JSON)
		} else if strings.TrimSpace(item.URL) != "" {
			reader, contentType, err = s.downloadImage(ctx, item.URL)
		} else {
			return nil, fmt.Errorf("image response item has neither b64_json nor url")
		}
		if err != nil {
			return nil, err
		}
		ext := imageExtensionFromContentType(contentType)
		objectKey := path.Join(strings.Trim(storageCfg.Prefix, "/"), "playground-images", fmt.Sprintf("%s-%02d.%s", taskID, index+1, ext))
		uploadResult, err := store.Upload(ctx, objectKey, reader, contentType)
		if err != nil {
			return nil, err
		}
		result.Data = append(result.Data, ImageResponseItem{
			URL:           strings.TrimSpace(uploadResult.URL),
			RevisedPrompt: item.RevisedPrompt,
		})
	}
	return json.Marshal(result)
}

func (s *PlaygroundImageTaskService) downloadImage(ctx context.Context, rawURL string) (*bytes.Reader, string, error) {
	normalizedURL, err := validatePlaygroundImageURL(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("invalid image url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalizedURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build image download request: %w", err)
	}
	resp, err := s.downloadClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, "", fmt.Errorf("download image: unexpected status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, playgroundImageMaxDownloadBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read image response: %w", err)
	}
	if int64(len(data)) > playgroundImageMaxDownloadBytes {
		return nil, "", fmt.Errorf("downloaded image exceeds %d bytes", playgroundImageMaxDownloadBytes)
	}
	contentType := strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0])
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", fmt.Errorf("downloaded content is not an image")
	}
	return bytes.NewReader(data), contentType, nil
}

func newPlaygroundImageDownloadClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, address := range addresses {
			if isBlockedPlaygroundImageIP(address.IP) {
				return nil, fmt.Errorf("image host resolves to a blocked address")
			}
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("image host resolved to no addresses")
		}
		var dialErr error
		for _, address := range addresses {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			dialErr = err
		}
		return nil, fmt.Errorf("dial image host: %w", dialErr)
	}
	return &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many image download redirects")
			}
			_, err := validatePlaygroundImageURL(req.URL.String())
			return err
		},
	}
}

func validatePlaygroundImageURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid url")
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return "", fmt.Errorf("invalid url scheme: %s", parsed.Scheme)
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" || strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return "", fmt.Errorf("image host is not allowed")
	}
	if ip := net.ParseIP(host); ip != nil && isBlockedPlaygroundImageIP(ip) {
		return "", fmt.Errorf("image host is not allowed")
	}
	return trimmed, nil
}

func isBlockedPlaygroundImageIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
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

func (s *PlaygroundImageTaskService) CreateUploadSession(ctx context.Context, key, contentType string, size int64) (*PlaygroundUploadSession, error) {
	storageCfg, err := s.loadStorageConfig(ctx)
	if err != nil {
		return nil, err
	}
	signer, err := s.signerFactory(ctx, storageCfg)
	if err != nil {
		return nil, err
	}
	return signer.CreateUploadSession(ctx, key, contentType, size)
}

func (s *PlaygroundImageTaskService) CompleteUploadSession(ctx context.Context, uploadID string) (string, error) {
	storageCfg, err := s.loadStorageConfig(ctx)
	if err != nil {
		return "", err
	}
	signer, err := s.signerFactory(ctx, storageCfg)
	if err != nil {
		return "", err
	}
	return signer.CompleteUploadSession(ctx, uploadID)
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

func ClonePlaygroundTaskHeaders(src http.Header) http.Header {
	if len(src) == 0 {
		return nil
	}
	dst := make(http.Header, len(src))
	for key, values := range src {
		copied := make([]string, len(values))
		copy(copied, values)
		dst[key] = copied
	}
	return dst
}
