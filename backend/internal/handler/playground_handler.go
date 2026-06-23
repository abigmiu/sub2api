package handler

import (
	"bytes"
	"io"
	"net/http"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

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
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

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
