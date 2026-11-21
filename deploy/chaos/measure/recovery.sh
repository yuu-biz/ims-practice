#!/usr/bin/env bash
# What a call and a registration keep when a node is recreated, and how
# long the network takes to serve them again.
#
#   deploy/chaos/measure/recovery.sh SERVICE
#
# COMPOSE_FILE picks the stack, as for docker compose; LABEL names the
# run in the result (default: memory). The stack must be up, with its
# capture. The steps:
#
#  1. ue-a calls ue-b, and the call is answered.
#  2. deploy/chaos/fault.sh recreate SERVICE: the container is killed,
#     removed and created again, with the same name and address.
#  3. ue-a puts the call on hold (a re-INVITE) until that works, for up
#     to HOLD_FOR seconds (default 40).
#  4. ue-a hangs up (BYE); the result says whether ue-b heard of it.
#  5. ue-b calls ue-a, which registered before the fault, every 2 s
#     until a call is answered, for up to CALL_FOR seconds (default
#     60). No UE registers again on its own in that time, unless its
#     registration runs out.
#
# The result is one row of a Markdown table, and the capture of the
# whole run is kept as a list of its SIP messages in captures/measure/.
set -euo pipefail
cd "$(dirname "$0")/../../.."
. deploy/chaos/measure/lib.sh

[ $# -eq 1 ] || {
	echo "usage: deploy/chaos/measure/recovery.sh SERVICE" >&2
	exit 2
}
service=$1
label=${LABEL:-memory}
hold_for=${HOLD_FOR:-40}
call_for=${CALL_FOR:-60}
# The RTP of the call runs for the tone of "ue serve" (3 s by default)
# from the answer; the fault comes SETTLE seconds after the answer.
settle=${SETTLE:-0.5}
start_ues
a=$(address_of "$UE_A")
b=$(address_of "$UE_B")

ue_api "$UE_A" /call "{\"target\":\"$NUMBER_B\"}" >/dev/null
wait_for 30 state_is "$UE_A" active || {
	echo "measure: the call was not answered" >&2
	exit 1
}
sleep "$settle"

from=$(now)
deploy/chaos/fault.sh recreate "$service" >/dev/null 2>&1
recreated=$(ms_since "$from")

# In the dialog: a re-INVITE that the network carries again.
hold=failed
attempts=0
deadline=$(($(date +%s) + hold_for))
while [ "$(date +%s)" -lt "$deadline" ]; do
	attempts=$((attempts + 1))
	if ue_api "$UE_A" /hold '{}' >/dev/null; then
		hold="$(ms_since "$from") ms ($attempts)"
		break
	fi
	state_is "$UE_A" active || {
		hold="call gone ($attempts)"
		break
	}
	sleep 1
done
call_ended=$(now)

# The BYE: ue-b hears it if the network still knows the dialog.
bye=lost
if state_is "$UE_A" active || state_is "$UE_A" none; then
	ue_api "$UE_A" /hangup '{}' >/dev/null 2>&1 || true
fi
if wait_for 5 state_is "$UE_B" none; then
	bye=reached
fi
# A callee that never heard the BYE is freed here, for the next step.
state_is "$UE_B" none || ue_api "$UE_B" /hangup '{}' >/dev/null 2>&1 || true
wait_for 10 state_is "$UE_A" none || true

# Outside the dialog: a new call to the UE that registered before.
incoming=failed
attempts=0
deadline=$(($(date +%s) + call_for))
while [ "$(date +%s)" -lt "$deadline" ]; do
	attempts=$((attempts + 1))
	if ue_api "$UE_B" /call "{\"target\":\"$NUMBER_A\"}" >/dev/null 2>&1 && wait_for 15 state_is "$UE_A" active; then
		incoming="$(ms_since "$from") ms ($attempts)"
		ue_api "$UE_B" /hangup '{}' >/dev/null 2>&1 || true
		break
	fi
	ue_api "$UE_B" /hangup '{}' >/dev/null 2>&1 || true
	wait_for 10 state_is "$UE_B" none || true
	sleep 2
done
wait_for 10 state_is "$UE_A" none || true
to=$(now)
sleep 1

pcap=$(capture_file)
mkdir -p captures/measure
raw="captures/measure/recovery-$label-$service-$(date -u +%Y%m%dT%H%M%SZ).tsv"
sip_events "$pcap" "$from" "$to" >"$raw"
registers=$(awk -F '\t' -v a="$a" -v b="$b" '($2 == a || $2 == b) && $4 == "REGISTER"' "$raw" | wc -l | tr -d ' ')
errors=$(awk -F '\t' '$5 >= 400 { print $5 "/" $6 }' "$raw" | sort | uniq -c | awk '{ printf "%s%s x%s", sep, $2, $1; sep = ", " }')
read -r gap packets < <(rtp_gap "$pcap" "$from" "$call_ended" "$b")

echo "| stack | node | recreated in | re-INVITE works after (tries) | BYE | new call to the UE works after (tries) | REGISTERs from the UEs | error responses | longest RTP gap to ue-b (packets) |"
echo "| --- | --- | --- | --- | --- | --- | --- | --- | --- |"
echo "| $label | $service | $recreated ms | $hold | $bye | $incoming | $registers | ${errors:-none} | $gap ms ($packets) |"
echo "raw: $raw (from the capture $pcap)" >&2
