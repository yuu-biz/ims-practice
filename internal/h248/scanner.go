package h248

import (
	"bytes"
	"fmt"
	"strconv"
)

// scanner walks over one message in the text encoding. Its methods
// follow the terminals of the grammar in RFC 3525 Annex B.2.
type scanner struct {
	b   []byte
	pos int

	// header tells that the version and the mId of the message have
	// been read.
	header bool
	// next is the token that starts the transaction being read.
	next string
	// part is what has been read of the transaction request being
	// read, from its TransactionID on.
	part *requestPart
}

// reach records how far the scanner has come in the request it reads.
func (s *scanner) reach(place requestPlace) {
	if s.part != nil {
		s.part.at = place
	}
}

// errorf reports a syntax error with the line it was found on, which is
// what a learner needs to find it in a capture.
func (s *scanner) errorf(format string, args ...any) error {
	line := 1 + bytes.Count(s.b[:s.pos], []byte("\n"))
	return fmt.Errorf("h248: line %d: %s", line, fmt.Sprintf(format, args...))
}

func (s *scanner) atEnd() bool {
	return s.pos >= len(s.b)
}

// peek returns the next octet without taking it, or 0 at the end.
func (s *scanner) peek() byte {
	if s.atEnd() {
		return 0
	}
	return s.b[s.pos]
}

// skipLWSP passes white space, line ends and comments. The grammar
// allows them around "=", "{", "}" and "," (LWSP in EQUAL, LBRKT,
// RBRKT and COMMA).
func (s *scanner) skipLWSP() {
	for !s.atEnd() {
		switch s.b[s.pos] {
		case ' ', '\t', '\r', '\n':
			s.pos++
		case ';':
			for !s.atEnd() && s.b[s.pos] != '\r' && s.b[s.pos] != '\n' {
				s.pos++
			}
		default:
			return
		}
	}
}

// eat takes c if it comes next, after any white space.
func (s *scanner) eat(c byte) bool {
	s.skipLWSP()
	if s.peek() != c {
		return false
	}
	s.pos++
	return true
}

func (s *scanner) expect(c byte) error {
	if !s.eat(c) {
		return s.errorf("expected %q, found %s", c, s.found())
	}
	return nil
}

// found describes what comes next, for error messages.
func (s *scanner) found() string {
	if s.atEnd() {
		return "the end of the message"
	}
	return strconv.QuoteRune(rune(s.b[s.pos]))
}

// takeWhile takes the longest run of octets that ok accepts.
func (s *scanner) takeWhile(ok func(byte) bool) string {
	start := s.pos
	for !s.atEnd() && ok(s.b[s.pos]) {
		s.pos++
	}
	return string(s.b[start:s.pos])
}

// word takes letters, digits and "_", which is what a NAME and every
// token except "!" are made of. It may return an empty string.
func (s *scanner) word() string {
	s.skipLWSP()
	return s.takeWhile(isNameChar)
}

// token takes a word and returns the long form of the token it spells.
func (s *scanner) token() (string, error) {
	word := s.word()
	long, ok := longToken(word)
	if !ok {
		if word == "" {
			return "", s.errorf("expected a token, found %s", s.found())
		}
		return "", s.errorf("unknown or unsupported token %q", word)
	}
	return long, nil
}

// uint takes a decimal number of at most the given number of bits
// (UINT16 and UINT32 in the grammar).
func (s *scanner) uint(bits int) (uint64, error) {
	s.skipLWSP()
	digits := s.takeWhile(isDigit)
	if digits == "" {
		return 0, s.errorf("expected a number, found %s", s.found())
	}
	n, err := strconv.ParseUint(digits, 10, bits)
	if err != nil {
		return 0, s.errorf("number %s does not fit in %d bits", digits, bits)
	}
	return n, nil
}

// quotedString takes a quotedString and returns what is between the
// quotes. The grammar has no way to put a double quote inside one.
func (s *scanner) quotedString() (string, error) {
	if err := s.expect('"'); err != nil {
		return "", err
	}
	text := s.takeWhile(func(c byte) bool { return c != '"' })
	if s.atEnd() {
		return "", s.errorf("a quoted string is not closed")
	}
	s.pos++
	return text, nil
}

// list reads "{ item, item, ... }", calling item at the start of each.
func (s *scanner) list(item func() error) error {
	if err := s.expect('{'); err != nil {
		return err
	}
	return s.items(item)
}

// listOrEmpty reads a list that may have no item: "{ }".
func (s *scanner) listOrEmpty(item func() error) error {
	if err := s.expect('{'); err != nil {
		return err
	}
	if s.eat('}') {
		return nil
	}
	return s.items(item)
}

// items reads "item, item, ... }".
func (s *scanner) items(item func() error) error {
	for {
		if err := item(); err != nil {
			return err
		}
		if !s.eat(',') {
			return s.expect('}')
		}
	}
}

func isDigit(c byte) bool {
	return '0' <= c && c <= '9'
}

func isAlpha(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func isNameChar(c byte) bool {
	return isAlpha(c) || isDigit(c) || c == '_'
}
