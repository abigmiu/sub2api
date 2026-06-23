package handler

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/gin-gonic/gin"
)

type PlaygroundHandler struct {
	playgroundService *service.PlaygroundService
	apiKeyService     *service.APIKeyService
	openAIGateway     *OpenAIGatewayHandler
}

func NewPlaygroundHandler(
	playgroundService *service.PlaygroundService,
	apiKeyService *service.APIKeyService,
	openAIGateway *OpenAIGatewayHandler,
) *PlaygroundHandler {
	return &PlaygroundHandler{
		playgroundService: playgroundService,
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
