package facts

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

const repositoryRoot = "../../.."

func parse(t *testing.T, body string) *deck.Deck {
	t.Helper()
	d, err := deck.Parse("stale.md", "---\ntitle: T\nfooter: F\nlang: en\n---\n\n# T\n\n---\n\n## Commands\n\n"+body)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestStaleCommandsFail checks every kind of command against the
// repository itself: each line of the first block names something that
// the repository does not have, and the commands of the second block
// and of the list are those of the decks.
func TestStaleCommandsFail(t *testing.T) {
	d := parse(t, "```sh\n"+
		"curl -X DELETE http://localhost:8080/api/epc/ues/001010000000010/bearers/6\n"+
		"docker compose run --rm nosuch register\n"+
		"docker compose --profile nosuch up -d\n"+
		"docker compose logs -f scscf nosuch2\n"+
		"docker compose frobnicate\n"+
		"docker compose run --rm ue dial tel:+12025550102\n"+
		"docker compose run --rm ue2 xcap \\\n  --no-such-flag x set cfu on\n"+
		"docker compose run --rm ue call --ring 1s tel:+12025550102\n"+
		"curl http://localhost:9999/api/calls\n"+
		"bash scripts/e2e-nosuch.sh && ls scripts/nosuch-*.sh\n"+
		"```\n\n"+
		"```sh\n"+
		"curl -X DELETE http://localhost:8080/api/epc/network-initiated/ues/001010000000010/bearers/6\n"+
		"curl http://localhost:8080/api/epc/ues/001010000000010   # the bearers\n"+
		"docker compose --profile ue --profile phones down --rmi all -v --remove-orphans\n"+
		"docker compose run --rm ue sms --status-report +12025550102 \"Hello; UE-B\"\n"+
		"docker compose run --rm volte-a call -epc http://172.28.0.80:8080 --tone=3s tel:+12025550112\n"+
		"docker compose logs scscf | grep I-Open\n"+
		"```\n\n"+
		"- `ue xcap set cfu on tel:+12025550103`, `ue serve`, the `curl -X DELETE` above, `scripts/e2e-*.sh` and `scripts/`.\n")
	var got []string
	for _, p := range NewRepository(repositoryRoot).Check(d) {
		got = append(got, strings.TrimPrefix(p.Error(), "stale.md: slide 2: "))
	}
	want := []string{
		"curl -X DELETE http://localhost:8080/api/epc/ues/001010000000010/bearers/6: internal/lab registers no route for DELETE /api/epc/ues/001010000000010/bearers/6",
		`docker compose run --rm nosuch register: no service "nosuch" in compose.yaml`,
		`docker compose --profile nosuch up -d: no profile "nosuch" in compose.yaml`,
		`docker compose logs -f scscf nosuch2: no service "nosuch2" in compose.yaml`,
		`docker compose frobnicate: docker compose has no command "frobnicate"`,
		`docker compose run --rm ue dial tel:+12025550102: ue has no subcommand "dial"`,
		"docker compose run --rm ue2 xcap --no-such-flag x set cfu on: ue xcap has no flag -no-such-flag",
		"docker compose run --rm ue call --ring 1s tel:+12025550102: ue call has no flag -ring",
		"curl http://localhost:9999/api/calls: curl to localhost:9999, whose routes this check does not know; add the address to httpHosts in slides/internal/facts",
		"scripts/e2e-nosuch.sh does not exist",
		"scripts/nosuch-*.sh matches no file",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestShellWords(t *testing.T) {
	got := splitCommands(shellWords(`A=1 ue sms "a \"b\"; c" 'd e'|grep x && ls # comment ; not a command`))
	want := [][]string{{"A=1", "ue", "sms", `a "b"; c`, "d e"}, {"grep", "x"}, {"ls"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func writeSource(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "routes.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadRoutes(t *testing.T) {
	dir := writeSource(t, `package p

var lists = []string{"GET /a/{id}", "DELETE /b/{id...}"}

var under = []string{"POST /call", "PUT /answer-policy"}

func handler(mux mux) {
	mux.HandleFunc("GET /api/calls", nil)
	for _, route := range lists {
		mux.Handle(route, nil)
	}
	for _, route := range under {
		method, path := cut(route)
		mux.HandleFunc(method+" /api/ues/{name}"+path, nil)
	}
	mux.Handle("GET /", nil)
}
`)
	routes, err := readRoutes(dir)
	if err != nil {
		t.Fatal(err)
	}
	var patterns []string
	for _, r := range routes {
		patterns = append(patterns, r.pattern)
	}
	if want := "GET /api/calls, GET /a/{id}, DELETE /b/{id...}, POST /api/ues/{name}/call, PUT /api/ues/{name}/answer-policy, GET /"; strings.Join(patterns, ", ") != want {
		t.Errorf("patterns = %s", strings.Join(patterns, ", "))
	}
	for _, c := range []struct {
		method, path string
		served       bool
	}{
		{"GET", "/api/calls", true},
		{"HEAD", "/api/calls", true},
		{"POST", "/api/calls", false},
		{"GET", "/a/7", true},
		{"GET", "/a/7/8", false},
		{"DELETE", "/b/7/8", true},
		{"POST", "/api/ues/phone1/call", true},
		{"POST", "/api/ues/phone1/dial", false},
		{"GET", "/", true},
		{"GET", "/api/nothing", false},
	} {
		served := false
		for _, r := range routes {
			served = served || r.matches(c.method, c.path)
		}
		if served != c.served {
			t.Errorf("%s %s served = %v, want %v", c.method, c.path, served, c.served)
		}
	}
}

// TestRoutesThatCannotBeRead makes sure that a route registered in a
// way that readRoutes does not follow fails the check instead of being
// left out.
func TestRoutesThatCannotBeRead(t *testing.T) {
	dir := writeSource(t, `package p

const prefix = "/api"

func handler(mux mux) {
	mux.HandleFunc("GET "+prefix+"/calls", nil)
}
`)
	if _, err := readRoutes(dir); err == nil || !strings.Contains(err.Error(), "neither a string literal nor read from a list") {
		t.Errorf("err = %v", err)
	}
}
