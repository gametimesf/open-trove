package fake

import "context"

func (s *Store) HasAllowedVerdict(_ context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.verdicts[key]
	return ok, nil
}

func (s *Store) PutAllowedVerdict(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verdicts[key] = struct{}{}
	return nil
}
