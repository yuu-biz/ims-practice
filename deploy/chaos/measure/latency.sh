#!/usr/bin/env bash
# The time that the network takes to handle a registration and a call.
# Run it
# once on a stack whose roles keep their state in memory and once on the
# same stack with the store outside; the difference is what the store
# adds.
#
#   deploy/chaos/measure/latency.sh [RUNS]
#
# COMPOSE_FILE picks the stack, as for docker compose; LABEL names the
# run in the result (default: memory). The stack must be up, with its
# capture. phone1 registers again RUNS times (deregister, then the initial
# registration with its 401) and calls phone2 RUNS times.
#
# Each time is measured on the capture between messages of the UEs, so
# that the UEs' own work (the ring, the answer) is not in it:
#
#   register     the REGISTER that the 401 answers, to the 200
#   register-2   the REGISTER with credentials, to the 200
#   invite       the INVITE from phone1, to the INVITE that reaches phone2
#   answer       the 200 from phone2, to the 200 that reaches phone1
#   bye          the BYE from phone1, to its 200
set -euo pipefail
cd "$(dirname "$0")/../../.."
. deploy/chaos/measure/lib.sh

runs=${1:-20}
label=${LABEL:-memory}
start_ues
a=$(address_of "$UE_A")
b=$(address_of "$UE_B")

from=$(now)
for i in $(seq 1 "$runs"); do
	ue_api "$UE_A" /deregister '{}' >/dev/null
	ue_api "$UE_A" /register '{}' >/dev/null
	ue_api "$UE_A" /call "{\"target\":\"$NUMBER_B\"}" >/dev/null
	wait_for 30 state_is "$UE_A" active || {
		echo "measure: call $i was not answered" >&2
		exit 1
	}
	ue_api "$UE_A" /hangup '{}' >/dev/null
	wait_for 30 state_is "$UE_B" none
	wait_for 30 state_is "$UE_A" none
done
to=$(now)
# tcpdump writes each packet at once (-U); a moment more is for the
# last ones.
sleep 1

pcap=$(capture_file)
mkdir -p captures/measure
raw="captures/measure/latency-$label-$(date -u +%Y%m%dT%H%M%SZ).tsv"
sip_events "$pcap" "$from" "$to" >"$raw"

# The pairs of messages, in the order of the capture. An event opens a
# measurement and a later one closes it; times are in milliseconds.
awk -F '\t' -v a="$a" -v b="$b" '
	function begin(name, t) { if (!(name in started)) started[name] = t }
	function finish(name, t) { if (name in started) { print name, int((t - started[name]) * 1000); delete started[name] } }
	$2 == a && $4 == "REGISTER" { if (challenged) { begin("register-2", $1); challenged = 0 } else begin("register", $1); next }
	$3 == a && $5 == "401" && $6 == "REGISTER" { if ("register" in started) { reg = started["register"]; challenged = 1 } next }
	$3 == a && $5 == "200" && $6 == "REGISTER" {
		if ("register-2" in started) { finish("register-2", $1); started["register"] = reg; finish("register", $1) }
		else delete started["register"]
		next }
	$2 == a && $4 == "INVITE" { begin("invite", $1); next }
	$3 == b && $4 == "INVITE" { finish("invite", $1); next }
	$2 == b && $5 == "200" && $6 == "INVITE" { begin("answer", $1); next }
	$3 == a && $5 == "200" && $6 == "INVITE" { finish("answer", $1); next }
	$2 == a && $4 == "BYE" { begin("bye", $1); next }
	$3 == a && $5 == "200" && $6 == "BYE" { finish("bye", $1); next }
' "$raw" >"$raw.pairs"

echo "| stack | measure | n | p50 ms | p90 ms | max ms |"
echo "| --- | --- | --- | --- | --- | --- |"
for m in register register-2 invite answer bye; do
	read -r n p50 p90 max < <(awk -v m="$m" '$1 == m { print $2 }' "$raw.pairs" | summary)
	echo "| $label | $m | $n | $p50 | $p90 | $max |"
done
echo "raw: $raw (from the capture $pcap)" >&2
