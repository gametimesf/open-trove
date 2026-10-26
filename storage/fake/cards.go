package fake

import (
	"bytes"
	"context"

	"github.com/gametimesf/open-trove/preview"
)

func (s *Store) PutCard(_ context.Context, slug string, card preview.Card) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	card.Image = bytes.Clone(card.Image)
	s.cards[slug] = card
	return nil
}

func (s *Store) GetCard(_ context.Context, slug string) (preview.Card, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	card, ok := s.cards[slug]
	if !ok {
		return preview.Card{}, preview.ErrCardNotFound
	}
	card.Image = bytes.Clone(card.Image)
	return card, nil
}

func (s *Store) DeleteCard(_ context.Context, slug string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cards[slug]; !ok {
		return preview.ErrCardNotFound
	}
	delete(s.cards, slug)
	return nil
}
