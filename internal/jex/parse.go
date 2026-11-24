package jex

import (
	"regexp"
	"strconv"
	"strings"
)

// The parts of an expression, as the grammar of TS 32.161 A.4 names
// them. A JexConditionsExpr is an OrExpr.

// orExpr is AndExpr (" or " AndExpr)*. "and" binds more tightly than
// "or" (7.5).
type orExpr struct{ terms []andExpr }

// andExpr is AndOperandExpr (" and " AndOperandExpr)*.
type andExpr struct{ operands []operand }

// operand is an AndOperandExpr: a comparison, a location path, a
// function call, or an OrExpr in parentheses, with "not" before them
// or without.
type operand interface {
	eval(root, context node) bool
}

// group is '(' OrExpr ')'.
type group struct{ expr orExpr }

// not is 'not' '(' OrExpr ')'.
type not struct{ expr orExpr }

// contains is 'contains' '(' LocationPath ',' String ')'.
type contains struct {
	path locationPath
	text string
}

// comparison is an EqualityExpr or a RelationalExpr: a location path,
// an operator, and a literal.
type comparison struct {
	path  locationPath
	op    string
	value literal
}

// pathTest is a LocationPath on its own as an operand.
type pathTest struct{ path locationPath }

// locationPath is a LocationPath (7.3): absolute when it starts with
// "/", at the root node, or relative, at the context node.
type locationPath struct {
	absolute bool
	steps    []step
}

// step is a ChildAxisStep: a data node name, "*" for every child, or
// "." for the context node itself, with an optional predicate.
type step struct {
	name      string
	predicate *orExpr
}

// literal is the right side of a comparison: a String, a Number, or
// one of true, false and null (7.2.2).
type literal struct {
	kind   literalKind
	text   string
	number float64
}

type literalKind int

const (
	stringLiteral literalKind = iota
	numberLiteral
	trueLiteral
	falseLiteral
	nullLiteral
)

// parser reads an expression from left to right. It needs no separate
// tokens: the grammar fixes the spelling of every operator and allows
// no white space but the one around "and" and "or".
type parser struct {
	text string
	pos  int
	// depth counts the OrExprs being read, one per level of
	// parentheses, not() and predicates.
	depth int
}

// The limits on an expression. The parser calls itself once for each
// level of nesting, so without a bound a client could make it use the
// stack until the process fails. TS 32.158 6.6.5.2 lets a producer
// refuse an expression that is longer or more complex than it is
// willing to process; beyond either limit an expression is an
// *UnsupportedError.
const (
	// maxLength is the longest expression read, in bytes.
	maxLength = 4096
	// maxDepth is the deepest nesting read: an expression with no
	// parentheses, not() or predicates is at depth 1.
	maxDepth = 64
)

func (p *parser) syntaxError(msg string) error {
	return &SyntaxError{Expr: p.text, Offset: p.pos, Msg: msg}
}

func (p *parser) unsupported(what string) error {
	return &UnsupportedError{Expr: p.text, Offset: p.pos, What: what}
}

func (p *parser) rest() string { return p.text[p.pos:] }

func (p *parser) consume(prefix string) bool {
	if strings.HasPrefix(p.rest(), prefix) {
		p.pos += len(prefix)
		return true
	}
	return false
}

func (p *parser) expect(prefix string) error {
	if !p.consume(prefix) {
		return p.syntaxError("expected " + strconv.Quote(prefix))
	}
	return nil
}

// nonNegativeInteger is the AttributeElementSelector of 7.4 and A.4.
var nonNegativeInteger = regexp.MustCompile(`^(0|[1-9][0-9]*)`)

// parseConditions reads a whole JexConditionsExpr.
func (p *parser) parseConditions() (orExpr, error) {
	if p.text == "" {
		return orExpr{}, p.syntaxError("empty expression")
	}
	if len(p.text) > maxLength {
		return orExpr{}, p.unsupported("an expression longer than " + strconv.Itoa(maxLength) + " bytes")
	}
	if m := nonNegativeInteger.FindString(p.text); m != "" && m == p.text {
		return orExpr{}, p.unsupported("a position")
	}
	e, err := p.parseOr()
	if err != nil {
		return orExpr{}, err
	}
	if p.pos < len(p.text) {
		return orExpr{}, p.syntaxError("unexpected " + strconv.Quote(p.rest()))
	}
	return e, nil
}

func (p *parser) parseOr() (orExpr, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDepth {
		return orExpr{}, p.unsupported("nesting deeper than " + strconv.Itoa(maxDepth))
	}
	var e orExpr
	for {
		a, err := p.parseAnd()
		if err != nil {
			return orExpr{}, err
		}
		e.terms = append(e.terms, a)
		if !p.consume(" or ") {
			return e, nil
		}
	}
}

func (p *parser) parseAnd() (andExpr, error) {
	var e andExpr
	for {
		o, err := p.parseOperand()
		if err != nil {
			return andExpr{}, err
		}
		e.operands = append(e.operands, o)
		if !p.consume(" and ") {
			return e, nil
		}
	}
}

// parseOperand reads an AndOperandExpr. "not(" and "contains(" cannot
// begin a location path, since a data node name has no "(".
func (p *parser) parseOperand() (operand, error) {
	switch {
	case p.consume("not("):
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		return not{e}, p.expect(")")
	case p.consume("contains("):
		path, err := p.parsePath()
		if err != nil {
			return nil, err
		}
		if err := p.expect(","); err != nil {
			return nil, err
		}
		text, err := p.parseString()
		if err != nil {
			return nil, err
		}
		return contains{path, text}, p.expect(")")
	case p.consume("("):
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		return group{e}, p.expect(")")
	}
	path, err := p.parsePath()
	if err != nil {
		return nil, err
	}
	for _, op := range []string{"!=", "<=", ">=", "=", "<", ">"} {
		if !p.consume(op) {
			continue
		}
		v, err := p.parseLiteral(op == "=" || op == "!=")
		if err != nil {
			return nil, err
		}
		return comparison{path, op, v}, nil
	}
	return pathTest{path}, nil
}

// parseLiteral reads the right side of a comparison: any literal after
// = and !=, a number after the other operators (7.5).
func (p *parser) parseLiteral(equality bool) (literal, error) {
	if !equality {
		return p.parseNumber()
	}
	switch {
	case strings.HasPrefix(p.rest(), `"`):
		s, err := p.parseString()
		return literal{kind: stringLiteral, text: s}, err
	case p.consume("true"):
		return literal{kind: trueLiteral}, nil
	case p.consume("false"):
		return literal{kind: falseLiteral}, nil
	case p.consume("null"):
		return literal{kind: nullLiteral}, nil
	}
	return p.parseNumber()
}

// number is the Number of 7.2.2.2, the number of JSON (RFC 8259 6).
var number = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?[0-9]+)?`)

func (p *parser) parseNumber() (literal, error) {
	m := number.FindString(p.rest())
	if m == "" {
		return literal{}, p.syntaxError("expected a number")
	}
	f, err := strconv.ParseFloat(m, 64)
	if err != nil {
		return literal{}, p.syntaxError("number out of range")
	}
	p.pos += len(m)
	return literal{kind: numberLiteral, text: m, number: f}, nil
}

// parseString reads a String: any characters but the quotation mark,
// between two of them (A.4).
func (p *parser) parseString() (string, error) {
	start := p.pos
	if err := p.expect(`"`); err != nil {
		return "", err
	}
	end := strings.IndexByte(p.rest(), '"')
	if end < 0 {
		p.pos = start
		return "", p.syntaxError("unterminated string")
	}
	s := p.rest()[:end]
	p.pos += end + 1
	return s, nil
}

// parsePath reads a LocationPath. An absolute path needs at least one
// step after its "/" (A.4); a "/" alone, which selects the root node,
// is a path of Jex advanced only (7.5).
func (p *parser) parsePath() (locationPath, error) {
	var path locationPath
	if p.consume("/") {
		path.absolute = true
		if p.pos == len(p.text) || strings.ContainsRune(" ])|,=!<>", rune(p.text[p.pos])) {
			return locationPath{}, p.syntaxError("expected a step after /")
		}
	}
	for {
		s, err := p.parseStep()
		if err != nil {
			return locationPath{}, err
		}
		path.steps = append(path.steps, s)
		if !p.consume("/") {
			return path, nil
		}
	}
}

// The characters that a DataNodeName may not begin with, and those
// that it may not hold at all (A.4). The grammar lets "|" begin a name
// though no other character of a name may be one; here it may not,
// so that " | " always reads as the union of Jex advanced.
const (
	firstNameExcluded = "\"[]=!<>\n (),/|0123456789+-"
	nameExcluded      = "\"[]=!<>\n (),/|"
)

func (p *parser) parseStep() (step, error) {
	start := p.pos
	if p.pos == len(p.text) || strings.ContainsRune(firstNameExcluded, rune(p.text[p.pos])) {
		return step{}, p.syntaxError("expected a data node name")
	}
	p.pos++
	for p.pos < len(p.text) && !strings.ContainsRune(nameExcluded, rune(p.text[p.pos])) {
		p.pos++
	}
	s := step{name: p.text[start:p.pos]}
	if !p.consume("[") {
		return s, nil
	}
	// A.4 gives the abbreviated step no predicate: a ChildAxisStep is
	// a DataNodeNameTest with an optional Predicate, or "." alone.
	if s.name == "." {
		return step{}, p.syntaxError("the step . takes no predicate")
	}
	if m := nonNegativeInteger.FindString(p.rest()); m != "" && strings.HasPrefix(p.rest()[len(m):], "]") {
		return step{}, p.unsupported("a position")
	}
	e, err := p.parseOr()
	if err != nil {
		return step{}, err
	}
	s.predicate = &e
	return s, p.expect("]")
}
