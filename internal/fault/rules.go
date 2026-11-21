package fault

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// The commands that the helper container runs inside the target's
// network namespace. deploy/chaos/fault.sh runs the same ones, so that
// a learner can type what the API does.

// hepPort is the HEP collector's port (the dashboard's). The
// copies that a node sends to the dashboard are not IMS traffic, so a
// fault leaves them alone: the dashboard keeps showing what the node
// believes it sent and received.
const hepPort = 9060

// addressesCommand lists the IPv4 addresses of the namespace, one line
// each, such as "2: eth0    inet 172.28.0.40/24 brd ...". Docker names
// the interfaces, and a node on two networks (an IBCF on its own
// network and on the NNI) has two, so a fault finds its interface by
// the address that Docker reports for the network.
func addressesCommand() []string {
	return []string{"ip", "-o", "-4", "addr", "show"}
}

// devicesOf picks, from the output of addressesCommand, the interfaces
// that hold the addresses, in the order of the output.
func devicesOf(output string, addresses []netip.Addr) []string {
	var devices []string
	for _, line := range strings.Split(output, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[2] != "inet" {
			continue
		}
		prefix, err := netip.ParsePrefix(f[3])
		if err != nil || !slices.Contains(addresses, prefix.Addr()) {
			continue
		}
		device, _, _ := strings.Cut(f[1], "@")
		if !slices.Contains(devices, device) {
			devices = append(devices, device)
		}
	}
	return devices
}

// tableName is the nftables table of a fault. The prefix lets
// fault.sh heal find the tables that a dead process left behind.
func tableName(id string) string { return "ims_fault_" + id }

// nftTable is the start of every table: a chain on each hook of each
// interface, before the packets reach the bridge (egress) or after they
// leave it (ingress). The chains are named after the hook and the
// interface, such as egress_eth0.
func nftTable(table string, devices []string) []string {
	s := []string{"add table netdev " + table}
	for _, d := range devices {
		s = append(s,
			fmt.Sprintf(`add chain netdev %s egress_%s { type filter hook egress device "%s" priority filter; policy accept; }`, table, d, d),
			fmt.Sprintf(`add chain netdev %s ingress_%s { type filter hook ingress device "%s" priority filter; policy accept; }`, table, d, d),
			fmt.Sprintf("add rule netdev %s egress_%s udp dport %d accept", table, d, hepPort))
	}
	return s
}

// nftCommand makes one nft invocation of the statements, which nft
// applies as one transaction: the table is there whole or not at all.
func nftCommand(statements []string) []string {
	return []string{"nft", strings.Join(statements, "; ")}
}

// lossCommand drops IP packets at random both ways. ARP is left alone,
// so that the loss is of the IMS traffic and not of the neighbours.
func lossCommand(table string, devices []string, percent int) []string {
	s := nftTable(table, devices)
	for _, d := range devices {
		for _, hook := range []string{"egress", "ingress"} {
			s = append(s, fmt.Sprintf("add rule netdev %s %s_%s meta protocol ip numgen random mod 100 < %d counter drop", table, hook, d, percent))
		}
	}
	return nftCommand(s)
}

// partitionCommand drops the IP packets to and from the peers, or all
// IP packets when there are none.
func partitionCommand(table string, devices []string, peers []netip.Addr) []string {
	s := nftTable(table, devices)
	egress, ingress := "meta protocol ip", "meta protocol ip"
	if len(peers) > 0 {
		set := make([]string, len(peers))
		for i, p := range peers {
			set[i] = p.String()
		}
		list := "{ " + strings.Join(set, ", ") + " }"
		egress, ingress = "ip daddr "+list, "ip saddr "+list
	}
	for _, d := range devices {
		s = append(s,
			fmt.Sprintf("add rule netdev %s egress_%s %s counter drop", table, d, egress),
			fmt.Sprintf("add rule netdev %s ingress_%s %s counter drop", table, d, ingress))
	}
	return nftCommand(s)
}

func nftDeleteCommand(table string) []string {
	return []string{"nft", "delete table netdev " + table}
}

// netemCommand delays what the target sends on one interface. netem
// works on the egress of the interface only; delaying what the target
// receives would need an ifb device as well.
//
// It needs sch_netem in the kernel that runs the containers. Without
// it, tc answers "Specified qdisc kind is unknown", which the fault
// then shows as its error.
func netemCommand(device string, delay, jitter time.Duration) []string {
	c := []string{"tc", "qdisc", "add", "dev", device, "root", "netem", "delay", ms(delay)}
	if jitter > 0 {
		c = append(c, ms(jitter))
	}
	return c
}

func netemDeleteCommand(device string) []string {
	return []string{"tc", "qdisc", "del", "dev", device, "root"}
}

// ms writes d the way tc reads it, in milliseconds or microseconds.
func ms(d time.Duration) string {
	if d%time.Millisecond == 0 {
		return fmt.Sprintf("%dms", d/time.Millisecond)
	}
	return fmt.Sprintf("%dus", d/time.Microsecond)
}

// alreadyGone tells whether a clear command failed only because the
// rule is not there, which happens when the target restarted and got a
// new network namespace. nft says "No such file or directory"; tc,
// for a root qdisc that is the default one, "Cannot delete qdisc with
// handle of zero".
func alreadyGone(output string) bool {
	return strings.Contains(output, "No such file or directory") ||
		strings.Contains(output, "Cannot delete qdisc with handle of zero")
}
