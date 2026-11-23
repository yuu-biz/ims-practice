package facts

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// composeFile is what a deck may name of a Compose file: its services,
// with the entrypoint and the address of each, its profiles, and the
// subnet of each of its networks.
type composeFile struct {
	entrypoints map[string]string
	profiles    map[string]bool
	addresses   map[string]string // the address of a service -> the service
	subnets     map[string]string // the subnet of a network -> the network
}

var (
	keyLine        = regexp.MustCompile(`^  ([A-Za-z0-9][A-Za-z0-9_.-]*):\s*$`)
	entrypointLine = regexp.MustCompile(`^    entrypoint:\s*\[\s*"?([^",\]]+)`)
	profilesLine   = regexp.MustCompile(`^\s+profiles:\s*\[([^\]]*)\]`)
	addressLine    = regexp.MustCompile(`^\s+ipv4_address:\s*(\S+)`)
	subnetLine     = regexp.MustCompile(`^\s+-\s*subnet:\s*(\S+)`)
)

// readCompose reads a Compose file line by line: the keys two spaces
// deep under "services:" and under "networks:", the "entrypoint:" and
// every "ipv4_address:" of a service, the "subnet:" of a network, and
// every "profiles:" list. The Compose files of the repository are
// written in that one layout. An entrypoint that a service takes from
// an anchor (<<: *name) is not followed, so the command of such a
// service is not checked.
func readCompose(path string) (*composeFile, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f := &composeFile{
		entrypoints: map[string]string{},
		profiles:    map[string]bool{},
		addresses:   map[string]string{},
		subnets:     map[string]string{},
	}
	inServices, inNetworks := false, false
	service, network := "", ""
	for _, line := range strings.Split(string(text), "\n") {
		line = strings.TrimRight(line, "\r")
		if line != "" && line[0] != ' ' && line[0] != '#' {
			inServices = strings.HasPrefix(line, "services:")
			inNetworks = strings.HasPrefix(line, "networks:")
			service, network = "", ""
		}
		if m := profilesLine.FindStringSubmatch(line); m != nil {
			for _, p := range strings.Split(m[1], ",") {
				f.profiles[strings.Trim(strings.TrimSpace(p), `"'`)] = true
			}
		}
		key := keyLine.FindStringSubmatch(line)
		switch {
		case inServices && key != nil:
			service = key[1]
			f.entrypoints[service] = ""
		case inServices && service != "":
			if m := entrypointLine.FindStringSubmatch(line); m != nil {
				f.entrypoints[service] = strings.TrimSpace(m[1])
			}
			if m := addressLine.FindStringSubmatch(line); m != nil {
				f.addresses[m[1]] = service
			}
		case inNetworks && key != nil:
			network = key[1]
		case inNetworks && network != "":
			if m := subnetLine.FindStringSubmatch(line); m != nil {
				f.subnets[m[1]] = network
			}
		}
	}
	if len(f.entrypoints) == 0 {
		return nil, fmt.Errorf("%s has no services", path)
	}
	return f, nil
}

func (r *Repository) composeFile(name string) (*composeFile, []string) {
	ok, problems := r.once("compose "+name, func() error {
		if r.compose[name] != nil {
			return nil
		}
		f, err := readCompose(filepath.Join(r.root, filepath.FromSlash(name)))
		r.compose[name] = f
		return err
	})
	if !ok {
		return nil, problems
	}
	return r.compose[name], nil
}

// The options of docker compose that take a value, before the command
// (global) and after each command. Every other option is taken to be a
// switch.
var (
	composeGlobalValues = []string{"-f", "--file", "-p", "--project-name", "--profile", "--project-directory", "--env-file", "--ansi", "--progress", "--parallel"}
	composeValues       = map[string][]string{
		"run":     {"-e", "--env", "--name", "--entrypoint", "-u", "--user", "-w", "--workdir", "-v", "--volume", "-p", "--publish", "-l", "--label", "--pull"},
		"exec":    {"-e", "--env", "-u", "--user", "-w", "--workdir", "--index"},
		"up":      {"-t", "--timeout", "--scale", "--exit-code-from", "--pull", "--wait-timeout"},
		"down":    {"--rmi", "-t", "--timeout"},
		"logs":    {"-n", "--tail", "--since", "--until", "--index"},
		"stop":    {"-t", "--timeout"},
		"restart": {"-t", "--timeout"},
		"kill":    {"-s", "--signal"},
	}
)

// How the arguments after a command of docker compose are read: run
// and exec take one service and then the command to run in it; the
// commands of composeServiceLists take services only; the others are
// not checked beyond their name.
var (
	composeRunLike       = []string{"run", "exec"}
	composeServiceLists  = []string{"up", "logs", "rm", "stop", "start", "restart", "build", "ps", "pull", "push", "kill", "create", "pause", "unpause", "top", "images", "events", "attach", "wait", "watch", "stats"}
	composeOtherCommands = []string{"down", "config", "ls", "version", "cp", "port", "scale", "volumes", "alpha", "commit", "export"}
)

// checkCompose checks the arguments of "docker compose": the profiles,
// the command, and the services, against the Compose files that -f
// names, compose.yaml at the root by default. The command run in a
// service whose entrypoint is the UE simulator is checked as a ue
// command.
func (r *Repository) checkCompose(args []string) []string {
	var files, profiles []string
	i := 0
	for ; i < len(args) && strings.HasPrefix(args[i], "-"); i++ {
		name, value, hasValue := strings.Cut(args[i], "=")
		if !slices.Contains(composeGlobalValues, name) {
			continue
		}
		if !hasValue && i+1 < len(args) {
			i++
			value = args[i]
		}
		switch name {
		case "-f", "--file":
			files = append(files, value)
		case "--profile":
			profiles = append(profiles, value)
		}
	}
	if len(files) == 0 {
		files = []string{"compose.yaml"}
	}
	project := &composeFile{entrypoints: map[string]string{}, profiles: map[string]bool{}}
	for _, name := range files {
		f, problems := r.composeFile(name)
		if f == nil {
			return problems
		}
		for s, e := range f.entrypoints {
			project.entrypoints[s] = e
		}
		for p := range f.profiles {
			project.profiles[p] = true
		}
	}
	var problems []string
	for _, p := range profiles {
		if !project.profiles[p] {
			problems = append(problems, fmt.Sprintf("no profile %q in %s", p, strings.Join(files, ", ")))
		}
	}
	if i == len(args) {
		return problems
	}
	verb := args[i]
	var positional []string
	for i++; i < len(args); i++ {
		name, _, hasValue := strings.Cut(args[i], "=")
		switch {
		case strings.HasPrefix(name, "-"):
			if !hasValue && slices.Contains(composeValues[verb], name) {
				i++
			}
		case slices.Contains(composeRunLike, verb):
			// The service, then the command to run in it with its own
			// options.
			positional = args[i:]
			i = len(args)
		default:
			positional = append(positional, args[i])
		}
	}
	service := func(s string) bool {
		if _, ok := project.entrypoints[s]; !ok {
			problems = append(problems, fmt.Sprintf("no service %q in %s", s, strings.Join(files, ", ")))
			return false
		}
		return true
	}
	switch {
	case slices.Contains(composeRunLike, verb):
		if len(positional) > 0 && service(positional[0]) && project.entrypoints[positional[0]] == "ue" {
			problems = append(problems, r.checkUE(positional[1:])...)
		}
	case slices.Contains(composeServiceLists, verb):
		for _, s := range positional {
			service(s)
		}
	case !slices.Contains(composeOtherCommands, verb):
		problems = append(problems, fmt.Sprintf("docker compose has no command %q", verb))
	}
	return problems
}
