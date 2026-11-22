#!/usr/bin/env bash
# End-to-end check: UE 1 calls UE 2 by its telephone number. The
# call goes through ENUM, the P-CSCF, the S-CSCFs, the I-CSCF, the MMTel
# AS of each user and the media relay, and the capture holds the call in
# order. The callee must
# receive exactly the G.711 payloads the caller sent. Then a busy callee
# gives 486 and an unregistered one 480.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
export MSYS_NO_PATHCONV=1
# Use the project's own builder when it exists, so the build
# cache stays separate from other projects. CI has no such builder.
if docker buildx inspect ims-practice >/dev/null 2>&1; then
	export BUILDX_BUILDER=ims-practice
fi

docker compose up -d --build

wait_for_open() {
	local service="$1" i
	# Up to two minutes: a node that tried before the HSS listened
	# tries again after Tc, 30 s (RFC 6733 2.1).
	for i in $(seq 1 120); do
		if grep -q '"to":"I-Open (OKAY)"' <<<"$(docker compose logs --no-color "$service" 2>/dev/null)"; then
			return 0
		fi
		sleep 1
	done
	echo "FAIL $service never opened its Diameter connection" >&2
	exit 1
}
wait_for_open icscf
wait_for_open scscf
wait_for_h248_relay

ue2=(--impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org
	--impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org --password test-password-2)
rm -f captures/ue1-sent.raw captures/ue2-received.raw
remember_nodes
fail=0

# The callee waits in the background for one call. Flags go before the
# URI of "ue call", as the flag package stops at the first argument.
docker compose run --no-deps -d --name ims-practice-ue2-call ue2 answer "${ue2[@]}" \
	--ring 1s --tone 5s --received-out /captures/ue2-received.raw --wav-out /captures/ue2-received.wav >/dev/null
for i in $(seq 1 20); do
	grep -q '"msg":"waiting for a call"' <<<"$(docker logs ims-practice-ue2-call 2>/dev/null)" && break
	sleep 1
done
if docker compose run --no-deps --rm ue call --tone 3s --sent-out /captures/ue1-sent.raw tel:+1-202-555-0102; then
	echo "ok   UE 1 called UE 2 by number"
else
	echo "FAIL the call"
	fail=1
fi
if [ "$(timeout 60 docker wait ims-practice-ue2-call)" = "0" ]; then
	echo "ok   UE 2 answered and the call ended"
else
	echo "FAIL UE 2"
	docker logs ims-practice-ue2-call | tail -n 20
	fail=1
fi
docker rm ims-practice-ue2-call >/dev/null

# A busy callee, then one that is not registered at all.
docker compose run --no-deps -d --name ims-practice-ue2-busy ue2 answer "${ue2[@]}" --reject 486 >/dev/null
for i in $(seq 1 20); do
	grep -q '"msg":"waiting for a call"' <<<"$(docker logs ims-practice-ue2-busy 2>/dev/null)" && break
	sleep 1
done
# The UE exits 1 when the call fails, which is expected here; the log
# says how it failed.
out="$(docker compose run --no-deps --rm ue call sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org 2>/dev/null || true)"
if grep -q '"call failed: 486 ' <<<"$out"; then
	echo "ok   busy callee answered 486"
else
	echo "FAIL busy callee"
	fail=1
fi
timeout 60 docker wait ims-practice-ue2-busy >/dev/null || true
docker rm ims-practice-ue2-busy >/dev/null
# No answer: UE 2 rings for longer than UE 1 waits, so UE 1 cancels.
docker compose run --no-deps -d --name ims-practice-ue2-ring ue2 answer "${ue2[@]}" --ring 30s >/dev/null
for i in $(seq 1 20); do
	grep -q '"msg":"waiting for a call"' <<<"$(docker logs ims-practice-ue2-ring 2>/dev/null)" && break
	sleep 1
done
out="$(docker compose run --no-deps --rm ue call --give-up-after 2s tel:+12025550102 2>/dev/null || true)"
if grep -q '"call failed: 487 ' <<<"$out"; then
	echo "ok   unanswered call cancelled with 487"
else
	echo "FAIL unanswered call"
	fail=1
fi
docker rm -f ims-practice-ue2-ring >/dev/null
out="$(docker compose run --no-deps --rm ue call tel:+12025550103 2>/dev/null || true)"
if grep -q '"call failed: 480 ' <<<"$out"; then
	echo "ok   unregistered callee answered 480"
else
	echo "FAIL unregistered callee"
	fail=1
fi

# pjsua, a softphone written by others, both ways: it answers a
# call from UE 1, then calls UE 2.
pjsua=(--null-audio --no-tcp --local-port 5060
	--id sip:+12025550104@ims.mnc001.mcc001.3gppnetwork.org
	--registrar sip:ims.mnc001.mcc001.3gppnetwork.org --realm ims.mnc001.mcc001.3gppnetwork.org
	--username +12025550104@ims.mnc001.mcc001.3gppnetwork.org --password test-password-4
	"--outbound=sip:pcscf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr")
pjsua_registered() {
	local since="$1" i registered
	for i in $(seq 1 20); do
		registered="$(grep '"msg":"registered"' <<<"$(docker compose logs --no-color --since "$since" scscf 2>/dev/null)" || true)"
		grep -q '+12025550104@' <<<"$registered" && return 0
		sleep 1
	done
	return 1
}
since="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
docker compose run --no-deps -d -T --name ims-practice-pjsua-answer peer "${pjsua[@]}" --auto-answer 200 >/dev/null
if ! pjsua_registered "$since"; then
	echo "FAIL pjsua did not register"
	fail=1
elif out="$(docker compose run --no-deps --rm ue call --tone 2s tel:+12025550104 2>&1)"; then
	echo "ok   UE 1 called pjsua, which answered"
else
	echo "FAIL UE 1 to pjsua"
	grep -E '"msg":"(call progress|ue failed)"' <<<"$out" | tail -n 3
	fail=1
fi
docker rm -f ims-practice-pjsua-answer >/dev/null

docker compose run --no-deps -d --name ims-practice-ue2-pjsua ue2 answer "${ue2[@]}" --ring 500ms --tone 4s >/dev/null
for i in $(seq 1 20); do
	grep -q '"msg":"waiting for a call"' <<<"$(docker logs ims-practice-ue2-pjsua 2>/dev/null)" && break
	sleep 1
done
docker compose run --no-deps -d -T --name ims-practice-pjsua-call peer "${pjsua[@]}" --duration 2 	sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org >/dev/null
# The log is read whole before grep -q looks at it. Piped, grep -q exits
# at the first match, docker logs dies writing the rest (SIGPIPE), and
# pipefail fails the check although pjsua's call was confirmed.
if [ "$(timeout 60 docker wait ims-practice-ue2-pjsua)" = "0" ] &&
	grep -q "state changed to CONFIRMED" <<<"$(docker logs ims-practice-pjsua-call 2>&1)"; then
	echo "ok   pjsua called UE 2, which answered"
else
	echo "FAIL pjsua to UE 2"
	docker logs ims-practice-ue2-pjsua 2>&1 | tail -n 15
	docker logs ims-practice-pjsua-call 2>&1 | grep -iE "state changed|error|status|reason" | tail -n 15 || true
	fail=1
fi
docker rm -f ims-practice-ue2-pjsua ims-practice-pjsua-call >/dev/null
same_nodes
sleep 1

pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
echo "reading $pcap"

# RTP is found by the heuristic too, as some of it flows before tshark
# has seen its SDP.
tshark_options=(-o rtp.heuristic_rtp:TRUE)
last=0

ue1=172.28.0.100
ue2ip=172.28.0.101
pcscf=172.28.0.40
icscf=172.28.0.20
scscf=172.28.0.30
agw=172.28.0.50
mmtel=172.28.0.75
domain=ims.mnc001.mcc001.3gppnetwork.org
invite='sip.Method == "INVITE"'

echo "call setup"
expect "INVITE UE 1 -> P-CSCF with SDP" "$invite && ip.src == $ue1 && sip.Request-Line contains \"tel:+1-202-555-0102\" && sdp"
expect "INVITE P-CSCF -> S-CSCF with the relay in the SDP" "$invite && ip.src == $pcscf && ip.dst == $scscf && sdp.connection_info.address == $agw && sip.P-Asserted-Identity"
call_start="$last"
# The P-CSCF writes the icid-value of the call (TS 24.229 5.2.6.3.3
# step 7), and every node after it passes it on.
icid="$(field sip.P-Charging-Vector | sed -n 's/^icid-value=\([^;]*\);orig-ioi=.*/\1/p')"
if [ -n "$icid" ]; then
	echo "ok   the INVITE leaves the P-CSCF with the icid-value $icid and an orig-ioi"
else
	echo "FAIL the INVITE left the P-CSCF with P-Charging-Vector \"$(field sip.P-Charging-Vector)\""
	fail=1
	icid=none
fi
expect "ENUM query for the number" "dns.qry.type == 35 && dns.qry.name == \"2.0.1.0.5.5.5.2.0.2.1.e164.arpa\""
expect "INVITE S-CSCF -> I-CSCF to the SIP URI from ENUM, the icid-value and the S-CSCF's orig-ioi" "$invite && ip.src == $scscf && ip.dst == $icscf && sip.r-uri.user == \"+12025550102\" && sip.P-Charging-Vector == \"icid-value=$icid;orig-ioi=$domain\""
# The P-CSCF's reg event SUBSCRIBE for UE 1 brings an LIR of its own.
expect "LIR for the callee" "diameter.cmd.code == 302 && diameter.flags.request == 1 && diameter.Public-Identity == \"sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org\""
expect "LIA with the callee's S-CSCF" "diameter.cmd.code == 302 && diameter.flags.request == 0 && diameter.Result-Code == 2001 && diameter.Server-Name"
expect "INVITE I-CSCF -> S-CSCF with the same icid-value" "$invite && ip.src == $icscf && ip.dst == $scscf && sip.P-Charging-Vector contains \"icid-value=$icid\""
expect "INVITE S-CSCF -> P-CSCF along the Path, the icid-value and the S-CSCF's orig-ioi" "$invite && ip.src == $scscf && ip.dst == $pcscf && sip.P-Called-Party-ID && sip.P-Charging-Vector == \"icid-value=$icid;orig-ioi=$domain\""
expect "INVITE P-CSCF -> UE 2" "$invite && ip.src == $pcscf && ip.dst == $ue2ip && sdp.connection_info.address == $agw"
expect "183 reliable from UE 2" "sip.Status-Code == 183 && ip.src == $ue2ip && sip.RSeq"
expect "PRACK" "sip.Method == \"PRACK\" && ip.dst == $ue2ip && sip.RAck"
expect "200 OK to the INVITE from UE 2" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.src == $ue2ip"
# The P-CSCF stored the icid-value and the orig-ioi of the INVITE before
# it removed them, and writes them back with its own term-ioi (TS 24.229
# 5.2.6.4.3 item 9, 5.2.6.4.4 item 6).
expect "200 OK P-CSCF -> S-CSCF with the charging vector of the INVITE and a term-ioi" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.src == $pcscf && ip.dst == $scscf && sip.P-Charging-Vector == \"icid-value=$icid;orig-ioi=$domain;term-ioi=$domain\""
answered="$last"
# The MMTel AS is a B2BUA (TS 24.229 5.7.5): it acknowledges UE 2's 200
# on its own dialog with UE 2 at once (RFC 3261 13.2.2.4), while its
# 200 to UE 1 still has the other legs of the call to cross. The two
# may come in either order.
expect "ACK reaches UE 2" "sip.Method == \"ACK\" && ip.dst == $ue2ip"
last="$answered"
expect "200 OK to UE 1" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.dst == $ue1"
expect "RTP UE 1 -> relay" "rtp && ip.src == $ue1 && ip.dst == $agw"
# The relay passes each packet on unchanged, so the SSRC of UE 1 marks
# this call's packets on the side of UE 2 as well.
ssrc="$(tshark_fields "frame.number == $last" rtp.ssrc)"
expect "RTP relay -> UE 2" "rtp && ip.src == $agw && ip.dst == $ue2ip"
echo "call release"
expect "BYE from UE 1" "sip.Method == \"BYE\" && ip.src == $ue1"
first_bye="$last"
# The AS answers UE 1's BYE on UE 1's dialog and sends a BYE of its own
# on the next one (TS 24.229 5.7.5.1), so either may come first.
expect "200 OK to the BYE reaches UE 1" "sip.Status-Code == 200 && sip.CSeq.method == \"BYE\" && ip.dst == $ue1"
last="$first_bye"
expect "BYE reaches UE 2" "sip.Method == \"BYE\" && ip.dst == $ue2ip"

echo "one icid-value at every node (TS 24.229 5.3.2.1, 5.4.3.2 step 6, 5.4.3.3 step 6, 5.7.5.1)"
# The INVITEs and the responses to them between two nodes, from the
# first INVITE that left the P-CSCF to the BYE of UE 1. A 100 (Trying)
# belongs to one hop and carries none.
nodes="{$pcscf, $scscf, $icscf, $mmtel}"
call_window="frame.number >= $call_start && frame.number < $first_bye && ip.src in $nodes && ip.dst in $nodes"
for hop in "$pcscf $scscf" "$scscf $mmtel" "$mmtel $scscf" "$scscf $icscf" "$icscf $scscf" "$scscf $pcscf"; do
	read -r src dst <<<"$hop"
	if [ -n "$(tshark_fields "$call_window && $invite && ip.src == $src && ip.dst == $dst && sip.P-Charging-Vector contains \"icid-value=$icid\"" | first_line)" ]; then
		echo "ok   INVITE $src -> $dst with the icid-value"
	else
		echo "FAIL no INVITE $src -> $dst with the icid-value $icid"
		fail=1
	fi
done
none "INVITE, or a response to one, between two nodes of the call without its icid-value" \
	"$call_window && ($invite || (sip.CSeq.method == \"INVITE\" && sip.Status-Code > 100)) && !(sip.P-Charging-Vector contains \"icid-value=$icid\")"
echo "failures"
expect "486 to UE 1" "sip.Status-Code == 486 && ip.dst == $ue1"
expect "CANCEL from UE 1" "sip.Method == \"CANCEL\" && ip.src == $ue1"
expect "487 to UE 1" "sip.Status-Code == 487 && ip.dst == $ue1"
expect "480 to UE 1" "sip.Status-Code == 480 && ip.dst == $ue1"

# The roadmap's completion condition: the callee's RTP payload equals
# what the caller encoded. G.711 is lossy, so no WAV is compared.
sent="$(od -An -v -tx1 captures/ue1-sent.raw | tr -d ' \n')"
received="$(od -An -v -tx1 captures/ue2-received.raw | tr -d ' \n')"
# !icmp leaves out ICMP errors that quote an RTP packet: UE 2 keeps
# sending for a moment after the relay has closed the context.
# Only the first call's packets count, by their SSRC: later calls also
# reach UE 2, and the relay may pass the last packets on after UE 1's
# BYE has gone out.
on_wire="$(tshark_fields "rtp && !icmp && ip.src == $agw && ip.dst == $ue2ip && rtp.ssrc == ${ssrc:-0} && rtp.p_type == 0" rtp.payload | tr -d ':\n')"
if [ -n "$sent" ] && [ "$sent" = "$received" ] && [ "$sent" = "$on_wire" ]; then
	echo "ok   UE 2 received the ${#sent} hex digits of PCMU that UE 1 sent, and so did the wire"
else
	echo "FAIL payloads differ: sent ${#sent}, received ${#received}, on the wire ${#on_wire} hex digits"
	fail=1
fi

# The charging information stays in the network (TS 24.229 5.2.1):
# the P-CSCF sends none of it to a UE. Every UE of this setup has an
# address from 172.28.0.100 up.
none "P-Charging-Vector or P-Charging-Function-Addresses from the P-CSCF to a UE" 'ip.src == 172.28.0.40 && ip.dst >= 172.28.0.100 && (sip.P-Charging-Vector || sip.P-Charging-Function-Addresses)'
none "malformed SIP, SDP, DNS or Diameter packets" '(sip || sdp || dns || diameter) && (_ws.malformed || _ws.expert.severity >= warning)'

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color | grep -E '"level":"(WARN|ERROR)"' | tail -n 40
fi
exit "$fail"
