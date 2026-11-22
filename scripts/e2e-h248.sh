#!/usr/bin/env bash
# End-to-end check: the P-CSCF controls the media relay over H.248
# (deploy/h248) instead of over the relay's HTTP control API.
#
# The scenario is the one of scripts/e2e-call.sh, which this script runs
# on the H.248 deployment: the same calls, the same checks of the
# capture, and the same comparison of the G.711 that UE 2 received with
# what UE 1 sent, which is what shows that the media really flowed
# through the terminations the H.248 commands reserved. Only the media
# control changes, so that script needs no change of its own.
#
# What this script adds is what is new: it reads the H.248 of the calls
# out of the same capture.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
export MSYS_NO_PATHCONV=1
# The P-CSCF and the relay take their files from deploy/h248; every
# other node is the one of compose.yaml. scripts/e2e-call.sh below reads
# the same files from here.
export COMPOSE_FILE=compose.yaml:deploy/h248/h248.yaml COMPOSE_PATH_SEPARATOR=:
# scripts/e2e-call.sh starts the network itself, and waits for the relay
# to register before the first call (wait_for_h248_relay in
# scripts/e2e-lib.sh). Starting it here first would not help: that
# script builds again, and Compose then recreates every node, so a
# registration seen here would be with a P-CSCF that is gone.

# The whole voice call scenario on this deployment. Its failure is this
# script's failure, but the H.248 of whatever did run is still worth
# reading, so the checks below run either way.
calls=0
scripts/e2e-call.sh || calls=$?
fail="$calls"

pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
echo "reading $pcap for the H.248 of the calls"

last=0
pcscf=172.28.0.40
agw=172.28.0.50
# Wireshark reads H.248 in the text encoding as "megaco" on port 2944,
# so every check below is on fields it decoded, not on the bytes.
iq="megaco && ip.addr == $pcscf && ip.addr == $agw"
request="$iq && megaco.transaction == \"Request\""
reply="$iq && megaco.transaction == \"Reply\""
subtract="$request && ip.src == $pcscf && megaco.command == \"Subtract\""

# The Subtract of the last call leaves after its BYE, so it is the last
# of the H.248 to reach the capture.
await "the Subtract that released the context of a call" "$subtract"

if [ -n "$(tshark_fields "$iq" | first_line)" ]; then
	echo "ok   Wireshark decoded the H.248 between the P-CSCF and the media relay"
else
	echo "FAIL no megaco frames between $pcscf and $agw in the capture"
	fail=1
fi

echo "the registration of the media relay (TS 29.334 5.17.3.5)"
# IMS-AGW Register: the relay sends ServiceChange on ROOT, and the
# P-CSCF accepts it.
expect "ServiceChange relay -> P-CSCF on ROOT" \
	"$request && ip.src == $agw && megaco.command == \"ServiceChange\" && megaco.termid == \"ROOT\""
expect "the P-CSCF replies to the ServiceChange" \
	"$reply && ip.src == $pcscf && megaco.command == \"ServiceChange\""

echo "the call related procedures of a call (TS 29.334 5.17.2)"
# Reserve AGW Connection Point: one transaction with the two Adds of
# the context, the access termination towards the UE and the core
# termination towards the rest of the network.
expect "Add P-CSCF -> relay for the access termination" \
	"$request && ip.src == $pcscf && megaco.command == \"Add\" && megaco.termid contains \"ip/1/access/\""
add="$last"
if [ -n "$(tshark_fields "frame.number == $add && megaco.termid contains \"ip/1/core/\"" | first_line)" ]; then
	echo "ok   #$add the same transaction adds the core termination"
else
	echo "FAIL the Add of frame $add does not also add the core termination"
	fail=1
fi
expect "the relay replies with the context it made" "$reply && ip.src == $agw && megaco.command == \"Add\""
# Configure AGW Connection Point: where each termination sends.
expect "Modify P-CSCF -> relay for a termination of the context" \
	"$request && ip.src == $pcscf && megaco.command == \"Modify\" && megaco.termid contains \"ip/1/\""
expect "the relay replies to the Modify" "$reply && ip.src == $agw && megaco.command == \"Modify\""
# Release AGW Termination: both terminations with a wildcard.
expect "Subtract P-CSCF -> relay at the end of the call" "$subtract"
expect "the relay replies with both terminations released" \
	"$reply && ip.src == $agw && megaco.command == \"Subtract\" && megaco.termid contains \"ip/1/core/\""

echo "nothing went wrong in the H.248"
# The roadmap's completion condition: Wireshark reads every H.248
# message of the capture to the end.
none "malformed H.248" 'megaco && _ws.malformed'
# A command the relay or the P-CSCF refused carries an Error descriptor
# (H.248.1 (05/2002) 13). None of the calls of the scenario is refused
# by the relay: the ones that fail are refused in SIP, after the context
# has been reserved.
none "H.248 error descriptors" "$iq && megaco.error"
# The relay takes its commands over H.248 only; this deployment gives
# it no HTTP control API to serve.
none "HTTP to the media relay" "http && ip.dst == $agw"

if [ "$calls" -ne 0 ]; then
	echo "FAIL the voice call scenario failed on this deployment (exit $calls)"
fi
exit "$fail"
