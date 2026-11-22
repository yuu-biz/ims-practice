package facts

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// httpHosts are the addresses that a curl of a deck may reach, each
// with the package whose routes answer there. compose.yaml publishes
// the port 8080 of the Lab on the host.
var httpHosts = map[string]string{
	"localhost:8080": "internal/lab",
}

// route is a pattern of net/http.ServeMux: a method, or "" for any, and
// the segments of the path. A segment {name} matches any one segment,
// {name...} the rest of the path.
type route struct {
	pattern  string
	method   string
	segments []string
	// subtree is a pattern that ends in "/", which matches every path
	// below it.
	subtree bool
}

func parseRoute(pattern string) route {
	r := route{pattern: pattern}
	path := pattern
	if method, rest, ok := strings.Cut(pattern, " "); ok {
		r.method, path = method, strings.TrimSpace(rest)
	}
	if i := strings.IndexByte(path, '/'); i > 0 {
		path = path[i:]
	}
	r.subtree = strings.HasSuffix(path, "/")
	r.segments = strings.Split(strings.Trim(path, "/"), "/")
	return r
}

// matches tells whether the route serves a request. The pattern "/" of
// a web UI, which net/http matches with every path, matches only "/"
// here: a curl to a path that no API serves would otherwise pass.
func (r route) matches(method, path string) bool {
	if r.method != "" && r.method != method && !(r.method == "GET" && method == "HEAD") {
		return false
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if r.subtree && len(r.segments) == 1 && r.segments[0] == "" {
		return strings.Trim(path, "/") == ""
	}
	for i, s := range r.segments {
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "...}") {
			return true
		}
		if i >= len(segments) {
			return false
		}
		if s == "{$}" {
			return false
		}
		if !(strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")) && s != segments[i] {
			return false
		}
	}
	return len(segments) == len(r.segments) || (r.subtree && len(segments) > len(r.segments))
}

// readRoutes reads the routes that the package in dir registers with
// Handle and HandleFunc, from its source. A pattern is a string literal,
// or comes from a package-level []string of literals that the call
// ranges over: the element itself, or the element with the one literal
// that the call adds between its method and its path, as
// method+" /api/ues/{name}"+path. A call of another shape is an error,
// so that a route this function cannot read is never missed in silence.
func readRoutes(dir string) ([]route, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	lists := map[string][]string{}
	for _, f := range files {
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				for i, name := range value.Names {
					if i < len(value.Values) {
						if elements, ok := stringList(value.Values[i]); ok {
							lists[name.Name] = elements
						}
					}
				}
			}
		}
	}
	var routes []route
	var problems []string
	// walk visits the calls under n, with the range statements that
	// enclose them, innermost last.
	var walk func(n ast.Node, ranges []*ast.RangeStmt)
	walk = func(n ast.Node, ranges []*ast.RangeStmt) {
		ast.Inspect(n, func(m ast.Node) bool {
			switch m := m.(type) {
			case *ast.RangeStmt:
				walk(m.Body, append(slices.Clip(ranges), m))
				return false
			case *ast.CallExpr:
				patterns, err := patternsOf(m, ranges, lists)
				if err != nil {
					problems = append(problems, fmt.Sprintf("%s: %v", fset.Position(m.Pos()), err))
				}
				for _, p := range patterns {
					routes = append(routes, parseRoute(p))
				}
			}
			return true
		})
	}
	for _, f := range files {
		walk(f, nil)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("cannot read the routes of %s:\n  %s", dir, strings.Join(problems, "\n  "))
	}
	if len(routes) == 0 {
		return nil, fmt.Errorf("%s registers no routes", dir)
	}
	return routes, nil
}

// patternsOf returns the patterns of a call of Handle or HandleFunc,
// and nothing for any other call.
func patternsOf(call *ast.CallExpr, ranges []*ast.RangeStmt, lists map[string][]string) ([]string, error) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (selector.Sel.Name != "Handle" && selector.Sel.Name != "HandleFunc") || len(call.Args) != 2 {
		return nil, nil
	}
	if literal, ok := call.Args[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
		pattern, err := strconv.Unquote(literal.Value)
		return []string{pattern}, err
	}
	for k := len(ranges) - 1; k >= 0; k-- {
		over, ok := ranges[k].X.(*ast.Ident)
		if !ok || lists[over.Name] == nil {
			continue
		}
		if value, ok := ranges[k].Value.(*ast.Ident); ok {
			if arg, ok := call.Args[0].(*ast.Ident); ok && arg.Name == value.Name {
				return lists[over.Name], nil
			}
		}
		var between []string
		ast.Inspect(call.Args[0], func(n ast.Node) bool {
			if literal, ok := n.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				s, _ := strconv.Unquote(literal.Value)
				between = append(between, s)
			}
			return true
		})
		if _, ok := call.Args[0].(*ast.BinaryExpr); !ok || len(between) != 1 || !strings.HasPrefix(between[0], " /") {
			break
		}
		var patterns []string
		for _, element := range lists[over.Name] {
			method, path, _ := strings.Cut(element, " ")
			patterns = append(patterns, method+between[0]+path)
		}
		return patterns, nil
	}
	return nil, fmt.Errorf("the pattern of this %s is neither a string literal nor read from a list of them", selector.Sel.Name)
}

// stringList returns the elements of a []string{...} of literals.
func stringList(e ast.Expr) ([]string, bool) {
	composite, ok := e.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	array, ok := composite.Type.(*ast.ArrayType)
	if !ok {
		return nil, false
	}
	if element, ok := array.Elt.(*ast.Ident); !ok || element.Name != "string" {
		return nil, false
	}
	var out []string
	for _, elt := range composite.Elts {
		literal, ok := elt.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return nil, false
		}
		s, err := strconv.Unquote(literal.Value)
		if err != nil {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// The options of curl that take a value, and those among them that
// send a body, which makes the method POST unless -X says otherwise.
var (
	curlValues = []string{"-X", "--request", "-d", "--data", "--data-raw", "--data-binary", "--data-urlencode", "--json",
		"-H", "--header", "-o", "--output", "-w", "--write-out", "-u", "--user", "-F", "--form", "--form-string",
		"-m", "--max-time", "-T", "--upload-file", "--url", "-A", "--user-agent", "-b", "--cookie"}
	curlBodies = []string{"-d", "--data", "--data-raw", "--data-binary", "--data-urlencode", "--json", "-F", "--form", "--form-string"}
)

// checkCurl checks that a route of the package that serves the address
// of a curl takes its method and its path.
func (r *Repository) checkCurl(args []string) []string {
	method, target := "", ""
	body := false
	for i := 0; i < len(args); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if !strings.HasPrefix(name, "-") {
			if target == "" {
				target = args[i]
			}
			continue
		}
		if !slices.Contains(curlValues, name) {
			continue
		}
		if !hasValue && i+1 < len(args) {
			i++
			value = args[i]
		}
		switch {
		case name == "-X" || name == "--request":
			method = value
		case name == "--url":
			target = value
		case slices.Contains(curlBodies, name):
			body = true
		}
	}
	if method == "" {
		method = "GET"
		if body {
			method = "POST"
		}
	}
	if target == "" {
		// A mention in the text, such as "the `curl -X DELETE` of the
		// previous slide".
		return nil
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return []string{fmt.Sprintf("curl to %q, which is not an absolute URL", target)}
	}
	dir, known := httpHosts[u.Host]
	if !known {
		return []string{fmt.Sprintf("curl to %s, whose routes this check does not know; add the address to httpHosts in slides/internal/facts", u.Host)}
	}
	ok, problems := r.once("routes "+dir, func() error {
		if r.routes[dir] != nil {
			return nil
		}
		var err error
		r.routes[dir], err = readRoutes(filepath.Join(r.root, filepath.FromSlash(dir)))
		return err
	})
	if !ok {
		return problems
	}
	for _, route := range r.routes[dir] {
		if route.matches(method, u.Path) {
			return nil
		}
	}
	return []string{fmt.Sprintf("%s registers no route for %s %s", dir, method, u.Path)}
}
