// StorageService -- klien MinIO (S3-compatible), dipakai PERTAMA KALI oleh
// Import Data (S4G-18, Track S4G) untuk simpan berkas CSV asli sebelum
// diproses job Asynq. MinIO SENDIRI sudah jalan sejak S0 (infra/docker-
// compose.dev.yml, bucket prodo-attachments) dan config-nya sudah lengkap
// di config.Config (S0-06) -- CUMA belum pernah dipakai kode Go manapun
// sampai sekarang (task_attachments/logo upload lain masih deferred, lihat
// implementation_gaps.md IG-19).
package service

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type StorageService struct {
	client *minio.Client
	bucket string
}

func NewStorageService(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*StorageService, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("service.NewStorageService: %w", err)
	}
	return &StorageService{client: client, bucket: bucket}, nil
}

// Upload menyimpan data di bawah key yang diberikan -- caller yang
// menyusun key (lihat CSVImportService: "csv-imports/{groupID}/{uuid}-{nama file}").
func (s *StorageService) Upload(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("service.Upload: %w", err)
	}
	return nil
}

func (s *StorageService) Download(ctx context.Context, key string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("service.Download: %w", err)
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("service.Download: baca objek: %w", err)
	}
	return data, nil
}
