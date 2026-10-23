package storage

import (
	"bytes"
	"context"
	"io"
	"sort"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/gametimesf/open-trove/comments"
)

type memoryS3API struct {
	objects     map[string][]byte
	lastHeadKey string
}

func newMemoryS3API() *memoryS3API {
	return &memoryS3API{objects: map[string][]byte{}}
}

func (m *memoryS3API) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	data, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	m.objects[aws.ToString(input.Key)] = data
	return &s3.PutObjectOutput{}, nil
}

func (m *memoryS3API) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	data := m.objects[aws.ToString(input.Key)]
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
}

func (m *memoryS3API) DeleteObject(_ context.Context, input *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	delete(m.objects, aws.ToString(input.Key))
	return &s3.DeleteObjectOutput{}, nil
}

func (m *memoryS3API) HeadObject(_ context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	m.lastHeadKey = aws.ToString(input.Key)
	return &s3.HeadObjectOutput{
		ContentType: aws.String("text/html; charset=utf-8"),
		ETag:        aws.String(`"site-version"`),
	}, nil
}

func (m *memoryS3API) ListObjectsV2(_ context.Context, input *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	keys := make([]string, 0)
	for key := range m.objects {
		if len(key) >= len(aws.ToString(input.Prefix)) && key[:len(aws.ToString(input.Prefix))] == aws.ToString(input.Prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := &s3.ListObjectsV2Output{IsTruncated: aws.Bool(false)}
	for _, key := range keys {
		out.Contents = append(out.Contents, types.Object{Key: aws.String(key)})
	}
	return out, nil
}

func TestS3StoreCommentsRoundTrip(t *testing.T) {
	api := newMemoryS3API()
	store := &s3Store{client: api, bucket: "test"}
	want := comments.Comment{
		ID:          "comment-1",
		Slug:        "report",
		AuthorEmail: "author@example.com",
		Body:        "Looks good",
		CreatedAt:   time.Date(2026, 7, 29, 18, 0, 0, 0, time.UTC),
		Anchor:      comments.Anchor{Type: comments.AnchorFile},
	}

	if err := store.CreateComment(context.Background(), want); err != nil {
		t.Fatalf("CreateComment() error = %v", err)
	}
	single, err := store.GetComment(context.Background(), "report", "comment-1")
	if err != nil || single.ID != want.ID || single.Body != want.Body {
		t.Fatalf("GetComment() = %#v, %v", single, err)
	}
	got, err := store.ListComments(context.Background(), "report")
	if err != nil {
		t.Fatalf("ListComments() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != want.ID || got[0].Body != want.Body {
		t.Fatalf("ListComments() = %#v", got)
	}
	want.Body = "Updated"
	if err := store.UpdateComment(context.Background(), want); err != nil {
		t.Fatalf("UpdateComment() error = %v", err)
	}
	got, err = store.ListComments(context.Background(), "report")
	if err != nil || len(got) != 1 || got[0].Body != "Updated" {
		t.Fatalf("ListComments() after update = %#v, %v", got, err)
	}
	if err := store.DeleteComments(context.Background(), "report"); err != nil {
		t.Fatalf("DeleteComments() error = %v", err)
	}
	got, err = store.ListComments(context.Background(), "report")
	if err != nil || len(got) != 0 {
		t.Fatalf("ListComments() after delete = %#v, %v", got, err)
	}
}

func TestS3StoreHeadSiteFile(t *testing.T) {
	api := newMemoryS3API()
	store := &s3Store{client: api, bucket: "test"}

	meta, err := store.HeadSiteFile(context.Background(), "site", "pages/index.html")
	if err != nil {
		t.Fatalf("HeadSiteFile() error = %v", err)
	}
	if api.lastHeadKey != "site/pages/index.html" {
		t.Fatalf("HeadObject key = %q, want site file key", api.lastHeadKey)
	}
	if meta.Filename != "pages/index.html" || meta.ContentType != "text/html; charset=utf-8" || meta.Version != "site-version" {
		t.Fatalf("HeadSiteFile() metadata = %#v", meta)
	}
}
