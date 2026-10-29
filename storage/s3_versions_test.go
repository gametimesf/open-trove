package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestS3PutIfMatch(t *testing.T) {
	ctx := context.Background()
	api := newConditionalS3API()
	store := &s3Store{client: api, bucket: "b"}

	if err := store.Put(ctx, "doc", strings.NewReader("v1"), PutOptions{ContentType: "text/plain", IfMatch: "1"}); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("conditional put on a missing object = %v, want ErrVersionMismatch", err)
	}
	if err := store.Put(ctx, "doc", strings.NewReader("v1"), PutOptions{ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	current := strings.Trim(api.etags["doc"], `"`)

	if err := store.Put(ctx, "doc", strings.NewReader("stale"), PutOptions{ContentType: "text/plain", IfMatch: "not-" + current}); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("stale conditional put = %v, want ErrVersionMismatch", err)
	}
	if string(api.objects["doc"]) != "v1" {
		t.Fatalf("stale write changed the object to %q", api.objects["doc"])
	}
	if err := store.Put(ctx, "doc", strings.NewReader("v2"), PutOptions{ContentType: "text/plain", IfMatch: current}); err != nil {
		t.Fatalf("conditional put with the current version: %v", err)
	}
	if string(api.objects["doc"]) != "v2" {
		t.Fatalf("object = %q, want v2", api.objects["doc"])
	}
}
