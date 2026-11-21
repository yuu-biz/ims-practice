package diameter

import (
	"context"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam"
)

// Request sends a request to the peer that connected with originHost in
// its CER, and waits for the answer as Peer.Request does. The HSS uses
// it to reach an S-CSCF, which is the one that opened the connection.
// Without such a peer it returns ErrPeerUnavailable.
func (s *Server) Request(ctx context.Context, originHost string, request *diam.Message) (*diam.Message, error) {
	s.mu.Lock()
	p, ok := s.peers[strings.ToLower(originHost)]
	s.mu.Unlock()
	if !ok {
		return nil, ErrPeerUnavailable
	}
	return p.Request(ctx, request)
}
