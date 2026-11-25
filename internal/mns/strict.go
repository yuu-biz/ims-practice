package mns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

// DecodeStrictly decodes the JSON text body into v, a pointer to a type
// that follows the OpenAPI files, more strictly than encoding/json does,
// at any depth. The bodies are then read as a consumer or producer built
// from the files reads them:
//
//   - Every attribute must be, spelt exactly so, the JSON name of a
//     field of v. encoding/json alone would take "ackSystemID" for the
//     field "ackSystemId", since it matches names without regard to
//     case.
//   - No attribute, entry of a map or item of a list may be null, but
//     an attribute whose field has the option nullable in its schema
//     tag, as the id of Top. encoding/json would take null as the zero
//     value.
//   - An attribute whose field has the option required in its schema
//     tag must be there.
//   - No object may have the same name twice. encoding/json would keep
//     the last.
//
// A field of type json.RawMessage, for an attribute whose schema has no
// type, takes any value.
func DecodeStrictly(body []byte, v any) error {
	if isNull(body) {
		return fmt.Errorf("null")
	}
	if err := checkValue(body, reflect.TypeOf(v).Elem(), ""); err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

var (
	timeType = reflect.TypeFor[time.Time]()
	rawType  = reflect.TypeFor[json.RawMessage]()
)

// checkValue checks the JSON value raw against the Go type t that it is
// to decode into. at is where raw is, as a JSON pointer, for the error.
// A value of the wrong JSON type is left for json.Unmarshal, which says
// what is wrong with it.
func checkValue(raw []byte, t reflect.Type, at string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == rawType:
	case t.Kind() == reflect.Struct && t != timeType:
		members, err := objectMembers(raw, at)
		if err != nil || members == nil {
			return err
		}
		fields := jsonFields(t)
		seen := map[string]bool{}
		for _, m := range members {
			field, ok := fields[m.name]
			if !ok {
				return fmt.Errorf("%q at %s/ is not an attribute of %s", m.name, at, t)
			}
			seen[m.name] = true
			if isNull(m.value) {
				if hasSchemaOption(field, "nullable") {
					continue
				}
				return fmt.Errorf("null at %s/%s", at, m.name)
			}
			if err := checkValue(m.value, field.Type, at+"/"+m.name); err != nil {
				return err
			}
		}
		for name, field := range fields {
			if !seen[name] && hasSchemaOption(field, "required") {
				return fmt.Errorf("no %q at %s/, which %s requires", name, at, t)
			}
		}
	case t.Kind() == reflect.Map:
		members, err := objectMembers(raw, at)
		if err != nil || members == nil {
			return err
		}
		for _, m := range members {
			if isNull(m.value) {
				return fmt.Errorf("null at %s/%s", at, m.name)
			}
			if err := checkValue(m.value, t.Elem(), at+"/"+m.name); err != nil {
				return err
			}
		}
	case t.Kind() == reflect.Slice:
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return nil
		}
		for i, item := range items {
			if isNull(item) {
				return fmt.Errorf("null at %s/%d", at, i)
			}
			if err := checkValue(item, t.Elem(), fmt.Sprintf("%s/%d", at, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

type member struct {
	name  string
	value json.RawMessage
}

// objectMembers reads the members of the JSON object raw in order. It
// returns none, and no error, when raw is no object, and an error for a
// name that comes twice.
func objectMembers(raw []byte, at string) ([]member, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return nil, nil
	}
	members := []member{}
	seen := map[string]bool{}
	for d.More() {
		t, err := d.Token()
		name, ok := t.(string)
		if err != nil || !ok {
			return nil, nil
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, nil
		}
		if seen[name] {
			return nil, fmt.Errorf("%q comes twice at %s/", name, at)
		}
		seen[name] = true
		members = append(members, member{name, value})
	}
	return members, nil
}

func isNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

// jsonFields maps the JSON name of each field of the struct type t to
// the field, as encoding/json reads them: the name of the json tag, and
// those of a struct that is embedded without one, unless t has the name
// itself.
func jsonFields(t reflect.Type) map[string]reflect.StructField {
	out := map[string]reflect.StructField{}
	var embedded []reflect.Type
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch {
		case name == "" && f.Anonymous:
			embedded = append(embedded, f.Type)
		case name != "-" && name != "":
			out[name] = f
		}
	}
	for _, e := range embedded {
		for name, f := range jsonFields(e) {
			if _, ok := out[name]; !ok {
				out[name] = f
			}
		}
	}
	return out
}

// hasSchemaOption says whether the schema tag of f has option, which
// says what the schema of the OpenAPI file says of the attribute:
// required or nullable.
func hasSchemaOption(f reflect.StructField, option string) bool {
	return slices.Contains(strings.Split(f.Tag.Get("schema"), ","), option)
}
