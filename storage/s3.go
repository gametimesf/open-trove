package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/gametimesf/open-trove/comments"
)

// splitCSV is a tiny helper for the comma-separated category list.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// S3Config holds S3-specific configuration.
type S3Config struct {
	Bucket   string
	Endpoint string
	Region   string
}

// s3api is the subset of the S3 SDK the store actually calls.
type s3api interface {
	PutObject(ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(ctx context.Context, input *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(ctx context.Context, input *s3.DeleteObjectInput, opts ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	HeadObject(ctx context.Context, input *s3.HeadObjectInput, opts ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	ListObjectsV2(ctx context.Context, input *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

// s3Store implements object storage using S3.
type s3Store struct {
	client s3api
	bucket string
}

func newS3Store(ctx context.Context, cfg S3Config) (Store, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3: bucket is required")
	}

	var awsOpts []func(*awsconfig.LoadOptions) error
	if cfg.Region != "" {
		awsOpts = append(awsOpts, awsconfig.WithRegion(cfg.Region))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsOpts...)
	if err != nil {
		return nil, fmt.Errorf("s3: loading AWS config: %w", err)
	}

	var s3Opts []func(*s3.Options)
	if cfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(awsCfg, s3Opts...)
	return &s3Store{client: client, bucket: cfg.Bucket}, nil
}

func (s *s3Store) Put(ctx context.Context, slug string, body io.Reader, contentType, filename string, customSlug, overwrite bool) error {
	if !overwrite {
		_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(slug),
		})
		if err == nil {
			return ErrSlugConflict
		}
		var nsk *types.NotFound
		if !errors.As(err, &nsk) {
			var apiErr interface{ ErrorCode() string }
			if errors.As(err, &apiErr) {
				code := apiErr.ErrorCode()
				if code != "NotFound" && code != "NoSuchKey" {
					return fmt.Errorf("checking slug existence: %w", err)
				}
			}
		}
	}

	meta := map[string]string{"filename": filename}
	if customSlug {
		meta["custom-slug"] = "true"
	}
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(slug),
		Body:        body,
		ContentType: aws.String(contentType),
		Metadata:    meta,
	}); err != nil {
		return fmt.Errorf("putting object: %w", err)
	}
	return nil
}

func (s *s3Store) Get(ctx context.Context, slug string, rangeHeader string) (io.ReadCloser, *FileMetadata, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(slug),
	}
	if rangeHeader != "" {
		input.Range = aws.String(rangeHeader)
	}
	out, err := s.client.GetObject(ctx, input)
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, nil, ErrNotFound
		}
		var apiErr interface{ ErrorCode() string }
		if errors.As(err, &apiErr) {
			if code := apiErr.ErrorCode(); code == "NoSuchKey" || code == "NotFound" {
				return nil, nil, ErrNotFound
			}
		}
		return nil, nil, fmt.Errorf("getting object: %w", err)
	}

	meta := &FileMetadata{
		ContentType:    aws.ToString(out.ContentType),
		Filename:       out.Metadata["filename"],
		Version:        strings.Trim(aws.ToString(out.ETag), `"`),
		CustomSlug:     out.Metadata["custom-slug"] == "true",
		Flagged:        out.Metadata["intake-flagged"] == "true",
		FlagReason:     out.Metadata["intake-flag-reason"],
		FlagCategories: splitCSV(out.Metadata["intake-flag-categories"]),
	}
	if out.ContentRange != nil {
		meta.ContentRange = *out.ContentRange
		meta.ContentLength = aws.ToInt64(out.ContentLength)
	}
	return out.Body, meta, nil
}

func (s *s3Store) Delete(ctx context.Context, slug string) error {
	// Verify it exists first so we can return ErrNotFound
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(slug),
	})
	if err != nil {
		var nsk *types.NotFound
		if errors.As(err, &nsk) {
			return ErrNotFound
		}
		var apiErr interface{ ErrorCode() string }
		if errors.As(err, &apiErr) {
			if code := apiErr.ErrorCode(); code == "NotFound" || code == "NoSuchKey" {
				return ErrNotFound
			}
		}
		return fmt.Errorf("checking object: %w", err)
	}

	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(slug),
	})
	if err != nil {
		return fmt.Errorf("deleting object: %w", err)
	}
	return nil
}

func (s *s3Store) Metadata(ctx context.Context, slug string) (*FileMetadata, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(slug),
	})
	if err != nil {
		var nsk *types.NotFound
		if errors.As(err, &nsk) {
			return nil, ErrNotFound
		}
		var apiErr interface{ ErrorCode() string }
		if errors.As(err, &apiErr) {
			if code := apiErr.ErrorCode(); code == "NotFound" || code == "NoSuchKey" {
				return nil, ErrNotFound
			}
		}
		return nil, fmt.Errorf("heading object: %w", err)
	}

	return &FileMetadata{
		ContentType:    aws.ToString(out.ContentType),
		Filename:       out.Metadata["filename"],
		Version:        strings.Trim(aws.ToString(out.ETag), `"`),
		CustomSlug:     out.Metadata["custom-slug"] == "true",
		Flagged:        out.Metadata["intake-flagged"] == "true",
		FlagReason:     out.Metadata["intake-flag-reason"],
		FlagCategories: splitCSV(out.Metadata["intake-flag-categories"]),
	}, nil
}

func manifestKey(userID string) string {
	return "_users/" + userID + ".json"
}

func (s *s3Store) getManifest(ctx context.Context, userID string) (*UserManifest, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(manifestKey(userID)),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return &UserManifest{}, nil
		}
		var apiErr interface{ ErrorCode() string }
		if errors.As(err, &apiErr) {
			if code := apiErr.ErrorCode(); code == "NoSuchKey" || code == "NotFound" {
				return &UserManifest{}, nil
			}
		}
		return nil, fmt.Errorf("getting manifest: %w", err)
	}
	defer out.Body.Close()

	var m UserManifest
	if err := json.NewDecoder(out.Body).Decode(&m); err != nil {
		return &UserManifest{}, nil
	}
	return &m, nil
}

func (s *s3Store) putManifest(ctx context.Context, userID string, m *UserManifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshaling manifest: %w", err)
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(manifestKey(userID)),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("putting manifest: %w", err)
	}
	return nil
}

func (s *s3Store) RecordUpload(ctx context.Context, userID string, record ActivityRecord) error {
	m, err := s.getManifest(ctx, userID)
	if err != nil {
		return err
	}
	record.At = time.Now().UTC().Format(time.RFC3339)
	m.Uploads = append(m.Uploads, record)
	return s.putManifest(ctx, userID, m)
}

func (s *s3Store) RecordView(ctx context.Context, userID string, record ActivityRecord) error {
	m, err := s.getManifest(ctx, userID)
	if err != nil {
		return err
	}
	record.At = time.Now().UTC().Format(time.RFC3339)
	for i, v := range m.Views {
		if v.Slug == record.Slug {
			m.Views[i] = record
			return s.putManifest(ctx, userID, m)
		}
	}
	m.Views = append(m.Views, record)
	return s.putManifest(ctx, userID, m)
}

func (s *s3Store) GetManifest(ctx context.Context, userID string) (*UserManifest, error) {
	return s.getManifest(ctx, userID)
}

func siteManifestKey(siteSlug string) string {
	return "_sites/" + siteSlug + ".json"
}

func siteFileKey(siteSlug, path string) string {
	return siteSlug + "/" + path
}

func (s *s3Store) PutSiteManifest(ctx context.Context, siteSlug string, m *SiteManifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshaling site manifest: %w", err)
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(siteManifestKey(siteSlug)),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("putting site manifest: %w", err)
	}
	return nil
}

func (s *s3Store) HeadSite(ctx context.Context, siteSlug string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(siteManifestKey(siteSlug)),
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
	return false, fmt.Errorf("checking site manifest: %w", err)
}

func (s *s3Store) PutSiteFile(ctx context.Context, siteSlug, path string, body io.Reader, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(siteFileKey(siteSlug, path)),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("putting site file: %w", err)
	}
	return nil
}

func (s *s3Store) GetSiteFile(ctx context.Context, siteSlug, path string) (io.ReadCloser, *FileMetadata, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(siteFileKey(siteSlug, path)),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, nil, ErrNotFound
		}
		var apiErr interface{ ErrorCode() string }
		if errors.As(err, &apiErr) {
			if code := apiErr.ErrorCode(); code == "NoSuchKey" || code == "NotFound" {
				return nil, nil, ErrNotFound
			}
		}
		return nil, nil, fmt.Errorf("getting site file: %w", err)
	}
	meta := &FileMetadata{
		ContentType: aws.ToString(out.ContentType),
		Filename:    path,
		Version:     strings.Trim(aws.ToString(out.ETag), `"`),
	}
	return out.Body, meta, nil
}

func (s *s3Store) HeadSiteFile(ctx context.Context, siteSlug, path string) (*FileMetadata, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(siteFileKey(siteSlug, path)),
	})
	if err != nil {
		var nsk *types.NotFound
		if errors.As(err, &nsk) {
			return nil, ErrNotFound
		}
		var apiErr interface{ ErrorCode() string }
		if errors.As(err, &apiErr) {
			if code := apiErr.ErrorCode(); code == "NotFound" || code == "NoSuchKey" {
				return nil, ErrNotFound
			}
		}
		return nil, fmt.Errorf("heading site file: %w", err)
	}
	return &FileMetadata{
		ContentType: aws.ToString(out.ContentType),
		Filename:    path,
		Version:     strings.Trim(aws.ToString(out.ETag), `"`),
	}, nil
}

func commentKey(slug, id string) string {
	return "_comments/" + slug + "/" + id + ".json"
}

func commentPrefix(slug string) string {
	return "_comments/" + slug + "/"
}

func (s *s3Store) CreateComment(ctx context.Context, comment comments.Comment) error {
	return s.putComment(ctx, comment, "creating")
}

func (s *s3Store) UpdateComment(ctx context.Context, comment comments.Comment) error {
	return s.putComment(ctx, comment, "updating")
}

func (s *s3Store) GetComment(ctx context.Context, slug, id string) (comments.Comment, error) {
	got, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(commentKey(slug, id)),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return comments.Comment{}, comments.ErrCommentNotFound
		}
		var apiErr interface{ ErrorCode() string }
		if errors.As(err, &apiErr) {
			if code := apiErr.ErrorCode(); code == "NoSuchKey" || code == "NotFound" {
				return comments.Comment{}, comments.ErrCommentNotFound
			}
		}
		return comments.Comment{}, fmt.Errorf("getting comment: %w", err)
	}
	var comment comments.Comment
	decodeErr := json.NewDecoder(got.Body).Decode(&comment)
	closeErr := got.Body.Close()
	if decodeErr != nil {
		return comments.Comment{}, fmt.Errorf("decoding comment: %w", decodeErr)
	}
	if closeErr != nil {
		return comments.Comment{}, fmt.Errorf("closing comment: %w", closeErr)
	}
	return comment, nil
}

func (s *s3Store) putComment(ctx context.Context, comment comments.Comment, operation string) error {
	data, err := json.Marshal(comment)
	if err != nil {
		return fmt.Errorf("marshaling comment: %w", err)
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(commentKey(comment.Slug, comment.ID)),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("%s comment: %w", operation, err)
	}
	return nil
}

func (s *s3Store) ListComments(ctx context.Context, slug string) ([]comments.Comment, error) {
	var (
		items []comments.Comment
		token *string
	)
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(commentPrefix(slug)),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("listing comments: %w", err)
		}
		for _, object := range out.Contents {
			got, err := s.client.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(s.bucket),
				Key:    object.Key,
			})
			if err != nil {
				return nil, fmt.Errorf("getting comment %q: %w", aws.ToString(object.Key), err)
			}
			var comment comments.Comment
			decodeErr := json.NewDecoder(got.Body).Decode(&comment)
			closeErr := got.Body.Close()
			if decodeErr != nil {
				return nil, fmt.Errorf("decoding comment %q: %w", aws.ToString(object.Key), decodeErr)
			}
			if closeErr != nil {
				return nil, fmt.Errorf("closing comment %q: %w", aws.ToString(object.Key), closeErr)
			}
			items = append(items, comment)
		}
		if !aws.ToBool(out.IsTruncated) || out.NextContinuationToken == nil {
			return items, nil
		}
		token = out.NextContinuationToken
	}
}

func (s *s3Store) DeleteComments(ctx context.Context, slug string) error {
	token := (*string)(nil)
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(commentPrefix(slug)),
			ContinuationToken: token,
		})
		if err != nil {
			return fmt.Errorf("listing comments for deletion: %w", err)
		}
		for _, object := range out.Contents {
			if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(s.bucket),
				Key:    object.Key,
			}); err != nil {
				return fmt.Errorf("deleting comment %q: %w", aws.ToString(object.Key), err)
			}
		}
		if !aws.ToBool(out.IsTruncated) || out.NextContinuationToken == nil {
			return nil
		}
		token = out.NextContinuationToken
	}
}
