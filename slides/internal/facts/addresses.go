package facts

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
)

// networkFiles are the Compose files that put the nodes of the sessions
// on their networks: the home network, and the second network with the
// link between the two that the session on interconnection adds on top
// of it. A deck does not say which of the two a node is in, so an
// address may come from either.
var networkFiles = []string{"compose.yaml", "deploy/interconnect/compose.yaml"}

// network is where the addresses of the Compose files come from: the
// service that has each address, and the network that has each subnet.
type network struct {
	services map[string]string
	subnets  map[string]string
}

// composeNetwork returns the addresses and the subnets of networkFiles.
func (r *Repository) composeNetwork() (*network, []string) {
	if r.network != nil {
		return r.network, nil
	}
	n := &network{services: map[string]string{}, subnets: map[string]string{}}
	for _, name := range networkFiles {
		f, problems := r.composeFile(name)
		if f == nil {
			return nil, problems
		}
		for address, service := range f.addresses {
			n.services[address] = service
		}
		for subnet, bridge := range f.subnets {
			n.subnets[subnet] = bridge
		}
	}
	r.network = n
	return n, nil
}

// nodeTables are the first two cells of the header of a table that
// gives the nodes of a session and their addresses, in each language of
// the decks. In such a table the first column names the nodes and the
// second holds their addresses.
var nodeTables = [][]string{{"Node", "Address"}, {"ノード", "アドレス"}}

// checkTables reports what the tables of a deck say about the Compose
// network that is no longer so: an address that no service has, a
// subnet that no network has, and a row of a table of nodes whose first
// column names services other than those of the row's addresses.
//
// Only the tables are read. A deck names an address in a diagram or in
// a sentence as well, and there it may be an address on purpose that no
// node has, such as the host of no peer network whose requests a border
// drops.
func (r *Repository) checkTables(d *deck.Deck) []error {
	all := tables(d)
	if !slices.ContainsFunc(all, func(t slideTable) bool { return holdsFourNumbers(t.table) }) {
		return nil
	}
	n, problems := r.composeNetwork()
	var out []error
	for _, p := range problems {
		out = append(out, fmt.Errorf("%s: %s", d.Path, p))
	}
	if n == nil {
		return out
	}
	for _, t := range all {
		for _, p := range n.checkTable(t.table) {
			out = append(out, fmt.Errorf("%s: slide %d: %s", d.Path, t.slide, p))
		}
	}
	return out
}

// slideTable is one table of a deck with the number of its slide.
type slideTable struct {
	slide int
	table deck.Table
}

func tables(d *deck.Deck) []slideTable {
	var out []slideTable
	for _, slide := range d.Slides {
		for _, column := range d.Sections(slide) {
			for _, b := range column {
				if t, ok := b.(deck.Table); ok {
					out = append(out, slideTable{slide.Number, t})
				}
			}
		}
	}
	return out
}

// holdsFourNumbers reports whether any cell of a table holds four
// numbers separated by dots, which may be an address. A deck with none,
// such as one in a test, needs no Compose file.
func holdsFourNumbers(t deck.Table) bool {
	for _, row := range t.Rows {
		for _, cell := range row {
			if addressList.MatchString(deck.PlainText(cell)) {
				return true
			}
		}
	}
	return false
}

func (n *network) checkTable(t deck.Table) []string {
	nodes := isNodeTable(t.Header)
	var problems []string
	for _, row := range t.Rows {
		var addresses []string // those of the Address column
		for i, cell := range row {
			for _, a := range n.addressesIn(deck.PlainText(cell)) {
				if nodes && i == 1 {
					addresses = append(addresses, a)
				}
				if p := n.checkAddress(a); p != "" {
					problems = append(problems, p)
				}
			}
		}
		if nodes && len(addresses) > 0 {
			if p := n.checkNames(row[0], addresses); p != "" {
				problems = append(problems, p)
			}
		}
	}
	return problems
}

func isNodeTable(header []deck.Cell) bool {
	if len(header) < 2 {
		return false
	}
	first, second := deck.PlainText(header[0]), deck.PlainText(header[1])
	return slices.ContainsFunc(nodeTables, func(h []string) bool {
		return h[0] == first && h[1] == second
	})
}

// checkAddress reports an address that no service of the Compose files
// has, or a subnet that no network of them has.
func (n *network) checkAddress(address string) string {
	if strings.Contains(address, "/") {
		if _, ok := n.subnets[address]; !ok {
			return fmt.Sprintf("%s is the subnet of no network of %s", address, strings.Join(networkFiles, " and "))
		}
		return ""
	}
	if _, ok := n.services[address]; !ok {
		return fmt.Sprintf("%s is the address of no service of %s", address, strings.Join(networkFiles, " and "))
	}
	return ""
}

// checkNames reports a row of a table of nodes whose first column names,
// in code, services other than those of the addresses of the row. The
// decks write the name of a service in code (`mrfp`) and the name of a
// node in plain text (MRFP), so only a code span is read as the name of
// a service; a row that names none is checked by its addresses alone.
func (n *network) checkNames(cell deck.Cell, addresses []string) string {
	var names []string
	for _, span := range cell {
		if span.Code {
			names = append(names, span.Text)
		}
	}
	if len(names) == 0 {
		return ""
	}
	var services []string
	for _, a := range addresses {
		if s, ok := n.services[a]; ok && !slices.Contains(services, s) {
			services = append(services, s)
		}
	}
	slices.Sort(names)
	slices.Sort(services)
	if slices.Equal(names, services) {
		return ""
	}
	return fmt.Sprintf("the row names %s; the services of its addresses are %s",
		strings.Join(names, ", "), strings.Join(services, ", "))
}

// addressList matches an address or a subnet, and the list that a table
// may write after it with the last number only: "172.28.0.100, 101, 103"
// and "172.28.0.110、.111、.112" are three addresses each.
var addressList = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?:/\d{1,2})?(?:[,、]\s*\.?\d{1,3}\b)*`)

// addressesIn returns the addresses and subnets of the Compose networks
// that a text names. Four numbers outside the range of every network
// are the clause of a specification, such as TS 24.229 5.4.1.2, and not
// an address.
func (n *network) addressesIn(text string) []string {
	var out []string
	for _, match := range addressList.FindAllString(text, -1) {
		items := strings.FieldsFunc(match, func(r rune) bool { return r == ',' || r == '、' })
		first := strings.TrimSpace(items[0])
		if !n.inRange(first) {
			continue
		}
		out = append(out, first)
		address, _, _ := strings.Cut(first, "/")
		prefix := address[:strings.LastIndex(address, ".")+1]
		for _, item := range items[1:] {
			out = append(out, prefix+strings.TrimLeft(strings.TrimSpace(item), "."))
		}
	}
	return out
}

// inRange reports whether an address or a subnet is in the range of one
// of the networks of the Compose files. The first two numbers decide,
// so that an address whose third number is wrong is reported as an
// address of no service instead of being taken for something else.
func (n *network) inRange(address string) bool {
	for subnet := range n.subnets {
		if firstTwoNumbers(subnet) == firstTwoNumbers(address) {
			return true
		}
	}
	return false
}

func firstTwoNumbers(address string) string {
	parts := strings.SplitN(address, ".", 3)
	if len(parts) < 3 {
		return ""
	}
	return parts[0] + "." + parts[1]
}
