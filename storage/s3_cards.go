package storage

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/gametimesf/open-trove/preview"
)

// Preview cards live at _previews/<slug>.json (metadata) with the image stored
// by content hash at _previews/<slug>/<sha256>.<ext>, so a reader never pairs
// new metadata with an old image.
func cardKey(slug string) string {
	return "_previews/" + slug + ".json"
}

func cardImageKey(slug string, card preview.Card) string {
	return "_previews/" + slug + "/" + card.SHA256 + card.Extension()
}

// validCardSlug rejects slugs that could address other internal objects.
func validCardSlug(slug string) bool {
	return slug != "" && !strings.HasPrefix(slug, "_") && !strings.ContainsAny(slug, "/\\")
}

func validCardHash(sum string) bool {
	b, err := hex.DecodeString(sum)
	return err == nil && len(b) == 32
}

func (s *s3Store) PutCard(ctx context.Context, slug string, card preview.Card) error {
	if !validCardSlug(slug) || !validCardHash(card.SHA256) {
		return fmt.Errorf("invalid preview card for %q", slug)
	}
	previous, prevErr := s.getCardMeta(ctx, slug)
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(cardImageKey(slug, card)),
		Body:        bytes.NewReader(card.Image),
		ContentType: aws.String(card.ContentType),
	}); err != nil {
		return fmt.Errorf("putting preview image: %w", err)
	}
	data, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshaling preview card: %w", err)
	}
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(cardKey(slug)),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
	}); err != nil {
		return fmt.Errorf("putting preview card: %w", err)
	}
	if prevErr == nil && previous.SHA256 != card.SHA256 {
		s.deleteCardImage(ctx, slug, previous)
	}
	return nil
}

func (s *s3Store) GetCard(ctx context.Context, slug string) (preview.Card, error) {
	card, err := s.getCardMeta(ctx, slug)
	if err != nil {
		return preview.Card{}, err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(cardImageKey(slug, card)),
	})
	if err != nil {
		if s3ErrorCode(err, "NoSuchKey", "NotFound") {
			return preview.Card{}, preview.ErrCardNotFound
		}
		return preview.Card{}, fmt.Errorf("getting preview image: %w", err)
	}
	defer out.Body.Close()
	card.Image, err = io.ReadAll(io.LimitReader(out.Body, preview.MaxCardBytes*2))
	if err != nil {
		return preview.Card{}, fmt.Errorf("reading preview image: %w", err)
	}
	return card, nil
}

func (s *s3Store) DeleteCard(ctx context.Context, slug string) error {
	card, err := s.getCardMeta(ctx, slug)
	if err != nil {
		return err
	}
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(cardKey(slug)),
	}); err != nil {
		return fmt.Errorf("deleting preview card: %w", err)
	}
	s.deleteCardImage(ctx, slug, card)
	return nil
}

func (s *s3Store) getCardMeta(ctx context.Context, slug string) (preview.Card, error) {
	if !validCardSlug(slug) {
		return preview.Card{}, preview.ErrCardNotFound
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(cardKey(slug)),
	})
	if err != nil {
		if s3ErrorCode(err, "NoSuchKey", "NotFound") {
			return preview.Card{}, preview.ErrCardNotFound
		}
		return preview.Card{}, fmt.Errorf("getting preview card: %w", err)
	}
	defer out.Body.Close()
	var card preview.Card
	if err := json.NewDecoder(out.Body).Decode(&card); err != nil {
		return preview.Card{}, fmt.Errorf("decoding preview card: %w", err)
	}
	if !validCardHash(card.SHA256) {
		return preview.Card{}, fmt.Errorf("preview card for %q has an invalid hash", slug)
	}
	return card, nil
}

// deleteCardImage removes a superseded image; a leftover image is harmless,
// so failures are only logged.
func (s *s3Store) deleteCardImage(ctx context.Context, slug string, card preview.Card) {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(cardImageKey(slug, card)),
	}); err != nil {
		log.Printf("WARN  deleting old preview image slug=%s: %v", slug, err)
	}
}
