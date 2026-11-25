package diameter

import (
	"context"
	"slices"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam"
)

// Request sends a request to the peer that connected with originHost in
// its CER, and waits for the answer as Peer.Request does. The HSS uses
// it to reach an S-CSCF, which is the one that opened the connection.
// Without such a peer it returns ErrPeerUnavailable.
func (s *Server) Request(ctx context.Context, originHost string, request *diam.Message) (*diam.Message, error) {
	s.mu.Lock()
	c, ok := s.peers[strings.ToLower(originHost)]
	s.mu.Unlock()
	if !ok {
		return nil, ErrPeerUnavailable
	}
	return c.peer.Request(ctx, request)
}

// Hosts are the Origin-Hosts of the peers connected to the server whose
// CER named realm in Origin-Realm and advertised application: the peers
// of that realm that a request of the application can be sent to, as a
// realm and an Application Id are what RFC 6733 6.1.6 routes a request
// by. A realm is an FQDN, which compares without case. The hosts come
// sorted, so that their order does not change from one call to the
// next.
func (s *Server) Hosts(realm string, application uint32) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var hosts []string
	for _, c := range s.peers {
		if strings.EqualFold(c.realm, realm) && slices.Contains(c.applications, application) {
			hosts = append(hosts, c.peer.Host())
		}
	}
	slices.Sort(hosts)
	return hosts
}
