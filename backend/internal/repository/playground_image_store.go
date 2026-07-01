package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func NewPlaygroundImageObjectStoreFactory() service.PlaygroundImageObjectStoreFactory {
	return func(ctx context.Context, cfg *service.PlaygroundImageStorageConfig) (service.PlaygroundImageObjectStore, error) {
		if cfg == nil || cfg.Provider != "cloudfile" {
			return nil, service.ErrPlaygroundImageStorageNotConfigured
		}
		return NewCloudFilePlaygroundImageStore(cfg), nil
	}
}

func NewPlaygroundUploadSignerFactory() service.PlaygroundUploadSignerFactory {
	return func(ctx context.Context, cfg *service.PlaygroundImageStorageConfig) (service.PlaygroundUploadSigner, error) {
		if cfg == nil || cfg.Provider != "cloudfile" {
			return nil, service.ErrPlaygroundImageStorageNotConfigured
		}
		return NewCloudFilePlaygroundImageStore(cfg), nil
	}
}
