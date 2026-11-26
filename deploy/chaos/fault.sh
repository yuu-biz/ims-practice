#!/bin/sh
# Inject faults into the nodes of the Compose network and remove them,
# to watch what a failure does and how the network recovers. Run it from the root of the repository; README.md in this
# directory explains each fault and what to look for.
#
# The faults are the same ones as those of the fault API
# (internal/fault): the network faults run the same nft and tc commands,
# in the target's network namespace, in a container of the fault-tools
# image of deploy/chaos/chaos.yaml, and the node faults the same docker
# commands. A test of that package runs this script against a docker of
# its own and compares the two, command by command, so that neither side
# drifts from the other. recreate is the one fault where the commands
# differ: here Compose makes the new container from its file, and the
# API, which has no Compose file, copies the configuration of the
# container it replaces (deploy/chaos/README.md). Both find the
# containers by the labels that Compose puts on them, so every service
# of compose.yaml and of the override files is a target.
set -eu

usage() {
	cat >&2 <<'EOF'
usage: deploy/chaos/fault.sh COMMAND [ARGUMENTS]

TARGET is a service, such as scscf, ibcf-a or scscf-1, or one container
of a service that runs several, such as agw:2. A UE that "docker compose
run" started is a target while it runs.

Node faults:
  stop TARGET         stop gracefully (SIGTERM); undo with: start TARGET
  kill TARGET         kill (SIGKILL); undo with: start TARGET
  pause TARGET        freeze; undo with: unpause TARGET
  crash TARGET        make the process die; Docker restarts it by itself
  recreate SERVICE    kill the container, remove it and create a new one,
                      with the same name and address; nothing to undo
  start TARGET
  unpause TARGET

Network faults, at the interfaces of TARGET; undo with: clear TARGET
  loss [--network NET] TARGET PERCENT
        drop that share of the IP packets, both ways
  delay [--network NET] TARGET MS [JITTER_MS]
        delay what TARGET sends (needs sch_netem)
  partition [--network NET] TARGET [PEER...]
        cut TARGET off from the peers, or from everything. A peer is a
        service (all its containers), service:number, a pattern such as
        'scscf-[0-9]*' for every numbered instance of a role, or an IPv4 address.

  --network NET puts the fault on the interface of TARGET on the Compose
  network NET only, such as nni for the link between the two networks of
  deploy/interconnect; without it, the fault is on every interface.

  clear TARGET        remove the network faults of TARGET
  show TARGET         show the network faults of TARGET, with counters
  heal                undo every fault on every container of the project

Set COMPOSE_FILE and COMPOSE_PROJECT_NAME as for docker compose to act
on another stack; the default is compose.yaml with deploy/chaos/chaos.yaml.
FAULT_TOOLS_IMAGE names another image with nft, tc and ip.
EOF
	exit 2
}

: "${COMPOSE_FILE:=compose.yaml:deploy/chaos/chaos.yaml}"
# Compose splits COMPOSE_FILE at ";" on Windows unless told otherwise.
export COMPOSE_FILE COMPOSE_PATH_SEPARATOR=:
export MSYS_NO_PATHCONV=1

# Built by: docker compose -f compose.yaml -f deploy/chaos/chaos.yaml build fault-tools
tools_image=${FAULT_TOOLS_IMAGE:-ims-practice/fault-tools:dev}
# The HEP copies for the Lab (UDP 9060) are not IMS traffic; they
# pass, so that the Lab keeps showing what each node believes it
# sent and received.
hep_port=9060

fail() {
	echo "fault.sh: $*" >&2
	exit 1
}

# quote ARG... prints the arguments as a shell takes them, so that the
# printed command can be typed again.
quote() {
	for a in "$@"; do
		case $a in
		'' | *[!A-Za-z0-9_./:=@%+-]*) printf " '%s'" "$(printf '%s' "$a" | sed "s/'/'\\\\''/g")" ;;
		*) printf ' %s' "$a" ;;
		esac
	done
}

# run COMMAND... prints the command, then runs it.
run() {
	printf '+%s\n' "$(quote "$@")" >&2
	"$@"
}

# The project of the stack: COMPOSE_PROJECT_NAME, or the name in the
# Compose files (ims-practice for compose.yaml).
project_name() {
	if [ -n "${COMPOSE_PROJECT_NAME:-}" ]; then
		echo "$COMPOSE_PROJECT_NAME"
	else
		docker compose config --format json | sed -n 's/^  "name": "\(.*\)",$/\1/p'
	fi
}

# containers SERVICE [NUMBER] prints the names of the containers of the
# service, one per line: the ones that "docker compose up" made, or, for
# a service that has none (the UEs ue to ue3), its running containers
# of "docker compose run". NUMBER picks the container of that
# com.docker.compose.container-number.
containers() {
	set -- --filter "label=com.docker.compose.project=$project" --filter "label=com.docker.compose.service=$1" ${2:+--filter} ${2:+"label=com.docker.compose.container-number=$2"}
	ids=$(docker ps -a -q --filter label=com.docker.compose.oneoff=False "$@")
	[ -n "$ids" ] || ids=$(docker ps -q --filter label=com.docker.compose.oneoff=True "$@")
	[ -z "$ids" ] || docker inspect -f '{{slice .Name 1}}' $ids
}

# services PATTERN prints the services of the project that the shell
# pattern matches, such as scscf-* for scscf-1 and scscf-2.
services() {
	for s in $(docker ps -a --filter "label=com.docker.compose.project=$project" --filter label=com.docker.compose.service --format '{{.Label "com.docker.compose.service"}}' | sort -u); do
		# shellcheck disable=SC2254 # the pattern is meant to match
		case $s in $1) echo "$s" ;; esac
	done
}

# container_of TARGET prints the name of the one container of TARGET,
# SERVICE or SERVICE:NUMBER.
container_of() {
	service=${1%%:*}
	number=
	case $1 in *:*) number=${1#*:} ;; esac
	names=$(containers "$service" "$number")
	count=$(printf '%s' "$names" | grep -c . || true)
	case $count in
	0) fail "$1 has no container in the project $project" ;;
	1) echo "$names" ;;
	*)
		numbers=$(docker inspect -f "{{index .Config.Labels \"com.docker.compose.container-number\"}}" $names | sort -n | sed "s/^/$service:/" | paste -s -d , - | sed 's/,/, /g')
		fail "the service $service has $count containers ($numbers); name one"
		;;
	esac
}

# addresses_of NAME [NETWORK] prints the IPv4 addresses of the
# container, on every network or on the Compose network NETWORK, which
# Docker names <project>_<network>.
addresses_of() {
	if [ -n "${2:-}" ]; then
		docker inspect -f "{{with index .NetworkSettings.Networks \"${project}_$2\"}}{{.IPAddress}}{{end}}" "$1"
	else
		docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' "$1"
	fi
}

# helper NAME COMMAND... runs a command of the tools image in the
# network namespace of the container, with NET_ADMIN, and prints the
# docker command first.
helper() {
	name=$1
	shift
	if [ "$(docker inspect -f '{{.HostConfig.NetworkMode}}' "$name")" = host ]; then
		# The capture containers share the host's namespace, to record the
		# bridges of the Compose networks.
		fail "$name uses the host's network; a network fault there would change the host"
	fi
	printf '+ docker run --rm --network container:%s --cap-add NET_ADMIN %s%s\n' "$name" "$tools_image" "$(quote "$@")" >&2
	docker run --rm --network "container:$name" --cap-add NET_ADMIN "$tools_image" "$@"
}

# devices NAME [NETWORK] prints the interfaces of the container that
# hold its addresses. Docker names the interfaces, and a node on two
# networks (an IBCF on its own network and on the NNI) has two, so the
# address that Docker reports for the network finds the interface.
devices() {
	addresses=$(addresses_of "$1" "${2:-}")
	if [ -z "$addresses" ]; then
		[ -z "${2:-}" ] || fail "$1 is not on the network $2"
		fail "$1 has no IPv4 address; is it running?"
	fi
	if [ "$(docker inspect -f '{{.HostConfig.NetworkMode}}' "$1")" = host ]; then
		fail "$1 uses the host's network; a network fault there would change the host"
	fi
	out=$(helper "$1" ip -o -4 addr show)
	found=$(for a in $addresses; do
		printf '%s\n' "$out" | awk -v a="$a" '$3 == "inet" { split($4, p, "/"); if (p[1] == a) { sub(/@.*/, "", $2); print $2 } }'
	done)
	[ -n "$found" ] || fail "no interface of $1 holds $addresses"
	echo "$found"
}

# table TABLE DEVICE... prints the nft statements that every table
# starts with: for each interface, a chain on its egress hook, where a
# packet is lost before it reaches the bridge, and one on its ingress
# hook, where it is lost after the bridge (and so after the capture).
table() {
	t=$1
	shift
	printf 'add table netdev %s' "$t"
	for d in "$@"; do
		printf '; add chain netdev %s egress_%s { type filter hook egress device "%s" priority filter; policy accept; }' "$t" "$d" "$d"
		printf '; add chain netdev %s ingress_%s { type filter hook ingress device "%s" priority filter; policy accept; }' "$t" "$d" "$d"
		printf '; add rule netdev %s egress_%s udp dport %s accept' "$t" "$d" "$hep_port"
	done
}

# drop TABLE EGRESS INGRESS DEVICE... prints the rules that drop what
# the two matches match on each interface.
drop() {
	t=$1 egress=$2 ingress=$3
	shift 3
	for d in "$@"; do
		printf '; add rule netdev %s egress_%s %s counter drop' "$t" "$d" "$egress"
		printf '; add rule netdev %s ingress_%s %s counter drop' "$t" "$d" "$ingress"
	done
}

# peer_addresses PEER prints the addresses of a peer of a partition.
peer_addresses() {
	case $1 in
	*[!0-9.]*) ;;
	*)
		echo "$1"
		return
		;;
	esac
	service=${1%%:*}
	number=
	case $1 in *:*) number=${1#*:} ;; esac
	case $service in
	*[*?]*) matched=$(services "$service") ;;
	*) matched=$service ;;
	esac
	found=
	for s in $matched; do
		for name in $(containers "$s" "$number"); do
			for a in $(addresses_of "$name"); do
				echo "$a"
				found=yes
			done
		done
	done
	[ -n "$found" ] || fail "peer $1 has no running container with an IPv4 address in the project $project"
}

# clear_container NAME removes every table of a fault, whether this
# script or the API made it, and the netem of every interface; tc fails
# harmlessly where there is no netem.
clear_container() {
	helper "$1" sh -c 'for t in $(nft list tables netdev | grep -o "ims_fault_[A-Za-z0-9_]*"); do nft delete table netdev $t && echo "deleted $t"; done; for d in $(ip -o link show | awk -F": " "{ sub(/@.*/, \"\", \$2); print \$2 }"); do tc qdisc del dev $d root 2>/dev/null && echo "deleted netem on $d"; done; true'
}

[ $# -ge 1 ] || usage
cmd=$1
shift
network=
if [ "${1:-}" = --network ]; then
	[ $# -ge 2 ] || usage
	case $cmd in loss | delay | partition) ;; *) usage ;; esac
	network=$2
	shift 2
fi
project=$(project_name)
[ -n "$project" ] || fail "cannot tell the Compose project; set COMPOSE_PROJECT_NAME"

case $cmd in
stop | kill | pause | unpause | start)
	[ $# -eq 1 ] || usage
	name=$(container_of "$1")
	run docker "$cmd" "$name"
	;;
crash)
	[ $# -eq 1 ] || usage
	# PID 1 of a namespace ignores SIGKILL sent from inside it, but the
	# Go runtime handles SIGQUIT: it prints every goroutine and exits
	# with 2. Docker sees a crash, not a stop, and the restart policy of
	# compose.yaml starts the node again. A docker kill would count as
	# a stop and leave it down.
	name=$(container_of "$1")
	run docker exec "$name" kill -QUIT 1
	;;
recreate)
	[ $# -eq 1 ] || usage
	case $1 in *:*) fail "recreate takes a service; Compose recreates its containers" ;; esac
	container_of "$1" >/dev/null
	# As when an orchestrator replaces a node that died: no goodbye
	# (DPR), a new container and a new network namespace, the same name
	# and address. The volumes of the service stay, so a store with
	# persistence reads its data again.
	run docker compose kill "$1"
	run docker compose rm -f "$1"
	run docker compose up -d --no-deps "$1"
	;;
loss)
	[ $# -eq 2 ] || usage
	name=$(container_of "$1")
	t=ims_fault_loss
	# numgen gives each packet a number from 0 to 99, and nft refuses to
	# compare it with 100, so a loss of every packet drops every IP
	# packet instead.
	case $2 in
	100) match="meta protocol ip" ;;
	[1-9] | [1-9][0-9]) match="meta protocol ip numgen random mod 100 < $2" ;;
	*) fail "the loss is a percentage from 1 to 100, not $2" ;;
	esac
	devs=$(devices "$name" "$network")
	# shellcheck disable=SC2086 # one word per interface
	helper "$name" nft "$(table $t $devs)$(drop $t "$match" "$match" $devs)"
	;;
delay)
	[ $# -eq 2 ] || [ $# -eq 3 ] || usage
	name=$(container_of "$1")
	for d in $(devices "$name" "$network"); do
		helper "$name" tc qdisc add dev "$d" root netem delay "${2}ms" ${3:+"${3}ms"}
	done
	;;
partition)
	[ $# -ge 1 ] || usage
	name=$(container_of "$1")
	shift
	t=ims_fault_partition
	if [ $# -eq 0 ]; then
		egress="meta protocol ip" ingress="meta protocol ip"
	else
		addresses=
		for peer in "$@"; do
			addresses="$addresses $(peer_addresses "$peer")"
		done
		# shellcheck disable=SC2086 # one word per address
		set=$(printf '%s\n' $addresses | sort -u -t . -k1,1n -k2,2n -k3,3n -k4,4n | paste -s -d , - | sed 's/,/, /g')
		egress="ip daddr { $set }" ingress="ip saddr { $set }"
	fi
	devs=$(devices "$name" "$network")
	# shellcheck disable=SC2086 # one word per interface
	helper "$name" nft "$(table $t $devs)$(drop $t "$egress" "$ingress" $devs)"
	;;
clear)
	[ $# -eq 1 ] || usage
	name=$(container_of "$1")
	clear_container "$name"
	;;
show)
	[ $# -eq 1 ] || usage
	name=$(container_of "$1")
	helper "$name" sh -c 'nft list ruleset netdev; tc qdisc show'
	;;
heal)
	[ $# -eq 0 ] || usage
	filter="--filter label=com.docker.compose.project=$project --filter label=com.docker.compose.service"
	# shellcheck disable=SC2086 # the filter is two options
	for name in $(docker ps -q $filter --filter status=paused); do
		run docker unpause "$(docker inspect -f '{{slice .Name 1}}' "$name")"
	done
	# shellcheck disable=SC2086
	for name in $(docker ps -q $filter --filter status=exited --filter label=com.docker.compose.oneoff=False); do
		run docker start "$(docker inspect -f '{{slice .Name 1}}' "$name")"
	done
	# shellcheck disable=SC2086
	for id in $(docker ps -q $filter --filter status=running); do
		name=$(docker inspect -f '{{slice .Name 1}}' "$id")
		[ "$(docker inspect -f '{{.HostConfig.NetworkMode}}' "$id")" = host ] && continue
		echo "$name:"
		clear_container "$name"
	done
	;;
*) usage ;;
esac
