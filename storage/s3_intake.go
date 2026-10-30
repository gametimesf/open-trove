package storage

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Allowed intake verdicts are empty marker objects at _intake/<key>, where
// the key hashes the inspector's policy and the input (intake.CacheKey).
func intakeVerdictKey(key string) string {
	return "_intake/" + key
}

func validIntakeKey(key string) bool {
	b, err := hex.DecodeString(key)
	return err == nil && len(b) == 32
}

func (s *s3Store) HasAllowedVerdict(ctx context.Context, key string) (bool, error) {
	if !validIntakeKey(key) {
		return false, fmt.Errorf("invalid intake cache key %q", key)
	}
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(intakeVerdictKey(key)),
	})
	if err == nil {
		return true, nil
	}
	var nsk *types.NotFound
	if errors.As(err, &nsk) {
		return false, nil
	}
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) {
		if code := apiErr.ErrorCode(); code == "NotFound" || code == "NoSuchKey" {
			return false, nil
		}
	}
	return false, fmt.Errorf("checking intake verdict: %w", err)
}

func (s *s3Store) PutAllowedVerdict(ctx context.Context, key string) error {
	if !validIntakeKey(key) {
		return fmt.Errorf("invalid intake cache key %q", key)
	}
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(intakeVerdictKey(key)),
		Body:        bytes.NewReader(nil),
		ContentType: aws.String("application/octet-stream"),
	}); err != nil {
		return fmt.Errorf("storing intake verdict: %w", err)
	}
	return nil
}
