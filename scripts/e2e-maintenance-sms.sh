#!/usr/bin/env bash
# End-to-end check: the maintenance API takes the nodes that carry a
# short message out of service, and back, on the deployment of
# scripts/e2e-observability.sh. A shut down of the IP-SM-GW makes it
# answer 503 with Retry-After to the third-party REGISTER and to the
# MESSAGE of a short message. The S-CSCF leaves the registration
# standing, since the criterion of the third-party REGISTER says
# SESSION_CONTINUED, and ends the MESSAGE, since its criterion says
# SESSION_TERMINATED, so UE 1 sees the 503 turned into 500; after the
# unlock the short message reaches the SC again. A shut down of the
# Service Centre makes it end the SGd connection of the IP-SM-GW with a
# DPR whose Disconnect-Cause is REBOOTING and stop listening on 3868, so
# the IP-SM-GW, with no SC left to hand a short message to, answers UE 1
# with an RP-ERROR of RP-Cause 41; after the unlock the IP-SM-GW
# connects again and the SC takes the short message, and Prometheus
# holds the SRRs that the SC sent the HSS for it.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
. scripts/e2e-observability-lib.sh

start_network
remember_nodes
fail=0

# The scenes count what the capture holds before a step, and the checks
# of the flow read it at the end: the file that the capture service
# writes now, the newest one.
pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"

echo "drain of the IP-SM-GW"
# The IP-SM-GW takes its work from the S-CSCF by the filter criteria of
# the served user, so the drain shows in a short message that UE 1
# sends. The IP-SM-GW answers each MESSAGE it accepts with 202, so count
# those first.
gwAccepts='sip.CSeq.method == "MESSAGE" && sip.Status-Code == 202 && ip.src == 172.28.0.70'
gwAccepted="$(count "$gwAccepts")"
# No UE is registered and no short message has been sent, so the
# IP-SM-GW has nothing to finish: no delivery waits for a delivery
# report either. Wait until it says so, so that
# the shut down below is the shut down of a function with no user,
# which X.731 7.1.3.3 makes LOCKED at once.
if until_state ipsmgw '"users":0'; then
	echo "ok   the IP-SM-GW has nothing in its hands before the shut down"
else
	echo "FAIL the IP-SM-GW still counts users: $(node_state ipsmgw || true)"
	fail=1
fi
answer="$(administrative_state ipsmgw SHUTTINGDOWN || true)"
state="$(node_state ipsmgw || true)"
if grep -q '"administrative_state":"LOCKED"' <<<"$state"; then
	echo "ok   the maintenance API shut the IP-SM-GW down, which locked it at once"
else
	echo "FAIL the shut down of the IP-SM-GW: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# "ue sms" registers, sends the short message and waits for the submit
# report. The MESSAGE gets 500 here, since the criterion of the MESSAGE
# says SESSION_TERMINATED, so the command exits 1, hence "|| true"; the
# registration itself goes through, since the criterion of the
# third-party REGISTER says SESSION_CONTINUED.
out="$(docker compose run --no-deps --rm ue sms +12025550102 'Is anyone on duty?' 2>&1 || true)"
if grep -q '"msg":"registered"' <<<"$out" &&
	grep -q '"msg":"sms not sent".*MESSAGE answered 500' <<<"$out"; then
	echo "ok   UE 1 registered but could not send a short message while the IP-SM-GW is shut down"
else
	echo "FAIL the short message while the IP-SM-GW is shut down: $(tail -n 2 <<<"$out")"
	fail=1
fi
answer="$(administrative_state ipsmgw UNLOCKED || true)"
state="$(node_state ipsmgw || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the IP-SM-GW"
else
	echo "FAIL the unlock of the IP-SM-GW: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# UE 2 is not running, so the SC cannot deliver the short message; what
# this checks is that the IP-SM-GW accepts it again and the submit
# report comes back.
out="$(docker compose run --no-deps --rm ue sms +12025550102 'Back on duty?' 2>&1 || true)"
if grep -q '"msg":"submit report"' <<<"$out"; then
	echo "ok   UE 1 sent a short message after the unlock of the IP-SM-GW"
else
	echo "FAIL the short message after the unlock of the IP-SM-GW: $(tail -n 2 <<<"$out")"
	fail=1
fi

await "the 202 of the IP-SM-GW after its unlock" "$gwAccepts" $((gwAccepted + 1))

echo "drain of the Service Centre"
# The IP-SM-GW opened the SGd connection, and the OFR it sends names no
# Destination-Host, so the SC cannot turn a short message away in its
# answer. It ends the connection with a DPR whose Disconnect-Cause is
# REBOOTING and stops listening on 3868 instead, as the HSS does. Count
# what is there first: the IP-SM-GW opens its connection again only
# after Tc, 30 s.
scDPRs='diameter.cmd.code == 282 && diameter.flags.request == 1 && ip.src == 172.28.0.71 && diameter.Disconnect-Cause == 0'
scDPRsSent="$(count "$scDPRs")"
scOFAs='diameter.cmd.code == 8388645 && diameter.flags.request == 0 && ip.src == 172.28.0.71 && diameter.Result-Code == 2001'
sc_ipsmgw_opens="$(diameter_opens ipsmgw)"
# The short message after the unlock of the IP-SM-GW is for UE 2, which
# is not registered, so its first attempt ended at once and the SC holds
# it. Wait until the SC says that it has nothing in its hands, so that
# the shut down below locks it at once (X.731 7.1.3.3).
if until_state sc '"users":0'; then
	echo "ok   the SC has nothing in its hands before the shut down"
else
	echo "FAIL the SC still counts users: $(node_state sc || true)"
	fail=1
fi
answer="$(administrative_state sc SHUTTINGDOWN || true)"
state="$(node_state sc || true)"
if grep -q '"administrative_state":"LOCKED"' <<<"$state"; then
	echo "ok   the maintenance API shut the SC down, which locked it at once"
else
	echo "FAIL the shut down of the SC: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# "ue sms" exits 1 when the submit report is an RP-ERROR, hence
# "|| true". The IP-SM-GW is in service, so the MESSAGE gets its 202;
# with no connection to an SC it answers with RP-Cause 41 (temporary
# failure).
out="$(docker compose run --no-deps --rm ue sms +12025550102 'Is the Service Centre in?' 2>&1 || true)"
if grep -q '"msg":"submit report".*"state":"failed".*"rp_cause":41' <<<"$out"; then
	echo "ok   UE 1 got RP-Cause 41 for its short message while the SC is shut down"
else
	echo "FAIL the short message while the SC is shut down: $(tail -n 2 <<<"$out")"
	fail=1
fi
# The OFAs are counted while the SC is shut down, when no OFR reaches
# it, so that the OFA of the short message before the shut down is
# already among them.
scOFAsSent="$(count "$scOFAs")"
answer="$(administrative_state sc UNLOCKED || true)"
state="$(node_state sc || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the SC"
else
	echo "FAIL the unlock of the SC: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# REBOOTING lets the IP-SM-GW come back (RFC 6733 5.4.3), which it does
# at its next attempt after Tc.
if wait_for_opens ipsmgw $((sc_ipsmgw_opens + 1)); then
	echo "ok   the IP-SM-GW opened its connection to the SC again"
else
	echo "FAIL the IP-SM-GW did not open its connection to the SC again after the unlock"
	fail=1
fi
out="$(docker compose run --no-deps --rm ue sms +12025550102 'Is the Service Centre back?' 2>&1 || true)"
if grep -q '"msg":"submit report".*"state":"accepted"' <<<"$out"; then
	echo "ok   UE 1 sent a short message after the unlock of the SC"
else
	echo "FAIL the short message after the unlock of the SC: $(tail -n 2 <<<"$out")"
	fail=1
fi

await "the DPR of the drained SC" "$scDPRs" $((scDPRsSent + 1))
await "the OFA of the SC after its unlock" "$scOFAs" $((scOFAsSent + 1))
# The SC asks the HSS about the recipient of each short message it is
# handed, and again for each delivery it attempts (SRR), so the short
# messages above left it requests and the answers that came back, on
# its connection to the HSS.
if value="$(until_positive prometheus_values 'up{job="ims",node="sc"}')"; then
	echo "ok   Prometheus scrapes the SC: up = $value"
else
	echo "FAIL the sc target of Prometheus is not up: $value"
	curl -fsS "$prometheus/api/v1/targets?state=active" || true
	echo
	fail=1
fi
for series in 'ims_diameter_requests_total{role="sc",command="SRR"}' \
	'ims_diameter_answers_total{role="sc",command="SRR"}' \
	'ims_diameter_peer_up{node="sc"}'; do
	if value="$(until_positive prometheus_values "$series")"; then
		echo "ok   Prometheus has $series = $value"
	else
		echo "FAIL Prometheus has no $series above 0: $value"
		# Shown only to explain the failure, if the SC answers at all,
		# hence "|| true".
		docker compose exec -T sc wget -qO- http://127.0.0.1:8085/metrics || true
		fail=1
	fi
done
same_nodes

echo "reading $pcap"
last=0
ue=172.28.0.100
pcscf=172.28.0.40
icscf=172.28.0.20
scscf=172.28.0.30
ipsmgw=172.28.0.70
sc=172.28.0.71
register='sip.CSeq.method == "REGISTER"'

echo "the short message while the IP-SM-GW is shut down"
# The S-CSCF sends the third-party REGISTER of UE 1's registration and
# the MESSAGE of its short message to the IP-SM-GW, which answers 503
# with Retry-After to both. The criterion of the third-party REGISTER
# says SESSION_CONTINUED, so the registration stands (TS 24.229
# 5.4.1.7). The criterion of the MESSAGE says SESSION_TERMINATED, so the
# S-CSCF passes the failure on (5.4.3.2), and as a proxy turns the 503
# into 500 (RFC 3261 16.7 step 6).
#
# The S-CSCF answers the UE before it registers the user anywhere else
# (TS 24.229 5.4.1.7), so the 200 of the registration comes before the
# third-party REGISTER that the IP-SM-GW refuses. The Service-Route
# tells that 200 from the one of a deregistration, which carries none.
message='sip.CSeq.method == "MESSAGE"'
expect "200 S-CSCF -> I-CSCF for UE 1's registration, with Service-Route" "$register && sip.Status-Code == 200 && ip.src == $scscf && ip.dst == $icscf && sip.Service-Route && sip.To contains \"+12025550101\""
expect "third-party REGISTER S-CSCF -> IP-SM-GW" "sip.Method == \"REGISTER\" && ip.src == $scscf && ip.dst == $ipsmgw"
refusedRegister="$last"
expect "503 IP-SM-GW -> S-CSCF for the third-party REGISTER, with Retry-After 30" "$register && sip.Status-Code == 503 && ip.src == $ipsmgw && ip.dst == $scscf && sip.Retry-After == 30"
expect "MESSAGE S-CSCF -> IP-SM-GW with the short message" "sip.Method == \"MESSAGE\" && ip.src == $scscf && ip.dst == $ipsmgw && sip.Content-Type == \"application/vnd.3gpp.sms\""
refusedMessage="$last"
expect "503 IP-SM-GW -> S-CSCF with Retry-After 30" "$message && sip.Status-Code == 503 && ip.src == $ipsmgw && ip.dst == $scscf && sip.Retry-After == 30"
expect "500 S-CSCF -> P-CSCF for the short message, without Retry-After" "$message && sip.Status-Code == 500 && ip.src == $scscf && ip.dst == $pcscf && !sip.Retry-After"
expect "500 P-CSCF -> UE 1 for the short message, without Retry-After" "$message && sip.Status-Code == 500 && ip.src == $pcscf && ip.dst == $ue && !sip.Retry-After"
none "202 of the IP-SM-GW while it was shut down" "frame.number > $refusedMessage && frame.number <= $last && $message && sip.Status-Code == 202 && ip.src == $ipsmgw"
# The refused third-party REGISTER leaves no subscription behind: the
# IP-SM-GW subscribes to the registration state only for one it took.
none "SUBSCRIBEs of the IP-SM-GW while it was shut down" "frame.number > $refusedRegister && frame.number <= $last && sip.Method == \"SUBSCRIBE\" && ip.src == $ipsmgw"
echo "the short message after the unlock of the IP-SM-GW"
expect "MESSAGE S-CSCF -> IP-SM-GW with the short message" "sip.Method == \"MESSAGE\" && ip.src == $scscf && ip.dst == $ipsmgw && sip.Content-Type == \"application/vnd.3gpp.sms\""
expect "202 IP-SM-GW -> S-CSCF" "$message && sip.Status-Code == 202 && ip.src == $ipsmgw && ip.dst == $scscf"
expect "submit report IP-SM-GW -> I-CSCF, in reply to the short message" "sip.Method == \"MESSAGE\" && ip.src == $ipsmgw && ip.dst == $icscf && sip.In-Reply-To"

echo "the short message while the Service Centre is shut down"
# The SC ends the connection that the IP-SM-GW opened (RFC 6733 5.4),
# and the IP-SM-GW, with no SC left to send the OFR to, answers UE 1
# with an RP-ERROR of RP-Cause 41 (TS 24.011 Table 8.4).
expect "DPR SC -> IP-SM-GW with Disconnect-Cause REBOOTING" "diameter.cmd.code == 282 && diameter.flags.request == 1 && ip.src == $sc && ip.dst == $ipsmgw && diameter.Disconnect-Cause == 0"
scDPR="$last"
expect "DPA IP-SM-GW -> SC" "diameter.cmd.code == 282 && diameter.flags.request == 0 && ip.src == $ipsmgw && ip.dst == $sc"
expect "MESSAGE S-CSCF -> IP-SM-GW with the short message" "sip.Method == \"MESSAGE\" && ip.src == $scscf && ip.dst == $ipsmgw && sip.Content-Type == \"application/vnd.3gpp.sms\""
expect "202 IP-SM-GW -> S-CSCF" "$message && sip.Status-Code == 202 && ip.src == $ipsmgw && ip.dst == $scscf"
expect "submit report P-CSCF -> UE 1: RP-ERROR, cause 41" "sip.Method == \"MESSAGE\" && ip.src == $pcscf && ip.dst == $ue && sip.In-Reply-To && gsm_a.rp.msg_type == 0x05 && gsm_a.rp.cause == 41"
none "OFRs while the SC was shut down" "frame.number > $scDPR && frame.number <= $last && diameter.cmd.code == 8388645"
echo "the short message after the unlock of the Service Centre"
expect "OFR IP-SM-GW -> SC" "diameter.cmd.code == 8388645 && diameter.flags.request == 1 && ip.src == $ipsmgw && ip.dst == $sc"
expect "OFA SC -> IP-SM-GW with DIAMETER_SUCCESS" "diameter.cmd.code == 8388645 && diameter.flags.request == 0 && ip.src == $sc && ip.dst == $ipsmgw && diameter.Result-Code == 2001"

finish
