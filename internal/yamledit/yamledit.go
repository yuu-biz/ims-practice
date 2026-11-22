// Package yamledit changes the text of a YAML file in place, for the
// generators of the copies of deploy/compose (deploy/k8s and
// deploy/interconnect). The comments, the order of the keys and the
// style of every value that a change does not touch stay as they are.
package yamledit

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// A Change is one entry of the table of changes of a generator.
type Change struct {
	// File is the role file the change applies to. What an empty File
	// means is up to the generator.
	File string
	// Key is a dotted path, with the index of an item of a sequence, as
	// in dashboard.ues.0.control. With Add it is the path of a new key.
	Key string
	// Comment, when not empty, replaces the comment lines right above
	// the key, or goes there when there are none.
	Comment string
	// Value, when not nil, replaces the value of the key.
	Value any
	// Remove takes the key and its value out, and leaves Comment in
	// their place.
	Remove bool
	// Add puts a new key, with Comment above it and Value, after the
	// last key of the mapping that the rest of Key names. The mapping
	// must exist, and Value must be set.
	Add bool
}

// An edit replaces lines[start:end] of a file with lines.
type edit struct {
	start, end int
	lines      []string
	// key is the key of the change that made the edit, for errors.
	key string
}

// Apply edits the text of a YAML file, so that its comments,
// the order of its keys and the style of every value it does not
// change stay as they are. Encoding a yaml.Node again would change
// quoting and spacing. The YAML parser only finds where each key is.
func Apply(src []byte, cs []Change) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(src), "\n")
	var edits []edit
	for _, c := range cs {
		var e []edit
		var err error
		if c.Add {
			e, err = c.insert(&doc, lines)
		} else {
			var key, value *yaml.Node
			if key, value, err = lookup(&doc, c.Key); err == nil {
				e, err = c.edits(lines, key, value)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Key, err)
		}
		for i := range e {
			e[i].key = c.Key
		}
		edits = append(edits, e...)
	}
	// From the bottom up, so that the line numbers of the edits not yet
	// made stay right. Of two edits at one line, the one that replaces
	// lines goes first and the one that only inserts goes above it.
	slices.SortFunc(edits, func(a, b edit) int {
		if a.start != b.start {
			return b.start - a.start
		}
		return b.end - a.end
	})
	for i, e := range edits {
		if i > 0 && e.end > edits[i-1].start {
			return nil, errors.New("two changes touch the same lines")
		}
		// Which of two insertions at one line comes first would be left
		// to the sort, so no order is promised and neither is allowed.
		if i > 0 && e.start == e.end && edits[i-1].start == edits[i-1].end && e.start == edits[i-1].start {
			return nil, fmt.Errorf("%s and %s both insert at line %d", edits[i-1].key, e.key, e.start+1)
		}
		lines = slices.Replace(lines, e.start, e.end, e.lines...)
	}
	out := []byte(strings.Join(lines, ""))
	for _, c := range cs {
		if c.Add && !HasKey(out, c.Key) {
			return nil, fmt.Errorf("%s: the new key is not at its path in the changed file", c.Key)
		}
	}
	if err := yaml.Unmarshal(out, &yaml.Node{}); err != nil {
		return nil, fmt.Errorf("the changed file is no longer YAML: %w", err)
	}
	return out, nil
}

// HasKey reports whether src is YAML with a key at the dotted path, as
// Change.Key writes it.
func HasKey(src []byte, path string) bool {
	var doc yaml.Node
	if yaml.Unmarshal(src, &doc) != nil {
		return false
	}
	_, _, err := lookup(&doc, path)
	return err == nil
}

// lookup finds the key at a dotted path and its value.
func lookup(doc *yaml.Node, path string) (key, value *yaml.Node, err error) {
	if len(doc.Content) == 0 {
		return nil, nil, errors.New("empty document")
	}
	node := doc.Content[0]
	for _, part := range strings.Split(path, ".") {
		key = nil
		switch node.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(node.Content); i += 2 {
				if node.Content[i].Value == part {
					key, node = node.Content[i], node.Content[i+1]
					break
				}
			}
			if key == nil {
				return nil, nil, fmt.Errorf("no key %q", part)
			}
		case yaml.SequenceNode:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node.Content) {
				return nil, nil, fmt.Errorf("no item %q", part)
			}
			node = node.Content[i]
		default:
			return nil, nil, fmt.Errorf("%q is inside a scalar", part)
		}
	}
	if key == nil {
		return nil, nil, errors.New("the path ends at an item of a sequence, not at a key")
	}
	return key, node, nil
}

// edits turns a change into edits of the lines of the file. It relies
// on the block style of deploy/compose: a key starts its own line, or
// follows "- " as the first key of an item, and whatever belongs to it
// is indented further.
func (c Change) edits(lines []string, key, value *yaml.Node) ([]edit, error) {
	at := key.Line - 1
	indent := key.Column - 1
	line := lines[at]
	if !strings.HasPrefix(line[indent:], key.Value+":") {
		return nil, errors.New("the key is not written as a plain key")
	}
	ownLine := strings.TrimSpace(line[:indent]) == ""
	if (c.Comment != "" || c.Remove) && !ownLine {
		return nil, errors.New("only a key on its own line can have its comment changed or be removed")
	}

	commentStart := at
	for commentStart > 0 && isCommentAt(lines[commentStart-1], indent) {
		commentStart--
	}
	valueEnd := blockEnd(lines, at, indent, value)

	comment := CommentLines(strings.Repeat(" ", indent), "#", c.Comment)
	if c.Remove {
		return []edit{{start: commentStart, end: valueEnd, lines: comment}}, nil
	}
	var edits []edit
	if c.Comment != "" {
		edits = append(edits, edit{start: commentStart, end: at, lines: comment})
	}
	if c.Value != nil {
		v, err := valueLines(line[:indent]+key.Value+":", indent, c.Value)
		if err != nil {
			return nil, err
		}
		edits = append(edits, edit{start: at, end: valueEnd, lines: v})
	}
	return edits, nil
}

// blockEnd returns the index after the last line of the value of the key
// that is at lines[at], with the key at column indent.
func blockEnd(lines []string, at, indent int, value *yaml.Node) int {
	end := at + 1
	for i := at + 1; i < len(lines); i++ {
		text := strings.TrimSpace(lines[i])
		if text == "" {
			continue
		}
		in := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
		compactItem := in == indent && value.Kind == yaml.SequenceNode && strings.HasPrefix(text, "-")
		if in <= indent && !compactItem {
			break
		}
		end = i + 1
	}
	return end
}

// insert turns an Add change into an edit that puts the new key below
// the last key of its mapping, indented like the keys already there.
func (c Change) insert(doc *yaml.Node, lines []string) ([]edit, error) {
	i := strings.LastIndex(c.Key, ".")
	if i < 0 || c.Value == nil || c.Remove {
		return nil, errors.New("Add needs a key below a mapping, and a value")
	}
	parentPath, name := c.Key[:i], c.Key[i+1:]
	parent, mapping, err := lookup(doc, parentPath)
	if err != nil {
		return nil, err
	}
	if mapping.Kind != yaml.MappingNode || len(mapping.Content) == 0 || mapping.Style&yaml.FlowStyle != 0 {
		return nil, fmt.Errorf("%s is not a mapping with keys in block style", parentPath)
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			return nil, fmt.Errorf("%s has the key %q already", parentPath, name)
		}
	}
	indent := mapping.Content[0].Column - 1
	end := blockEnd(lines, parent.Line-1, parent.Column-1, mapping)
	if end > 0 && !strings.HasSuffix(lines[end-1], "\n") {
		return nil, errors.New("the mapping ends the file without a newline")
	}
	v, err := valueLines(strings.Repeat(" ", indent)+name+":", indent, c.Value)
	if err != nil {
		return nil, err
	}
	pad := strings.Repeat(" ", indent)
	return []edit{{start: end, end: end, lines: append(CommentLines(pad, "#", c.Comment), v...)}}, nil
}

func isCommentAt(line string, indent int) bool {
	text := strings.TrimLeft(line, " ")
	return strings.HasPrefix(text, "#") && len(line)-len(text) == indent
}

// CommentLines writes text as comment lines, each after pad and mark.
func CommentLines(pad, mark, text string) []string {
	if text == "" {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		lines = append(lines, strings.TrimRight(pad+mark+" "+l, " ")+"\n")
	}
	return lines
}

// valueLines writes a scalar after the key, and a sequence or a mapping
// in block style below it.
func valueLines(keyText string, indent int, value any) ([]string, error) {
	b, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(string(b), "\n")
	switch reflect.ValueOf(value).Kind() {
	case reflect.Slice, reflect.Map, reflect.Struct:
	default:
		if !strings.Contains(text, "\n") {
			return []string{keyText + " " + text + "\n"}, nil
		}
	}
	lines := []string{keyText + "\n"}
	pad := strings.Repeat(" ", indent+2)
	for _, l := range strings.Split(text, "\n") {
		lines = append(lines, pad+l+"\n")
	}
	return lines, nil
}

// Header says in a comment of the given mark where a copy comes from,
// so that nobody edits it by hand. A person runs command from the root
// of the repository to make it again from original and table.
func Header(mark, command, original, table string) []byte {
	return fmt.Appendf(nil, "%[1]s Generated by %[2]q from\n%[1]s %[3]s and\n%[1]s %[4]s. Change those, not this file.\n\n",
		mark, command, original, table)
}

// FirstDifference finds the first line in which two files differ, and
// returns its number and its text in each. The tests of the generators
// use it to say where a copy is out of date.
func FirstDifference(got, want []byte) (line int, g, w string, differ bool) {
	gl := strings.Split(string(got), "\n")
	wl := strings.Split(string(want), "\n")
	for i := 0; i < max(len(gl), len(wl)); i++ {
		g, w = "", ""
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if g != w || i >= len(gl) || i >= len(wl) {
			return i + 1, g, w, true
		}
	}
	return 0, "", "", false
}
