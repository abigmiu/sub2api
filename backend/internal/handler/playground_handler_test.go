package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestEnsurePlaygroundImageRequestModel_PreservesJSONBody(t *testing.T) {
	body := []byte(`{"prompt":"生产一个猫咪","size":"1280x720","quality":"auto","output_format":"png","moderation":"auto"}`)

	gotBody, gotContentType, err := ensurePlaygroundImageRequestModel(body, "application/json", service.PlaygroundImagesModel)
	require.NoError(t, err)
	require.Equal(t, "application/json", gotContentType)
	require.True(t, gjson.ValidBytes(gotBody))
	require.Equal(t, service.PlaygroundImagesModel, gjson.GetBytes(gotBody, "model").String())
	require.Equal(t, "生产一个猫咪", gjson.GetBytes(gotBody, "prompt").String())
	require.Equal(t, "1280x720", gjson.GetBytes(gotBody, "size").String())
	require.Equal(t, "auto", gjson.GetBytes(gotBody, "quality").String())
	require.Equal(t, "png", gjson.GetBytes(gotBody, "output_format").String())
	require.Equal(t, "auto", gjson.GetBytes(gotBody, "moderation").String())
	require.Greater(t, len(gotBody), len(body))
}
