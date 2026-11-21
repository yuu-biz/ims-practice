package ibcf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"

	"github.com/yuu-biz/ims-practice/internal/agw"
)

// AGWClient is a TrGW over the control API of the media relay of
// internal/agw, the same relay that serves the P-CSCF, here serving as
// the TrGW: POST
// /contexts reserves, PUT /contexts/{id}/terminations/{side} configures
// and DELETE /contexts/{id} releases. The relay names its terminations
// after the P-CSCF's use, so the core termination faces the home
// network here and the access termination faces the peer.
//
// The relay writes one address for both terminations. A TrGW has one
// address in each network, so CorePublic and PeerPublic, when set, take
// the place of the relay's address in what the SDP shows on that side,
// with the port the relay chose; the relay takes the media on every
// interface.
type AGWClient struct {
	base       string // such as http://trgw:8080
	http       *http.Client
	corePublic netip.Addr
	peerPublic netip.Addr
}

// NewAGWClient drives the relay at base. corePublic and peerPublic are
// the TrGW's addresses in the home and the peer network; the zero
// address keeps the relay's own.
func NewAGWClient(base string, corePublic, peerPublic netip.Addr) *AGWClient {
	return &AGWClient{base: base, http: &http.Client{Timeout: trgwTimeout}, corePublic: corePublic, peerPublic: peerPublic}
}

// agwSides maps the sides of a TrGW to the terminations of the relay.
var agwSides = map[Side]agw.TerminationID{CoreSide: agw.Core, PeerSide: agw.Access}

func (c *AGWClient) Reserve(ctx context.Context) (MediaContext, error) {
	var relay agw.Context
	add := map[string][]agw.TerminationRequest{"terminations": {{ID: agw.Access}, {ID: agw.Core}}}
	if err := c.do(ctx, http.MethodPost, "/contexts", add, &relay); err != nil {
		return MediaContext{}, err
	}
	core, peer := relay.Terminations[agw.Core], relay.Terminations[agw.Access]
	if core == nil || peer == nil {
		return MediaContext{}, fmt.Errorf("ibcf: TrGW context %s lacks a termination", relay.ID)
	}
	return MediaContext{
		ID:   relay.ID,
		Core: withAddress(core.Local, c.corePublic),
		Peer: withAddress(peer.Local, c.peerPublic),
	}, nil
}

func (c *AGWClient) Configure(ctx context.Context, id string, s Side, remote netip.AddrPort) error {
	side, ok := agwSides[s]
	if !ok {
		return fmt.Errorf("ibcf: unknown TrGW side %q", s)
	}
	return c.do(ctx, http.MethodPut, "/contexts/"+id+"/terminations/"+string(side),
		map[string]netip.AddrPort{"remote": remote}, nil)
}

func (c *AGWClient) Release(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/contexts/"+id, nil, nil)
}

func withAddress(local netip.AddrPort, public netip.Addr) netip.AddrPort {
	if !public.IsValid() {
		return local
	}
	return netip.AddrPortFrom(public, local.Port())
}

// ErrNoContext reports a context that the TrGW does not know, such as
// one that has been released already.
var ErrNoContext = errors.New("ibcf: the TrGW has no such context")

func (c *AGWClient) do(ctx context.Context, method, path string, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&body).Encode(in); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, &body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ibcf: TrGW %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s %s", ErrNoContext, method, path)
	case resp.StatusCode != http.StatusOK:
		var msg bytes.Buffer
		msg.ReadFrom(resp.Body)
		return fmt.Errorf("ibcf: TrGW %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(msg.Bytes()))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
