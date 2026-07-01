package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type CloudFilePlaygroundImageStore struct {
	host             string
	appID            string
	appToken         string
	providerID       int64
	httpClient       *http.Client
	uploadHTTPClient *http.Client
}

func NewCloudFilePlaygroundImageStore(cfg *service.PlaygroundImageStorageConfig) *CloudFilePlaygroundImageStore {
	return &CloudFilePlaygroundImageStore{
		host:             strings.TrimRight(strings.TrimSpace(cfg.CloudFileHost), "/"),
		appID:            strings.TrimSpace(cfg.CloudFileAppID),
		appToken:         strings.TrimSpace(cfg.CloudFileToken),
		providerID:       cfg.CloudFileProviderID,
		httpClient:       newCloudFileHTTPClient(),
		uploadHTTPClient: newCloudFileUploadHTTPClient(),
	}
}

func newCloudFileHTTPClient() *http.Client {
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok || baseTransport == nil {
		return &http.Client{}
	}
	transport := baseTransport.Clone()
	transport.Proxy = nil
	return &http.Client{Transport: transport}
}

func newCloudFileUploadHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          0,
			MaxIdleConnsPerHost:   0,
			MaxConnsPerHost:       1,
			IdleConnTimeout:       0,
			DisableKeepAlives:     true,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

type cloudFilePresignRequest struct {
	ProviderID             *int64 `json:"providerId,omitempty"`
	IsCompressionRequested bool   `json:"isCompressionRequested"`
	IsThumbnailRequested   bool   `json:"isThumbnailRequested"`
	OriginName             string `json:"originName"`
	Mime                   string `json:"mime"`
	Size                   int64  `json:"size"`
}

type cloudFileHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type cloudFileUploadTarget struct {
	Method    string            `json:"method"`
	UploadURL string            `json:"uploadUrl"`
	Headers   []cloudFileHeader `json:"headers"`
}

type cloudFilePresignResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		UploadID     int64                 `json:"uploadId"`
		FileURL      string                `json:"fileUrl"`
		UploadTarget cloudFileUploadTarget `json:"uploadTarget"`
	} `json:"data"`
}

type cloudFileCompleteResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		URL string `json:"url"`
	} `json:"data"`
}

const cloudFileUploadRetryDelay = 200 * time.Millisecond

func (s *CloudFilePlaygroundImageStore) Upload(ctx context.Context, key string, body io.Reader, contentType string) (*service.PlaygroundImageUploadResult, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	presignResp, err := s.presign(ctx, key, contentType, int64(len(data)))
	if err != nil {
		return nil, err
	}
	if err := s.uploadObject(ctx, presignResp.Data.UploadTarget, data, contentType); err != nil {
		return nil, err
	}
	finalURL, err := s.complete(ctx, presignResp.Data.UploadID)
	if err != nil {
		return nil, err
	}
	return &service.PlaygroundImageUploadResult{
		SizeBytes: int64(len(data)),
		URL:       finalURL,
	}, nil
}

func (s *CloudFilePlaygroundImageStore) CreateUploadSession(ctx context.Context, key, contentType string, size int64) (*service.PlaygroundUploadSession, error) {
	presignResp, err := s.presign(ctx, key, contentType, size)
	if err != nil {
		return nil, err
	}
	headers := make([]service.PlaygroundUploadHeader, 0, len(presignResp.Data.UploadTarget.Headers))
	for _, header := range presignResp.Data.UploadTarget.Headers {
		headers = append(headers, service.PlaygroundUploadHeader{
			Name:  header.Name,
			Value: header.Value,
		})
	}
	return &service.PlaygroundUploadSession{
		UploadID:    fmt.Sprintf("%d", presignResp.Data.UploadID),
		ObjectKey:   key,
		FileURL:     strings.TrimSpace(presignResp.Data.FileURL),
		ContentType: contentType,
		UploadTarget: service.PlaygroundUploadTarget{
			Method:    strings.ToUpper(strings.TrimSpace(presignResp.Data.UploadTarget.Method)),
			UploadURL: strings.TrimSpace(presignResp.Data.UploadTarget.UploadURL),
			Headers:   headers,
		},
	}, nil
}

func (s *CloudFilePlaygroundImageStore) CompleteUploadSession(ctx context.Context, uploadID string) (string, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(uploadID), 10, 64)
	if err != nil || id <= 0 {
		return "", fmt.Errorf("invalid upload id")
	}
	return s.complete(ctx, id)
}

func (s *CloudFilePlaygroundImageStore) presign(ctx context.Context, key, contentType string, size int64) (*cloudFilePresignResponse, error) {
	reqBody := cloudFilePresignRequest{
		IsCompressionRequested: false,
		IsThumbnailRequested:   false,
		OriginName:             key,
		Mime:                   contentType,
		Size:                   size,
	}
	if s.providerID > 0 {
		reqBody.ProviderID = &s.providerID
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal cloudfile presign request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.host+"/api/open/uploads/presign", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create cloudfile presign request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-App-Id", s.appID)
	req.Header.Set("X-App-Token", s.appToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudfile presign request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read cloudfile presign response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("cloudfile presign status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var parsed cloudFilePresignResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("parse cloudfile presign response: %w", err)
	}
	if parsed.Code != 0 {
		return nil, fmt.Errorf("cloudfile presign failed: %s", strings.TrimSpace(parsed.Message))
	}
	if parsed.Data.UploadID == 0 || strings.TrimSpace(parsed.Data.UploadTarget.UploadURL) == "" {
		return nil, fmt.Errorf("cloudfile presign response missing upload target")
	}
	return &parsed, nil
}

func (s *CloudFilePlaygroundImageStore) uploadObject(ctx context.Context, target cloudFileUploadTarget, data []byte, contentType string) error {
	method := strings.ToUpper(strings.TrimSpace(target.Method))
	if method == "" {
		method = http.MethodPut
	}
	if method != http.MethodPut {
		return fmt.Errorf("cloudfile upload target method %q is not supported", target.Method)
	}

	var lastUploadError error
	var attempt = 0
	for attempt < 2 {
		var _, uploadError = s.doUploadObjectRequest(ctx, method, target, data, contentType)
		if uploadError == nil {
			return nil
		}
		lastUploadError = uploadError
		if !isRetryableCloudFileUploadError(uploadError) || attempt == 1 {
			break
		}

		var retryTimer = time.NewTimer(cloudFileUploadRetryDelay)
		select {
		case <-ctx.Done():
			retryTimer.Stop()
			return fmt.Errorf("cloudfile upload request failed: %w", ctx.Err())
		case <-retryTimer.C:
		}
	}

	return fmt.Errorf("cloudfile upload request failed: %w", lastUploadError)
}

func (s *CloudFilePlaygroundImageStore) doUploadObjectRequest(ctx context.Context, method string, target cloudFileUploadTarget, data []byte, contentType string) (*http.Response, error) {
	var req, createRequestError = http.NewRequestWithContext(ctx, method, target.UploadURL, bytes.NewReader(data))
	if createRequestError != nil {
		return nil, fmt.Errorf("create cloudfile upload request: %w", createRequestError)
	}
	req.Close = true
	req.Header.Set("Content-Type", contentType)
	for _, header := range target.Headers {
		var name = strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		req.Header.Set(name, header.Value)
	}

	var resp, requestError = s.uploadHTTPClient.Do(req)
	if requestError != nil {
		return nil, requestError
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var respBody, _ = io.ReadAll(resp.Body)
		return nil, fmt.Errorf("cloudfile upload status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return resp, nil
}

func isRetryableCloudFileUploadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	var errorMessage = strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(errorMessage, "broken pipe") ||
		strings.Contains(errorMessage, "connection reset by peer") ||
		strings.Contains(errorMessage, "unexpected eof")
}

func (s *CloudFilePlaygroundImageStore) complete(ctx context.Context, uploadID int64) (string, error) {
	body, err := json.Marshal(map[string]int64{"uploadId": uploadID})
	if err != nil {
		return "", fmt.Errorf("marshal cloudfile complete request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.host+"/api/open/uploads/complete", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create cloudfile complete request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-App-Id", s.appID)
	req.Header.Set("X-App-Token", s.appToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cloudfile complete request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read cloudfile complete response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("cloudfile complete status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var parsed cloudFileCompleteResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("parse cloudfile complete response: %w", err)
	}
	if parsed.Code != 0 {
		return "", fmt.Errorf("cloudfile complete failed: %s", strings.TrimSpace(parsed.Message))
	}
	if strings.TrimSpace(parsed.Data.URL) == "" {
		return "", fmt.Errorf("cloudfile complete response missing url")
	}
	return strings.TrimSpace(parsed.Data.URL), nil
}
