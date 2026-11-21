package h248

import (
	"fmt"
	"strings"
)

// PkgdName names an item of a package: a property, an event, a signal
// or a statistic, as "package/item" (pkgdName in RFC 3525 Annex B.2).
// "package/*" stands for every item of the package and "*/*" for every
// item the media gateway supports. What the items mean is laid down in
// the definition of each package, not here.
type PkgdName string

// Value is one VALUE of the grammar. The type of a value decides its
// form (RFC 3525 Annex B.2, the notes at the head of the grammar): a
// string is always quoted; a number, an enumeration and a boolean
// ("ON" or "OFF") never are.
type Value struct {
	Text   string
	Quoted bool
}

// PropertyParm is a property of a package with the value it is set to
// (propertyParm), such as ipdc/realm = "access".
type PropertyParm struct {
	Name PkgdName
	// Value is the value, unless Sublist is used.
	Value Value
	// Sublist holds the values of a property whose type is a list,
	// written as "[a, b]".
	Sublist []Value
}

// pkgdNameOrWord reads either a pkgdName or a single word, and tells
// which it found. Lists that mix tokens and package items, such as the
// LocalControl descriptor, are told apart this way: only a pkgdName
// has a "/".
func (s *scanner) pkgdNameOrWord() (text string, pkgd bool, err error) {
	s.skipLWSP()
	part := func() string {
		if s.peek() == '*' {
			s.pos++
			return "*"
		}
		return s.takeWhile(isNameChar)
	}
	text = part()
	if s.peek() != '/' {
		return text, false, nil
	}
	s.pos++
	text += "/" + part()
	if !validPkgdName(PkgdName(text)) {
		return "", false, s.errorf("%q is not the name of a package item", text)
	}
	return text, true, nil
}

func (s *scanner) pkgdName() (PkgdName, error) {
	text, pkgd, err := s.pkgdNameOrWord()
	if err != nil {
		return "", err
	}
	if !pkgd {
		return "", s.errorf("expected the name of a package item, found %q", text)
	}
	return PkgdName(text), nil
}

// validPkgdName follows pkgdName in the grammar: "NAME/NAME", "NAME/*"
// or "*/*".
func validPkgdName(name PkgdName) bool {
	pkg, item, ok := strings.Cut(string(name), "/")
	if !ok {
		return false
	}
	if pkg == "*" {
		return item == "*"
	}
	return validName(pkg) && (item == "*" || validName(item))
}

// validName follows NAME in the grammar: a letter and up to 63 more
// letters, digits and "_".
func validName(name string) bool {
	if name == "" || len(name) > 64 || !isAlpha(name[0]) {
		return false
	}
	return strings.IndexFunc(name, func(r rune) bool { return r > 127 || !isNameChar(byte(r)) }) < 0
}

// value reads a VALUE: a quotedString or a run of SafeChar.
func (s *scanner) value() (Value, error) {
	s.skipLWSP()
	if s.peek() == '"' {
		text, err := s.quotedString()
		return Value{Text: text, Quoted: true}, err
	}
	text := s.takeWhile(isSafeChar)
	if text == "" {
		return Value{}, s.errorf("expected a value, found %s", s.found())
	}
	return Value{Text: text}, nil
}

// parmValue reads what follows the name of a property or of a
// parameter of an event or a signal (parmValue). Only "= VALUE" and
// the sub-list "= [VALUE, ...]" are read.
//
// SIMPLIFIED: H.248.1 (05/2002) with Corrigendum 1 (03/2004), Annex
// B.2 (parmValue, alternativeValue). The
// alternatives "{a, b}", the range "[a:b]" and the relations "<", ">"
// and "#" are left out. They let a controller offer several values for
// the media gateway to choose from, and the procedures of the three
// profiles hand over choices in the SDP of the Local descriptor
// instead (TS 29.334 5.17.2.2).
func (s *scanner) parmValue() (Value, []Value, error) {
	s.skipLWSP()
	switch s.peek() {
	case '<', '>', '#':
		return Value{}, nil, s.errorf("a value with the relation %q is not supported", s.peek())
	}
	if err := s.expect('='); err != nil {
		return Value{}, nil, err
	}
	s.skipLWSP()
	switch s.peek() {
	case '{':
		return Value{}, nil, s.errorf("a list of alternative values is not supported")
	case '[':
		s.pos++
		var sublist []Value
		for {
			v, err := s.value()
			if err != nil {
				return Value{}, nil, err
			}
			sublist = append(sublist, v)
			s.skipLWSP()
			if s.peek() == ':' {
				return Value{}, nil, s.errorf("a range of values is not supported")
			}
			if !s.eat(',') {
				return Value{}, sublist, s.expect(']')
			}
		}
	}
	v, err := s.value()
	return v, nil, err
}

// value returns v as it is written.
func (w *writer) value(v Value) string {
	if v.Quoted {
		return w.quoted(v.Text)
	}
	if v.Text == "" {
		w.fail("a value that is not quoted cannot be empty")
	}
	for i := 0; i < len(v.Text); i++ {
		if !isSafeChar(v.Text[i]) {
			w.fail("the value %q has to be quoted because of %q", v.Text, v.Text[i])
			break
		}
	}
	return v.Text
}

// parm returns "name = value" or "name = [value, ...]".
func (w *writer) parm(name string, value Value, sublist []Value) string {
	if len(sublist) == 0 {
		return fmt.Sprintf("%s = %s", name, w.value(value))
	}
	if value != (Value{}) {
		w.fail("%s has both a value and a sub-list", name)
	}
	values := make([]string, len(sublist))
	for i, v := range sublist {
		values[i] = w.value(v)
	}
	return fmt.Sprintf("%s = [%s]", name, strings.Join(values, ", "))
}

func (w *writer) propertyParm(p PropertyParm) string {
	if !validPkgdName(p.Name) {
		w.fail("%q is not the name of a package item", string(p.Name))
	}
	return w.parm(string(p.Name), p.Value, p.Sublist)
}

func (s *scanner) propertyParm(name PkgdName) (PropertyParm, error) {
	p := PropertyParm{Name: name}
	var err error
	p.Value, p.Sublist, err = s.parmValue()
	return p, err
}
