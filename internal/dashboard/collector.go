package dashboard

import (
	"errors"
	"net"

	"github.com/yuu-biz/ims-practice/internal/hep"
)

// ServeHEP collects the HEP packets that arrive on conn, one per
// datagram, until conn is closed. A packet that cannot be read is logged
// and skipped.
func (d *Dashboard) ServeHEP(conn *net.UDPConn) error {
	// A HEP packet's total length is two octets, so no packet is larger.
	buf := make([]byte, 65535)
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		if err != nil {
			return err
		}
		// A dual-stack socket reports an IPv4 sender as ::ffff:a.b.c.d.
		sender := from.Addr().Unmap()
		p, err := hep.Unmarshal(buf[:n])
		if err != nil {
			d.log.Warn("HEP packet skipped", "from", sender.String(), "error", err.Error())
			continue
		}
		d.ingest(p, sender)
	}
}
