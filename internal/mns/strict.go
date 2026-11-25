package mns

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"time"
)

// DecodeStrictly decodes the JSON text body into v, a pointer to a type
// of this package, more strictly than encoding/json does, at any depth.
// The bodies are then read as a consumer or producer built from the
// OpenAPI files reads them:
//
//   - Every attribute must be, spelt exactly so, the JSON name of a
//     field of v. encoding/json alone would take "ackSystemID" for the
//     field "ackSystemId", since it matches names without regard to
//     case.
//   - No attribute, entry of a map or item of a list may be null: no
//     schema of the files makes one nullable. encoding/json would take
//     null as the zero value.
//   - No object may have the same name twice. encoding/json would keep
//     the last.
func DecodeStrictly(body []byte, v any) error {
	if err := checkValue(body, reflect.TypeOf(v).Elem(), ""); err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

var timeType = reflect.TypeFor[time.Time]()

// checkValue checks the JSON value raw against the Go type t that it is
// to decode into. at is where raw is, as a JSON pointer, for the error.
// A value of the wrong JSON type is left for json.Unmarshal, which says
// what is wrong with it.
func checkValue(raw []byte, t reflect.Type, at string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t.Kind() == reflect.Struct && t != timeType:
		members, err := objectMembers(raw, at)
		if err != nil || members == nil {
			return err
		}
		fields := jsonFields(t)
		for _, m := range members {
			field, ok := fields[m.name]
			if !ok {
				return fmt.Errorf("%q at %s/ is not an attribute of %s", m.name, at, t)
			}
			if err := checkValue(m.value, field, at+"/"+m.name); err != nil {
				return err
			}
		}
	case t.Kind() == reflect.Map:
		members, err := objectMembers(raw, at)
		if err != nil || members == nil {
			return err
		}
		for _, m := range members {
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
// member that is null or a name that comes twice.
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
		switch {
		case seen[name]:
			return nil, fmt.Errorf("%q comes twice at %s/", name, at)
		case isNull(value):
			return nil, fmt.Errorf("null at %s/%s", at, name)
		}
		seen[name] = true
		members = append(members, member{name, value})
	}
	return members, nil
}

func isNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

// jsonFields maps the JSON name of each field of the struct type t, and
// of the structs it embeds, to the type of the field.
func jsonFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch {
		case name == "" && f.Anonymous:
			maps.Copy(out, jsonFields(f.Type))
		case name != "-" && name != "":
			out[name] = f.Type
		}
	}
	return out
}
