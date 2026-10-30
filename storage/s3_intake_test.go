package storage

import (
	"context"
	"strings"
	"testing"
)

func TestS3IntakeVerdicts(t *testing.T) {
	ctx := context.Background()
	api := newConditionalS3API()
	store := &s3Store{client: api, bucket: "b"}
	key := strings.Repeat("ab", 32)

	if ok, err := store.HasAllowedVerdict(ctx, key); err != nil || ok {
		t.Fatalf("HasAllowedVerdict on empty store = %v, %v", ok, err)
	}
	if err := store.PutAllowedVerdict(ctx, key); err != nil {
		t.Fatalf("PutAllowedVerdict: %v", err)
	}
	if _, ok := api.objects["_intake/"+key]; !ok {
		t.Errorf("verdict not stored under _intake/: %v", api.objects)
	}
	if ok, err := store.HasAllowedVerdict(ctx, key); err != nil || !ok {
		t.Fatalf("HasAllowedVerdict after put = %v, %v", ok, err)
	}
}

func TestS3IntakeVerdictsRejectUnsafeKeys(t *testing.T) {
	ctx := context.Background()
	store := &s3Store{client: newConditionalS3API(), bucket: "b"}
	for _, key := range []string{"", "../_tokens", "abc", strings.Repeat("zz", 32)} {
		if err := store.PutAllowedVerdict(ctx, key); err == nil {
			t.Errorf("PutAllowedVerdict(%q) succeeded", key)
		}
		if _, err := store.HasAllowedVerdict(ctx, key); err == nil {
			t.Errorf("HasAllowedVerdict(%q) succeeded", key)
		}
	}
}
