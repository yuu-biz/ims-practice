package jex

import (
	"strconv"
	"strings"
)

// AlarmRecordSelector is a filter or a dataNodeSelector of a GET of an
// alarm list in the form of TS 28.111 A.1.4: a location path from the
// AlarmList through its alarmRecords to the records, with a condition
// on each record, such as
//
//	/AlarmList[id="1"]/attributes/alarmRecords/*[perceivedSeverity="MAJOR" or perceivedSeverity="CRITICAL"]
//
// TS 32.158 6.7 names the parameter that selects attribute elements
// such as alarm records dataNodeSelector, and TS 28.111 A.1.4 uses
// filter for the same; both take this form.
type AlarmRecordSelector struct {
	path locationPath
}

// The steps that a selector goes through to the records. The step
// after them is "*", or the alarmId of one record.
var alarmRecordSteps = []string{"AlarmList", "attributes", "alarmRecords"}

// ParseAlarmRecordSelector reads a selector. Any other expression of
// Jex, such as a condition that is no path, a path to another
// attribute, or a union of paths, is an *UnsupportedError: TS 32.158
// 6.6.5.2 lets a producer refuse an expression it cannot evaluate.
//
// The last step may name one record by its alarmId only when the id is
// a DataNodeName of A.4: it may not begin with a digit, "+" or "-", nor
// hold a space or any of "[]=!<>(),/|. A record with such an id can
// still be picked by "*" with a condition, or named in fields. An
// alarmId that begins with a letter and holds letters, digits, "." and
// "-" can be named everywhere.
func ParseAlarmRecordSelector(s string) (*AlarmRecordSelector, error) {
	p := &parser{text: s, nodeSelection: true}
	e, err := p.parseConditions()
	if err != nil {
		return nil, err
	}
	refuse := &UnsupportedError{Expr: s, What: "an expression other than /AlarmList/attributes/alarmRecords/*[condition]"}
	if len(e.terms) != 1 || len(e.terms[0].operands) != 1 {
		return nil, refuse
	}
	t, ok := e.terms[0].operands[0].(pathTest)
	if !ok || !t.path.absolute || len(t.path.steps) != len(alarmRecordSteps)+1 {
		return nil, refuse
	}
	for i, name := range alarmRecordSteps {
		if t.path.steps[i].name != name {
			return nil, refuse
		}
	}
	if t.path.steps[len(alarmRecordSteps)].name == "." {
		return nil, refuse
	}
	return &AlarmRecordSelector{path: t.path}, nil
}

// Select returns the alarmIds of the records that the selector picks,
// in order. alarmList is the AlarmList-Single resource as a JSON
// document (Document): the object with id and attributes. The root
// node that the path starts at has the alarm list as its only child,
// named AlarmList, as the path of A.1.4 has it.
func (s *AlarmRecordSelector) Select(alarmList map[string]any) []string {
	root := node{value: map[string]any{"AlarmList": alarmList}}
	var ids []string
	for _, n := range s.path.selectNodes(root, root) {
		ids = append(ids, n.name)
	}
	return ids
}

// alarmRecordsPointer is the start of a fields entry that selects
// alarm records: a JSON pointer from the AlarmList (TS 32.158 6.2.2).
const alarmRecordsPointer = "/attributes/alarmRecords/"

// ParseAlarmRecordFields reads the fields parameter of a GET of an
// alarm list in the forms of TS 28.111 A.1.4, and returns the alarmIds
// it selects:
//
//	/attributes/alarmRecords/<alarmId>
//	/attributes/alarmRecords/(<alarmId> | <alarmId>)
//
// Entries are separated by commas (TS 32.158 6.2.2). The first form is
// a JSON pointer (RFC 6901), in which ~1 stands for "/" and ~0 for "~".
// The second unites ids as a step in parentheses of Jex advanced does
// (TS 32.161 7.5), so its ids are read as written, with no ~ escapes,
// and may not hold a space, "/", "(" or ")". A.1.4 writes a space on
// each side of "|", where the grammar of A.3 has none, and both are
// read. A pointer to another attribute, to the whole of alarmRecords
// (/attributes/alarmRecords, with no slash after it), or into a record
// is an *UnsupportedError; an entry that is no pointer is a
// *SyntaxError. Like an expression, fields longer than 4096 bytes is
// an *UnsupportedError.
func ParseAlarmRecordFields(s string) ([]string, error) {
	if len(s) > maxLength {
		return nil, &UnsupportedError{Expr: s, What: "fields longer than " + strconv.Itoa(maxLength) + " bytes"}
	}
	var ids []string
	offset := 0
	for entry := range strings.SplitSeq(s, ",") {
		got, err := parseFieldsEntry(s, offset, entry)
		if err != nil {
			return nil, err
		}
		ids = append(ids, got...)
		offset += len(entry) + 1
	}
	return ids, nil
}

func parseFieldsEntry(s string, offset int, entry string) ([]string, error) {
	syntax := func(at int, msg string) error { return &SyntaxError{Expr: s, Offset: offset + at, Msg: msg} }
	if !strings.HasPrefix(entry, "/") {
		return nil, syntax(0, "expected a JSON pointer")
	}
	rest, ok := strings.CutPrefix(entry, alarmRecordsPointer)
	if !ok {
		return nil, &UnsupportedError{Expr: s, Offset: offset, What: "a field other than an alarm record"}
	}
	at := len(alarmRecordsPointer)
	if inner, ok := strings.CutPrefix(rest, "("); ok {
		inner, ok = strings.CutSuffix(inner, ")")
		if !ok {
			return nil, syntax(len(entry), `expected ")"`)
		}
		var ids []string
		for id := range strings.SplitSeq(inner, "|") {
			id = strings.TrimPrefix(strings.TrimSuffix(id, " "), " ")
			if id == "" || strings.ContainsAny(id, " /()") {
				return nil, syntax(at, "expected an alarmId")
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
	switch {
	case rest == "":
		return nil, syntax(at, "expected an alarmId")
	case strings.Contains(rest, "/"):
		return nil, &UnsupportedError{Expr: s, Offset: offset, What: "a field inside an alarm record"}
	}
	return []string{strings.NewReplacer("~1", "/", "~0", "~").Replace(rest)}, nil
}
