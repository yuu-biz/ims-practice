# Shared by latency.sh and recovery.sh, which measure what keeping the
# state of the roles outside their processes costs and what it saves.
# Source it from bash, at the root of the repository.
#
# The UEs are the two that "ue serve" runs, phone1 and phone2 of
# compose.yaml (profile phones), driven through their control API
# (internal/ue/phone_http.go) from inside their containers, so that
# nothing depends on reaching the Compose network from the host, which
# Docker Desktop does not allow. The times come from the capture of the
# bridge, which every message between a UE and the network crosses.

export MSYS_NO_PATHCONV=1
: "${COMPOSE_FILE:=compose.yaml:deploy/chaos/chaos.yaml}"
export COMPOSE_FILE COMPOSE_PATH_SEPARATOR=:

UE_A=${UE_A:-phone1}
UE_B=${UE_B:-phone2}
# The number of phone2's subscriber (deploy/compose/hss.yaml), and phone1's.
NUMBER_A=${NUMBER_A:-tel:+12025550101}
NUMBER_B=${NUMBER_B:-tel:+12025550102}
# tshark from the host, or from the capture image when the host has none.
CAPTURE_IMAGE=${CAPTURE_IMAGE:-ims-practice/capture:dev}

c() { docker compose "$@"; }

# now prints the time as seconds since 1970 with milliseconds.
now() {
	local t
	t=$(date +%s.%N)
	case $t in *N) date +%s ;; *) echo "${t%??????}" ;; esac
}

# ms_since T prints the milliseconds from T to now.
ms_since() { awk -v a="$1" -v b="$(now)" 'BEGIN { printf "%d", (b - a) * 1000 }'; }

# ue_api SERVICE PATH [BODY] calls the control API of a UE: a GET, or a
# POST with BODY. It prints the answer, and fails on an HTTP error.
ue_api() {
	local service=$1 path=$2
	if [ $# -ge 3 ]; then
		c exec -T "$service" wget -q -T 60 -O - --header 'Content-Type: application/json' --post-data "$3" "http://127.0.0.1:8090$path" 2>&1
	else
		c exec -T "$service" wget -q -T 10 -O - "http://127.0.0.1:8090$path" 2>&1
	fi
}

# call_state SERVICE prints the state of the UE's call: none, calling,
# ringing or active.
call_state() {
	local s
	s=$(ue_api "$1" /status) || {
		echo unreachable
		return
	}
	case $s in
	*'"call":null'*) echo none ;;
	*) sed -n 's/.*"state":"\([a-z]*\)".*/\1/p' <<<"$s" ;;
	esac
}

registered() { [[ $(ue_api "$1" /status) == *'"registered":true'* ]]; }

# wait_for SECONDS COMMAND... runs the command every 0.1 s until it
# succeeds, and fails after SECONDS.
wait_for() {
	local deadline=$(($(date +%s) + $1))
	shift
	until "$@"; do
		[ "$(date +%s)" -lt "$deadline" ] || return 1
		sleep 0.1
	done
}

state_is() { [ "$(call_state "$1")" = "$2" ]; }

# start_ues starts phone1 and phone2, ends any call they have, and registers
# both again from the start, so that each measurement begins with the
# network knowing both: a node that a measurement before recreated may
# have forgotten them, while each UE still believes it is registered.
start_ues() {
	c --profile phones up -d "$UE_A" "$UE_B" >/dev/null 2>&1
	for u in "$UE_A" "$UE_B"; do
		wait_for 60 registered "$u" || {
			echo "measure: $u did not register" >&2
			return 1
		}
		state_is "$u" none || ue_api "$u" /hangup '{}' >/dev/null || true
		wait_for 10 state_is "$u" none || true
		ue_api "$u" /deregister '{}' >/dev/null || true
		ue_api "$u" /register '{}' >/dev/null || {
			echo "measure: $u did not register again" >&2
			return 1
		}
	done
}

# capture_file prints the newest capture of network A, or CAPTURE_FILE.
capture_file() {
	if [ -n "${CAPTURE_FILE:-}" ]; then
		echo "$CAPTURE_FILE"
	else
		ls -t captures/ims-*.pcap | head -n 1
	fi
}

tshark_() {
	if command -v tshark >/dev/null; then
		tshark "$@"
	else
		docker run --rm -v "$PWD/captures:/captures" --entrypoint tshark "$CAPTURE_IMAGE" "$@"
	fi
}

# sip_events PCAP FROM TO prints the SIP messages between FROM and TO
# (seconds since 1970), one line each: time, source, destination,
# method, status, method of the CSeq. The copies to the Lab (HEP,
# UDP 9060) are left out. tshark complains about the last packet of a
# capture that is still being written; that is harmless.
sip_events() {
	tshark_ -r "$1" -Y "sip && !(udp.port == 9060) && frame.time_epoch >= $2 && frame.time_epoch <= $3" \
		-T fields -E separator=/t -e frame.time_epoch -e ip.src -e ip.dst -e sip.Method -e sip.Status-Code -e sip.CSeq.method 2>/dev/null || true
}

# rtp_gap PCAP FROM TO DESTINATION prints the longest time in
# milliseconds between two RTP packets to DESTINATION, and how many came.
rtp_gap() {
	tshark_ -r "$1" -o rtp.heuristic_rtp:TRUE -Y "rtp && ip.dst == $4 && frame.time_epoch >= $2 && frame.time_epoch <= $3" \
		-T fields -e frame.time_epoch 2>/dev/null |
		awk 'NR > 1 && ($1 - last) * 1000 > gap { gap = ($1 - last) * 1000 } { last = $1; n++ } END { printf "%d %d\n", gap, n }' || true
}

address_of() { docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' "$(c ps -q "$1")" | awk '{ print $1 }'; }

# summary prints n, p50, p90 and the maximum of the numbers on stdin.
summary() {
	sort -n | awk '{ v[NR] = $1 } END {
		if (NR == 0) { print "0 - - -"; exit }
		p50 = v[int((NR - 1) * 0.5) + 1]; p90 = v[int((NR - 1) * 0.9) + 1]
		printf "%d %d %d %d\n", NR, p50, p90, v[NR] }'
}
