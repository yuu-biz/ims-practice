package kvstore

// MemoryItems is the number of items that s holds, expired or not.
func MemoryItems(s *MemoryStore) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}
