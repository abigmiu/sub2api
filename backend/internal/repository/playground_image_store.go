package repository

import (
	"context"
	"io"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func NewPlaygroundImageObjectStoreFactory() service.PlaygroundImageObjectStoreFactory {
	backupFactory := NewS3BackupStoreFactory()
	return func(ctx context.Context, cfg *service.PlaygroundImageStorageConfig) (service.PlaygroundImageObjectStore, error) {
		if cfg != nil && cfg.Provider == "cloudfile" {
			return NewCloudFilePlaygroundImageStore(cfg), nil
		}
		store, err := backupFactory(ctx, &service.BackupS3Config{
			Endpoint:        cfg.Endpoint,
			Region:          cfg.Region,
			Bucket:          cfg.Bucket,
			AccessKeyID:     cfg.AccessKeyID,
			SecretAccessKey: cfg.SecretAccessKey,
			Prefix:          cfg.Prefix,
			ForcePathStyle:  cfg.ForcePathStyle,
		})
		if err != nil {
			return nil, err
		}
		return &s3PlaygroundImageStore{
			BackupObjectStore: store,
			publicBaseURL:     cfg.PublicBaseURL,
		}, nil
	}
}

type s3PlaygroundImageStore struct {
	service.BackupObjectStore
	publicBaseURL string
}

func (s *s3PlaygroundImageStore) Upload(ctx context.Context, key string, body io.Reader, contentType string) (*service.PlaygroundImageUploadResult, error) {
	sizeBytes, err := s.BackupObjectStore.Upload(ctx, key, body, contentType)
	if err != nil {
		return nil, err
	}
	return &service.PlaygroundImageUploadResult{
		SizeBytes: sizeBytes,
		URL:       strings.TrimRight(s.publicBaseURL, "/") + "/" + key,
	}, nil
}
