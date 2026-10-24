package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/gametimesf/open-trove/auth"
)

// conditionalS3API models the S3 behavior token storage relies on: missing
// keys, ETags, and If-None-Match / If-Match on PutObject.
type conditionalS3API struct {
	objects map[string][]byte
	etags   map[string]string
	serial  int
	// beforePut runs before each PutObject, letting tests interleave writes.
	beforePut func(key string)
}

func newConditionalS3API() *conditionalS3API {
	return &conditionalS3API{objects: map[string][]byte{}, etags: map[string]string{}}
}

func (m *conditionalS3API) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	key := aws.ToString(in.Key)
	if m.beforePut != nil {
		m.beforePut(key)
	}
	_, exists := m.objects[key]
	if aws.ToString(in.IfNoneMatch) == "*" && exists {
		return nil, &smithy.GenericAPIError{Code: "PreconditionFailed"}
	}
	if in.IfMatch != nil && (!exists || aws.ToString(in.IfMatch) != m.etags[key]) {
		return nil, &smithy.GenericAPIError{Code: "PreconditionFailed"}
	}
	data, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	m.serial++
	m.objects[key] = data
	m.etags[key] = fmt.Sprintf(`"%d"`, m.serial)
	return &s3.PutObjectOutput{ETag: aws.String(m.etags[key])}, nil
}

func (m *conditionalS3API) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	key := aws.ToString(in.Key)
	data, ok := m.objects[key]
	if !ok {
		return nil, &types.NoSuchKey{}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data)), ETag: aws.String(m.etags[key])}, nil
}

func (m *conditionalS3API) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	delete(m.objects, aws.ToString(in.Key))
	delete(m.etags, aws.ToString(in.Key))
	return &s3.DeleteObjectOutput{}, nil
}

func (m *conditionalS3API) HeadObject(_ context.Context, in *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	if _, ok := m.objects[aws.ToString(in.Key)]; !ok {
		return nil, &types.NotFound{}
	}
	return &s3.HeadObjectOutput{}, nil
}

func (m *conditionalS3API) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	keys := make([]string, 0, len(m.objects))
	for key := range m.objects {
		if strings.HasPrefix(key, aws.ToString(in.Prefix)) {
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

var tokenTestTime = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

func testToken(id, owner string, created time.Time) auth.Token {
	return auth.Token{ID: id, OwnerEmail: owner, Name: "cli", CreatedAt: created, ExpiresAt: created.Add(24 * time.Hour)}
}

func tokenID(n int) string {
	return fmt.Sprintf("%064x", n)
}

func TestS3TokenLifecycle(t *testing.T) {
	ctx := context.Background()
	api := newConditionalS3API()
	store := &s3Store{client: api, bucket: "b"}

	alice1 := testToken(tokenID(1), "alice@example.com", tokenTestTime)
	alice2 := testToken(tokenID(2), "alice@example.com", tokenTestTime.Add(time.Hour))
	bob := testToken(tokenID(3), "bob@example.com", tokenTestTime)
	for _, tok := range []auth.Token{alice1, alice2, bob} {
		if err := store.CreateToken(ctx, tok); err != nil {
			t.Fatalf("CreateToken(%s): %v", tok.ID, err)
		}
	}
	if err := store.CreateToken(ctx, alice1); !errors.Is(err, auth.ErrTokenExists) {
		t.Fatalf("duplicate CreateToken error = %v, want ErrTokenExists", err)
	}

	got, err := store.GetToken(ctx, alice1.ID)
	if err != nil || got.OwnerEmail != "alice@example.com" {
		t.Fatalf("GetToken = %+v, %v", got, err)
	}

	list, err := store.ListTokens(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(list) != 2 || list[0].ID != alice2.ID || list[1].ID != alice1.ID {
		t.Fatalf("ListTokens = %+v, want alice2 then alice1", list)
	}

	if err := store.DeleteToken(ctx, "bob@example.com", alice1.ID); !errors.Is(err, auth.ErrTokenNotFound) {
		t.Fatalf("cross-owner DeleteToken error = %v, want ErrTokenNotFound", err)
	}
	if err := store.DeleteToken(ctx, "alice@example.com", alice1.ID); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	if _, err := store.GetToken(ctx, alice1.ID); !errors.Is(err, auth.ErrTokenNotFound) {
		t.Fatalf("GetToken after delete error = %v, want ErrTokenNotFound", err)
	}
	if _, ok := api.objects[tokenOwnerPrefix("alice@example.com")+alice1.ID]; ok {
		t.Fatal("owner index marker was not removed")
	}
}

func TestS3ListTokensSkipsOrphanedIndex(t *testing.T) {
	ctx := context.Background()
	api := newConditionalS3API()
	store := &s3Store{client: api, bucket: "b"}
	tok := testToken(tokenID(1), "alice@example.com", tokenTestTime)
	if err := store.CreateToken(ctx, tok); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	// Simulate a revoke whose index cleanup failed.
	delete(api.objects, tokenKey(tok.ID))

	list, err := store.ListTokens(ctx, "alice@example.com")
	if err != nil || len(list) != 0 {
		t.Fatalf("ListTokens = %+v, %v; want empty", list, err)
	}
}

func TestS3TouchToken(t *testing.T) {
	ctx := context.Background()
	used := tokenTestTime.Add(2 * time.Hour)

	t.Run("records last used", func(t *testing.T) {
		store := &s3Store{client: newConditionalS3API(), bucket: "b"}
		tok := testToken(tokenID(1), "alice@example.com", tokenTestTime)
		if err := store.CreateToken(ctx, tok); err != nil {
			t.Fatalf("CreateToken: %v", err)
		}
		if err := store.TouchToken(ctx, tok.ID, used); err != nil {
			t.Fatalf("TouchToken: %v", err)
		}
		got, _ := store.GetToken(ctx, tok.ID)
		if got.LastUsedAt == nil || !got.LastUsedAt.Equal(used) {
			t.Fatalf("LastUsedAt = %v, want %v", got.LastUsedAt, used)
		}
	})

	t.Run("revocation during touch is not undone", func(t *testing.T) {
		api := newConditionalS3API()
		store := &s3Store{client: api, bucket: "b"}
		tok := testToken(tokenID(1), "alice@example.com", tokenTestTime)
		if err := store.CreateToken(ctx, tok); err != nil {
			t.Fatalf("CreateToken: %v", err)
		}
		// Revoke after TouchToken reads the record but before it writes.
		api.beforePut = func(key string) {
			if key == tokenKey(tok.ID) {
				api.beforePut = nil
				if err := store.DeleteToken(ctx, "alice@example.com", tok.ID); err != nil {
					t.Fatalf("DeleteToken: %v", err)
				}
			}
		}
		if err := store.TouchToken(ctx, tok.ID, used); !errors.Is(err, auth.ErrTokenNotFound) {
			t.Fatalf("TouchToken error = %v, want ErrTokenNotFound", err)
		}
		if _, err := store.GetToken(ctx, tok.ID); !errors.Is(err, auth.ErrTokenNotFound) {
			t.Fatal("revoked token was resurrected by TouchToken")
		}
	})
}

func TestS3TokenRejectsInvalidIDs(t *testing.T) {
	ctx := context.Background()
	store := &s3Store{client: newConditionalS3API(), bucket: "b"}
	for _, id := range []string{"", "../comments/x", "zz" + tokenID(1)[2:], tokenID(1) + "/x"} {
		if _, err := store.GetToken(ctx, id); !errors.Is(err, auth.ErrTokenNotFound) {
			t.Errorf("GetToken(%q) error = %v, want ErrTokenNotFound", id, err)
		}
	}
}
