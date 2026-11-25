#!/usr/bin/env bash
# End-to-end check: the maintenance API takes the HSS out of service,
# and back, on the deployment of scripts/e2e-observability.sh. A shut
# down of the HSS sends every Diameter peer a DPR with Disconnect-Cause
# REBOOTING and stops the node listening on 3868, so the I-CSCF reports
# itself DISABLED and no UE can register, and Prometheus reads the
# connection of the I-CSCF as down. Once their next attempt to connect
# is refused, the I-CSCF, the S-CSCF, the IP-SM-GW, the MMTel AS and the
# Service Centre each raise the alarm "Diameter peer down" about the HSS
# in the alarm list of their maintenance API, which the OAM gathers;
# after the unlock they open their connections again by themselves and
# clear the alarm, Prometheus reads the connection of the I-CSCF as up
# again, and a UE registers.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
. scripts/e2e-observability-lib.sh

# hss_alarm prints the alarm that the node of the service $1 holds about
# its Diameter peer the HSS, from the alarm list of its maintenance API,
# as "<perceived_severity> <alarm_type> <probable_cause>", or "none".
hss_alarm() {
	docker compose exec -T "$1" wget -qO- http://127.0.0.1:8085/maintenance/v1/alarms 2>/dev/null | tr -d '\r' |
		"$python" -c '
import json, sys
found = [r for r in json.load(sys.stdin)["alarm_records"]
         if r.get("peer") == "hss.ims.mnc001.mcc001.3gppnetwork.org" and r.get("specific_problem") == "Diameter peer down"]
print(" ".join((found[0]["perceived_severity"], found[0]["alarm_type"], found[0]["probable_cause"])) if found else "none")' 2>/dev/null || true
}

# until_hss_alarm waits up to $3 seconds until hss_alarm of the service
# $1 prints $2.
until_hss_alarm() {
	local i
	for i in $(seq 1 "$3"); do
		[ "$(hss_alarm "$1")" = "$2" ] && return 0
		sleep 1
	done
	return 1
}

start_network
remember_nodes
fail=0

# The scene counts what the capture holds before a step and waits for
# what the step adds to it: the file that the capture service writes
# now, the newest one. registered is the 200 OK of the S-CSCF to a
# REGISTER.
pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
registered='sip.CSeq.method == "REGISTER" && sip.Status-Code == 200 && ip.src == 172.28.0.30'

echo "drain of the HSS"
# The HSS has no SIP to answer with: it ends each Diameter connection
# with a DPR whose Disconnect-Cause is REBOOTING and stops listening on
# 3868, so the CSCFs have no HSS until the unlock. Count what is there
# first: each node opens its connection again only after Tc, 30 s.
hss_address=172.28.0.10
hss_peers="icscf scscf ipsmgw mmtel sc"
dprs="$(count "diameter.cmd.code == 282 && diameter.flags.request == 1 && ip.src == $hss_address")"
# The kernel of the HSS answers each attempt to connect with a reset
# while nothing listens on 3868. A reset that answers a SYN carries ACK
# and the sequence number 0, as the SYN acknowledges nothing (RFC 9293
# 3.10.7.1); a reset on a connection that is closing has the sequence
# number of that connection, so it does not count here.
refusals="tcp.flags.reset == 1 && tcp.flags.ack == 1 && tcp.seq_raw == 0 && ip.src == $hss_address && tcp.srcport == 3868 && (ip.dst == 172.28.0.20 || ip.dst == 172.28.0.30)"
refused="$(count "$refusals")"
# A node that started before the HSS listened has a cleared alarm from
# its first attempt; none has one that is not cleared.
for service in $hss_peers; do
	alarm="$(hss_alarm "$service")"
	if [ "$alarm" = none ] || [ "${alarm%% *}" = CLEARED ]; then
		echo "ok   $service has no alarm about the HSS that is not cleared before the drain"
	else
		echo "FAIL $service has an alarm about the HSS before the drain: $alarm"
		fail=1
	fi
done
icscf_opens="$(diameter_opens icscf)"
scscf_opens="$(diameter_opens scscf)"
ipsmgw_opens="$(diameter_opens ipsmgw)"
mmtel_opens="$(diameter_opens mmtel)"
sc_opens="$(diameter_opens sc)"
answer="$(administrative_state hss SHUTTINGDOWN || true)"
state="$(node_state hss || true)"
if grep -Eq '"administrative_state":"(SHUTTINGDOWN|LOCKED)"' <<<"$state"; then
	echo "ok   the maintenance API shut the HSS down"
else
	echo "FAIL the shut down of the HSS: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# With no instance of the HSS left, the I-CSCF cannot work, which it
# says in its operational state (TS 28.622 5.3.3) without the operator
# having taken it out of service.
if until_state icscf '"operational_state":"DISABLED"'; then
	echo "ok   the I-CSCF reports itself DISABLED without the HSS"
else
	echo "FAIL the I-CSCF stayed ENABLED without the HSS: $(node_state icscf || true)"
	fail=1
fi
if docker compose run --no-deps --rm ue register -deregister-after 2s 2>/dev/null; then
	echo "FAIL UE registered with a shut down HSS"
	fail=1
else
	echo "ok   UE could not register while the HSS is shut down"
fi
# The gauge is read from the state of the connection at each scrape, so
# the closed connection shows without anything writing to it.
peer_up='ims_diameter_peer_up{node="icscf"}'
if value="$(until_equal prometheus_values "$peer_up" 0)"; then
	echo "ok   $peer_up is $value while the HSS is out of service"
else
	echo "FAIL $peer_up did not fall to 0 while the HSS is out of service: $value"
	fail=1
fi
# The DPR alone raises no alarm: REBOOTING lets the node connect again
# after Tc. The alarm comes once that attempt is refused, so the wait is
# Tc and then some.
raised="MAJOR COMMUNICATIONS_ALARM COMMUNICATIONS_SUBSYSTEM_FAILURE"
for service in $hss_peers; do
	if until_hss_alarm "$service" "$raised" 90; then
		echo "ok   $service raised the alarm Diameter peer down about the HSS: $raised"
	else
		echo "FAIL $service raised no alarm about the HSS within 90 s: $(hss_alarm "$service")"
		docker compose exec -T "$service" wget -qO- http://127.0.0.1:8085/maintenance/v1/alarms || true
		fail=1
	fi
done
await "the refused attempts of the CSCFs to connect to the HSS" "$refusals" $((refused + 2))
# The OAM gathers the alarm lists of the nodes of its inventory, so it
# lists the alarm of each of the five.
oam_alarms="$(oam_request GET '/oam/v1/alarms?cleared=false' || true)"
if "$python" -c '
import json, sys
_, _, body = sys.argv[1].partition("\n\n")
nodes = {a["node"] for a in json.loads(body)["alarms"] if a.get("specific_problem") == "Diameter peer down"}
sys.exit(0 if set(sys.argv[2].split()) <= nodes else 1)' "$oam_alarms" "$hss_peers" 2>/dev/null; then
	echo "ok   the OAM lists the alarms of $hss_peers about the HSS"
else
	echo "FAIL the OAM does not list the alarms of $hss_peers about the HSS: $oam_alarms"
	fail=1
fi
answer="$(administrative_state hss UNLOCKED || true)"
state="$(node_state hss || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the HSS"
else
	echo "FAIL the unlock of the HSS: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# REBOOTING lets the peer come back (RFC 6733 5.4.3), which it does at
# its next attempt after Tc.
if wait_for_opens icscf $((icscf_opens + 1)) && wait_for_opens scscf $((scscf_opens + 1)) &&
	wait_for_opens ipsmgw $((ipsmgw_opens + 1)) && wait_for_opens mmtel $((mmtel_opens + 1)) &&
	wait_for_opens sc $((sc_opens + 1)); then
	echo "ok   every peer of the HSS opened its Diameter connection again"
else
	echo "FAIL a peer of the HSS did not open its Diameter connection again after the unlock"
	echo "     opens so far: icscf $(diameter_opens icscf), scscf $(diameter_opens scscf), ipsmgw $(diameter_opens ipsmgw), mmtel $(diameter_opens mmtel), sc $(diameter_opens sc)"
	fail=1
fi
# The CEA of the new connection clears the alarm. The record stays in
# the list, CLEARED, until an operator acknowledges it.
cleared="CLEARED COMMUNICATIONS_ALARM COMMUNICATIONS_SUBSYSTEM_FAILURE"
for service in $hss_peers; do
	if until_hss_alarm "$service" "$cleared" 30; then
		echo "ok   $service cleared its alarm about the HSS"
	else
		echo "FAIL $service did not clear its alarm about the HSS: $(hss_alarm "$service")"
		fail=1
	fi
done
if value="$(until_positive prometheus_values "$peer_up")"; then
	echo "ok   $peer_up is $value again after the unlock of the HSS"
else
	echo "FAIL $peer_up did not come back to 1 after the unlock of the HSS: $value"
	fail=1
fi
registrations="$(count "$registered")"
if docker compose run --no-deps --rm ue register -deregister-after 2s; then
	echo "ok   UE registered after the unlock of the HSS"
else
	echo "FAIL UE registration after the unlock of the HSS"
	fail=1
fi
await "the 200 OK to the registration after the unlock of the HSS" "$registered" $((registrations + 1))
# One DPR per peer: the two Cx connections, the two Sh connections and
# the S6c connection of the SC.
await "the DPRs of the drained HSS" "diameter.cmd.code == 282 && diameter.flags.request == 1 && ip.src == $hss_address" $((dprs + 5))
same_nodes

finish
