#!/usr/bin/env bash
# End-to-end check: the access network and the VoLTE procedures.
#
# SIPp, a user agent written by others, registers with IMS AKA. Then two
# VoLTE UEs, volte-a and volte-b, each attach to the EPC stub, register
# with IMS AKA and the security agreement, and volte-a calls volte-b with
# AMR-WB and preconditions: the PCRF authorises the media over Rx, the
# stub sets up a voice bearer for each UE, and the callee alerts only
# once the caller's UPDATE says its resources are ready. volte-b must
# receive exactly the AMR-WB frames that volte-a sent. A VoLTE UE then
# calls ue2, which offers PCMU only. Last, the two VoLTE UEs of the volte
# profile, driven through the dashboard, send DTMF, hold and resume a
# call, and in a second call the network takes volte-a's voice bearer
# and the P-CSCF ends the call. The checks read the capture.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
export MSYS_NO_PATHCONV=1
# Use the project's own builder when it exists, so the build
# cache stays separate from other projects. CI has no such builder.
if docker buildx inspect ims-practice >/dev/null 2>&1; then
	export BUILDX_BUILDER=ims-practice
fi
# Windows has a python3 stub that only opens the Store.
python=python3
"$python" -c 1 2>/dev/null || python=python

docker compose up -d --build
# SIPp's scenarios are in the image of the peers (images/peer).
docker compose --profile ue build peer

wait_for_open() {
	local service="$1" i
	for i in $(seq 1 60); do
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
wait_for_open mmtel
# The P-CSCF's peer is the PCRF.
wait_for_open pcscf
for i in $(seq 1 30); do
	curl -fsS http://localhost:8080/api/calls >/dev/null 2>&1 && break
	sleep 1
done
# The VoLTE UEs depend on the P-CSCF, the EPC and the dashboard in
# compose.yaml, so "run" and "up" start them with --no-deps (see
# scripts/e2e-lib.sh). A node recreated here would have lost the
# Diameter connections waited for above, and a REGISTER that came before
# they were open again would get a 500.
remember_nodes

domain=ims.mnc001.mcc001.3gppnetwork.org
epc_api=http://172.28.0.80:8080
# The USIMs of the two VoLTE UEs, as in compose.yaml and hss.yaml:
# MILENAGE test sets 1 and 3 (TS 35.208 4.3.1, 4.3.3).
va=(-epc "$epc_api" -impi "001010000000010@$domain" -impu "sip:001010000000010@$domain"
	-k 465b5ce8b199b49faa5f0a2ee238a6bc -opc cd63cb71954a9f4e48a5994e37a02baf)
vb=(-epc "$epc_api" -impi "001010000000012@$domain" -impu "sip:001010000000012@$domain"
	-k fec86ba6eb707ed08905757b1bb44b8f -opc 1006020f0a478bf6b699f15c062e42b3)
ue2=(-impi "001010000000002@$domain" -impu "sip:001010000000002@$domain" -password test-password-2)
fail=0
mkdir -p e2e-out
rm -f captures/volte-a-sent.amr captures/volte-b-received.amr

# wait_log waits until the log of a container started with "run -d"
# matches pattern.
wait_log() {
	local name="$1" pattern="$2" i
	for i in $(seq 1 30); do
		grep -q "$pattern" <<<"$(docker logs "$name" 2>/dev/null)" && return 0
		sleep 1
	done
	return 1
}
# ended NAME waits for the container to end, keeps its log, removes it,
# and prints its exit status.
ended() {
	local status
	status="$(timeout 60 docker wait "$1" 2>/dev/null || echo timeout)"
	docker logs "$1" >"e2e-out/$1.log" 2>&1 || true
	docker rm -f "$1" >/dev/null 2>&1 || true
	echo "$status"
}

echo "SIPp registers with IMS AKA"
# SIPp exits 0 when its one call, the registration and the
# deregistration, went through the scenario.
if out="$(docker compose run --no-deps --rm -T --entrypoint sipp peer -sf /etc/sipp/register-aka.xml -m 1 -t u1 -p 5060 \
	-nostdin -timeout 30 -timeout_error 172.28.0.40:5060 2>&1)"; then
	echo "ok   SIPp registered and deregistered with AKAv1-MD5 and the security agreement"
else
	echo "FAIL SIPp: $(tail -n 5 <<<"$out")"
	fail=1
fi

echo "a VoLTE call: volte-a calls volte-b"
docker compose run --no-deps -d --name ims-practice-volte-b-answer volte-b answer "${vb[@]}" \
	--ring 1s --tone 4s --received-out /captures/volte-b-received.amr >/dev/null
if ! wait_log ims-practice-volte-b-answer '"msg":"waiting for a call"'; then
	echo "FAIL volte-b did not attach and register"
	docker logs ims-practice-volte-b-answer 2>&1 | tail -n 5
	fail=1
fi
out="$(docker compose run --no-deps --rm volte-a call "${va[@]}" --tone 3s --sent-out /captures/volte-a-sent.amr tel:+12025550112 2>&1 || true)"
printf '%s\n' "$out" >e2e-out/volte-a-call.log
if grep -q '"msg":"call answered"' <<<"$out" && grep -q '"msg":"media","codec":"AMR-WB"' <<<"$out" &&
	[ "$(ended ims-practice-volte-b-answer)" = 0 ]; then
	echo "ok   volte-a called volte-b in AMR-WB, and volte-b answered"
else
	echo "FAIL the VoLTE call: $(grep -E '"msg":"(call progress|ue failed|media)"' <<<"$out" | tail -n 3)"
	docker logs ims-practice-volte-b-answer 2>&1 | tail -n 5 || true
	fail=1
fi

# The UE writes the frames in the storage format of RFC 4867 5, so the
# two files are equal when every frame arrived as it was sent.
sent="$(od -An -v -tx1 captures/volte-a-sent.amr 2>/dev/null | tr -d ' \n')"
received="$(od -An -v -tx1 captures/volte-b-received.amr 2>/dev/null | tr -d ' \n')"
if [ "${#sent}" -gt 100 ] && [ "$sent" = "$received" ]; then
	echo "ok   volte-b received the AMR-WB frames that volte-a sent, ${#sent} hex digits of storage format"
else
	echo "FAIL the AMR-WB frames differ: sent ${#sent}, received ${#received} hex digits"
	fail=1
fi

echo "a VoLTE UE calls a UE that offers PCMU only"
docker compose run --no-deps -d --name ims-practice-ue2-pcmu ue2 answer "${ue2[@]}" --ring 500ms --tone 2s >/dev/null
wait_log ims-practice-ue2-pcmu '"msg":"waiting for a call"' || true
out="$(docker compose run --no-deps --rm volte-a call "${va[@]}" --tone 2s tel:+12025550102 2>&1 || true)"
printf '%s\n' "$out" >e2e-out/volte-a-pcmu.log
if grep -q '"msg":"call answered"' <<<"$out" && grep -q '"msg":"media","codec":"PCMU"' <<<"$out" &&
	[ "$(ended ims-practice-ue2-pcmu)" = 0 ]; then
	echo "ok   volte-a and ue2 agreed on PCMU"
else
	echo "FAIL the call to ue2: $(grep -E '"msg":"(call progress|ue failed|media)"' <<<"$out" | tail -n 3)"
	fail=1
fi

echo "a VoLTE UE sends a short message"
# ue2 is not registered: the IP-SM-GW accepts the short message for
# the service centre all the same, and the submit report comes back.
if out="$(docker compose run --no-deps --rm volte-a sms "${va[@]}" +12025550102 'From the number of volte-a' 2>&1)"; then
	echo "ok   volte-a sent a short message, and it was accepted"
else
	echo "FAIL volte-a's short message: $(grep -E '"msg":"(sms sent|submit report|ue failed)"' <<<"$out" | tail -n 2)"
	fail=1
fi
printf '%s\n' "$out" >e2e-out/volte-a-sms.log

echo "the VoLTE UEs of the volte profile, driven through the dashboard"
# ues prints "name registered|unregistered|unreachable call-state|idle"
# for each UE, the UEs separated by commas.
ues() {
	curl -fsS http://localhost:8080/api/ues | "$python" -c '
import json, sys
out = []
for u in json.load(sys.stdin):
    s = u["status"] or {}
    reg = "unreachable" if not u["reachable"] else "registered" if s["registered"] else "unregistered"
    out.append(u["name"] + " " + reg + " " + ((s.get("call") or {}).get("state") or "idle"))
print(",".join(out))
' | tr -d '\r'
}
# wait_ues waits until the output of ues matches the glob pattern.
wait_ues() {
	local i
	for i in $(seq 1 30); do
		[[ "$(ues)" == $1 ]] && return 0
		sleep 1
	done
	return 1
}
ue_post() {
	curl -s -w '
%{http_code}' -X "${3:-POST}" -H 'Content-Type: application/json' -d "${2:-}" "http://localhost:8080/api/ues/$1" | tail -n 1
}
# session NAME prints the session of the UE's call as JSON, from the
# status that the dashboard lists for it.
session() {
	curl -fsS http://localhost:8080/api/ues | "$python" -c '
import json, sys
for u in json.load(sys.stdin):
    if u["name"] == sys.argv[1]:
        print(json.dumps((((u["status"] or {}).get("call") or {}).get("session")) or {}))
' "$1" | tr -d '\r'
}
# They restart by themselves and hold subscribers 10 and 12, so they
# must not outlive the script.
trap 'docker compose --profile volte rm -sf volte-a volte-b >/dev/null 2>&1' EXIT
# Only the two UEs start: the network is up already.
docker compose --profile volte up -d --no-deps volte-a volte-b
# ue-c, the third UE of the sim profile, is listed after them and stays
# down here.
both_idle="*volte-a registered idle,volte-b registered idle,ue-c unreachable idle"
if wait_ues "$both_idle"; then
	echo "ok   volte-a and volte-b attached and registered"
else
	echo "FAIL the VoLTE UEs did not register: $(ues)"
	exit 1
fi
# The card shows the default public identity, and the UE reads its
# supplementary services over Ut with that identity as the XUI (GSMA
# IR.92 2.3.2), not with the temporary one it registered.
impu_a="$(curl -fsS http://localhost:8080/api/ues | "$python" -c '
import json, sys
print(next((u["status"] or {}).get("impu", "") for u in json.load(sys.stdin) if u["name"] == "volte-a"))
' | tr -d '\r')"
if [ "$impu_a" = "sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org" ] &&
	curl -fsS http://localhost:8080/api/ues/volte-a/xcap >e2e-out/volte-a-xcap.json; then
	echo "ok   volte-a shows its default public identity and read its services over Ut"
else
	echo "FAIL volte-a shows $impu_a, or its Ut read failed"
	fail=1
fi
both_active="*volte-a registered active,volte-b registered active,ue-c unreachable idle"
status="$(ue_post volte-a/call '{"target":"tel:+12025550112"}')"
if [ "$status" = 200 ] && wait_ues "$both_active"; then
	echo "ok   volte-a called volte-b through the dashboard, and volte-b answered"
else
	echo "FAIL the call through the dashboard (status $status): $(ues)"
	exit 1
fi
dashboard_call="$(curl -fsS http://localhost:8080/api/ues | "$python" -c '
import json, sys
print(next(u["status"]["call"]["call_id"] for u in json.load(sys.stdin) if u["name"] == "volte-a"))
' | tr -d '\r')"
status="$(ue_post volte-a/dtmf '{"digits":"159#"}')"
for i in $(seq 1 10); do
	grep -q '"dtmf_received": "159#"' <<<"$(session volte-b)" && break
	sleep 1
done
if [ "$status" = 200 ] && grep -q '"dtmf_received": "159#"' <<<"$(session volte-b)"; then
	echo "ok   volte-b heard the DTMF digits 159# that volte-a sent"
else
	echo "FAIL DTMF (status $status): volte-b has $(session volte-b)"
	fail=1
fi
status="$(ue_post volte-a/hold)"
if [ "$status" = 200 ] && grep -q '"peer_direction": "recvonly"' <<<"$(session volte-a)"; then
	echo "ok   volte-a held the call, and volte-b answered recvonly"
else
	echo "FAIL hold (status $status): volte-a has $(session volte-a)"
	fail=1
fi
sleep 1
status="$(ue_post volte-a/resume)"
if [ "$status" = 200 ] && grep -q '"peer_direction": "sendrecv"' <<<"$(session volte-a)"; then
	echo "ok   volte-a resumed the call"
else
	echo "FAIL resume (status $status): volte-a has $(session volte-a)"
	fail=1
fi
sleep 1
status="$(ue_post volte-a/hangup)"
if [ "$status" = 200 ] && wait_ues "$both_idle"; then
	echo "ok   volte-a hung up"
else
	echo "FAIL hanging up (status $status): $(ues)"
	fail=1
fi
# The dashboard links the Rx sessions of the call to it: one for the
# media of each UE, through its P-CSCF.
sleep 2
rx="$(curl -fsS http://localhost:8080/api/calls | "$python" -c '
import json, sys, urllib.parse, urllib.request
call = next(c for c in json.load(sys.stdin) if c["call_id"] == sys.argv[1])
query = "&".join("call_id=" + urllib.parse.quote(l, safe="") for l in call["legs"])
messages = json.load(urllib.request.urlopen("http://localhost:8080/api/messages?" + query))
print(" ".join(sorted({m["summary"] for m in messages if m["protocol"] == "diameter"})))
' "$dashboard_call" | tr -d '\r')"
if [[ " $rx " == *" AAA 2001 AAR "* ]] && [[ " $rx " == *" STA 2001 STR "* ]]; then
	echo "ok   the dashboard shows the call's Rx messages: $rx"
else
	echo "FAIL the dashboard shows these Diameter messages for the call: $rx"
	fail=1
fi

echo "the network takes volte-a's voice bearer in a call (IR.92 2.4.2.3)"
status="$(ue_post volte-a/call '{"target":"tel:+12025550112"}')"
if [ "$status" = 200 ] && wait_ues "$both_active"; then
	echo "ok   volte-a called volte-b again"
else
	echo "FAIL the second call (status $status): $(ues)"
	exit 1
fi
sleep 2
# The network initiated EPS bearer context deactivation of the EPC stub
# (TS 24.301 6.4.4.2), through the dashboard: the voice bearer is the
# one of QCI 1 (TS 23.203 6.1.7.2).
ebi="$(curl -fsS http://localhost:8080/api/epc/ues/001010000000010 | "$python" -c '
import json, sys
print(next((b["eps_bearer_identity"] for b in json.load(sys.stdin)["bearers"] if b["eps_qos"]["qci"] == 1), ""))
' | tr -d '\r')"
status="$(curl -s -w '
%{http_code}' -X DELETE "http://localhost:8080/api/epc/network-initiated/ues/001010000000010/bearers/$ebi" | tail -n 1)"
if [ -n "$ebi" ] && [ "${status:0:1}" = 2 ] && wait_ues "$both_idle"; then
	echo "ok   the stub deactivated bearer $ebi of volte-a, and both UEs are out of the call"
else
	echo "FAIL deactivating the voice bearer $ebi (status $status): $(ues)"
	fail=1
fi
docker compose --profile volte rm -sf volte-a volte-b >/dev/null 2>&1
trap - EXIT
same_nodes
sleep 2

pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
echo "reading $pcap"

last=0

sipp=172.28.0.102
va_ip=172.28.0.120
vb_ip=172.28.0.121
ue2ip=172.28.0.101
pcscf=172.28.0.40
scscf=172.28.0.30
agw=172.28.0.50
epc=172.28.0.80
dashboard=172.28.0.60
impi_a="001010000000010@$domain"
# volte-a registers the temporary public identity derived from its IMSI,
# which is barred, and uses its default public identity, the SIP URI of
# its number, in everything else (TS 24.229 5.1.1.1A, 5.1.1.2.1).
temporary_a="sip:001010000000010@$domain"
default_a="sip:+12025550110@$domain"
invite='sip.Method == "INVITE"'
req='diameter.flags.request == 1'
# Rx (TS 29.214 5.6): AA, ST and AS.
aar="diameter.cmd.code == 265 && $req && ip.src == $pcscf"
str="diameter.cmd.code == 275 && $req && ip.src == $pcscf"
asr="diameter.cmd.code == 274 && $req && ip.src == $epc"
# The UE API of the EPC stub, and what it sends on a UE's event stream.
ue_api() { echo "http.request.method == \"POST\" && ip.dst == $epc && http.request.uri == \"/ues/$1/$2\""; }
event_to() { echo "ip.src == $epc && ip.dst == $1 && frame contains \"$2\""; }

echo "SIPp registers with IMS AKA (TS 24.229 5.1.1.2.2, 5.1.1.5.1; TS 33.203 6.1.1, 7.2)"
expect "REGISTER SIPp -> P-CSCF:5060 with Security-Client" "sip.Method == \"REGISTER\" && ip.src == $sipp && udp.dstport == 5060 && sip.Security-Client"
expect "MAR for SIPp's subscriber, Digest-AKAv1-MD5" "diameter.cmd.code == 303 && $req && diameter.User-Name == \"001010000000011@$domain\" && diameter.3GPP-SIP-Authentication-Scheme == \"Digest-AKAv1-MD5\""
expect "401 P-CSCF -> SIPp: AKAv1-MD5 and Security-Server" "sip.Status-Code == 401 && ip.dst == $sipp && sip.WWW-Authenticate contains \"AKAv1-MD5\" && sip.Security-Server"
expect "REGISTER SIPp -> the protected server port 5064 with Security-Verify" "sip.Method == \"REGISTER\" && ip.src == $sipp && udp.dstport == 5064 && sip.Security-Verify"
expect "200 OK P-CSCF:5064 -> SIPp" "sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ip.dst == $sipp && udp.srcport == 5064"

echo "volte-a attaches to the EPC stub (the messages of TS 24.301 8.2 over the stub's API)"
expect "ATTACH REQUEST volte-a -> EPC stub, with a PDN CONNECTIVITY REQUEST for the APN ims" "$(ue_api 001010000000010 attach-request) && json.value.string == \"ims\""
expect "ATTACH ACCEPT: the PDN address, and the P-CSCF in the PCO (container 000CH)" "http.response.code == 200 && ip.src == $epc && ip.dst == $va_ip && json.value.string == \"$va_ip\" && json.value.string == \"000CH\" && json.value.string == \"$pcscf\""
expect "ATTACH COMPLETE" "$(ue_api 001010000000010 attach-complete)"

echo "volte-a registers with IMS AKA (TS 24.229 5.1.1.2.2, 5.2.2.2, 5.4.1.2.1; TS 33.203 6.1.1, 7.1)"
expect "REGISTER volte-a -> P-CSCF:5060: To the temporary public identity, Security-Client, sec-agree, AKAv1-MD5 without a response, P-Access-Network-Info" "sip.Method == \"REGISTER\" && ip.src == $va_ip && udp.dstport == 5060 && sip.to.addr == \"$temporary_a\" && sip.Security-Client && sip.Require contains \"sec-agree\" && sip.Authorization contains \"algorithm=AKAv1-MD5\" && sip.Authorization contains \"response=\\\"\\\"\" && sip.P-Access-Network-Info"
reg="$(field sip.Call-ID)"
expect "MAR S-CSCF -> HSS: Digest-AKAv1-MD5" "diameter.cmd.code == 303 && $req && diameter.User-Name == \"$impi_a\" && diameter.3GPP-SIP-Authentication-Scheme == \"Digest-AKAv1-MD5\""
expect "MAA 2001 with RAND and AUTN, XRES, CK and IK (TS 29.228 6.3)" "$(answer_to) && diameter.Result-Code == 2001 && diameter.3GPP-SIP-Authenticate && diameter.3GPP-SIP-Authorization && diameter.Confidentiality-Key && diameter.Integrity-Key"
# The nonce is base64 and can end in "ck=" or "ik=", so the parameters
# are found by the comma or space before them, which base64 has not.
expect "401 S-CSCF -> I-CSCF with ck and ik for the P-CSCF" "sip.Status-Code == 401 && ip.src == $scscf && sip.Call-ID == \"$reg\" && sip.WWW-Authenticate matches \"[, ]ck=\" && sip.WWW-Authenticate matches \"[, ]ik=\""
expect "401 P-CSCF -> volte-a: Security-Server, ck and ik removed" "sip.Status-Code == 401 && ip.dst == $va_ip && sip.Security-Server && !(sip.WWW-Authenticate matches \"[, ]ck=\") && !(sip.WWW-Authenticate matches \"[, ]ik=\")"
# The answer is close to 1300 bytes: with long SPIs it goes over TCP
# (RFC 3261 18.1.1), from a port of the moment, and its 200 OK comes
# back on that connection.
expect "REGISTER volte-a:5062 -> P-CSCF:5064, or over TCP to 5064: Security-Verify and the response from RES" "sip.Method == \"REGISTER\" && ip.src == $va_ip && ((udp.srcport == 5062 && udp.dstport == 5064) || tcp.dstport == 5064) && sip.Security-Verify && !(sip.Authorization contains \"response=\\\"\\\"\")"
expect "REGISTER P-CSCF -> I-CSCF: integrity-protected=\"yes\"" "sip.Method == \"REGISTER\" && ip.src == $pcscf && sip.Call-ID == \"$reg\" && sip.Authorization contains \"integrity-protected=\\\"yes\\\"\""
expect "SAR S-CSCF -> HSS" "diameter.cmd.code == 301 && $req && diameter.User-Name == \"$impi_a\""
expect "200 OK I-CSCF -> P-CSCF" "sip.Status-Code == 200 && sip.Call-ID == \"$reg\" && ip.dst == $pcscf"
mark
expect "200 OK P-CSCF:5064 -> volte-a: P-Associated-URI with the default public identity, without the barred temporary one (TS 24.229 5.4.1.2.2F)" "sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ip.dst == $va_ip && ((udp.srcport == 5064 && udp.dstport == 5062) || tcp.srcport == 5064) && sip.P-Associated-URI contains \"<$default_a>\" && !(sip.P-Associated-URI contains \"$temporary_a\")"
registered="$last"
back
expect "AAR P-CSCF -> PCRF for the signalling path: AF_SIGNALLING, loss and release of the bearer (TS 29.214 A.4)" "$aar && diameter.Framed-IP-Address.IPv4 == $va_ip && diameter.Flow-Usage == 2 && diameter.Specific-Action == 2 && diameter.Specific-Action == 4"
expect "AAA 2001" "$(answer_to) && diameter.Result-Code == 2001"
# The UE calls as soon as it has the 200, so its INVITE can come before
# the AAA.
last="$registered"

echo "a VoLTE call: volte-a calls volte-b (TS 24.229 5.1.4.1, 6.1.2; RFC 3312; TS 29.214 A.1)"
expect "INVITE volte-a -> P-CSCF:5064 over TCP: From the default public identity, precondition, session timer, AMR-WB first, curr:qos local none" "$invite && ip.src == $va_ip && tcp.dstport == 5064 && sip.from.addr == \"$default_a\" && sip.Supported contains \"precondition\" && sip.Session-Expires && sip.P-Access-Network-Info && sdp.media_attr == \"rtpmap:96 AMR-WB/16000/1\" && sdp.media_attr == \"curr:qos local none\" && sdp.media_attr == \"des:qos mandatory local sendrecv\""
a="$(field sip.Call-ID)"
call_start="$last"
mark
expect "INVITE P-CSCF -> S-CSCF: P-Asserted-Identity the default public identity (TS 24.229 5.2.6.3.1)" "$invite && ip.src == $pcscf && ip.dst == $scscf && sip.Call-ID == \"$a\" && sip.P-Asserted-Identity == \"<$default_a>\""
back
expect "INVITE P-CSCF -> volte-b at its protected server port 5062, without P-Asserted-Service (RFC 6050 5.1.2) or the caller's P-Access-Network-Info (TS 24.229 4.4.3)" "$invite && ip.src == $pcscf && ip.dst == $vb_ip && tcp.dstport == 5062 && !sip.P-Asserted-Service && !sip.P-Access-Network-Info"
c="$(field sip.Call-ID)"
expect "183 volte-b: Require precondition, RSeq, the answer in AMR-WB with conf:qos remote sendrecv" "sip.Status-Code == 183 && ip.src == $vb_ip && sip.Call-ID == \"$c\" && sip.Require contains \"precondition\" && sip.RSeq && sdp.media_attr == \"rtpmap:96 AMR-WB/16000/1\" && sdp.media_attr == \"conf:qos remote sendrecv\""
expect "AAR P-CSCF -> PCRF for volte-b's media: Media-Component-Description, the MMTel ICSI" "$aar && diameter.Framed-IP-Address.IPv4 == $vb_ip && diameter.Media-Component-Description && diameter.Codec-Data contains \"AMR-WB\" && diameter.AF-Application-Identifier == \"urn:urn-7:3gpp-service.ims.icsi.mmtel\""
session_b="$(field diameter.Session-Id)"
mark
expect "AAA 2001" "$(answer_to) && diameter.Result-Code == 2001"
back
expect "ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST to volte-b, QCI 1" "$(event_to $vb_ip "ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST") && frame contains \"\\\"qci\\\":1\""
expect "ACTIVATE DEDICATED EPS BEARER CONTEXT ACCEPT from volte-b" "$(ue_api 001010000000012 activate-dedicated-eps-bearer-context-accept)"
back
expect "183 S-CSCF -> P-CSCF for volte-a" "sip.Status-Code == 183 && ip.dst == $pcscf && sip.Call-ID == \"$a\""
expect "AAR P-CSCF -> PCRF for volte-a's media" "$aar && diameter.Framed-IP-Address.IPv4 == $va_ip && diameter.Media-Component-Description && diameter.Codec-Data contains \"AMR-WB\""
session_a="$(field diameter.Session-Id)"
mark
expect "AAA 2001" "$(answer_to) && diameter.Result-Code == 2001"
back
expect "ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST to volte-a" "$(event_to $va_ip "ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST")"
expect "ACTIVATE DEDICATED EPS BEARER CONTEXT ACCEPT from volte-a" "$(ue_api 001010000000010 activate-dedicated-eps-bearer-context-accept)"
back
expect "183 reaches volte-a" "sip.Status-Code == 183 && ip.dst == $va_ip && sip.Call-ID == \"$a\""
expect "PRACK volte-a -> P-CSCF" "sip.Method == \"PRACK\" && ip.src == $va_ip && sip.RAck"
expect "200 OK to the PRACK reaches volte-a" "sip.Status-Code == 200 && sip.CSeq.method == \"PRACK\" && ip.dst == $va_ip"
expect "UPDATE volte-a: curr:qos local sendrecv, its resources are reserved (RFC 3312 5.1)" "sip.Method == \"UPDATE\" && ip.src == $va_ip && sdp.media_attr == \"curr:qos local sendrecv\""
expect "UPDATE reaches volte-b" "sip.Method == \"UPDATE\" && ip.dst == $vb_ip && sip.Call-ID == \"$c\""
expect "200 OK to the UPDATE from volte-b: every precondition met" "sip.Status-Code == 200 && sip.CSeq.method == \"UPDATE\" && ip.src == $vb_ip && sdp.media_attr == \"curr:qos local sendrecv\" && sdp.media_attr == \"curr:qos remote sendrecv\""
updated="$last"
expect "180 volte-b, only now that every mandatory precondition is met (RFC 3312 6)" "sip.Status-Code == 180 && ip.src == $vb_ip && sip.Call-ID == \"$c\""
none "180 from volte-b before its 200 OK to the UPDATE" "frame.number > $call_start && frame.number < $updated && sip.Status-Code == 180 && ip.src == $vb_ip"
expect "200 OK to the INVITE from volte-b: Session-Expires with the refresher (RFC 4028 9)" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.src == $vb_ip && sip.Call-ID == \"$c\" && sip.Session-Expires contains \"refresher=\""
answered="$last"
expect "200 OK reaches volte-a" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.dst == $va_ip && sip.Call-ID == \"$a\""
expect "ACK volte-a -> P-CSCF" "sip.Method == \"ACK\" && ip.src == $va_ip && sip.Call-ID == \"$a\""
# The media flows both ways at once, so each stream is checked from the
# answer on.
last="$answered"
mark
expect "RTP volte-a -> relay in AMR-WB: CMR 15, one frame of FT 8 (RFC 4867 4.3)" "amr && ip.src == $va_ip && ip.dst == $agw && amr.wb.cmr == 15 && amr.wb.toc.ft == 8"
expect "RTP relay -> volte-b in AMR-WB" "amr && ip.src == $agw && ip.dst == $vb_ip"
back
expect "RTP volte-b -> relay in AMR-WB" "amr && ip.src == $vb_ip && ip.dst == $agw"
back
expect "RTCP sender report from volte-a (RFC 3550 6.4.1)" "rtcp.pt == 200 && ip.src == $va_ip"
back
expect "RTCP sender report from volte-b" "rtcp.pt == 200 && ip.src == $vb_ip"
back
expect "BYE volte-a -> P-CSCF with Reason RELEASE_CAUSE (TS 24.229 7.2A.18.11)" "sip.Method == \"BYE\" && ip.src == $va_ip && sip.Call-ID == \"$a\" && sip.Reason contains \"RELEASE_CAUSE\""
bye="$last"
# The STA and the release of the bearer run side by side, on each side.
expect "STR P-CSCF -> PCRF for volte-a's session, DIAMETER_LOGOUT (TS 29.214 4.4.4)" "$str && diameter.Session-Id == \"$session_a\" && diameter.Termination-Cause == 1"
mark
expect "STA 2001" "$(answer_to) && diameter.Result-Code == 2001"
back
expect "DEACTIVATE EPS BEARER CONTEXT REQUEST to volte-a" "$(event_to $va_ip "DEACTIVATE EPS BEARER CONTEXT REQUEST")"
expect "DEACTIVATE EPS BEARER CONTEXT ACCEPT from volte-a" "$(ue_api 001010000000010 deactivate-eps-bearer-context-accept)"
last="$bye"
expect "BYE reaches volte-b" "sip.Method == \"BYE\" && ip.dst == $vb_ip && sip.Call-ID == \"$c\""
expect "STR P-CSCF -> PCRF for volte-b's session" "$str && diameter.Session-Id == \"$session_b\""
mark
expect "STA 2001" "$(answer_to) && diameter.Result-Code == 2001"
back
expect "DEACTIVATE EPS BEARER CONTEXT REQUEST to volte-b" "$(event_to $vb_ip "DEACTIVATE EPS BEARER CONTEXT REQUEST")"
expect "DEACTIVATE EPS BEARER CONTEXT ACCEPT from volte-b" "$(ue_api 001010000000012 deactivate-eps-bearer-context-accept)"
call_end="$last"
# The relay passes the RTP payloads on as they are: what volte-a sent is
# what volte-b got, packet by packet. Packets quoted in ICMP do not
# count: volte-b's last packets can reach the relay after its context
# has gone, and the port unreachable from the relay to volte-b quotes
# one, with both addresses in it.
sent="$(tshark_fields "frame.number > $answered && frame.number < $bye && amr && !icmp && ip.src == $va_ip && ip.dst == $agw" rtp.payload | tr -d ':\n')"
relayed="$(tshark_fields "frame.number > $answered && frame.number < $call_end && amr && !icmp && ip.src == $agw && ip.dst == $vb_ip" rtp.payload | tr -d ':\n')"
if [ -n "$sent" ] && [ "$sent" = "$relayed" ]; then
	echo "ok   the relay passed volte-a's AMR-WB payloads to volte-b unchanged, ${#sent} hex digits"
else
	echo "FAIL the AMR-WB payloads on the wire differ: ${#sent} sent, ${#relayed} relayed hex digits"
	fail=1
fi

echo "a VoLTE UE and a UE that offers PCMU only"
expect "INVITE volte-a -> P-CSCF to ue2, offering AMR-WB, AMR and PCMU" "$invite && ip.src == $va_ip && sip.r-uri == \"tel:+12025550102\" && sdp.media_attr == \"rtpmap:96 AMR-WB/16000/1\" && sdp.media_attr == \"rtpmap:0 PCMU/8000\""
a="$(field sip.Call-ID)"
interop="$last"
expect "INVITE P-CSCF -> ue2 with the same offer" "$invite && ip.dst == $ue2ip && sdp.media_attr == \"rtpmap:96 AMR-WB/16000/1\""
expect "183 ue2: PCMU only" "sip.Status-Code == 183 && ip.src == $ue2ip && sdp.media_attr == \"rtpmap:0 PCMU/8000\" && !(sdp.media_attr contains \"AMR\")"
expect "AAR P-CSCF -> PCRF for volte-a's media, with the answer in PCMU" "$aar && diameter.Framed-IP-Address.IPv4 == $va_ip && diameter.Codec-Data contains \"RTP/AVP 0 101\""
expect "200 OK reaches volte-a" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.dst == $va_ip && sip.Call-ID == \"$a\""
expect "RTP relay -> volte-a in PCMU" "rtp.p_type == 0 && ip.src == $agw && ip.dst == $va_ip"
none "AAR for the media of ue2, which is not on the EPC stub" "frame.number > $interop && $aar && diameter.Framed-IP-Address.IPv4 == $ue2ip && diameter.Codec-Data"

echo "a short message from volte-a (TS 24.341 5.3.1.2)"
expect "MESSAGE volte-a -> P-CSCF: From the default public identity" "sip.Method == \"MESSAGE\" && ip.src == $va_ip && sip.from.addr == \"$default_a\""
m="$(field sip.Call-ID)"
expect "MESSAGE P-CSCF -> S-CSCF: P-Asserted-Identity the default public identity" "sip.Method == \"MESSAGE\" && ip.src == $pcscf && ip.dst == $scscf && sip.Call-ID == \"$m\" && sip.P-Asserted-Identity == \"<$default_a>\""
expect "202 Accepted reaches volte-a" "sip.Status-Code == 202 && ip.dst == $va_ip && sip.Call-ID == \"$m\""

echo "volte-a reads its supplementary services over Ut (GSMA IR.92 2.3.2)"
expect "XCAP GET volte-a -> MMTel AS: the XUI is the default public identity" "http.request.method == \"GET\" && ip.src == $va_ip && http.request.uri contains \"/users/$default_a/simservs.xml\""

echo "DTMF, hold and resume in a call that the dashboard drives (RFC 4733, TS 24.610)"
expect "INVITE volte-a -> P-CSCF to volte-b" "$invite && ip.src == $va_ip && sip.r-uri == \"tel:+12025550112\" && !(sip.To contains \"tag=\")"
a="$(field sip.Call-ID)"
expect "200 OK reaches volte-a" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.dst == $va_ip && sip.Call-ID == \"$a\""
for digit in 1 5 9 11; do
	expect "telephone-event $digit, end of the event, volte-a -> relay" "rtpevent && ip.src == $va_ip && rtpevent.event_id == $digit && rtpevent.end_of_event == 1"
	expect "the same event relayed to volte-b" "rtpevent && ip.dst == $vb_ip && rtpevent.event_id == $digit"
done
expect "re-INVITE volte-a: sendonly, on hold" "$invite && ip.src == $va_ip && sip.Call-ID == \"$a\" && sdp.media_attr == \"sendonly\""
expect "200 OK from volte-b: recvonly" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.src == $vb_ip && sdp.media_attr == \"recvonly\""
expect "re-INVITE volte-a: sendrecv, resumed" "$invite && ip.src == $va_ip && sip.Call-ID == \"$a\" && sdp.media_attr == \"sendrecv\""
expect "200 OK from volte-b: sendrecv" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.src == $vb_ip && sdp.media_attr == \"sendrecv\""
expect "BYE volte-a -> P-CSCF" "sip.Method == \"BYE\" && ip.src == $va_ip && sip.Call-ID == \"$a\""

echo "the network takes volte-a's voice bearer (IR.92 2.4.2.3; TS 29.214 4.4.6.2; TS 24.229 5.2.8.1.2)"
expect "INVITE volte-a -> P-CSCF to volte-b" "$invite && ip.src == $va_ip && sip.r-uri == \"tel:+12025550112\" && !(sip.To contains \"tag=\")"
a="$(field sip.Call-ID)"
expect "200 OK reaches volte-a" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.dst == $va_ip && sip.Call-ID == \"$a\""
expect "DELETE dashboard -> EPC stub: the network deactivates bearer $ebi" "http.request.method == \"DELETE\" && ip.src == $dashboard && ip.dst == $epc && http.request.uri == \"/network-initiated/ues/001010000000010/bearers/$ebi\""
released="$last"
mark
expect "DEACTIVATE EPS BEARER CONTEXT REQUEST to volte-a" "$(event_to $va_ip "DEACTIVATE EPS BEARER CONTEXT REQUEST")"
back
expect "ASR PCRF -> P-CSCF: BEARER_RELEASED" "$asr && diameter.Abort-Cause == 0"
aborted="$(field diameter.Session-Id)"
expect "ASA 2001" "$(answer_to) && diameter.Result-Code == 2001"
expect "STR P-CSCF -> PCRF for that session" "$str && diameter.Session-Id == \"$aborted\""
mark
expect "BYE P-CSCF -> volte-a with Reason SIP 503" "sip.Method == \"BYE\" && ip.src == $pcscf && ip.dst == $va_ip && sip.Call-ID == \"$a\" && sip.Reason contains \"cause=503\""
back
expect "BYE P-CSCF -> S-CSCF in volte-a's name, Reason SIP 503" "sip.Method == \"BYE\" && ip.src == $pcscf && ip.dst == $scscf && sip.Call-ID == \"$a\" && sip.Reason contains \"cause=503\""
expect "BYE reaches volte-b with that Reason" "sip.Method == \"BYE\" && ip.dst == $vb_ip && sip.Reason contains \"cause=503\""
expect "DEACTIVATE EPS BEARER CONTEXT REQUEST to volte-b: its bearer goes with the call" "$(event_to $vb_ip "DEACTIVATE EPS BEARER CONTEXT REQUEST")"
none "BYE of volte-a's own in that call" "frame.number > $released && sip.Method == \"BYE\" && ip.src == $va_ip && sip.Call-ID == \"$a\""

echo "the capture as a whole"
# The charging information stays in the network (TS 24.229 5.2.1):
# the P-CSCF sends none of it to a UE. Every UE of this setup has an
# address from 172.28.0.100 up.
none "P-Charging-Vector or P-Charging-Function-Addresses from the P-CSCF to a UE" 'ip.src == 172.28.0.40 && ip.dst >= 172.28.0.100 && (sip.P-Charging-Vector || sip.P-Charging-Function-Addresses)'
# The temporary public identities of the VoLTE UEs serve REGISTER only
# (TS 23.228 E.3.1): no other request or response names them.
none "a temporary public identity outside REGISTER" "((sip && !(sip.CSeq.method == \"REGISTER\")) || http) && (frame contains \"$temporary_a\" || frame contains \"sip:001010000000012@$domain\")"
# The user part of a UE's contact is a UUID of its own, not its IMSI
# (TS 24.229 5.1.1.2.1 item c), in REGISTER and in its calls alike.
none "the IMSI of a VoLTE UE in a Contact" 'sip.Contact contains "001010000000010" || sip.Contact contains "001010000000012"'
# A request to the UE has that contact as its Request-URI.
none "the IMSI of a VoLTE UE in the Request-URI of a request to it" 'sip.r-uri contains "001010000000010@172." || sip.r-uri contains "001010000000012@172."'
none "malformed SIP, SDP, Diameter, HTTP, JSON, RTP, RTCP or AMR packets" '(sip || sdp || diameter || http || json || rtp || rtcp || amr || rtpevent) && (_ws.malformed || _ws.expert.severity >= warning)'
# RFC 3261 18.1.1: a request over 1300 bytes does not go over UDP.
none "SIP requests over 1300 bytes on UDP" 'sip.Method && udp && udp.length > 1308'

# The ladder of the registrations, the calls, Rx and the EPC stub's API,
# for the guide and the CI artifact.
docker compose exec -T capture tshark -r "$pcap" -T fields -E separator=/t \
	-Y '(sip && !(sip.CSeq.method == "SUBSCRIBE") && !(sip.CSeq.method == "NOTIFY") && !(sip.CSeq.method == "MESSAGE")) || (diameter && !(diameter.cmd.code == 280) && !(diameter.cmd.code == 257)) || (http && ip.addr == 172.28.0.80)' \
	-e frame.number -e frame.time_relative -e ip.src -e ip.dst -e _ws.col.Info -e sip.Call-ID | tr -d '\r' >e2e-out/volte-ladder.txt

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color | grep -E '"level":"(WARN|ERROR)"' | tail -n 40
fi
exit "$fail"
