package fake

import (
	"context"
	"sort"
	"time"

	"github.com/gametimesf/open-trove/auth"
)

// CreateToken stores a token record, failing if the ID exists.
func (s *Store) CreateToken(_ context.Context, token auth.Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tokens[token.ID]; ok {
		return auth.ErrTokenExists
	}
	s.tokens[token.ID] = token
	return nil
}

// GetToken returns a token record or auth.ErrTokenNotFound.
func (s *Store) GetToken(_ context.Context, id string) (auth.Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	token, ok := s.tokens[id]
	if !ok {
		return auth.Token{}, auth.ErrTokenNotFound
	}
	return token, nil
}

// ListTokens returns the owner's tokens, newest first.
func (s *Store) ListTokens(_ context.Context, ownerEmail string) ([]auth.Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tokens := make([]auth.Token, 0, len(s.tokens))
	for _, token := range s.tokens {
		if token.OwnerEmail == ownerEmail {
			tokens = append(tokens, token)
		}
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].CreatedAt.After(tokens[j].CreatedAt) })
	return tokens, nil
}

// DeleteToken removes one of the owner's tokens.
func (s *Store) DeleteToken(_ context.Context, ownerEmail, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.tokens[id]
	if !ok || token.OwnerEmail != ownerEmail {
		return auth.ErrTokenNotFound
	}
	delete(s.tokens, id)
	return nil
}

// TouchToken records the last-used time.
func (s *Store) TouchToken(_ context.Context, id string, usedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.tokens[id]
	if !ok {
		return auth.ErrTokenNotFound
	}
	token.LastUsedAt = &usedAt
	s.tokens[id] = token
	return nil
}
