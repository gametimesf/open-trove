package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type manifestAPI struct {
	*memoryS3API
	data       []byte
	revision   int
	puts       int
	concurrent []byte
	failCode   string
}

func (m *manifestAPI) GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.data == nil {
		return nil, &smithy.GenericAPIError{Code: "NoSuchKey"}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(m.data)), ETag: aws.String(fmt.Sprint(m.revision))}, nil
}
func (m *manifestAPI) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	m.puts++
	if m.concurrent != nil {
		m.data = m.concurrent
		m.concurrent = nil
		m.revision++
	}
	if m.failCode != "" {
		return nil, &smithy.GenericAPIError{Code: m.failCode}
	}
	if (aws.ToString(in.IfNoneMatch) == "*" && m.data != nil) || (in.IfMatch != nil && aws.ToString(in.IfMatch) != fmt.Sprint(m.revision)) {
		return nil, &smithy.GenericAPIError{Code: "PreconditionFailed"}
	}
	if in.IfMatch == nil && in.IfNoneMatch == nil {
		return nil, errors.New("unconditional manifest write")
	}
	m.data, _ = io.ReadAll(in.Body)
	m.revision++
	return &s3.PutObjectOutput{}, nil
}
func TestManifestConflictsPreserveOtherActivity(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			api := &manifestAPI{memoryS3API: newMemoryS3API()}
			if existing {
				api.data = []byte(`{"uploads":[],"views":[]}`)
				api.revision = 1
			}
			api.concurrent = []byte(`{"uploads":[{"slug":"concurrent-upload"}],"views":[{"slug":"concurrent-view","at":"2026-01-01T00:00:00Z"}]}`)
			store := &s3Store{client: api, bucket: "test"}
			if err := store.RecordView(t.Context(), "browser", ActivityRecord{Slug: "new-view"}); err != nil {
				t.Fatal(err)
			}
			got, err := store.GetManifest(t.Context(), "browser")
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Uploads) != 1 || len(got.Views) != 2 || api.puts != 2 {
				t.Fatalf("lost concurrent state: %+v, writes=%d", got, api.puts)
			}
		})
	}
}
func TestManifestUpdateFailureIsBounded(t *testing.T) {
	for _, code := range []string{"PreconditionFailed", "ConditionalRequestConflict", "AccessDenied"} {
		t.Run(code, func(t *testing.T) {
			api := &manifestAPI{memoryS3API: newMemoryS3API(), failCode: code}
			store := &s3Store{client: api, bucket: "test"}
			err := store.RecordUpload(t.Context(), "browser", ActivityRecord{Slug: "upload"})
			want := 5
			if code == "AccessDenied" {
				want = 1
			}
			if err == nil || api.puts != want {
				t.Fatalf("error=%v writes=%d want=%d", err, api.puts, want)
			}
		})
	}
}
func TestInvalidManifestIsNotOverwritten(t *testing.T) {
	api := &manifestAPI{memoryS3API: newMemoryS3API(), data: []byte(`{"uploads":`), revision: 1}
	store := &s3Store{client: api, bucket: "test"}
	if _, err := store.GetManifest(t.Context(), "browser"); err == nil {
		t.Fatal("read must fail")
	}
	if err := store.RecordView(t.Context(), "browser", ActivityRecord{Slug: "view"}); err == nil {
		t.Fatal("write must fail")
	}
	if api.puts != 0 {
		t.Fatal("corrupt manifest overwritten")
	}
}
func TestManifestCancelledConflictStopsRetry(t *testing.T) {
	api := &manifestAPI{memoryS3API: newMemoryS3API(), failCode: "PreconditionFailed"}
	store := &s3Store{client: api, bucket: "test"}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := store.RecordView(ctx, "browser", ActivityRecord{Slug: "view"})
	if !errors.Is(err, context.Canceled) || api.puts > 1 {
		t.Fatalf("err=%v writes=%d", err, api.puts)
	}
}
func TestActivityMergingPreservesRecencyAndAttribution(t *testing.T) {
	m := &UserManifest{Uploads: []ActivityRecord{{Slug: "own", UserEmail: "owner@example.com"}}}
	m.RecordView(ActivityRecord{Slug: "own", UserEmail: "viewer@example.com", At: "2026-01-01T00:00:00.2Z"})
	m.RecordView(ActivityRecord{Slug: "own", UserEmail: "viewer@example.com", At: "2026-01-01T00:00:00.1Z"})
	if len(m.Views) != 1 || m.Views[0].At != "2026-01-01T00:00:00.2Z" || m.Views[0].OwnerEmail != "owner@example.com" {
		t.Fatalf("%+v", m.Views)
	}
	m.RecordUpload(ActivityRecord{Slug: "own", At: "2026-01-01T00:00:00.3Z"})
	if len(m.Uploads) != 1 {
		t.Fatalf("duplicate uploads: %+v", m.Uploads)
	}
}
func TestS3SiteAttributionRoundTrip(t *testing.T) {
	api := newMemoryS3API()
	store := &s3Store{client: api, bucket: "test"}
	want := &SiteManifest{Entry: "index.html", FileCount: 2, OwnerEmail: "owner@example.com"}
	if err := store.PutSiteManifest(t.Context(), "site", want); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSiteManifest(t.Context(), "site")
	if err != nil || *got != *want {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	var legacy SiteManifest
	if err := json.NewDecoder(strings.NewReader(`{"entry":"index.html","file_count":1}`)).Decode(&legacy); err != nil || legacy.OwnerEmail != "" {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
}
