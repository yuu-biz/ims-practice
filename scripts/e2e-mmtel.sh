#!/usr/bin/env bash
# End-to-end check: the supplementary services of the MMTel AS.
# Three UE simulators: UE 1 calls, UE 2 is the served user who sets up
# its services over Ut, UE 3 is where UE 2's calls are diverted to.
#
# A plain call goes through the AS of each user. With OIR, UE 2 gets no
# identity of UE 1. UE 2 sets CFU over Ut (HTTP Digest, a PUT of one
# rule, Sh to the HSS), and a call to it rings UE 3 with History-Info
# while UE 1 gets 181. Then CFB (UE 2 busy), CFNR (UE 2 rings until the
# no reply timer of 5 s ends), CFNL (UE 2 not registered), incoming and
# outgoing barring, and last a call with every service off again.
# Before the AS rejects a barred call, the MRF plays an announcement to
# the caller as early media. The checks read the capture.
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
wait_for_open mmtel

domain=ims.mnc001.mcc001.3gppnetwork.org
ue2=(--impi "001010000000002@$domain" --impu "sip:+12025550102@$domain" --password test-password-2)
ue3=(--impi "001010000000003@$domain" --impu "sip:+12025550103@$domain" --password test-password-3)
remember_nodes
fail=0
pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"

# The UE simulators are ue, ue2 and ue3 of compose.yaml, with fixed
# addresses, so each runs once at a time.
flags_of() {
	case "$1" in
	ue2) printf '%s\n' "${ue2[@]}" ;;
	ue3) printf '%s\n' "${ue3[@]}" ;;
	esac
}
# xcap SERVICE ARGS... runs "ue xcap" as that UE, which needs no
# registration, and prints the settings it reports.
xcap() {
	local service="$1" flags
	shift
	mapfile -t flags < <(flags_of "$service")
	docker compose run --no-deps --rm "$service" xcap "${flags[@]}" "$@" 2>&1
}
# set_service WHO UE NAME on|off [TARGET] changes one service of that UE
# and checks that the settings say so afterwards.
set_service() {
	local who="$1" service="$2" name="$3" state="$4" out
	shift 4
	# A failed "ue xcap" is reported by the check below, from what it
	# printed, hence "|| true".
	out="$(xcap "$service" set "$name" "$state" "$@" || true)"
	# The settings are the JSON object at the end; docker compose may
	# write lines of its own before it.
	if "$python" -c '
import json, sys
text = sys.stdin.read()
settings = json.loads(text[text.index("{\n"):])
service = next(s for s in settings["services"] if s["name"] == sys.argv[1])
sys.exit(0 if service["active"] == (sys.argv[2] == "on") else 1)
' "$name" "$state" <<<"$out" 2>/dev/null; then
		echo "ok   $who sets $name $state over Ut"
	else
		echo "FAIL $who sets $name $state over Ut: $(tail -n 3 <<<"$out")"
		fail=1
	fi
}
# answer NAME SERVICE ARGS... has that UE wait, as container NAME, for
# one call that "ue answer" takes as ARGS say.
answer() {
	local name="$1" service="$2" flags
	shift 2
	mapfile -t flags < <(flags_of "$service")
	if ! docker compose run --no-deps -d --name "$name" "$service" answer "${flags[@]}" "$@" >/dev/null ||
		! wait_log "$name" '"msg":"waiting for a call"'; then
		echo "FAIL $name did not register"
		fail=1
	fi
}
# call ARGS... is UE 1's "ue call", whose log it prints. The UE exits 1
# when the call fails, as the barred calls do; the checks read the log,
# hence "|| true".
call() { docker compose run --no-deps --rm ue call --tone 1s "$@" 2>&1 || true; }
mkdir -p e2e-out

echo "a plain call through the MMTel AS of each user"
answer ims-practice-ue2-plain ue2 --ring 500ms --tone 1s
out="$(call tel:+12025550102)"
if grep -q '"msg":"call answered"' <<<"$out" && [ "$(ended ims-practice-ue2-plain)" = 0 ]; then
	echo "ok   UE 1 called UE 2, which answered"
else
	echo "FAIL the plain call: $(grep -E '"msg":"(call progress|ue failed)"' <<<"$out" | tail -n 2)"
	fail=1
fi

echo "OIR: UE 1 withholds its identity by default"
if grep -q '"oir_default_behaviour": "presentation-restricted"' <<<"$(xcap ue oir restricted)"; then
	echo "ok   UE 1 sets OIR to restricted by default over Ut"
else
	echo "FAIL UE 1 could not set OIR"
	fail=1
fi
answer ims-practice-ue2-oir ue2 --ring 500ms --tone 1s
out="$(call tel:+12025550102)"
status="$(ended ims-practice-ue2-oir)"
# Empty when UE 2 logged no incoming call, which the check reports.
incoming="$(grep '"msg":"incoming call"' e2e-out/ims-practice-ue2-oir.log || true)"
if [ "$status" = 0 ] && grep -q 'anonymous@anonymous.invalid' <<<"$incoming" && grep -q '"p_asserted_identity":""' <<<"$incoming"; then
	echo "ok   UE 2 got a call from an anonymous caller, with no asserted identity"
else
	echo "FAIL the call with OIR: UE 2 says $incoming"
	fail=1
fi
xcap ue oir not-restricted >/dev/null

echo "CFU: UE 2 diverts every call to UE 3, while it is registered"
# "ue xcap" of UE 2 runs in the container of ue2, at UE 2's address, so
# it runs while no other container of ue2 does.
set_service "UE 2" ue2 cfu on tel:+12025550103
docker compose run --no-deps -d --name ims-practice-ue2-reg ue2 register "${ue2[@]}" >/dev/null
wait_log ims-practice-ue2-reg '"msg":"registered"' || fail=1
answer ims-practice-ue3-cfu ue3 --ring 500ms --tone 1s
out="$(call tel:+12025550102)"
if grep -q '"msg":"call progress","status":181' <<<"$out" && grep -q '"msg":"call answered"' <<<"$out" &&
	[ "$(ended ims-practice-ue3-cfu)" = 0 ]; then
	echo "ok   UE 1 called UE 2, was told of the diversion with 181, and UE 3 answered"
else
	echo "FAIL CFU: $(grep -E '"msg":"(call progress|ue failed)"' <<<"$out" | tail -n 3)"
	fail=1
fi
docker stop ims-practice-ue2-reg >/dev/null
docker rm ims-practice-ue2-reg >/dev/null
set_service "UE 2" ue2 cfu off

echo "CFB: UE 2 is busy"
set_service "UE 2" ue2 cfb on tel:+12025550103
answer ims-practice-ue2-busy ue2 --reject 486
answer ims-practice-ue3-cfb ue3 --ring 500ms --tone 1s
out="$(call tel:+12025550102)"
ended ims-practice-ue2-busy >/dev/null
if grep -q '"msg":"call progress","status":181' <<<"$out" && grep -q '"msg":"call answered"' <<<"$out" &&
	[ "$(ended ims-practice-ue3-cfb)" = 0 ]; then
	echo "ok   UE 2 answered 486, and the call went on to UE 3"
else
	echo "FAIL CFB: $(grep -E '"msg":"(call progress|ue failed)"' <<<"$out" | tail -n 3)"
	fail=1
fi
set_service "UE 2" ue2 cfb off

echo "CFNR: UE 2 does not answer within its no reply timer"
set_service "UE 2" ue2 cfnr on tel:+12025550103
if grep -q '"no_reply_timer": 5' <<<"$(xcap ue2 noreply 5)"; then
	echo "ok   UE 2 sets its no reply timer to 5 s over Ut"
else
	echo "FAIL UE 2 could not set its no reply timer"
	fail=1
fi
answer ims-practice-ue2-ring ue2 --ring 30s
answer ims-practice-ue3-cfnr ue3 --ring 500ms --tone 1s
out="$(call tel:+12025550102)"
ended ims-practice-ue2-ring >/dev/null
if grep -q '"msg":"call progress","status":181' <<<"$out" && grep -q '"msg":"call answered"' <<<"$out" &&
	[ "$(ended ims-practice-ue3-cfnr)" = 0 ]; then
	echo "ok   UE 2 rang without an answer, and the call went on to UE 3"
else
	echo "FAIL CFNR: $(grep -E '"msg":"(call progress|ue failed)"' <<<"$out" | tail -n 3)"
	fail=1
fi
set_service "UE 2" ue2 cfnr off

echo "CFNL: UE 2 is not registered"
set_service "UE 2" ue2 cfnl on tel:+12025550103
answer ims-practice-ue3-cfnl ue3 --ring 500ms --tone 1s
out="$(call tel:+12025550102)"
if grep -q '"msg":"call progress","status":181' <<<"$out" && grep -q '"msg":"call answered"' <<<"$out" &&
	[ "$(ended ims-practice-ue3-cfnl)" = 0 ]; then
	echo "ok   the call to UE 2, which is not registered, went on to UE 3"
else
	echo "FAIL CFNL: $(grep -E '"msg":"(call progress|ue failed)"' <<<"$out" | tail -n 3)"
	fail=1
fi
set_service "UE 2" ue2 cfnl off

echo "barring: incoming calls to UE 2, outgoing calls of UE 1"
set_service "UE 2" ue2 baic on
answer ims-practice-ue2-baic ue2 --ring 500ms --tone 1s
out="$(call tel:+12025550102)"
docker rm -f ims-practice-ue2-baic >/dev/null
if grep -q '"call failed: 603 ' <<<"$out"; then
	echo "ok   the call to UE 2 was barred with 603"
else
	echo "FAIL BAIC: $(tail -n 1 <<<"$out")"
	fail=1
fi
set_service "UE 2" ue2 baic off
set_service "UE 1" ue baoc on
out="$(call tel:+12025550102)"
if grep -q '"call failed: 603 ' <<<"$out"; then
	echo "ok   UE 1's call was barred with 603"
else
	echo "FAIL BAOC: $(tail -n 1 <<<"$out")"
	fail=1
fi
set_service "UE 1" ue baoc off

echo "every service off again: a plain call"
# The checks read the capture up to this call's 200 OK to UE 1: the one
# after those that UE 1 has had so far.
answered_ue1='sip.Status-Code == 200 && sip.CSeq.method == "INVITE" && ip.dst == 172.28.0.100'
answers="$(count "$answered_ue1")"
answer ims-practice-ue2-last ue2 --ring 500ms --tone 1s
out="$(call tel:+12025550102)"
if grep -q '"msg":"call answered"' <<<"$out" && ! grep -q '"status":181' <<<"$out" && [ "$(ended ims-practice-ue2-last)" = 0 ]; then
	echo "ok   UE 2 answered the call itself"
else
	echo "FAIL the last call: $(grep -E '"msg":"(call progress|ue failed)"' <<<"$out" | tail -n 3)"
	fail=1
fi
same_nodes
await "the 200 OK of the last call to UE 1" "$answered_ue1" $((answers + 1))

echo "reading $pcap"

# RTP is found by the heuristic too, as some of it flows before tshark
# has seen its SDP.
tshark_options=(-o rtp.heuristic_rtp:TRUE)
last=0

ue1=172.28.0.100
ue2ip=172.28.0.101
ue3ip=172.28.0.103
pcscf=172.28.0.40
icscf=172.28.0.20
scscf=172.28.0.30
hss=172.28.0.10
mmtel=172.28.0.75
mrfc=172.28.0.85
mrfp=172.28.0.86
agw=172.28.0.50
impu1="sip:+12025550101@$domain"
impu2="sip:+12025550102@$domain"
invite='sip.Method == "INVITE"'
req='diameter.flags.request == 1'
udr="diameter.cmd.code == 306 && $req && ip.src == $mmtel"
mmtel_services='diameter.Data-Reference == 0 && diameter.Service-Indication == "MMTEL-Services"'
# A new call of UE 1: its INVITE to the P-CSCF.
from_ue1="$invite && ip.src == $ue1 && ip.dst == $pcscf && sip.r-uri == \"tel:+12025550102\""

echo "a plain call through the MMTel AS of each user (TS 24.229 5.4.3.2 step 5, 5.4.3.3 step 4, 5.7.5)"
expect "INVITE UE 1 -> P-CSCF" "$from_ue1"
a="$(field sip.Call-ID)"
expect "INVITE S-CSCF -> MMTel AS: P-Served-User, UE 1 originating" "$invite && sip.Call-ID == \"$a\" && ip.src == $scscf && ip.dst == $mmtel && sip.P-Served-User == \"<$impu1>;sescase=orig;regstate=reg\""
routes="$(field sip.Route)"
if [[ "$routes" == "<sip:mmtel.$domain:5060;lr>,<sip:scscf.$domain:5060;lr;odi="* ]]; then
	echo "ok   Route is the AS, then the S-CSCF with its original dialog identifier"
else
	echo "FAIL Route is $routes"
	fail=1
fi
icid="$(field sip.P-Charging-Vector | sed -n 's/.*icid-value=\([^;]*\).*/\1/p')"
expect "UDR MMTel AS -> HSS: UE 1's MMTEL-Services" "$udr && $mmtel_services && diameter.Public-Identity == \"$impu1\""
expect "UDA 2001 with the MMTelServices document" "$(answer_to) && diameter.Result-Code == 2001 && diameter.Sh-User-Data contains \"MMTelServices\""
expect "INVITE MMTel AS -> S-CSCF: a new dialog, the icid-value kept" "$invite && ip.src == $mmtel && ip.dst == $scscf && sip.Call-ID != \"$a\" && sip.P-Charging-Vector contains \"icid-value=$icid\""
b="$(field sip.Call-ID)"
expect "LIR for UE 2, on the AS's dialog" "diameter.cmd.code == 302 && $req && diameter.Public-Identity == \"$impu2\""
expect "INVITE S-CSCF -> MMTel AS: P-Served-User, UE 2 terminating" "$invite && sip.Call-ID == \"$b\" && ip.dst == $mmtel && sip.P-Served-User == \"<$impu2>;sescase=term;regstate=reg\""
expect "INVITE MMTel AS -> S-CSCF: a third dialog, the icid-value kept" "$invite && ip.src == $mmtel && sip.Call-ID != \"$b\" && sip.Call-ID != \"$a\" && sip.P-Charging-Vector contains \"icid-value=$icid\""
c="$(field sip.Call-ID)"
expect "INVITE P-CSCF -> UE 2" "$invite && sip.Call-ID == \"$c\" && ip.dst == $ue2ip"
expect "200 OK P-CSCF -> S-CSCF, asserting UE 2 (TS 24.229 5.2.6.4.4 step 1)" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && sip.Call-ID == \"$c\" && ip.src == $pcscf && sip.P-Asserted-Identity == \"<$impu2>\""
expect "200 OK reaches UE 1 with UE 2's identity (TIP)" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && sip.Call-ID == \"$a\" && ip.dst == $ue1 && sip.P-Asserted-Identity == \"<$impu2>\""
if [ "$a" != "$b" ] && [ "$b" != "$c" ] && [ "$a" != "$c" ] && [ -n "$icid" ]; then
	echo "ok   the call has three Call-IDs, one per dialog, and one icid-value ($icid)"
else
	echo "FAIL Call-IDs $a, $b, $c with icid-value $icid"
	fail=1
fi
expect "BYE UE 1 -> P-CSCF" "sip.Method == \"BYE\" && sip.Call-ID == \"$a\" && ip.src == $ue1"
expect "BYE reaches UE 2 on its own dialog" "sip.Method == \"BYE\" && sip.Call-ID == \"$c\" && ip.dst == $ue2ip"

echo "OIR (TS 24.607 4.5.2.4, 4.5.2.9)"
expect "PUT of OIR's default behaviour, UE 1 -> XCAP server" "http.request.method == \"PUT\" && ip.src == $ue1 && ip.dst == $mmtel && http.request.uri contains \"originating-identity-presentation-restriction/default-behaviour\""
expect "200 OK to the PUT" "http.response.code == 200 && ip.dst == $ue1"
expect "INVITE UE 1 -> P-CSCF" "$from_ue1"
a="$(field sip.Call-ID)"
expect "INVITE MMTel AS -> S-CSCF: an anonymous From and Privacy: id, the identity still asserted inside the network" "$invite && ip.src == $mmtel && ip.dst == $scscf && sip.from.addr == \"sip:anonymous@anonymous.invalid\" && sip.Privacy == \"id\" && sip.P-Asserted-Identity contains \"+12025550101\""
expect "INVITE P-CSCF -> UE 2: anonymous, Privacy: id, no P-Asserted-Identity" "$invite && ip.dst == $ue2ip && sip.from.addr == \"sip:anonymous@anonymous.invalid\" && sip.Privacy == \"id\" && !sip.P-Asserted-Identity"
oir_at_ue2="$last"
if [ -z "$(tshark_fields "frame.number == $oir_at_ue2 && sip contains \"+12025550101\"")" ]; then
	echo "ok   nothing in that INVITE names UE 1"
else
	echo "FAIL the INVITE to UE 2 still names UE 1"
	fail=1
fi
expect "200 OK reaches UE 1" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && sip.Call-ID == \"$a\" && ip.dst == $ue1"

echo "UE 2 sets CFU over Ut (TS 24.623 5.2.3.2.1, 6.1; RFC 4825; RFC 7616)"
expect "GET UE 2 -> XCAP server without credentials" "http.request.method == \"GET\" && ip.src == $ue2ip && ip.dst == $mmtel && !http.authorization && http.request.uri contains \"simservs.xml\""
expect "UDR for UE 2's Ut credential" "$udr && diameter.Data-Reference == 0 && diameter.Service-Indication == \"Ut-Digest-Credential\""
expect "401 with a Digest challenge" "http.response.code == 401 && ip.dst == $ue2ip && http.www_authenticate contains \"Digest\""
expect "GET with Authorization: Digest" "http.request.method == \"GET\" && ip.src == $ue2ip && http.authorization contains \"Digest\""
expect "UDR for UE 2's MMTEL-Services" "$udr && $mmtel_services && diameter.Public-Identity == \"$impu2\""
# Header names are case-insensitive (RFC 9110 5.1); Go writes "Etag".
# The ETag is the Sequence Number of the document in the HSS.
expect "200 OK with the document and its ETag" "http.response.code == 200 && ip.dst == $ue2ip && http.response.line matches \"^etag: \\\"[0-9]+\\\"\""
version="$(field http.response.line | tr ',' '\n' | sed -n 's/^[Ee][Tt][Aa][Gg]: *"\([0-9]*\)".*/\1/p' | first_line)"
next=$((${version:-0} + 1))
expect "PUT of the rule cfu, If-Match that ETag" "http.request.method == \"PUT\" && ip.src == $ue2ip && http.request.uri contains \"communication-diversion\" && http.request.line matches \"^if-match: \\\"$version\\\"\""
# The PUR carries the service indication and the next Sequence Number
# in the Repository Data (TS 29.328 7.6.1, 6.1.2.1).
expect "PUR MMTel AS -> HSS: MMTEL-Services with CFU on, Sequence Number $next" "diameter.cmd.code == 307 && $req && ip.src == $mmtel && diameter.Data-Reference == 0 && diameter.Sh-User-Data contains \"<ServiceIndication>MMTEL-Services<\" && diameter.Sh-User-Data contains \"<SequenceNumber>$next<\" && diameter.Sh-User-Data contains \"tel:+12025550103\""
expect "PUA 2001" "$(answer_to) && diameter.Result-Code == 2001"
expect "200 OK to the PUT with the ETag of the new version" "http.response.code == 200 && ip.dst == $ue2ip && http.response.line matches \"^etag: \\\"$next\\\"\""

echo "CFU (TS 24.604 4.5.2.6.2, 4.5.2.6.4)"
expect "INVITE UE 1 -> P-CSCF" "$from_ue1"
a="$(field sip.Call-ID)"
cfu_start="$last"
expect "INVITE S-CSCF -> MMTel AS for UE 2, terminating" "$invite && ip.dst == $mmtel && sip.P-Served-User contains \"sescase=term\" && sip.P-Served-User contains \"+12025550102\""
expect "181 MMTel AS -> S-CSCF with History-Info" "sip.Status-Code == 181 && ip.src == $mmtel && sip.History-Info"
expect "INVITE MMTel AS -> S-CSCF to UE 3, cause 302, with History-Info" "$invite && ip.src == $mmtel && sip.r-uri contains \"+12025550103\" && sip.r-uri contains \"cause=302\" && sip.History-Info contains \"index=1.1\" && sip.History-Info contains \"cause=302\""
expect "INVITE S-CSCF -> MMTel AS: the originating services of UE 2 after the diversion" "$invite && ip.dst == $mmtel && sip.P-Served-User contains \"orig-cdiv\" && sip.P-Served-User contains \"+12025550102\""
expect "INVITE P-CSCF -> UE 3 with History-Info" "$invite && ip.dst == $ue3ip && sip.History-Info contains \"cause=302\""
cfu_at_ue3="$last"
expect "200 OK reaches UE 1" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && sip.Call-ID == \"$a\" && ip.dst == $ue1"
last="$cfu_start"
expect "181 reaches UE 1 with History-Info" "sip.Status-Code == 181 && sip.Call-ID == \"$a\" && ip.dst == $ue1 && sip.History-Info"
none "INVITEs to UE 2 while CFU diverts its calls" "frame.number > $cfu_start && frame.number < $cfu_at_ue3 && $invite && ip.dst == $ue2ip"
last="$cfu_at_ue3"

echo "CFB (TS 24.604 4.5.2.6.3)"
expect "INVITE UE 1 -> P-CSCF" "$from_ue1"
a="$(field sip.Call-ID)"
expect "INVITE P-CSCF -> UE 2" "$invite && ip.dst == $ue2ip"
expect "486 UE 2 -> P-CSCF" "sip.Status-Code == 486 && ip.src == $ue2ip"
expect "INVITE MMTel AS -> S-CSCF to UE 3, cause 486, with History-Info" "$invite && ip.src == $mmtel && sip.r-uri contains \"cause=486\" && sip.History-Info contains \"cause=486\""
expect "INVITE P-CSCF -> UE 3" "$invite && ip.dst == $ue3ip"
expect "200 OK reaches UE 1" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && sip.Call-ID == \"$a\" && ip.dst == $ue1"

echo "CFNR (TS 24.604 4.5.2.6.3 item 2)"
expect "PUT of NoReplyTimer, UE 2 -> XCAP server" "http.request.method == \"PUT\" && ip.src == $ue2ip && http.request.uri contains \"NoReplyTimer\""
expect "200 OK to the PUT" "http.response.code == 200 && ip.dst == $ue2ip"
expect "INVITE UE 1 -> P-CSCF" "$from_ue1"
a="$(field sip.Call-ID)"
expect "180 UE 2 -> P-CSCF" "sip.Status-Code == 180 && ip.src == $ue2ip"
expect "180 reaches the MMTel AS" "sip.Status-Code == 180 && ip.dst == $mmtel"
ringing_at="$(field frame.time_relative)"
expect "CANCEL MMTel AS -> S-CSCF with Reason SIP cause 408" "sip.Method == \"CANCEL\" && ip.src == $mmtel && sip.Reason contains \"cause=408\""
cancel_at="$(field frame.time_relative)"
if "$python" -c 'import sys; d = float(sys.argv[2]) - float(sys.argv[1]); sys.exit(0 if 5 <= d < 7 else 1)' "$ringing_at" "$cancel_at"; then
	echo "ok   the AS cancelled the call to UE 2 5 s after its 180"
else
	echo "FAIL the AS cancelled the call to UE 2 $ringing_at s .. $cancel_at s"
	fail=1
fi
expect "CANCEL reaches UE 2 with that Reason (RFC 3326 2)" "sip.Method == \"CANCEL\" && ip.dst == $ue2ip && sip.Reason contains \"cause=408\""
cancelled="$last"
# UE 2's AS ends its early dialog with UE 1's AS, which keeps its one
# early dialog with UE 1 and does not pass the 199 on.
expect "199 UE 2's AS -> UE 1's AS, Reason SIP cause 408: the early dialog of UE 2 ends (RFC 6228)" "sip.Status-Code == 199 && ip.dst == $mmtel && sip.Reason contains \"cause=408\""
# The AS sends the 199 and the INVITE to UE 3 once UE 2's leg has ended.
# The 199 is seen as it comes back from the S-CSCF and the INVITE as it
# leaves the AS, so either may come first in the capture.
last="$cancelled"
expect "INVITE MMTel AS -> S-CSCF to UE 3, cause 408" "$invite && ip.src == $mmtel && sip.r-uri contains \"cause=408\" && sip.History-Info contains \"cause=408\""
expect "INVITE P-CSCF -> UE 3" "$invite && ip.dst == $ue3ip"
expect "200 OK reaches UE 1" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && sip.Call-ID == \"$a\" && ip.dst == $ue1"

echo "CFNL (TS 24.604 4.5.2.6.2; TS 29.228 6.1.4.1 step 3)"
expect "INVITE UE 1 -> P-CSCF" "$from_ue1"
a="$(field sip.Call-ID)"
expect "LIR for UE 2" "diameter.cmd.code == 302 && $req && diameter.Public-Identity == \"$impu2\""
expect "LIA DIAMETER_UNREGISTERED_SERVICE" "$(answer_to) && diameter.Experimental-Result-Code == 2003"
expect "SAR UNREGISTERED_USER for UE 2" "diameter.cmd.code == 301 && $req && diameter.Server-Assignment-Type == 3 && diameter.Public-Identity == \"$impu2\""
expect "INVITE S-CSCF -> MMTel AS: UE 2 terminating, not registered" "$invite && ip.dst == $mmtel && sip.P-Served-User == \"<$impu2>;sescase=term;regstate=unreg\""
expect "INVITE MMTel AS -> S-CSCF to UE 3, cause 404" "$invite && ip.src == $mmtel && sip.r-uri contains \"cause=404\""
expect "INVITE P-CSCF -> UE 3" "$invite && ip.dst == $ue3ip"
expect "200 OK reaches UE 1" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && sip.Call-ID == \"$a\" && ip.dst == $ue1"

echo "barring (TS 24.611 4.5.2.6.1, 4.5.2.4.1), with an announcement first (TS 24.628 4.2.4, Annex G)"
# announced_rejection LABEL checks the announcement that comes before the
# rejection of the call $a: the AS asks the MRFC for it (RFC 4240 3),
# the caller gets a 183 that points it at the MRFP, the MRFP plays, the
# MRFC ends its dialog, and the 603 carries the cause in Reason.
announced_rejection() {
	expect "INVITE MMTel AS -> MRFC for the announcement notice, without SDP" "$invite && ip.src == $mmtel && ip.dst == $mrfc && sip.r-uri contains \"play=/provisioned/notice\" && !sdp"
	expect "200 OK MRFC -> MMTel AS with the offer of the MRFP" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.src == $mrfc && sdp.connection_info.address == \"$mrfp\""
	expect "183 reaches UE 1 with P-Early-Media: sendonly" "sip.Status-Code == 183 && sip.Call-ID == \"$a\" && ip.dst == $ue1 && sip.P-Early-Media == \"sendonly\""
	expect "ACK MMTel AS -> MRFC with the answer: the play starts" "sip.Method == \"ACK\" && ip.src == $mmtel && ip.dst == $mrfc && sdp"
	expect "RTP of the announcement MRFP -> media relay" "rtp && ip.src == $mrfp && ip.dst == $agw"
	expect "RTP of the announcement reaches UE 1" "rtp && ip.src == $agw && ip.dst == $ue1"
	expect "BYE MRFC -> MMTel AS: the announcement has ended" "sip.Method == \"BYE\" && ip.src == $mrfc && ip.dst == $mmtel"
	expect "$1" "sip.Status-Code == 603 && ip.src == $mmtel && sip.Reason contains \"cause=603\""
	expect "603 reaches UE 1 with that Reason" "sip.Status-Code == 603 && sip.Call-ID == \"$a\" && ip.dst == $ue1 && sip.Reason contains \"cause=603\""
}
expect "INVITE UE 1 -> P-CSCF" "$from_ue1"
a="$(field sip.Call-ID)"
barred="$last"
announced_rejection "603 MMTel AS -> S-CSCF with Reason: UE 2 bars incoming calls"
none "INVITEs to UE 2 while it bars them" "frame.number > $barred && frame.number < $last && $invite && ip.dst == $ue2ip"
expect "INVITE UE 1 -> P-CSCF" "$from_ue1"
a="$(field sip.Call-ID)"
barred="$last"
expect "INVITE S-CSCF -> MMTel AS for UE 1, originating" "$invite && sip.Call-ID == \"$a\" && ip.dst == $mmtel && sip.P-Served-User contains \"sescase=orig\""
announced_rejection "603 MMTel AS -> S-CSCF with Reason: UE 1 bars outgoing calls"
none "INVITEs from the MMTel AS toward the callee of the barred call" "frame.number > $barred && frame.number < $last && $invite && ip.src == $mmtel && ip.dst != $mrfc"

echo "every service off again"
expect "INVITE UE 1 -> P-CSCF" "$from_ue1"
a="$(field sip.Call-ID)"
expect "INVITE P-CSCF -> UE 2, with no History-Info" "$invite && ip.dst == $ue2ip && !sip.History-Info"
expect "200 OK reaches UE 1" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && sip.Call-ID == \"$a\" && ip.dst == $ue1"

echo "the capture as a whole"
for header in History-Info Privacy P-Served-User; do
	if [ -n "$(tshark_fields "sip.$header" | first_line)" ]; then
		echo "ok   the capture holds $header"
	else
		echo "FAIL the capture holds no $header"
		fail=1
	fi
done
# The charging information stays in the network (TS 24.229 5.2.1):
# the P-CSCF sends none of it to a UE. Every UE of this setup has an
# address from 172.28.0.100 up.
none "P-Charging-Vector or P-Charging-Function-Addresses from the P-CSCF to a UE" 'ip.src == 172.28.0.40 && ip.dst >= 172.28.0.100 && (sip.P-Charging-Vector || sip.P-Charging-Function-Addresses)'
none "malformed SIP, Diameter or HTTP packets" '(sip || diameter || http) && (_ws.malformed || _ws.expert.severity >= warning)'
# RFC 3261 18.1.1: a request over 1300 bytes does not go over UDP.
none "SIP requests over 1300 bytes on UDP" 'sip.Method && udp && udp.length > 1308'

# The ladder of the calls, Ut and Sh, for the guide and the CI artifact.
docker compose exec -T capture tshark -r "$pcap" -T fields -E separator=/t \
	-Y '(sip && !(sip.CSeq.method == "REGISTER") && !(sip.CSeq.method == "SUBSCRIBE") && !(sip.CSeq.method == "NOTIFY") && !(sip.CSeq.method == "MESSAGE")) || (http && ip.addr == 172.28.0.75) || diameter.cmd.code == 306 || diameter.cmd.code == 307 || diameter.cmd.code == 302 || diameter.cmd.code == 301' \
	-e frame.number -e ip.src -e ip.dst -e _ws.col.Info -e sip.Call-ID | tr -d '\r' >e2e-out/mmtel-ladder.txt

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color | grep -E '"level":"(WARN|ERROR)"' | tail -n 40
fi
exit "$fail"
