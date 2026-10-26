package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/gametimesf/open-trove/preview"
)

func testCard(image string, sum byte) preview.Card {
	return preview.Card{Image: []byte(image), ContentType: "image/png", Width: 1200, Height: 630, SHA256: string(bytes.Repeat([]byte{'0' + sum%10}, 64))}
}

func TestS3CardLifecycle(t *testing.T) {
	ctx := context.Background()
	api := newConditionalS3API()
	store := &s3Store{client: api, bucket: "b"}

	if _, err := store.GetCard(ctx, "doc"); !errors.Is(err, preview.ErrCardNotFound) {
		t.Fatalf("GetCard on empty store = %v, want ErrCardNotFound", err)
	}

	first := testCard("first", 1)
	if err := store.PutCard(ctx, "doc", first); err != nil {
		t.Fatalf("PutCard: %v", err)
	}
	got, err := store.GetCard(ctx, "doc")
	if err != nil || string(got.Image) != "first" || got.SHA256 != first.SHA256 || got.Width != 1200 {
		t.Fatalf("GetCard = %+v, %v", got, err)
	}

	second := testCard("second", 2)
	if err := store.PutCard(ctx, "doc", second); err != nil {
		t.Fatalf("replacing card: %v", err)
	}
	if _, ok := api.objects[cardImageKey("doc", first)]; ok {
		t.Error("superseded image was not deleted")
	}
	if got, _ := store.GetCard(ctx, "doc"); string(got.Image) != "second" {
		t.Fatalf("image after replace = %q", got.Image)
	}

	if err := store.DeleteCard(ctx, "doc"); err != nil {
		t.Fatalf("DeleteCard: %v", err)
	}
	if len(api.objects) != 0 {
		t.Errorf("objects left after delete: %v", api.objects)
	}
	if err := store.DeleteCard(ctx, "doc"); !errors.Is(err, preview.ErrCardNotFound) {
		t.Fatalf("second DeleteCard = %v, want ErrCardNotFound", err)
	}
}

func TestS3CardRejectsUnsafeKeys(t *testing.T) {
	ctx := context.Background()
	store := &s3Store{client: newConditionalS3API(), bucket: "b"}
	for _, slug := range []string{"", "_tokens", "a/b", `a\b`} {
		if err := store.PutCard(ctx, slug, testCard("x", 1)); err == nil {
			t.Errorf("PutCard(%q) succeeded", slug)
		}
		if _, err := store.GetCard(ctx, slug); !errors.Is(err, preview.ErrCardNotFound) {
			t.Errorf("GetCard(%q) = %v, want ErrCardNotFound", slug, err)
		}
	}
	bad := testCard("x", 1)
	bad.SHA256 = "../../_tokens/x"
	if err := store.PutCard(ctx, "doc", bad); err == nil {
		t.Error("PutCard accepted a non-hex hash")
	}
}
