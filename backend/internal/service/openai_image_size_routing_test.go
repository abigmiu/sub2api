package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type imageSizeRoutingRepoStub struct {
	values map[string]string
}

func (s *imageSizeRoutingRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *imageSizeRoutingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", ErrSettingNotFound
}

func (s *imageSizeRoutingRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *imageSizeRoutingRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if v, ok := s.values[key]; ok {
			out[key] = v
		}
	}
	return out, nil
}

func (s *imageSizeRoutingRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *imageSizeRoutingRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	for k, v := range s.values {
		out[k] = v
	}
	return out, nil
}

func (s *imageSizeRoutingRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

func TestResolveImageRequestGroupID_UsesGlobalImageSizeRouting(t *testing.T) {
	svc := &OpenAIGatewayService{
		settingService: NewSettingService(&imageSizeRoutingRepoStub{
			values: map[string]string{
				SettingKeyImageSizeRouting: `{"group_id_1k":11,"group_id_2k":22,"group_id_4k":44}`,
			},
		}, &config.Config{}),
	}

	groupID, err := svc.ResolveImageRequestGroupID(context.Background(), ImageBillingSize1K)
	require.NoError(t, err)
	require.Equal(t, int64(11), groupID)

	groupID, err = svc.ResolveImageRequestGroupID(context.Background(), ImageBillingSize2K)
	require.NoError(t, err)
	require.Equal(t, int64(22), groupID)

	groupID, err = svc.ResolveImageRequestGroupID(context.Background(), ImageBillingSize4K)
	require.NoError(t, err)
	require.Equal(t, int64(44), groupID)
}

func TestResolveImageRequestGroupID_RequiresConfiguredTier(t *testing.T) {
	svc := &OpenAIGatewayService{
		settingService: NewSettingService(&imageSizeRoutingRepoStub{
			values: map[string]string{
				SettingKeyImageSizeRouting: `{"group_id_1k":11}`,
			},
		}, &config.Config{}),
	}

	_, err := svc.ResolveImageRequestGroupID(context.Background(), ImageBillingSize2K)
	require.EqualError(t, err, "image size routing group not configured for 2K")
}
