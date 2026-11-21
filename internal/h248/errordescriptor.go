package h248

import "fmt"

// ErrorDescriptor reports why a message, a transaction, an action or a
// command failed (RFC 3525 7.1.19). The codes are those of H.248.8;
// the ones a node of each profile sends are listed in TS 29.334
// 5.7.10, TS 29.238 5.7.10 and TS 29.333 5.7.9.
type ErrorDescriptor struct {
	// ErrorCode is the number, such as 430 for an unknown
	// TerminationID.
	ErrorCode int
	// ErrorText explains the error to a person. It may be empty.
	ErrorText string
}

// errorDescriptor reads what follows the Error token:
// "= ErrorCode { [quotedString] }".
func (s *scanner) errorDescriptor() (*ErrorDescriptor, error) {
	if err := s.expect('='); err != nil {
		return nil, err
	}
	// ErrorCode is 1*4(DIGIT), which is less than 14 bits.
	code, err := s.uint(14)
	if err != nil {
		return nil, err
	}
	if code > 9999 {
		return nil, s.errorf("error code %d has more than four digits", code)
	}
	e := &ErrorDescriptor{ErrorCode: int(code)}
	if err := s.expect('{'); err != nil {
		return nil, err
	}
	if !s.eat('}') {
		if e.ErrorText, err = s.quotedString(); err != nil {
			return nil, err
		}
		if err := s.expect('}'); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func (w *writer) errorDescriptor(e *ErrorDescriptor) {
	if e.ErrorCode < 0 || e.ErrorCode > 9999 {
		w.fail("error code %d is not a number of up to four digits", e.ErrorCode)
	}
	if e.ErrorText == "" {
		w.item(fmt.Sprintf("%s = %d { }", tokError, e.ErrorCode))
		return
	}
	w.item(fmt.Sprintf("%s = %d { %s }", tokError, e.ErrorCode, w.quoted(e.ErrorText)))
}

// quoted returns text as a quotedString. The grammar allows only
// SafeChar, RestChar and white space inside one, so a double quote, a
// line end or an octet outside ASCII cannot be sent.
func (w *writer) quoted(text string) string {
	for i := 0; i < len(text); i++ {
		if c := text[i]; !isSafeChar(c) && !isRestChar(c) && c != ' ' && c != '\t' {
			w.fail("%q cannot be written as a quoted string because of %q", text, c)
			break
		}
	}
	return `"` + text + `"`
}

// isSafeChar reports whether c is a SafeChar of the grammar: an octet
// that a VALUE may hold without quotes.
func isSafeChar(c byte) bool {
	if isAlpha(c) || isDigit(c) {
		return true
	}
	switch c {
	case '+', '-', '&', '!', '_', '/', '\'', '?', '@', '^', '`', '~', '*', '$', '\\', '(', ')', '%', '|', '.':
		return true
	}
	return false
}

// isRestChar reports whether c is a RestChar of the grammar: an octet
// that is allowed inside quotes only.
func isRestChar(c byte) bool {
	switch c {
	case ';', '[', ']', '{', '}', ':', ',', '#', '<', '>', '=':
		return true
	}
	return false
}
