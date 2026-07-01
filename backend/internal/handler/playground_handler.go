package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/google/uuid"

	"github.com/gin-gonic/gin"
)

type PlaygroundHandler struct {
	playgroundService *service.PlaygroundService
	taskService       *service.PlaygroundImageTaskService
	apiKeyService     *service.APIKeyService
	openAIGateway     *OpenAIGatewayHandler
}

type playgroundUploadPresignRequest struct {
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

type playgroundUploadCompleteRequest struct {
	UploadID string `json:"upload_id"`
}

func NewPlaygroundHandler(
	playgroundService *service.PlaygroundService,
	taskService *service.PlaygroundImageTaskService,
	apiKeyService *service.APIKeyService,
	openAIGateway *OpenAIGatewayHandler,
) *PlaygroundHandler {
	return &PlaygroundHandler{
		playgroundService: playgroundService,
		taskService:       taskService,
		apiKeyService:     apiKeyService,
		openAIGateway:     openAIGateway,
	}
}

func (h *PlaygroundHandler) Responses(c *gin.Context) {
	user, ok := middleware2.GetAuthenticatedUserFromContext(c)
	if !ok || user == nil {
		h.writeOpenAIError(c, http.StatusUnauthorized, "authentication_error", "User not authenticated")
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		h.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	body, err = ensurePlaygroundJSONModel(body, service.PlaygroundResponsesModel)
	if err != nil {
		h.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))

	apiKey, _, subscription, err := h.playgroundService.ResolveUserPlaygroundContext(
		c.Request.Context(),
		user,
		service.IsImageGenerationIntent("/v1/responses", "", body),
	)
	if err != nil {
		h.writeOpenAIErrorFromError(c, err)
		return
	}

	middleware2.ApplyGatewayAuthContext(c, apiKey, user, subscription)
	if h.apiKeyService != nil {
		_ = h.apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)
	}
	h.openAIGateway.Responses(c)
}

func (h *PlaygroundHandler) Images(c *gin.Context) {
	user, ok := middleware2.GetAuthenticatedUserFromContext(c)
	if !ok || user == nil {
		h.writeOpenAIError(c, http.StatusUnauthorized, "authentication_error", "User not authenticated")
		return
	}

	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		h.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	body, contentType, err := ensurePlaygroundImageRequestModel(body, c.GetHeader("Content-Type"), service.PlaygroundImagesModel)
	if err != nil {
		h.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
	if contentType != "" {
		c.Request.Header.Set("Content-Type", contentType)
	}

	apiKey, _, subscription, err := h.playgroundService.ResolveUserPlaygroundContext(c.Request.Context(), user, true)
	if err != nil {
		h.writeOpenAIErrorFromError(c, err)
		return
	}

	middleware2.ApplyGatewayAuthContext(c, apiKey, user, subscription)
	if h.apiKeyService != nil {
		_ = h.apiKeyService.TouchLastUsed(c.Request.Context(), apiKey.ID)
	}
	h.openAIGateway.Images(c)
}

func (h *PlaygroundHandler) CreateImageTask(c *gin.Context) {
	user, ok := middleware2.GetAuthenticatedUserFromContext(c)
	if !ok || user == nil {
		h.writeOpenAIError(c, http.StatusUnauthorized, "authentication_error", "User not authenticated")
		return
	}
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		h.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	body, contentType, err := ensurePlaygroundImageRequestModel(body, c.GetHeader("Content-Type"), service.PlaygroundImagesModel)
	if err != nil {
		h.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	requestPath := "/v1/images/generations"
	if strings.HasSuffix(c.FullPath(), "/edits") {
		requestPath = "/v1/images/edits"
	}
	task, err := h.taskService.CreateTask(c.Request.Context(), user.ID, requestPath, contentType, c.Request.Header, body)
	if err != nil {
		h.writeOpenAIErrorFromError(c, err)
		return
	}
	h.ExecuteImageTask(task.ID, user)
	c.JSON(http.StatusAccepted, gin.H{
		"id":     task.ID,
		"status": string(task.Status),
	})
}

func (h *PlaygroundHandler) GetImageTask(c *gin.Context) {
	user, ok := middleware2.GetAuthenticatedUserFromContext(c)
	if !ok || user == nil {
		h.writeOpenAIError(c, http.StatusUnauthorized, "authentication_error", "User not authenticated")
		return
	}
	taskID := strings.TrimSpace(c.Param("id"))
	if taskID == "" {
		h.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", "task id is required")
		return
	}
	task, err := h.taskService.GetTask(c.Request.Context(), user.ID, taskID)
	if err != nil {
		h.writeOpenAIErrorFromError(c, err)
		return
	}
	c.JSON(http.StatusOK, task.ToView())
}

func (h *PlaygroundHandler) CreateUploadSession(c *gin.Context) {
	user, ok := middleware2.GetAuthenticatedUserFromContext(c)
	if !ok || user == nil {
		h.writeOpenAIError(c, http.StatusUnauthorized, "authentication_error", "User not authenticated")
		return
	}
	var req playgroundUploadPresignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", "invalid upload request")
		return
	}
	contentType := strings.TrimSpace(req.ContentType)
	if !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		h.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", "content_type must be an image")
		return
	}
	if req.Size <= 0 {
		h.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", "size must be greater than 0")
		return
	}
	fileName := sanitizePlaygroundUploadFileName(req.FileName)
	objectKey := path.Join("playground-inputs", strconv.FormatInt(user.ID, 10), sanitizePlaygroundUploadID(), fileName)
	session, err := h.taskService.CreateUploadSession(c.Request.Context(), objectKey, contentType, req.Size)
	if err != nil {
		h.writeOpenAIErrorFromError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": session})
}

func (h *PlaygroundHandler) CompleteUploadSession(c *gin.Context) {
	_, ok := middleware2.GetAuthenticatedUserFromContext(c)
	if !ok {
		h.writeOpenAIError(c, http.StatusUnauthorized, "authentication_error", "User not authenticated")
		return
	}
	var req playgroundUploadCompleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.writeOpenAIError(c, http.StatusBadRequest, "invalid_request_error", "invalid upload request")
		return
	}
	finalURL, err := h.taskService.CompleteUploadSession(c.Request.Context(), strings.TrimSpace(req.UploadID))
	if err != nil {
		h.writeOpenAIErrorFromError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"url": finalURL}})
}

func (h *PlaygroundHandler) ExecuteImageTask(taskID string, user *service.User) {
	go h.executeImageTask(taskID, user)
}

func (h *PlaygroundHandler) executeImageTask(taskID string, user *service.User) {
	ctx := context.Background()
	task, err := h.taskService.MarkTaskRunning(ctx, taskID)
	if err != nil {
		return
	}
	apiKey, _, subscription, err := h.playgroundService.ResolveUserPlaygroundContext(ctx, user, true)
	if err != nil {
		h.taskService.FailTask(ctx, taskID, err.Error())
		return
	}

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	req, err := http.NewRequest(http.MethodPost, task.RequestPath, bytes.NewReader(task.RequestBody))
	if err != nil {
		h.taskService.FailTask(ctx, taskID, err.Error())
		return
	}
	req.Header = service.ClonePlaygroundTaskHeaders(task.RequestHeaders)
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Content-Type", task.RequestContentType)
	req.ContentLength = int64(len(task.RequestBody))
	ginCtx.Request = req
	middleware2.ApplyGatewayAuthContext(ginCtx, apiKey, user, subscription)

	h.openAIGateway.Images(ginCtx)
	responseBody := recorder.Body.Bytes()

	if recorder.Code >= http.StatusBadRequest {
		h.taskService.FailTask(ctx, taskID, extractPlaygroundTaskError(responseBody, recorder.Code))
		return
	}
	if err := h.taskService.CompleteTask(ctx, taskID, responseBody); err != nil {
		h.taskService.FailTask(ctx, taskID, err.Error())
	}
}

func ensurePlaygroundJSONModel(body []byte, model string) ([]byte, error) {
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("Failed to parse request body")
	}
	updated, err := sjson.SetBytes(body, "model", model)
	if err != nil {
		return nil, fmt.Errorf("Failed to patch request body")
	}
	return updated, nil
}

func ensurePlaygroundImageRequestModel(body []byte, contentType, model string) ([]byte, string, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		mediaType = "application/json"
	}
	if mediaType != "multipart/form-data" {
		updated, patchErr := ensurePlaygroundJSONModel(body, model)
		return updated, contentType, patchErr
	}

	boundary := params["boundary"]
	if boundary == "" {
		return nil, "", fmt.Errorf("Failed to parse request body")
	}

	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	modelWritten := false

	for {
		part, readErr := reader.NextPart()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("Failed to parse request body")
		}

		fieldName := part.FormName()
		if fieldName == "model" {
			if writeErr := writer.WriteField("model", model); writeErr != nil {
				_ = writer.Close()
				return nil, "", fmt.Errorf("Failed to patch request body")
			}
			modelWritten = true
			_ = part.Close()
			continue
		}

		var targetWriter io.Writer
		if fileName := part.FileName(); fileName != "" {
			nextPart, createErr := writer.CreateFormFile(fieldName, fileName)
			if createErr != nil {
				_ = writer.Close()
				return nil, "", fmt.Errorf("Failed to patch request body")
			}
			targetWriter = nextPart
		} else {
			nextPart, createErr := writer.CreateFormField(fieldName)
			if createErr != nil {
				_ = writer.Close()
				return nil, "", fmt.Errorf("Failed to patch request body")
			}
			targetWriter = nextPart
		}

		if _, copyErr := io.Copy(targetWriter, part); copyErr != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("Failed to parse request body")
		}
		_ = part.Close()
	}

	if !modelWritten {
		if err := writer.WriteField("model", model); err != nil {
			_ = writer.Close()
			return nil, "", fmt.Errorf("Failed to patch request body")
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("Failed to patch request body")
	}

	return buffer.Bytes(), writer.FormDataContentType(), nil
}

func (h *PlaygroundHandler) writeOpenAIErrorFromError(c *gin.Context, err error) {
	status := infraerrors.Code(err)
	errType := "api_error"
	switch status {
	case http.StatusBadRequest:
		errType = "invalid_request_error"
	case http.StatusUnauthorized:
		errType = "authentication_error"
	case http.StatusForbidden:
		errType = "permission_error"
	case http.StatusTooManyRequests:
		errType = "rate_limit_error"
	}
	h.writeOpenAIError(c, status, errType, infraerrors.Message(err))
}

func (h *PlaygroundHandler) writeOpenAIError(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
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

func sanitizePlaygroundUploadFileName(name string) string {
	trimmed := strings.TrimSpace(path.Base(name))
	if trimmed == "" || trimmed == "." || trimmed == "/" {
		return "image.png"
	}
	return trimmed
}

func sanitizePlaygroundUploadID() string {
	return uuid.NewString()
}
