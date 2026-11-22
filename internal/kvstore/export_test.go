package kvstore

import (
	"context"
	"net"
)

// MemoryItems is the number of items that s holds, expired or not.
func MemoryItems(s *MemoryStore) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

// SetDialValkey replaces how the Valkey client opens its connections,
// until the test ends.
func SetDialValkey(t interface{ Cleanup(func()) }, dial func(ctx context.Context, addr string) (net.Conn, error)) {
	old := dialValkey
	dialValkey = dial
	t.Cleanup(func() { dialValkey = old })
}
