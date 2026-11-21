package facts

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// ueCommand is what the UE simulator takes on its command line: its
// subcommands, and its flags with whether each takes a value.
type ueCommand struct {
	subcommands map[string]bool
	flags       map[string]bool
}

var (
	usageLine = regexp.MustCompile(`^(?:usage:)?\s+ue ([a-z]+)`)
	flagLine  = regexp.MustCompile(`^  -([A-Za-z0-9-]+)( [^\t]+)?(?:\t|$)`)
)

// readUE runs "ue register -h" from the source of the repository, which
// prints the usage of every subcommand and then every flag, one flag to
// a line as package flag prints them: "  -name type" for a flag that
// takes a value, "  -name" for a switch.
func readUE(root string) (*ueCommand, error) {
	run := exec.Command("go", "run", "./cmd/ue", "register", "-h")
	run.Dir = root
	// The help ends with exit status 2, as package flag ends it.
	out, _ := run.CombinedOutput()
	c := &ueCommand{subcommands: map[string]bool{}, flags: map[string]bool{}}
	for _, line := range strings.Split(string(out), "\n") {
		if m := usageLine.FindStringSubmatch(line); m != nil {
			c.subcommands[m[1]] = true
		}
		if m := flagLine.FindStringSubmatch(line); m != nil {
			c.flags[m[1]] = m[2] != ""
		}
	}
	if len(c.subcommands) == 0 || len(c.flags) == 0 {
		return nil, fmt.Errorf("go run ./cmd/ue register -h in %s printed no usage and flags:\n%s", root, out)
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
	if !r.ue.subcommands[args[0]] {
		return []string{fmt.Sprintf("ue has no subcommand %q", args[0])}
	}
	for i := 1; i < len(args) && strings.HasPrefix(args[i], "-") && args[i] != "-" && args[i] != "--"; i++ {
		name, _, hasValue := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		takesValue, known := r.ue.flags[name]
		if !known {
			problems = append(problems, fmt.Sprintf("ue has no flag -%s", name))
			continue
		}
		if takesValue && !hasValue {
			i++
		}
	}
	return problems
}
