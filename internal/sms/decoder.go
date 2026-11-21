package sms

import "errors"

var errTruncated = errors.New("truncated")

// decoder reads octets from the front of b. The first error sticks, so a
// parser reads its fields in the order the layout lists them and checks
// err once at the end.
type decoder struct {
	b   []byte
	err error
}

func (d *decoder) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

func (d *decoder) octet() byte {
	b := d.octets(1)
	if len(b) == 0 {
		return 0
	}
	return b[0]
}

func (d *decoder) octets(n int) []byte {
	if d.err != nil {
		return nil
	}
	if len(d.b) < n {
		d.fail(errTruncated)
		return nil
	}
	b := d.b[:n:n]
	d.b = d.b[n:]
	return b
}

// end fails if octets are left over.
func (d *decoder) end() error {
	if d.err == nil && len(d.b) > 0 {
		d.fail(errors.New("octets left over after the last field"))
	}
	return d.err
}
