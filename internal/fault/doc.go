// Package fault injects faults into the nodes of the Compose network,
// so that a learner can watch what a failure does and how the network
// recovers. The Lab drives it through its HTTP API, which the
// fault agent of deploy/chaos/agent.yaml serves; deploy/chaos/fault.sh
// holds the same faults as shell commands, and runs the same commands
// for all of them but recreate, which it leaves to Compose. The OAM
// does not use it:
// taking a node out of service, a drain, is maintenance and not a
// fault.
//
// A fault has a target and a kind. The target is a Compose service,
// such as "pcscf", "ibcf-a" of deploy/interconnect or "scscf-1" of a
// redundant deployment, or one container of a service that runs
// several, such as "agw:2". Docker finds it by the labels that Compose
// puts on every container (project, service, container number), so any
// service of any override file is a target, with no list of names in
// this package; the UEs that "docker compose run" starts are targets
// too, while they run. The kinds are:
//
//	stop       docker stop, cleared by docker start
//	kill       docker kill, cleared by docker start
//	pause      docker pause, cleared by docker unpause
//	crash      the node's process dies (SIGQUIT) and the restart policy
//	           of the container brings it back
//	recreate   the container is replaced by a new one with the same
//	           configuration, name and address
//	delay      tc netem on what the target sends
//	loss       nftables drops packets at random, both ways
//	partition  nftables drops the packets to and from some peers, or
//	           all of them
//
// A crash and a recreate happen once: the node comes back by itself, so
// they take no duration, end in the state Done as soon as the runtime
// has done them, and have nothing to clear. The others stay until they
// are cleared or their duration is up.
//
// The peers of a partition are services (with every container),
// single containers, path.Match patterns of services such as "scscf-*"
// for every instance of a role, or IPv4 addresses. A network fault sits
// on every interface of the target, or only on the one on a Compose
// network: a partition of the IBCF on the network "nni", without
// peers, cuts the link between the two networks of deploy/interconnect
// and leaves the IBCF's own network alone.
//
// The network faults sit at the target's network interface, inside the
// target's network namespace: a packet that the target sends and loses
// never reaches the bridge, so the capture of the bridge does not hold
// it; a packet that the target receives and loses is in the capture,
// because it crossed the bridge before the interface dropped it. They are
// nftables rules in the netdev family, on the egress and ingress hooks
// of the interface. Dropped there, a packet is lost silently, as on a
// real link; an iptables rule in the OUTPUT chain would instead fail the
// sender's sendto with EPERM, which the SIP stack takes as a transport
// error and not as a loss.
//
// Every fault expires: the request gives a duration, or DefaultDuration
// applies, and Manager clears the fault when the time is up, so that a
// forgotten fault does not stay. A request that ends before its answer,
// because the Lab page was closed or curl stopped, does not cut the
// change short: the agent finishes applying or clearing the fault, and
// the list shows it. Restarting the target also clears its
// network faults, because the container gets a new network namespace.
//
// The bodies are JSON, and every error is ErrorBody, {"error": "..."}.
// Times are RFC 3339 in UTC, durations Go durations such as "200ms".
//
//	GET    /fault/v1/faults
//	       []Fault, the oldest first, with the ended ones for a while.
//	POST   /fault/v1/faults
//	       Request -> 201 Fault, in state active, or done for a fault
//	       that happens once. 400 for a request
//	       that the API cannot take, 409 when the target's container
//	       already has a fault that this one would meet, by any name
//	       of it, 500 with the failed Fault in
//	       ErrorBody.fault when the executor could not apply it. A
//	       network fault that failed part way is taken back; when that
//	       fails too, the fault's error says "clean-up".
//	GET    /fault/v1/faults/{id}
//	       Fault. 404 for an unknown id.
//	DELETE /fault/v1/faults/{id}
//	       Fault, in state cleared. A fault that has ended already is
//	       answered as it is. 409 while it is being applied or cleared,
//	       500 with the Fault when the executor could not clear it.
//
// Manager keeps the faults in its memory. It is not an IMS node, and a
// lab holds a handful of faults, so neither keeping them outside the
// process nor sharing them between instances is worth it; but a fault
// applied by a process that then dies stays until someone removes it
// (deploy/chaos/fault.sh heal).
package fault
