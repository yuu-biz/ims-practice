package facts

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ueCommand is what the UE simulator takes on its command line: its
// subcommands, and the flags of each with whether it takes a value.
type ueCommand struct {
	flags map[string]map[string]bool
}

var (
	usageLine = regexp.MustCompile(`^(?:usage:)?\s+ue ([a-z]+)`)
	flagLine  = regexp.MustCompile(`^  -([A-Za-z0-9-]+)( [^\t]+)?(?:\t|$)`)
)

// readUE builds the UE simulator from the source of the repository and
// reads what it prints for "ue register -h", the usage of every
// subcommand, and for "ue SUBCOMMAND -h", the flags of the subcommand,
// one to a line as package flag prints them: "  -name type" for a flag
// that takes a value, "  -name" for a switch.
func readUE(root string) (*ueCommand, error) {
	dir, err := os.MkdirTemp("", "ue-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	binary := filepath.Join(dir, "ue")
	build := exec.Command("go", "build", "-o", binary, "./cmd/ue")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("go build ./cmd/ue in %s: %w\n%s", root, err, out)
	}
	help := func(args ...string) string {
		// The help ends with exit status 0, and the usage of a command
		// that does not exist with 2; the output is what counts.
		out, _ := exec.Command(binary, append(args, "-h")...).CombinedOutput()
		return string(out)
	}
	c := &ueCommand{flags: map[string]map[string]bool{}}
	for _, line := range strings.Split(help("register"), "\n") {
		if m := usageLine.FindStringSubmatch(line); m != nil {
			c.flags[m[1]] = map[string]bool{}
		}
	}
	if len(c.flags) == 0 {
		return nil, fmt.Errorf("ue register -h in %s printed no usage:\n%s", root, help("register"))
	}
	for subcommand, flags := range c.flags {
		for _, line := range strings.Split(help(subcommand), "\n") {
			if m := flagLine.FindStringSubmatch(line); m != nil {
				flags[m[1]] = m[2] != ""
			}
		}
		if len(flags) == 0 {
			return nil, fmt.Errorf("ue %s -h in %s printed no flags", subcommand, root)
		}
	}
	return c, nil
}

// checkUE checks the arguments of the UE simulator: the subcommand,
// and the flags before the first argument that is not a flag, where
// package flag stops.
func (r *Repository) checkUE(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	ok, problems := r.once("ue", func() error {
		if r.ue != nil {
			return nil
		}
		var err error
		r.ue, err = readUE(r.root)
		return err
	})
	if !ok {
		return problems
	}
	flags, ok := r.ue.flags[args[0]]
	if !ok {
		return []string{fmt.Sprintf("ue has no subcommand %q", args[0])}
	}
	for i := 1; i < len(args) && strings.HasPrefix(args[i], "-") && args[i] != "-" && args[i] != "--"; i++ {
		name, _, hasValue := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		takesValue, known := flags[name]
		if !known {
			problems = append(problems, fmt.Sprintf("ue %s has no flag -%s", args[0], name))
			continue
		}
		if takesValue && !hasValue {
			i++
		}
	}
	return problems
}
