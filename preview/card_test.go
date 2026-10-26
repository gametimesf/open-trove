package preview

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func encodeImage(t *testing.T, format string, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buf, img)
	case "jpeg":
		err = jpeg.Encode(&buf, img, nil)
	case "gif":
		err = gif.Encode(&buf, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withTextChunk inserts a PNG tEXt chunk after IHDR, the way editors embed metadata.
func withTextChunk(pngData []byte, text string) []byte {
	const ihdrEnd = 8 + 25
	body := []byte("tEXt" + text)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(text)))
	chunk = append(chunk, body...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(body))
	return append(append(append([]byte{}, pngData[:ihdrEnd]...), chunk...), pngData[ihdrEnd:]...)
}

func TestNewCard(t *testing.T) {
	tests := []struct {
		name            string
		data            []byte
		alt             string
		wantErr         error
		wantContentType string
	}{
		{name: "png", data: encodeImage(t, "png", 1200, 630), alt: "Q3 summary", wantContentType: "image/png"},
		{name: "jpeg", data: encodeImage(t, "jpeg", 800, 400), wantContentType: "image/jpeg"},
		{name: "gif is rejected", data: encodeImage(t, "gif", 400, 400), wantErr: ErrCardFormat},
		{name: "not an image", data: []byte("<svg/>"), wantErr: ErrCardFormat},
		{name: "too small", data: encodeImage(t, "png", 99, 300), wantErr: ErrCardDimensions},
		{name: "too large in pixels", data: encodeImage(t, "png", 2001, 300), wantErr: ErrCardDimensions},
		{name: "too many bytes", data: make([]byte, MaxCardBytes+1), wantErr: ErrCardTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card, err := NewCard(tt.data, tt.alt)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewCard: %v", err)
			}
			if card.ContentType != tt.wantContentType || card.Alt != tt.alt || len(card.SHA256) != 64 || len(card.Image) == 0 {
				t.Fatalf("card = %+v", card)
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(card.Image))
			if err != nil || cfg.Width != card.Width || cfg.Height != card.Height {
				t.Fatalf("stored image %dx%d (%v), card says %dx%d", cfg.Width, cfg.Height, err, card.Width, card.Height)
			}
		})
	}
}

func TestNewCardStripsMetadataAndClipsAlt(t *testing.T) {
	data := withTextChunk(encodeImage(t, "png", 300, 200), "Author\x00someone@example.com")
	card, err := NewCard(data, strings.Repeat("a", MaxCardAlt+50))
	if err != nil {
		t.Fatalf("NewCard: %v", err)
	}
	if bytes.Contains(card.Image, []byte("someone@example.com")) {
		t.Error("embedded metadata survived re-encoding")
	}
	if n := len([]rune(card.Alt)); n > MaxCardAlt {
		t.Errorf("alt runes = %d", n)
	}
	if card.Extension() != ".png" || (Card{ContentType: "image/jpeg"}).Extension() != ".jpg" {
		t.Error("unexpected extensions")
	}
}
