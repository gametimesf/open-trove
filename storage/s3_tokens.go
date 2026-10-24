package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/gametimesf/open-trove/auth"
)

// Token records live at _tokens/<sha256>.json. An empty marker at
// _token_owners/<sha256(email)>/<sha256> lets owners list their tokens
// without scanning every record.
func tokenKey(id string) string {
	return "_tokens/" + id + ".json"
}

func tokenOwnerPrefix(ownerEmail string) string {
	sum := sha256.Sum256([]byte(ownerEmail))
	return "_token_owners/" + hex.EncodeToString(sum[:]) + "/"
}

// validTokenID rejects IDs that could escape the token prefix.
func validTokenID(id string) bool {
	if len(id) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func (s *s3Store) CreateToken(ctx context.Context, token auth.Token) error {
	if !validTokenID(token.ID) {
		return auth.ErrTokenNotFound
	}
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("marshaling token: %w", err)
	}
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(tokenKey(token.ID)),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
		IfNoneMatch: aws.String("*"),
	}); err != nil {
		if s3ErrorCode(err, "PreconditionFailed", "ConditionalRequestConflict") {
			return auth.ErrTokenExists
		}
		return fmt.Errorf("creating token: %w", err)
	}
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(tokenOwnerPrefix(token.OwnerEmail) + token.ID),
		Body:   bytes.NewReader(nil),
	}); err != nil {
		return fmt.Errorf("indexing token: %w", err)
	}
	return nil
}

func (s *s3Store) GetToken(ctx context.Context, id string) (auth.Token, error) {
	token, _, err := s.getToken(ctx, id)
	return token, err
}

// getToken also returns the record's ETag for conditional updates.
func (s *s3Store) getToken(ctx context.Context, id string) (auth.Token, string, error) {
	if !validTokenID(id) {
		return auth.Token{}, "", auth.ErrTokenNotFound
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(tokenKey(id)),
	})
	if err != nil {
		if s3ErrorCode(err, "NoSuchKey", "NotFound") {
			return auth.Token{}, "", auth.ErrTokenNotFound
		}
		return auth.Token{}, "", fmt.Errorf("getting token: %w", err)
	}
	var token auth.Token
	decodeErr := json.NewDecoder(out.Body).Decode(&token)
	closeErr := out.Body.Close()
	if decodeErr != nil {
		return auth.Token{}, "", fmt.Errorf("decoding token: %w", decodeErr)
	}
	if closeErr != nil {
		return auth.Token{}, "", fmt.Errorf("closing token: %w", closeErr)
	}
	return token, aws.ToString(out.ETag), nil
}

func (s *s3Store) ListTokens(ctx context.Context, ownerEmail string) ([]auth.Token, error) {
	var (
		tokens       []auth.Token
		continuation *string
	)
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(tokenOwnerPrefix(ownerEmail)),
			ContinuationToken: continuation,
		})
		if err != nil {
			return nil, fmt.Errorf("listing tokens: %w", err)
		}
		for _, object := range out.Contents {
			token, err := s.GetToken(ctx, path.Base(aws.ToString(object.Key)))
			if err != nil {
				// The record is deleted before its index marker, so a missing
				// record is a revoked token whose marker removal failed.
				if errors.Is(err, auth.ErrTokenNotFound) {
					continue
				}
				return nil, err
			}
			if token.OwnerEmail == ownerEmail {
				tokens = append(tokens, token)
			}
		}
		if !aws.ToBool(out.IsTruncated) || out.NextContinuationToken == nil {
			break
		}
		continuation = out.NextContinuationToken
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].CreatedAt.After(tokens[j].CreatedAt) })
	return tokens, nil
}

func (s *s3Store) DeleteToken(ctx context.Context, ownerEmail, id string) error {
	token, err := s.GetToken(ctx, id)
	if err != nil {
		return err
	}
	if !strings.EqualFold(token.OwnerEmail, ownerEmail) {
		return auth.ErrTokenNotFound
	}
	// Delete the record first so revocation takes effect even if the index
	// cleanup fails.
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(tokenKey(id)),
	}); err != nil {
		return fmt.Errorf("deleting token: %w", err)
	}
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(tokenOwnerPrefix(token.OwnerEmail) + id),
	}); err != nil {
		return fmt.Errorf("deleting token index: %w", err)
	}
	return nil
}

func (s *s3Store) TouchToken(ctx context.Context, id string, usedAt time.Time) error {
	token, etag, err := s.getToken(ctx, id)
	if err != nil {
		return err
	}
	token.LastUsedAt = &usedAt
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("marshaling token: %w", err)
	}
	// IfMatch makes the write conditional on the record read above, so a
	// revocation between the read and this write cannot be undone.
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(tokenKey(id)),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
		IfMatch:     aws.String(etag),
	}); err != nil {
		if s3ErrorCode(err, "PreconditionFailed", "ConditionalRequestConflict", "NoSuchKey", "NotFound") {
			return auth.ErrTokenNotFound
		}
		return fmt.Errorf("touching token: %w", err)
	}
	return nil
}
