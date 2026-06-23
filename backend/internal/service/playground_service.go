package service

import (
	"context"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const playgroundAPIKeyName = "playground"

var ErrPlaygroundOpenAIGroupUnavailable = infraerrors.Forbidden(
	"PLAYGROUND_OPENAI_GROUP_UNAVAILABLE",
	"No available OpenAI group for playground requests",
)

type PlaygroundService struct {
	apiKeyService       *APIKeyService
	subscriptionService *SubscriptionService
}

func NewPlaygroundService(apiKeyService *APIKeyService, subscriptionService *SubscriptionService) *PlaygroundService {
	return &PlaygroundService{
		apiKeyService:       apiKeyService,
		subscriptionService: subscriptionService,
	}
}

func (s *PlaygroundService) EnsureUserPlaygroundAPIKey(ctx context.Context, userID int64) (*APIKey, error) {
	keys, err := s.apiKeyService.SearchAPIKeys(ctx, userID, playgroundAPIKeyName, 20)
	if err != nil {
		return nil, err
	}
	for i := range keys {
		if strings.TrimSpace(keys[i].Name) == playgroundAPIKeyName {
			return s.apiKeyService.GetByID(ctx, keys[i].ID)
		}
	}
	return s.apiKeyService.Create(ctx, userID, CreateAPIKeyRequest{
		Name: playgroundAPIKeyName,
	})
}

func (s *PlaygroundService) ResolveUserPlaygroundContext(ctx context.Context, user *User, needImage bool) (*APIKey, *Group, *UserSubscription, error) {
	apiKey, err := s.EnsureUserPlaygroundAPIKey(ctx, user.ID)
	if err != nil {
		return nil, nil, nil, err
	}

	groups, err := s.apiKeyService.GetAvailableGroups(ctx, user.ID)
	if err != nil {
		return nil, nil, nil, err
	}

	var group *Group
	for i := range groups {
		candidate := groups[i]
		if candidate.Platform != PlatformOpenAI {
			continue
		}
		if needImage && !candidate.AllowImageGeneration {
			continue
		}
		group = &candidate
		break
	}
	if group == nil {
		if needImage {
			return nil, nil, nil, infraerrors.Forbidden(
				"PLAYGROUND_IMAGE_GROUP_UNAVAILABLE",
				"No available OpenAI image group for playground requests",
			)
		}
		return nil, nil, nil, ErrPlaygroundOpenAIGroupUnavailable
	}

	var subscription *UserSubscription
	if group.IsSubscriptionType() && s.subscriptionService != nil {
		subscription, err = s.subscriptionService.GetActiveSubscription(ctx, user.ID, group.ID)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	apiKeyCopy := *apiKey
	apiKeyCopy.User = user
	apiKeyCopy.Group = group
	apiKeyCopy.GroupID = &group.ID

	return &apiKeyCopy, group, subscription, nil
}
