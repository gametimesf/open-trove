package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

func (s *s3Store) readManifest(ctx context.Context, userID string) (*UserManifest, string, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(manifestKey(userID))})
	if err != nil {
		if s3ErrorCode(err, "NoSuchKey", "NotFound") {
			return &UserManifest{}, "", nil
		}
		return nil, "", fmt.Errorf("getting manifest: %w", err)
	}
	defer out.Body.Close()
	var manifest UserManifest
	if err := json.NewDecoder(out.Body).Decode(&manifest); err != nil {
		return nil, "", fmt.Errorf("decoding manifest: %w", err)
	}
	if aws.ToString(out.ETag) == "" {
		return nil, "", errors.New("manifest response missing ETag")
	}
	return &manifest, aws.ToString(out.ETag), nil
}

func (s *s3Store) GetManifest(ctx context.Context, userID string) (*UserManifest, error) {
	manifest, _, err := s.readManifest(ctx, userID)
	return manifest, err
}

func (s *s3Store) updateManifest(ctx context.Context, userID string, update func(*UserManifest)) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	const attempts = 5
	for attempt := 0; attempt < attempts; attempt++ {
		manifest, etag, err := s.readManifest(ctx, userID)
		if err != nil {
			return err
		}
		update(manifest)
		data, err := json.Marshal(manifest)
		if err != nil {
			return fmt.Errorf("marshaling manifest: %w", err)
		}
		input := &s3.PutObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(manifestKey(userID)), Body: bytes.NewReader(data), ContentType: aws.String("application/json")}
		if etag == "" {
			input.IfNoneMatch = aws.String("*")
		} else {
			input.IfMatch = aws.String(etag)
		}
		_, err = s.client.PutObject(ctx, input)
		if err == nil {
			return nil
		}
		if !s3ErrorCode(err, "PreconditionFailed", "ConditionalRequestConflict") {
			return fmt.Errorf("putting manifest: %w", err)
		}
		if attempt+1 < attempts {
			timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("manifest update conflict after %d attempts", attempts)
}

func (s *s3Store) RecordUpload(ctx context.Context, userID string, record ActivityRecord) error {
	record.At = time.Now().UTC().Format(time.RFC3339Nano)
	return s.updateManifest(ctx, userID, func(m *UserManifest) { m.RecordUpload(record) })
}

func (s *s3Store) RecordView(ctx context.Context, userID string, record ActivityRecord) error {
	record.At = time.Now().UTC().Format(time.RFC3339Nano)
	return s.updateManifest(ctx, userID, func(m *UserManifest) { m.RecordView(record) })
}

func s3ErrorCode(err error, codes ...string) bool {
	var apiError smithy.APIError
	if !errors.As(err, &apiError) {
		return false
	}
	for _, code := range codes {
		if apiError.ErrorCode() == code {
			return true
		}
	}
	return false
}

func (s *s3Store) GetSiteManifest(ctx context.Context, slug string) (*SiteManifest, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(siteManifestKey(slug))})
	if err != nil {
		if s3ErrorCode(err, "NoSuchKey", "NotFound") {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting site manifest: %w", err)
	}
	defer out.Body.Close()
	var manifest SiteManifest
	if err := json.NewDecoder(out.Body).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decoding site manifest: %w", err)
	}
	return &manifest, nil
}
