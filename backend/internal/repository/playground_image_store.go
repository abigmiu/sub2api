package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func NewPlaygroundImageObjectStoreFactory() service.PlaygroundImageObjectStoreFactory {
	backupFactory := NewS3BackupStoreFactory()
	return func(ctx context.Context, cfg *service.PlaygroundImageStorageConfig) (service.PlaygroundImageObjectStore, error) {
		return backupFactory(ctx, &service.BackupS3Config{
			Endpoint:        cfg.Endpoint,
			Region:          cfg.Region,
			Bucket:          cfg.Bucket,
			AccessKeyID:     cfg.AccessKeyID,
			SecretAccessKey: cfg.SecretAccessKey,
			Prefix:          cfg.Prefix,
			ForcePathStyle:  cfg.ForcePathStyle,
		})
	}
}
