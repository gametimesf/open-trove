package preview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"time"
)

// Card image limits. RecommendedCardSize is a common link-preview aspect.
const (
	MaxCardBytes     = 2 << 20
	MaxCardDimension = 2000
	MinCardDimension = 100
	MaxCardAlt       = 300
)

// RecommendedCardSize is the suggested card size in pixels.
var RecommendedCardSize = image.Point{X: 1200, Y: 630}

// Card errors. ErrCardNotFound means no card has been set for an artifact.
var (
	ErrCardNotFound   = errors.New("preview card not found")
	ErrCardTooLarge   = fmt.Errorf("preview image exceeds %d bytes", MaxCardBytes)
	ErrCardFormat     = errors.New("preview image must be PNG or JPEG")
	ErrCardDimensions = fmt.Errorf("preview image must be between %d and %d pixels on each side", MinCardDimension, MaxCardDimension)
)

// Card is an author-supplied preview image for an artifact, shown in place of
// the generated excerpt wherever previews are rendered.
type Card struct {
	Image           []byte    `json:"-"`
	ContentType     string    `json:"content_type"`
	Width           int       `json:"width"`
	Height          int       `json:"height"`
	Alt             string    `json:"alt,omitempty"`
	SHA256          string    `json:"sha256"`
	ArtifactVersion string    `json:"artifact_version,omitempty"` // version the card was made for
	UpdatedAt       time.Time `json:"updated_at"`
	UpdatedByEmail  string    `json:"updated_by_email,omitempty"`
}

// CardRepository persists preview cards.
type CardRepository interface {
	PutCard(ctx context.Context, slug string, card Card) error
	// GetCard returns ErrCardNotFound when no card is set.
	GetCard(ctx context.Context, slug string) (Card, error)
	// DeleteCard returns ErrCardNotFound when no card is set.
	DeleteCard(ctx context.Context, slug string) error
}

// NewCard validates an uploaded image and re-encodes it, dropping embedded
// metadata. Dimensions are checked before decoding pixels so oversized images
// are rejected cheaply.
func NewCard(data []byte, alt string) (Card, error) {
	if len(data) > MaxCardBytes {
		return Card{}, ErrCardTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") {
		return Card{}, ErrCardFormat
	}
	if cfg.Width < MinCardDimension || cfg.Height < MinCardDimension || cfg.Width > MaxCardDimension || cfg.Height > MaxCardDimension {
		return Card{}, ErrCardDimensions
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Card{}, ErrCardFormat
	}
	var buf bytes.Buffer
	contentType := "image/png"
	if format == "jpeg" {
		contentType = "image/jpeg"
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	} else {
		err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img)
	}
	if err != nil {
		return Card{}, fmt.Errorf("encoding preview image: %w", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return Card{
		Image:       buf.Bytes(),
		ContentType: contentType,
		Width:       cfg.Width,
		Height:      cfg.Height,
		Alt:         clip(alt, MaxCardAlt),
		SHA256:      hex.EncodeToString(sum[:]),
	}, nil
}

// Extension is the file extension for the card's image format.
func (c Card) Extension() string {
	if c.ContentType == "image/jpeg" {
		return ".jpg"
	}
	return ".png"
}
