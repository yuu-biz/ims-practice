#!/usr/bin/env bash
# End-to-end check: the MRF, which the MMTel AS drives with the
# Request-URIs of RFC 4240.
#
# UE 1 bars its outgoing calls and calls UE 2: it hears the announcement
# notice from the MRFP before the 603. Then UE 2 gets the customized
# alerting tone cat1 through the dashboard, and UE 1 hears it while UE 2
# rings; the audio that UE 1 received is compared with the WAV file. A
# call after the tone is switched off rings with UE 1's own ringing
# tone. Last, the three UE simulators of the sim profile, driven
# through the dashboard: ue-a calls ue-b, ue-c calls ue-a, whose second
# call waits; ue-a answers it, which holds the first, swaps between the
# two and merges them into a conference whose audio the MRFP mixes. The
# checks read the capture.
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
for i in $(seq 1 30); do
	curl -fsS http://localhost:8080/api/calls >/dev/null 2>&1 && break
	sleep 1
done
# Every "run" and "up" below takes --no-deps (see scripts/e2e-lib.sh).
remember_nodes

domain=ims.mnc001.mcc001.3gppnetwork.org
ue2=(--impi "001010000000002@$domain" --impu "sip:001010000000002@$domain" --password test-password-2)
fail=0
mkdir -p e2e-out
rm -f captures/mrf-cat-received.raw

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
# call ARGS... is UE 1's "ue call", whose log it prints.
call() { docker compose run --rm --no-deps ue call --tone 1s "$@" 2>&1 || true; }
# xcap ARGS... is UE 1's "ue xcap".
xcap() { docker compose run --rm --no-deps ue xcap "$@" 2>&1 || true; }
# alerting_tone TONE true|false provisions the CAT of UE 2 in the HSS,
# through the dashboard, as the learner does with the Subscribers panel.
# The status code is read from the end of the output, since curl on
# Windows cannot write to /dev/null when MSYS_NO_PATHCONV is set.
alerting_tone() {
	curl -s -w '
%{http_code}' -X PUT -H 'Content-Type: application/json' \
		-d "{\"tone\":\"$1\",\"active\":$2}" \
		"http://localhost:8080/api/subscribers/001010000000002%40$domain/customized-alerting-tone" | tail -n 1
}

echo "an announcement before a barred call is rejected"
if grep -q '"active": true' <<<"$(xcap set baoc on)"; then
	echo "ok   UE 1 bars its outgoing calls over Ut"
else
	echo "FAIL UE 1 could not set baoc"
	fail=1
fi
out="$(call tel:+12025550102)"
printf '%s\n' "$out" >e2e-out/mrf-barred.log
if grep -q '"msg":"early media authorization"' <<<"$out" && grep -q '"call failed: 603 ' <<<"$out"; then
	echo "ok   UE 1 got early media, and then 603"
else
	echo "FAIL the barred call: $(tail -n 2 <<<"$out")"
	fail=1
fi
xcap set baoc off >/dev/null

echo "CAT: UE 2's callers hear cat1 while it rings"
status="$(alerting_tone cat1 true)"
if [ "$status" = 200 ]; then
	echo "ok   the dashboard provisioned cat1 for UE 2 in the HSS"
else
	echo "FAIL provisioning cat1 for UE 2: HTTP $status"
	fail=1
fi
docker compose run -d --no-deps --name ims-practice-ue2-cat ue2 answer "${ue2[@]}" --ring 3s --tone 1s >/dev/null
wait_log ims-practice-ue2-cat '"msg":"waiting for a call"' || true
out="$(call --received-out /captures/mrf-cat-received.raw tel:+12025550102)"
printf '%s\n' "$out" >e2e-out/mrf-cat.log
if grep -Eq '"msg":"ringback before the answer".*"ringback":"network"' <<<"$out" && grep -q '"msg":"call answered"' <<<"$out" &&
	[ "$(ended ims-practice-ue2-cat)" = 0 ]; then
	echo "ok   UE 1 heard network early media until UE 2 answered"
else
	echo "FAIL the call with CAT: $(grep -E '"msg":"(ringback before the answer|call progress|ue failed)"' <<<"$out" | tail -n 3)"
	fail=1
fi
# The MRFP encodes the 16-bit samples of the WAV file to PCMU (G.711
# mu-law) and plays it from the start, over and over. The first second
# that UE 1 received must be one second of that loop.
if "$python" - deploy/announcements/cat1.wav captures/mrf-cat-received.raw <<'EOF'; then
import struct, sys, wave

def mulaw(sample):
    # ITU-T G.711, as internal/g711 encodes it.
    sign = 0
    if sample < 0:
        sample, sign = -sample, 0x80
    sample = min(sample, 32635) + 0x84
    segment = 7
    mask = 0x4000
    while sample & mask == 0 and segment > 0:
        segment -= 1
        mask >>= 1
    return ~(sign | segment << 4 | (sample >> (segment + 3)) & 0x0F) & 0xFF

with wave.open(sys.argv[1]) as w:
    frames = w.readframes(w.getnframes())
tone = bytes(mulaw(s) for (s,) in struct.iter_unpack("<h", frames))
received = open(sys.argv[2], "rb").read()
second = received[:8000]
sys.exit(0 if len(second) == 8000 and second in tone * 3 else 1)
EOF
	echo "ok   the first second UE 1 received is cat1.wav as the MRFP encodes it"
else
	echo "FAIL the audio UE 1 received is not cat1.wav ($(wc -c <captures/mrf-cat-received.raw 2>/dev/null || echo 0) bytes)"
	fail=1
fi
status="$(alerting_tone cat1 false)"
docker compose run -d --no-deps --name ims-practice-ue2-local ue2 answer "${ue2[@]}" --ring 2s --tone 1s >/dev/null
wait_log ims-practice-ue2-local '"msg":"waiting for a call"' || true
out="$(call tel:+12025550102)"
printf '%s\n' "$out" >e2e-out/mrf-local.log
if [ "$status" = 200 ] && grep -Eq '"msg":"ringback before the answer".*"ringback":"local"' <<<"$out" &&
	[ "$(ended ims-practice-ue2-local)" = 0 ]; then
	echo "ok   with CAT off, UE 1 played its own ringing tone"
else
	echo "FAIL the call without CAT (HTTP $status): $(grep -E '"msg":"(ringback before the answer|ue failed)"' <<<"$out" | tail -n 2)"
	fail=1
fi

echo "call waiting and a conference, driven through the dashboard"
# ues prints "name registered|unregistered|unreachable calls conference"
# for each UE of the sim profile, the UEs separated by commas: calls is
# the state of each call on the line, joined by "+", with "/held" and
# "/waiting"; conference is the number of its users that are connected.
ues() {
	curl -fsS http://localhost:8080/api/ues | "$python" -c '
import json, sys
out = []
for u in json.load(sys.stdin):
    if u["name"] not in ("ue-a", "ue-b", "ue-c"):
        continue
    s = u["status"] or {}
    reg = "unreachable" if not u["reachable"] else "registered" if s["registered"] else "unregistered"
    calls = "+".join(c["state"] + ("/held" if (c.get("session") or {}).get("held") else "") + ("/waiting" if c.get("waiting") else "")
                     for c in s.get("calls") or []) or "idle"
    conf = s.get("conference") or {}
    users = sum(1 for x in conf.get("users") or [] if x.get("status") == "connected")
    out.append(u["name"] + " " + reg + " " + calls + " " + str(users))
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
%{http_code}' -X POST -H 'Content-Type: application/json' -d "${2:-}" "http://localhost:8080/api/ues/$1" | tail -n 1
}
# call_ids NAME prints the Call-IDs of the calls of the UE, oldest first.
call_ids() {
	curl -fsS http://localhost:8080/api/ues | "$python" -c '
import json, sys
for u in json.load(sys.stdin):
    if u["name"] == sys.argv[1]:
        print(" ".join(c["call_id"] for c in (u["status"] or {}).get("calls") or []))
' "$1" | tr -d '\r'
}
# They restart by themselves and hold subscribers 1 to 3, so they must
# not outlive the script.
trap 'docker compose --profile sim rm -sf ue-a ue-b ue-c >/dev/null 2>&1' EXIT
docker compose --profile sim up -d --no-deps ue-a ue-b ue-c
if wait_ues "ue-a registered idle 0,ue-b registered idle 0,ue-c registered idle 0"; then
	echo "ok   ue-a, ue-b and ue-c registered"
else
	echo "FAIL the UE simulators did not register: $(ues)"
	exit 1
fi
status="$(ue_post ue-a/call '{"target":"tel:+12025550102"}')"
if [ "$status" = 200 ] && wait_ues "ue-a registered active 0,ue-b registered active 0,*"; then
	echo "ok   ue-a called ue-b, which answered"
else
	echo "FAIL ue-a's call to ue-b (status $status): $(ues)"
	exit 1
fi
first="$(call_ids ue-a)"
status="$(ue_post ue-c/call '{"target":"tel:+12025550101"}')"
if [ "$status" = 200 ] && wait_ues "ue-a registered active+ringing/waiting 0,*"; then
	echo "ok   ue-c's call to ue-a waits"
else
	echo "FAIL the waiting call (status $status): $(ues)"
	exit 1
fi
status="$(ue_post ue-a/answer)"
if [ "$status" = 200 ] && wait_ues "ue-a registered active/held+active 0,ue-b registered active 0,ue-c registered active 0"; then
	echo "ok   ue-a held ue-b and answered ue-c"
else
	echo "FAIL answering the waiting call (status $status): $(ues)"
	fail=1
fi
second="$(call_ids ue-a | cut -d' ' -f2)"
sleep 1
status="$(ue_post ue-a/swap)"
if [ "$status" = 200 ] && wait_ues "ue-a registered active+active/held 0,*"; then
	echo "ok   ue-a swapped to ue-b and held ue-c"
else
	echo "FAIL the swap (status $status): $(ues)"
	fail=1
fi
sleep 1
status="$(ue_post ue-a/merge)"
if [ "$status" = 200 ] && wait_ues "ue-a registered active 3,ue-b registered active 3,ue-c registered active 3"; then
	echo "ok   ue-a merged the calls, and each UE sees three users in the conference"
else
	echo "FAIL the merge (status $status): $(ues)"
	fail=1
fi
conference="$(call_ids ue-a)"
sleep 3
status="$(ue_post ue-a/hangup)"
if [ "$status" = 200 ] && wait_ues "ue-a registered idle 0,ue-b registered idle 0,ue-c registered idle 0"; then
	echo "ok   ue-a left, and the conference ended for all three"
else
	echo "FAIL ending the conference (status $status): $(ues)"
	fail=1
fi
docker compose --profile sim rm -sf ue-a ue-b ue-c >/dev/null 2>&1
trap - EXIT
same_nodes
sleep 2

pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
echo "reading $pcap"

# RTP is found by the heuristic too, as some of it flows before tshark
# has seen its SDP.
tshark_options=(-o rtp.heuristic_rtp:TRUE)
last=0
# at_least checks that the filter finds at least n distinct values of
# the field, such as one INVITE per party by its Call-ID. The one of
# e2e-diameter.sh counts frames.
at_least() {
	local label="$1" n="$2" filter="$3" field="$4" got
	got="$(tshark_fields "$filter" "$field" | sort -u | grep -c . || true)"
	if [ "$got" -ge "$n" ]; then
		echo "ok   $label: $got"
	else
		echo "FAIL $label: $got, want $n or more"
		fail=1
	fi
}

ue1=172.28.0.100
ue2ip=172.28.0.101
uea=172.28.0.110
ueb=172.28.0.111
uec=172.28.0.112
pcscf=172.28.0.40
scscf=172.28.0.30
agw=172.28.0.50
mmtel=172.28.0.75
mrfc=172.28.0.85
mrfp=172.28.0.86
invite='sip.Method == "INVITE"'
from_ue1="$invite && ip.src == $ue1 && ip.dst == $pcscf && sip.r-uri == \"tel:+12025550102\""
ok200='sip.Status-Code == 200 && sip.CSeq.method == "INVITE"'

echo "an announcement, then the rejection (RFC 4240 3, TS 24.628 4.2.4 and Annex G, TS 24.611 4.5.2.4.1)"
expect "INVITE UE 1 -> P-CSCF" "$from_ue1"
a="$(field sip.Call-ID)"
expect "INVITE MMTel AS -> MRFC: annc, play=/provisioned/notice, no SDP" "$invite && ip.src == $mmtel && ip.dst == $mrfc && sip.r-uri contains \"annc@\" && sip.r-uri contains \"play=/provisioned/notice\" && !sdp"
expect "200 OK MRFC -> MMTel AS: the offer of the MRFP, PCMU, sendonly" "$ok200 && ip.src == $mrfc && sdp.connection_info.address == \"$mrfp\" && sdp.media.format contains \"PCMU\" && sdp.media_attr == \"sendonly\""
expect "183 reaches UE 1 with P-Early-Media: sendonly, SDP of the MRFP" "sip.Status-Code == 183 && sip.Call-ID == \"$a\" && ip.dst == $ue1 && sip.P-Early-Media == \"sendonly\" && sdp && sip.Require contains \"100rel\""
expect "PRACK UE 1 -> P-CSCF" "sip.Method == \"PRACK\" && sip.Call-ID == \"$a\" && ip.src == $ue1"
expect "ACK MMTel AS -> MRFC with the answer: the MRFP starts" "sip.Method == \"ACK\" && ip.src == $mmtel && ip.dst == $mrfc && sdp.media_attr == \"recvonly\""
ack="$last"
expect "RTP of notice MRFP -> media relay" "rtp && ip.src == $mrfp && ip.dst == $agw && rtp.p_type == 0"
expect "RTP of notice media relay -> UE 1" "rtp && ip.src == $agw && ip.dst == $ue1"
expect "BYE MRFC -> MMTel AS once notice has played" "sip.Method == \"BYE\" && ip.src == $mrfc && ip.dst == $mmtel"
played="$(tshark_fields "frame.number == $last" frame.time_relative)"
started="$(tshark_fields "frame.number == $ack" frame.time_relative)"
if "$python" -c 'import sys; d = float(sys.argv[2]) - float(sys.argv[1]); sys.exit(0 if 1.0 <= d < 1.5 else 1)' "$started" "$played"; then
	echo "ok   notice played for the 1.08 s of its WAV file, ACK to BYE"
else
	echo "FAIL notice played from $started s to $played s"
	fail=1
fi
expect "603 reaches UE 1 with Reason: SIP;cause=603" "sip.Status-Code == 603 && sip.Call-ID == \"$a\" && ip.dst == $ue1 && sip.Reason contains \"cause=603\""
none "INVITEs from the MMTel AS toward UE 2 for the barred call" "frame.number > $ack && frame.number < $last && $invite && ip.src == $mmtel && ip.dst != $mrfc"

echo "CAT (TS 24.182 4.5.5.3.7, TS 24.628 Annex G)"
expect "INVITE UE 1 -> P-CSCF" "$from_ue1"
a="$(field sip.Call-ID)"
cat_start="$last"
expect "INVITE MMTel AS -> MRFC: play=/provisioned/cat1;repeat=forever" "$invite && ip.src == $mmtel && ip.dst == $mrfc && sip.r-uri contains \"play=/provisioned/cat1\" && sip.r-uri contains \"repeat=forever\""
mrfc_leg="$(field sip.Call-ID)"
expect "INVITE P-CSCF -> UE 2" "$invite && ip.dst == $ue2ip"
# UE 1 uses preconditions, so the callee's 183 goes to UE 1 as it came,
# and the tone comes in an UPDATE once the resources are reserved
# (TS 24.182 4.5.5.3.7 b 1).
expect "183 reaches UE 1: the callee's answer, with the precondition table" "sip.Status-Code == 183 && sip.Call-ID == \"$a\" && ip.dst == $ue1 && sdp.media_attr contains \"curr:qos\""
expect "UPDATE reaches UE 1: P-Early-Media sendonly, SDP of the MRFP with a=content:g.3gpp.cat" "sip.Method == \"UPDATE\" && sip.Call-ID == \"$a\" && ip.dst == $ue1 && sip.P-Early-Media == \"sendonly\" && sdp.media_attr == \"content:g.3gpp.cat\" && sdp.media_attr == \"sendonly\""
expect "ACK MMTel AS -> MRFC: the tone starts" "sip.Method == \"ACK\" && ip.src == $mmtel && ip.dst == $mrfc && sip.Call-ID == \"$mrfc_leg\""
expect "RTP of cat1 MRFP -> media relay" "rtp && ip.src == $mrfp && ip.dst == $agw"
expect "RTP of cat1 reaches UE 1 before the answer" "rtp && ip.src == $agw && ip.dst == $ue1"
expect "200 OK UE 2 -> P-CSCF: UE 2 answers" "$ok200 && ip.src == $ue2ip"
expect "BYE MMTel AS -> MRFC: the tone stops at the answer" "sip.Method == \"BYE\" && ip.src == $mmtel && ip.dst == $mrfc && sip.Call-ID == \"$mrfc_leg\""
expect "UPDATE reaches UE 1: its media goes to UE 2, without the content attribute" "sip.Method == \"UPDATE\" && sip.Call-ID == \"$a\" && ip.dst == $ue1 && sdp && !(sdp.media_attr == \"content:g.3gpp.cat\")"
expect "200 OK (INVITE) reaches UE 1" "$ok200 && sip.Call-ID == \"$a\" && ip.dst == $ue1"
# The dashboard groups the dialogs of a call by their icid-value, so
# the AS's dialog with the MRFC is a leg of the call that UE 1 made.
legs="$(curl -fsS http://localhost:8080/api/calls | "$python" -c '
import json, sys
print(" ".join(next((c["legs"] for c in json.load(sys.stdin) if sys.argv[1] in c["legs"]), [])))
 ' "$a" | tr -d '\r')"
if [[ " $legs " == *" $mrfc_leg "* ]]; then
	echo "ok   the dashboard draws the AS's dialog with the MRFC in the ladder of UE 1's call"
else
	echo "FAIL the dashboard's call of $a has the legs $legs, not $mrfc_leg"
	fail=1
fi

echo "the same call with CAT off: the caller's own ringing tone (TS 24.628 4.7.2.1)"
expect "INVITE UE 1 -> P-CSCF" "$from_ue1"
a="$(field sip.Call-ID)"
local_start="$last"
expect "180 reaches UE 1, without P-Early-Media" "sip.Status-Code == 180 && sip.Call-ID == \"$a\" && ip.dst == $ue1 && !sip.P-Early-Media"
expect "200 OK (INVITE) reaches UE 1" "$ok200 && sip.Call-ID == \"$a\" && ip.dst == $ue1"
none "INVITEs to the MRFC in that call" "frame.number > $local_start && frame.number < $last && $invite && ip.dst == $mrfc"
none "RTP from the MRFP in that call" "frame.number > $local_start && frame.number < $last && rtp && ip.src == $mrfp"

echo "call waiting (TS 24.615 4.5.5.3.2, 4.5.5.2.3) and hold (TS 24.610)"
c1="${first%% *}"
c2="$second"
expect "INVITE ue-a -> P-CSCF to ue-b" "$invite && ip.src == $uea && sip.Call-ID == \"$c1\""
expect "200 OK reaches ue-a" "$ok200 && ip.dst == $uea && sip.Call-ID == \"$c1\""
expect "INVITE P-CSCF -> ue-a: the second call, from ue-c" "$invite && ip.dst == $uea && sip.Call-ID == \"$c2\""
expect "180 ue-a -> P-CSCF with Alert-Info: <urn:alert:service:call-waiting>" "sip.Status-Code == 180 && ip.src == $uea && sip.Call-ID == \"$c2\" && sip.Alert-Info contains \"call-waiting\""
waiting="$last"
expect "180 reaches ue-c without that Alert-Info: the caller is not told, as hss.yaml says" "sip.Status-Code == 180 && ip.dst == $uec && !(sip.Alert-Info contains \"call-waiting\")"
# ue-a answers once it rings, which it says when it has sent its 180;
# the 180 may still be on its way to ue-c.
last="$waiting"
expect "re-INVITE ue-a -> P-CSCF: the call with ue-b on hold, a=sendonly" "$invite && ip.src == $uea && sip.Call-ID == \"$c1\" && sdp.media_attr == \"sendonly\""
expect "200 OK ue-a -> P-CSCF: ue-a answers ue-c" "$ok200 && ip.src == $uea && sip.Call-ID == \"$c2\""
expect "re-INVITE ue-a: swap, ue-c on hold" "$invite && ip.src == $uea && sip.Call-ID == \"$c2\" && sdp.media_attr == \"sendonly\""
expect "re-INVITE ue-a: swap, ue-b back with a=sendrecv" "$invite && ip.src == $uea && sip.Call-ID == \"$c1\" && sdp.media_attr == \"sendrecv\""

echo "a conference of three (TS 24.147 5.3.1.3.3, 5.3.2; RFC 4240 5; RFC 4575)"
expect "INVITE ue-a -> P-CSCF to the conference factory URI" "$invite && ip.src == $uea && sip.r-uri contains \"mmtel@conf-factory.$domain\""
merged="$last"
expect "INVITE MMTel AS -> MRFC: conf=, with ue-a's offer" "$invite && ip.src == $mmtel && ip.dst == $mrfc && sip.r-uri contains \"conf=\" && sdp"
expect "200 OK reaches ue-a: the conference URI in the Contact, isfocus, a PSI in conf.$domain" "$ok200 && ip.dst == $uea && sip.Contact contains \"isfocus\" && sip.Contact contains \"@conf.$domain>\""
# A conference URI is a public service identity of the home network,
# with no port (TS 23.003 13.5), so it names no node.
none "a conference URI with the name of a node or a port" "sip.Contact contains \"isfocus\" && (sip.Contact contains \"@mmtel\" || sip.Contact matches \"@[^>]*:[0-9]+>\")"
# The SUBSCRIBE and the REFER go out side by side.
mark
expect "SUBSCRIBE ue-a -> P-CSCF to the conference URI" "sip.Method == \"SUBSCRIBE\" && ip.src == $uea && sip.r-uri contains \"@conf.$domain\""
# TS 23.228 5.4.12.3: DNS leads from the subdomain of the PSI to the AS
# that hosts it, by NAPTR and SRV as for the home domain (RFC 3263).
expect "NAPTR query for conf.$domain, the subdomain of the conference URIs" "dns.flags.response == 0 && dns.qry.type == 35 && dns.qry.name == \"conf.$domain\""
expect "SRV answer: _sip._udp.conf.$domain is the MMTel AS" "dns.flags.response == 1 && dns.qry.type == 33 && dns.qry.name == \"_sip._udp.conf.$domain\" && dns.srv.target == \"mmtel.$domain\""
expect "SUBSCRIBE S-CSCF -> MMTel AS, routed by the Request-URI" "sip.Method == \"SUBSCRIBE\" && ip.src == $scscf && ip.dst == $mmtel && sip.r-uri contains \"@conf.$domain\""
back
expect "REFER ue-a -> P-CSCF: bring in a peer, with Replaces in the Refer-To" "sip.Method == \"REFER\" && ip.src == $uea && sip.Refer-To contains \"Replaces\""
# The answer and the focus's INVITE to the peer go out side by side.
mark
expect "200 OK (REFER) MMTel AS -> S-CSCF: the focus accepts with 200, never 202 (RFC 7647 5)" "sip.Status-Code == 200 && sip.CSeq.method == \"REFER\" && ip.src == $mmtel"
expect "200 OK (REFER) reaches ue-a" "sip.Status-Code == 200 && sip.CSeq.method == \"REFER\" && ip.dst == $uea"
back
expect "INVITE P-CSCF -> a peer with Replaces: the focus takes over its call with ue-a" "$invite && (ip.dst == $ueb || ip.dst == $uec) && sip.Replaces"
expect "REFER ue-a -> P-CSCF: the other peer" "sip.Method == \"REFER\" && ip.src == $uea && sip.Refer-To contains \"Replaces\""
expect "INVITE P-CSCF -> the other peer with Replaces" "$invite && (ip.dst == $ueb || ip.dst == $uec) && sip.Replaces"
none "202 to a REFER (RFC 7647 5)" "sip.Status-Code == 202 && sip.CSeq.method == \"REFER\""
expect "NOTIFY of the conference event package to ue-a with the three users" "sip.Method == \"NOTIFY\" && ip.dst == $uea && sip.Event contains \"conference\" && frame matches \"(?s)<user .*<user .*<user \""
# The focus names each endpoint by a key of its own (RFC 4575 5.7), not
# by the participant's Contact, so the conference state shows no
# subscriber the address of a UE.
none "NOTIFY of the conference event package with an endpoint named by its address" 'sip.Method == "NOTIFY" && sip.Event contains "conference" && frame matches "<endpoint entity=.[^>]*172[.]28[.]"'
at_least "NOTIFYs of the conference event package with endpoints named by a key of the focus" 1 'sip.Method == "NOTIFY" && sip.Event contains "conference" && frame matches "<endpoint entity=.urn:uuid:"' frame.number
at_least "INVITEs MMTel AS -> MRFC to the conference, one per party" 3 "frame.number > $merged && $invite && ip.dst == $mrfc && sip.r-uri contains \"conf=\" && !sip.to.tag" sip.Call-ID
# The focus passes each party's answer to the MRFC in its ACK, so the
# MRFC sends each 200 once and does not retransmit it (RFC 3261 13.3.1.4).
oks="$(tshark_fields "frame.number > $merged && $ok200 && ip.src == $mrfc" sip.Call-ID)"
if [ "$(grep -c . <<<"$oks")" -ge 3 ] && [ "$(grep -c . <<<"$oks")" = "$(sort -u <<<"$oks" | grep -c .)" ]; then
	echo "ok   the MRFC sent each of its $(grep -c . <<<"$oks") 200s once: each got its ACK"
else
	echo "FAIL the MRFC sent its 200s again, an ACK is missing: $(tr '\n' ' ' <<<"$oks")"
	fail=1
fi
at_least "RTP streams MRFP -> media relay, the mix for each party" 3 "frame.number > $merged && rtp && ip.src == $mrfp && ip.dst == $agw" udp.dstport
at_least "RTP streams media relay -> MRFP, one from each party" 3 "frame.number > $merged && rtp && ip.src == $agw && ip.dst == $mrfp" udp.srcport
# Each party plays a tone of the same loudness when its session starts,
# and the tones of the parties overlap. The MRFP sums what the others
# send (TS 23.333 6.2.9.1), so the mix that some party gets is louder
# than any one input.
if tshark_fields "frame.number > $merged && rtp && (ip.src == $mrfp || ip.dst == $mrfp)" ip.src rtp.payload | "$python" -c '
import sys

def decode(b):
    # ITU-T G.711 mu-law.
    b = ~b & 0xFF
    s = (((b & 0x0F) << 3) + 0x84 << (b >> 4 & 7)) - 0x84
    return -s if b & 0x80 else s

peak = {True: 0, False: 0}
for line in sys.stdin:
    src, _, payload = line.strip().partition("|")
    if payload:
        loudest = max(abs(decode(b)) for b in bytes.fromhex(payload.replace(":", "")))
        mixed = src == sys.argv[1]
        peak[mixed] = max(peak[mixed], loudest)
print(peak[False], peak[True])
sys.exit(0 if peak[True] > peak[False] > 0 else 1)
' "$mrfp" >e2e-out/mrf-mix-peaks.txt; then
	echo "ok   the MRFP sent a mix louder than any one party: peaks $(cat e2e-out/mrf-mix-peaks.txt) (in, out)"
else
	echo "FAIL the MRFP sent no mix of two parties: peaks $(cat e2e-out/mrf-mix-peaks.txt 2>/dev/null) (in, out)"
	fail=1
fi
expect "BYE ue-a -> P-CSCF: the creator leaves" "sip.Method == \"BYE\" && ip.src == $uea && sip.Call-ID == \"${conference%% *}\""
left="$last"
# The focus ends the call of each party on its own, in no set order.
expect "BYE reaches ue-b: the conference ends (TS 24.147 5.3.2.7)" "sip.Method == \"BYE\" && ip.dst == $ueb"
last="$left"
expect "BYE reaches ue-c" "sip.Method == \"BYE\" && ip.dst == $uec"

echo "the capture as a whole"
# The charging information stays in the network (TS 24.229 5.2.1):
# the P-CSCF sends none of it to a UE. Every UE of this setup has an
# address from 172.28.0.100 up.
none "P-Charging-Vector or P-Charging-Function-Addresses from the P-CSCF to a UE" 'ip.src == 172.28.0.40 && ip.dst >= 172.28.0.100 && (sip.P-Charging-Vector || sip.P-Charging-Function-Addresses)'
none "malformed SIP, SDP, HTTP, JSON, XML or RTP packets" '(sip || sdp || http || json || xml || rtp) && (_ws.malformed || _ws.expert.severity >= warning)'
# RFC 3261 18.1.1: a request over 1300 bytes does not go over UDP.
none "SIP requests over 1300 bytes on UDP" 'sip.Method && udp && udp.length > 1308'

# The ladder of the calls and the MRF, for the guide and the CI artifact.
docker compose exec -T capture tshark -r "$pcap" -T fields -E separator=/t \
	-Y '(sip && !(sip.CSeq.method == "REGISTER") && !(sip.CSeq.method == "SUBSCRIBE" && !(sip.Event contains "conference")) && !(sip.CSeq.method == "NOTIFY" && !(sip.Event contains "conference") && !(sip.Event contains "refer")) && !(sip.CSeq.method == "MESSAGE")) || (http && (ip.addr == 172.28.0.85 || ip.addr == 172.28.0.86))' \
	-e frame.number -e frame.time_relative -e ip.src -e ip.dst -e _ws.col.Info -e sip.Call-ID | tr -d '\r' >e2e-out/mrf-ladder.txt

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color | grep -E '"level":"(WARN|ERROR)"' | tail -n 40
fi
exit "$fail"
