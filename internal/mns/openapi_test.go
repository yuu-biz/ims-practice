package mns

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestOpenAPIFiles compares the lists of names_test.go with the OpenAPI
// files themselves, which are 3GPP's and not in the repository. It runs
// only when MNS_OPENAPI_DIR names a directory that holds the six files
// of fileVersions, taken from the archives of the specifications on
// the 3GPP file server, so CI skips it. Run it after reading a new
// version of the files, to see what the lists need:
//
//	MNS_OPENAPI_DIR=/path/to/files go test -run TestOpenAPIFiles ./internal/mns
//
// It checks the info.version of each file, the properties and the
// required properties of each schema, the values of each enumeration,
// and that the body of a fault notification may be each notification
// type of the package.
func TestOpenAPIFiles(t *testing.T) {
	dir := os.Getenv("MNS_OPENAPI_DIR")
	if dir == "" {
		t.Skip("MNS_OPENAPI_DIR names no directory with the OpenAPI files")
	}
	files := map[string]*yaml.Node{}
	for name, version := range fileVersions {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		files[name] = doc.Content[0]
		if got := value(lookup(doc.Content[0], "info", "version")); got != version {
			t.Errorf("%s has the version %q; the lists were written from %q", name, got, version)
		}
	}
	o := openAPI{t: t, files: files}

	for _, s := range schemas {
		file, n := o.schema(s.file, s.schema)
		names, required := o.properties(file, n)
		if !slices.Equal(names, s.names) {
			t.Errorf("%s of %s has the properties %q; the list has %q", s.schema, s.file, names, s.names)
		}
		if !slices.Equal(required, s.required) {
			t.Errorf("%s of %s requires %q; the list has %q", s.schema, s.file, required, s.required)
		}
	}
	for _, e := range enums {
		file, n := o.schema(e.file, e.schema)
		_, n = o.resolve(file, n)
		var values []string
		for _, v := range lookup(n, "enum").Content {
			values = append(values, v.Value)
		}
		if !slices.Equal(values, e.values) {
			t.Errorf("%s of %s has the values %q; the list has %q", e.schema, e.file, values, e.values)
		}
	}

	body := lookup(files[faultNotifs], "paths", "/", "post", "requestBody", "content", "application/json", "schema", "oneOf")
	var refs []string
	for _, r := range body.Content {
		refs = append(refs, value(lookup(r, "$ref")))
	}
	for _, s := range schemas {
		if strings.HasPrefix(s.schema, "Notify") && !slices.Contains(refs, faultNrm+"#/components/schemas/"+s.schema) {
			t.Errorf("the notification body of %s may not be %s: it may be %q", faultNotifs, s.schema, refs)
		}
	}
}

// openAPI finds schemas in the files, following $ref from one file to
// another.
type openAPI struct {
	t     *testing.T
	files map[string]*yaml.Node
}

// schema finds the node of a path of schemaNames: a schema below
// components/schemas, then "oneOf/<n>" for an alternative, or the name
// of a property.
func (o openAPI) schema(file, path string) (string, *yaml.Node) {
	parts := strings.Split(path, "/")
	n := lookup(o.files[file], "components", "schemas", parts[0])
	if n == nil {
		o.t.Fatalf("%s has no schema %s", file, parts[0])
	}
	for i := 1; i < len(parts); i++ {
		file, n = o.resolve(file, n)
		if parts[i] == "oneOf" && i+1 < len(parts) {
			k, err := strconv.Atoi(parts[i+1])
			alternatives := lookup(n, "oneOf")
			if err != nil || alternatives == nil || k >= len(alternatives.Content) {
				o.t.Fatalf("%s of %s has no %s", path, file, strings.Join(parts[:i+2], "/"))
			}
			n = alternatives.Content[k]
			i++
			continue
		}
		var found *yaml.Node
		o.walk(file, n, func(f string, props *yaml.Node) {
			if p := lookup(props, parts[i]); p != nil && found == nil {
				file, found = f, p
			}
		})
		if found == nil {
			o.t.Fatalf("%s of %s has no property %s", path, file, parts[i])
		}
		n = found
	}
	return file, n
}

// properties lists the names of the properties of n, in order, and
// those that it requires.
func (o openAPI) properties(file string, n *yaml.Node) (names, required []string) {
	o.walk(file, n, func(_ string, props *yaml.Node) {
		for i := 0; i < len(props.Content); i += 2 {
			if name := props.Content[i].Value; !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	})
	o.walkRequired(file, n, func(name string) { required = append(required, name) })
	return names, required
}

// walk calls f with the properties of n and of every schema that n
// combines with allOf or oneOf, in order.
func (o openAPI) walk(file string, n *yaml.Node, f func(file string, props *yaml.Node)) {
	file, n = o.resolve(file, n)
	if props := lookup(n, "properties"); props != nil {
		f(file, props)
	}
	for _, key := range []string{"allOf", "oneOf"} {
		if parts := lookup(n, key); parts != nil {
			for _, p := range parts.Content {
				o.walk(file, p, f)
			}
		}
	}
}

func (o openAPI) walkRequired(file string, n *yaml.Node, f func(string)) {
	file, n = o.resolve(file, n)
	if r := lookup(n, "required"); r != nil {
		for _, name := range r.Content {
			f(name.Value)
		}
	}
	if parts := lookup(n, "allOf"); parts != nil {
		for _, p := range parts.Content {
			o.walkRequired(file, p, f)
		}
	}
}

// resolve follows the $ref of n, if it has one, to the file and the
// schema it names.
func (o openAPI) resolve(file string, n *yaml.Node) (string, *yaml.Node) {
	for {
		ref := value(lookup(n, "$ref"))
		if ref == "" {
			return file, n
		}
		target, pointer, _ := strings.Cut(ref, "#")
		if target != "" {
			file = target
		}
		doc, ok := o.files[file]
		if !ok {
			o.t.Fatalf("%s refers to %s, which is not one of the files", ref, file)
		}
		n = lookup(doc, strings.Split(strings.TrimPrefix(pointer, "/"), "/")...)
		if n == nil {
			o.t.Fatalf("%s names no schema", ref)
		}
	}
}

// lookup follows keys through the mappings below n; nil when one is
// missing.
func lookup(n *yaml.Node, keys ...string) *yaml.Node {
	for _, key := range keys {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				next = n.Content[i+1]
				break
			}
		}
		n = next
	}
	return n
}

func value(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}
