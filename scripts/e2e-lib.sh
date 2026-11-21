# Helpers for the end-to-end scripts, which source this file. It is not
# a scenario of its own.
#
# A check is only worth something if the network it looks at is the one
# that was started. Every "run" and "up" after the start takes
# --no-deps: without it, Compose recreates each node that a UE depends
# on whose container was made from another digest of ims-practice/ims:dev
# than the tag has now. After a fresh build that is most of them, as
# each service builds the tag on its own. A recreated node has lost its
# Diameter connections and whatever it kept in memory, so the checks
# after it would look at another network. remember_nodes and same_nodes
# prove that no node was recreated or restarted.

# nodes prints the containers of the given services, one line each: the
# service, the container ID and the time the container last started. A
# node that exits is started again in the same container (restart:
# unless-stopped), so the ID alone does not show it; the start time does.
nodes() {
	local ids
	ids="$(docker compose ps -q "$@")"
	[ -n "$ids" ] || return 0
	# shellcheck disable=SC2086 # one argument per container ID
	docker inspect -f '{{index .Config.Labels "com.docker.compose.service"}} {{.Id}} {{.State.StartedAt}}' $ids | sort
}

# remember_nodes keeps the nodes that run now. Call it once the network
# is up and before the first UE starts.
remember_nodes() {
	# shellcheck disable=SC2207 # service names have no spaces
	node_services=($(docker compose ps --services))
	nodes_at_start="$(nodes "${node_services[@]}")"
}

# renew_node accepts that the script recreated one service on purpose,
# for example to start it with another configuration. It checks that the
# service runs in a new container and that no other node changed, and
# then remembers the nodes as they are now.
renew_node() {
	local service="$1" before="$nodes_at_start" now
	now="$(nodes "${node_services[@]}")"
	if [ "$(grep -v "^$service " <<<"$before" || true)" = "$(grep -v "^$service " <<<"$now" || true)" ] &&
		[ -n "$(grep "^$service " <<<"$now" || true)" ] &&
		[ "$(grep "^$service " <<<"$before" || true)" != "$(grep "^$service " <<<"$now" || true)" ]; then
		echo "ok   $service runs in a new container and no other node changed"
	else
		echo "FAIL $service is not in a new container, or other nodes changed:"
		diff <(echo "$before") <(echo "$now") || true
		fail=1
	fi
	nodes_at_start="$now"
}

# same_nodes checks that every node remembered still runs in the same
# container, never started again. After renew_node, that is the
# container the renewed service got then. The services given as arguments are
# the ones the script stopped on purpose: they must be gone.
same_nodes() {
	local want="$nodes_at_start" service
	for service in "$@"; do
		want="$(grep -v "^$service " <<<"$want" || true)"
	done
	local now
	now="$(nodes "${node_services[@]}")"
	if [ "$now" = "$want" ]; then
		echo "ok   every node kept its container, apart from those renewed on purpose"
	else
		echo "FAIL nodes were recreated, restarted or stopped during the test:"
		diff <(echo "$want") <(echo "$now") || true
		fail=1
	fi
}

# first_line prints the first line of its input. It reads the input to
# the end: "head -n 1" stops reading after one line, the program that
# writes then dies of SIGPIPE, and pipefail turns that into a failure
# of the whole script.
first_line() {
	local all
	all="$(cat)"
	printf '%s\n' "${all%%$'\n'*}"
}

# The checks of a capture read the pcap file named by $pcap in the
# capture container. A script sets pcap once the scenario has run,
# starts last and fail at 0, and may set tshark_options: for example
# (-o rtp.heuristic_rtp:TRUE) finds RTP that flows before tshark has
# seen the SDP that names its port.
tshark_options=()

# tshark_fields prints the fields of the frames that pass the filter, one
# frame per line, the fields separated by "|". Without fields it prints
# the frame number.
tshark_fields() {
	local filter="$1" out args=() f
	shift
	for f in "${@:-frame.number}"; do args+=(-e "$f"); done
	if ! out="$(docker compose exec -T capture tshark -r "$pcap" ${tshark_options[@]+"${tshark_options[@]}"} \
		-Y "$filter" -T fields -E separator='|' "${args[@]}" 2>&1)"; then
		echo "tshark failed on filter: $filter" >&2
		echo "$out" >&2
		exit 2
	fi
	printf '%s\n' "$out" | tr -d '\r'
}

# expect finds the first frame after the previous match that passes the
# filter, so that a run of expects checks the order of a flow. It leaves
# the frame in $last.
expect() {
	local label="$1" filter="$2" frame
	frame="$(tshark_fields "frame.number > $last && ($filter)" | first_line)"
	if [ -n "$frame" ]; then
		echo "ok   #$frame $label"
		last="$frame"
	else
		echo "FAIL $label (after frame $last)"
		fail=1
	fi
}

# field prints a field of the frame that the last expect found.
field() { tshark_fields "frame.number == $last" "$1"; }

# Some steps run side by side, such as the Rx answer and the bearer
# that the PCRF sets up for it. mark remembers where the steps fork, and
# back returns there, so that each branch is checked in its own order.
mark() { fork="$last"; }
back() { last="$fork"; }

# none checks that no frame passes the filter.
none() {
	local label="$1" filter="$2" n
	n="$(tshark_fields "$filter" | grep -c . || true)"
	if [ "$n" -eq 0 ]; then
		echo "ok   $label: none"
	else
		echo "FAIL $label: $n, want none"
		fail=1
	fi
}

# answer_to is the filter of the Diameter answer to the request that the
# last expect found: the same command code and Session-Id, with the R
# bit clear. A TCP segment may carry more than one message; the first
# one counts.
answer_to() {
	local code sid
	code="$(field diameter.cmd.code | cut -d, -f1)"
	sid="$(field diameter.Session-Id | cut -d, -f1)"
	if [ -z "$code" ]; then
		# The request was not found, so neither is its answer.
		echo "frame.number == 0"
		return
	fi
	echo "diameter.flags.request == 0 && diameter.cmd.code == $code && diameter.Session-Id == \"$sid\""
}
