package h248

import (
	"fmt"
	"net/netip"
	"strings"
)

// Message is one H.248 message: a header that says which version of
// the protocol it follows and who sent it, and a body (megacoMessage
// in RFC 3525 Annex B.2).
type Message struct {
	// Version is the protocol version, as in "MEGACO/2". The 3GPP
	// profiles ask for version 2 or later (TS 29.334 5.3, TS 29.238
	// 5.3, TS 29.333 5.3). The message with which a media gateway
	// registers is a version 1 message all the same (H.248.1 11.3).
	Version int
	// MId names the sender of the message.
	MId MId
	// MessageError takes the place of the transactions when the sender
	// could not make sense of a message it received.
	MessageError *ErrorDescriptor
	// Transactions is the body of every other message.
	Transactions []Transaction
}

// MId is the identity of a media gateway or of its controller (mId).
// Exactly one of DomainAddress, DomainName and DeviceName is set. The
// profiles recommend the domain name (TS 29.334 5.11).
type MId struct {
	// DomainAddress is written as "[192.0.2.10]".
	DomainAddress netip.Addr
	// DomainName is written as "<agw1.ims.example.net>".
	DomainName string
	// PortNumber may follow a domain address or a domain name. Zero
	// leaves it out.
	PortNumber uint16
	// DeviceName is written as it is.
	DeviceName string
}

// Parse reads one message in the text encoding. It returns an error
// for a syntax error and for the parts of H.248.1 that this package
// leaves out because the 3GPP profiles do not use them.
func Parse(b []byte) (Message, error) {
	s := &scanner{b: b}
	m, err := s.message()
	if err != nil {
		return Message{}, err
	}
	return m, nil
}

// Marshal writes the message in the text encoding, with the long form
// of the tokens.
func (m Message) Marshal() ([]byte, error) {
	w := &writer{}
	w.message(m)
	if w.err != nil {
		return nil, w.err
	}
	return w.buf.Bytes(), nil
}

func (s *scanner) message() (Message, error) {
	var m Message
	s.skipLWSP()
	if s.peek() == '!' {
		s.pos++
	} else if err := s.expectToken(tokMegaco); err != nil {
		// An authentication header (RFC 3525 10.2) may come first. It
		// is not read: the profiles use no security on these
		// interfaces (TS 29.334 5.13).
		return m, err
	}
	if s.peek() != '/' {
		return m, s.errorf("expected the version after %s", tokMegaco)
	}
	s.pos++
	version, err := s.uint(7)
	if err != nil {
		return m, err
	}
	if version > 99 {
		return m, s.errorf("version %d has more than two digits", version)
	}
	m.Version = int(version)
	if m.MId, err = s.mId(); err != nil {
		return m, err
	}
	s.header = true

	for s.skipLWSP(); !s.atEnd(); s.skipLWSP() {
		s.next = ""
		name, err := s.token()
		if err != nil {
			return m, err
		}
		s.next = name
		var t Transaction
		switch name {
		case tokError:
			if m.MessageError != nil {
				return m, s.errorf("a message carries one error descriptor")
			}
			m.MessageError, err = s.errorDescriptor()
		case tokTransaction:
			t, err = s.transactionRequest()
		case tokReply:
			t, err = s.transactionReply()
		case tokPending:
			t, err = s.transactionPending()
		case tokResponseAck:
			t, err = s.transactionResponseAck()
		default:
			err = s.errorf("%s cannot start a transaction", name)
		}
		if err != nil {
			return m, err
		}
		if t != nil {
			m.Transactions = append(m.Transactions, t)
		}
	}
	s.next = ""
	if (m.MessageError != nil) == (len(m.Transactions) > 0) {
		return m, s.errorf("the body is either one error descriptor or a list of transactions")
	}
	return m, nil
}

func (w *writer) message(m Message) {
	if m.Version < 1 || m.Version > 99 {
		w.fail("version %d is not between 1 and 99", m.Version)
	}
	w.buf.WriteString(fmt.Sprintf("%s/%d %s\n", tokMegaco, m.Version, w.mId(m.MId)))
	if (m.MessageError != nil) == (len(m.Transactions) > 0) {
		w.fail("the body is either one error descriptor or a list of transactions")
		return
	}
	if m.MessageError != nil {
		w.errorDescriptor(m.MessageError)
		w.buf.WriteByte('\n')
	}
	for _, t := range m.Transactions {
		if t == nil {
			w.fail("a transaction is nil")
			return
		}
		t.write(w)
		w.buf.WriteByte('\n')
	}
}

// mId reads an mId. The form that names an SS7 point code (mtpAddress)
// is not read: the profiles run over IP.
func (s *scanner) mId() (MId, error) {
	var id MId
	s.skipLWSP()
	switch s.peek() {
	case '[':
		s.pos++
		text := s.takeWhile(func(c byte) bool { return c != ']' && c != '\n' })
		if !s.eat(']') {
			return id, s.errorf("a domain address is not closed with \"]\"")
		}
		addr, err := netip.ParseAddr(text)
		if err != nil {
			return id, s.errorf("%q is not an IP address", text)
		}
		id.DomainAddress = addr
	case '<':
		s.pos++
		id.DomainName = s.takeWhile(isDomainNameChar)
		if s.peek() != '>' {
			return id, s.errorf("a domain name is not closed with \">\"")
		}
		s.pos++
		if !validDomainName(id.DomainName) {
			return id, s.errorf("%q is not a domain name", id.DomainName)
		}
	default:
		id.DeviceName = s.takeWhile(isPathNameChar)
		if !validPathName(id.DeviceName) {
			return id, s.errorf("expected an mId, found %s", s.found())
		}
		if strings.EqualFold(id.DeviceName, "MTP") && s.peek() == '{' {
			return id, s.errorf("an mId that is an MTP address is not supported")
		}
		return id, nil
	}
	if s.peek() == ':' {
		s.pos++
		port, err := s.uint(16)
		if err != nil {
			return id, err
		}
		id.PortNumber = uint16(port)
	}
	return id, nil
}

func (w *writer) mId(id MId) string {
	var text string
	switch {
	case id.DomainAddress.IsValid() && id.DomainName == "" && id.DeviceName == "":
		text = "[" + id.DomainAddress.String() + "]"
	case !id.DomainAddress.IsValid() && id.DomainName != "" && id.DeviceName == "":
		if !validDomainName(id.DomainName) {
			w.fail("%q is not a domain name", id.DomainName)
		}
		text = "<" + id.DomainName + ">"
	case !id.DomainAddress.IsValid() && id.DomainName == "" && id.DeviceName != "":
		if !validPathName(id.DeviceName) {
			w.fail("%q is not a device name", id.DeviceName)
		}
		if id.PortNumber != 0 {
			w.fail("a device name takes no port number")
		}
		return id.DeviceName
	default:
		w.fail("an mId is exactly one of a domain address, a domain name and a device name")
		return ""
	}
	if id.PortNumber != 0 {
		text += fmt.Sprintf(":%d", id.PortNumber)
	}
	return text
}

func isDomainNameChar(c byte) bool {
	return isAlpha(c) || isDigit(c) || c == '-' || c == '.'
}

// validDomainName follows domainName in the grammar: a letter or digit
// and up to 63 more letters, digits, "-" and ".".
func validDomainName(name string) bool {
	if name == "" || len(name) > 64 || !(isAlpha(name[0]) || isDigit(name[0])) {
		return false
	}
	return strings.IndexFunc(name, func(r rune) bool { return r > 127 || !isDomainNameChar(byte(r)) }) < 0
}

// isPathNameChar accepts the octets of a pathNAME, including those of
// the domain part after "@".
func isPathNameChar(c byte) bool {
	return isNameChar(c) || c == '/' || c == '*' || c == '$' || c == '@' || c == '-' || c == '.'
}

// validPathName follows pathNAME in the grammar loosely: it starts
// with "*" or a letter and is at most 64 octets long. The exact places
// where "/", "*" and "$" may stand are not checked, since each profile
// lays down its own structure (TS 29.334 5.6.1.1.1).
func validPathName(name string) bool {
	if name == "" || len(name) > 64 || !(isAlpha(name[0]) || name[0] == '*') {
		return false
	}
	return strings.IndexFunc(name, func(r rune) bool { return r > 127 || !isPathNameChar(byte(r)) }) < 0
}
