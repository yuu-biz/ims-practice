#!/usr/bin/env bash
# End-to-end check: two IMS networks joined by their IBCFs and
# TrGWs (deploy/interconnect). Network A is compose.yaml, network B the
# peer network of deploy/interconnect/compose.yaml. A UE of each network
# registers; UE A1 calls UE B1 by number, and UE B1 calls UE A2; UE A1
# sends UE B1 a short message, and UE B1 answers UE A1 with one. Then
# the mismatch scenarios switch the interconnect profiles: a mismatch
# fails with the status it should, and the adjusted profile passes. UE
# A1 merges its calls with UE B1 and UE A2 into a conference, which
# brings UE B1 in with a Replaces that each IBCF maps to the dialog of
# its other side; UE B1 leaves and comes back with an INVITE to the
# conference URI. A host on the link that belongs to no peer sends
# IBCF-A requests, which get no answer and do not get into network A.
# Last, the captures of
# the three bridges are read: each IBCF is a B2BUA, so the INVITEs on
# either side of it have Call-IDs of their own; the conference URI that
# reaches network B names no node; network B's capture
# and the link's hold no address, no node name and no identity made
# from an IMSI of network A, and network A's capture holds no address
# and no node name of network B. Every node of both networks and of the
# link must keep its container, apart from the IBCF that a mismatch
# scenario starts again with another agreement: that IBCF gets a new
# container each time, and no other node changes with it.
#
# The script builds the images, as the other e2e scripts do. With
# IMS_IMAGE and CAPTURE_IMAGE it builds none and uses those images
# instead, tagged as compose.yaml names them. E2E_KEEP=1 leaves the
# networks up afterwards; otherwise they are taken down, and the pcaps
# stay in captures/, captures/b/ and captures/nni/.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
export MSYS_NO_PATHCONV=1
# Use the project's own builder when it exists, so the build
# cache stays separate from other projects. CI has no such builder.
if docker buildx inspect ims-practice >/dev/null 2>&1; then
	export BUILDX_BUILDER=ims-practice
fi

project=ims-practice-interconnect
# Both networks and the link are one Compose project. It is named in the
# environment rather than in flags, so that the "docker compose" of
# scripts/e2e-lib.sh finds its nodes too.
export COMPOSE_PROJECT_NAME="$project" COMPOSE_FILE=compose.yaml:deploy/interconnect/compose.yaml COMPOSE_PATH_SEPARATOR=:
c() { docker compose "$@"; }
# The default agreements; the scenarios below change them one at a time.
export IBCF_A_PROFILE=network-b.yaml IBCF_B_PROFILE=network-a.yaml

tagged=()
build=(--build)
if [ -n "${IMS_IMAGE:-}" ] && [ -n "${CAPTURE_IMAGE:-}" ]; then
	for pair in "$IMS_IMAGE ims-practice/ims:dev" "$CAPTURE_IMAGE ims-practice/capture:dev"; do
		read -r image name <<<"$pair"
		if ! docker image inspect "$image" >/dev/null 2>&1; then
			echo "FAIL there is no image $image" >&2
			exit 2
		fi
		if [ "$image" != "$name" ]; then
			docker tag "$image" "$name"
			tagged+=("$name")
		fi
	done
	build=()
fi

# docker rm -f, docker stop and docker image rm fail on a container or
# an image that is gone already, which is no error here, hence their
# "|| true" in this script.
cleanup() {
	docker rm -f "${project}-ue-b1" "${project}-ue-a1" "${project}-ue-a2" >/dev/null 2>&1 || true
	if [ "${E2E_KEEP:-0}" != 1 ]; then
		c --profile ue down -v --remove-orphans >/dev/null 2>&1 || true
		for name in "${tagged[@]}"; do
			docker image rm "$name" >/dev/null 2>&1 || true
		done
	fi
}
trap cleanup EXIT

c up -d "${build[@]}"

wait_for_open() {
	local service="$1" i
	# Up to two minutes: a node that tried before the HSS listened
	# tries again after Tc, 30 s (RFC 6733 2.1).
	for i in $(seq 1 120); do
		if grep -q '"to":"I-Open (OKAY)"' <<<"$(c logs --no-color "$service" 2>/dev/null)"; then
			return 0
		fi
		sleep 1
	done
	echo "FAIL $service never opened its Diameter connection" >&2
	exit 1
}
for service in icscf scscf ipsmgw icscf-b scscf-b ipsmgw-b; do
	wait_for_open "$service"
done
# wait_ibcf waits for the IBCF of service to log that it is ready, since
# the time since.
wait_ibcf() {
	local service="$1" since="$2" i
	for i in $(seq 1 30); do
		if grep -q '"msg":"IBCF ready"' <<<"$(c logs --no-color --since "$since" "$service" 2>/dev/null)"; then
			return 0
		fi
		sleep 1
	done
	echo "FAIL $service did not start" >&2
	exit 1
}
wait_ibcf ibcf-a 1970-01-01T00:00:00Z
wait_ibcf ibcf-b 1970-01-01T00:00:00Z

# The UEs of network B are subscribers 1 and 2 of
# deploy/interconnect/b/hss.yaml, behind network B's P-CSCF.
network_b=(--domain ims.mnc002.mcc001.3gppnetwork.org
	--pcscf 172.28.1.40:5060 --pcscf-uri sip:pcscf.ims.mnc002.mcc001.3gppnetwork.org:5060)
# Only "ue sms" has -sc: the SC that UE B1's short messages go to.
sc_b=(--sc +12025550150)
ue_b1=("${network_b[@]}" --impi 001020000000001@ims.mnc002.mcc001.3gppnetwork.org
	--impu sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org --password test-password-b1)
ue_a2=(--impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org
	--impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org --password test-password-2)
gsm_text='Hello UE-B1, this is UE-A1 in another network.'
gsm_text_back='Hello UE-A1, this is UE-B1 answering.'
local_number='sip:2025550151;phone-context=+1@ims.mnc002.mcc001.3gppnetwork.org;user=phone'
rm -f captures/interconnect-a1-sent.raw captures/interconnect-b1-received.raw
mkdir -p e2e-out
fail=0
remember_nodes
# Every node of the default profile must be among those remembered:
# those of network A, of network B and of the link.
running="$(c ps --services | sort)"
not_running="$(comm -23 <(c config --services | sort) <(echo "$running"))"
if [ -z "$not_running" ]; then
	echo "ok   remembered the $(grep -c . <<<"$running") nodes of both networks and the link"
else
	echo "FAIL nodes that do not run: ${not_running//$'\n'/ }"
	fail=1
fi

# answer_b1 has UE B1 wait in the background for one call; the flags go
# before those of the call.
answer_b1() {
	docker rm -f "${project}-ue-b1" >/dev/null 2>&1 || true
	c run --no-deps -d --name "${project}-ue-b1" ue-b1 answer "${ue_b1[@]}" --ring 1s --tone 4s "$@" >/dev/null
	wait_log "${project}-ue-b1" '"msg":"waiting for a call"' || fail=1
}
# call_status calls uri from UE A1 and prints the status it ended with:
# 200 for a call that was answered and ended.
call_status() {
	local out
	if out="$(c run --no-deps --rm ue call --tone 2s "$@" 2>&1)" && grep -q '"msg":"call ended"' <<<"$out"; then
		echo 200
		return
	fi
	grep -o '"call failed: [0-9]*' <<<"$out" | grep -o '[0-9]*$' || echo none
}
# use_profile starts the IBCF of service with the agreement in profile.
# Compose makes a new container for it, as one of its volumes changed:
# the one node that the script recreates on purpose. renew_node checks
# that no other node changed with it.
use_profile() {
	local service="$1" profile="$2" since
	echo "$service takes the agreement $profile"
	since="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	if [ "$service" = ibcf-a ]; then
		IBCF_A_PROFILE="$profile" c up -d --no-deps ibcf-a >/dev/null 2>&1
	else
		IBCF_B_PROFILE="$profile" c up -d --no-deps ibcf-b >/dev/null 2>&1
	fi
	wait_ibcf "$service" "$since"
	renew_node "$service"
}

echo "call from network A to network B"
answer_b1 --received-out /captures/interconnect-b1-received.raw
if c run --no-deps --rm ue call --tone 3s --sent-out /captures/interconnect-a1-sent.raw tel:+12025550151 >/dev/null 2>&1; then
	echo "ok   UE A1 called UE B1 by number"
else
	echo "FAIL UE A1 to UE B1"
	fail=1
fi
if [ "$(timeout 60 docker wait "${project}-ue-b1")" = "0" ]; then
	echo "ok   UE B1 answered and the call ended"
else
	echo "FAIL UE B1"
	docker logs "${project}-ue-b1" 2>&1 | tail -n 20
	fail=1
fi
sent="$(od -An -v -tx1 captures/interconnect-a1-sent.raw 2>/dev/null | tr -d ' \n')"
received="$(od -An -v -tx1 captures/interconnect-b1-received.raw 2>/dev/null | tr -d ' \n')"
if [ -n "$sent" ] && [ "$sent" = "$received" ]; then
	echo "ok   UE B1 received the ${#sent} hex digits of PCMU that UE A1 sent, through both TrGWs"
else
	echo "FAIL payloads differ: sent ${#sent}, received ${#received} hex digits"
	fail=1
fi

echo "call from network B to network A"
docker rm -f "${project}-ue-a2" >/dev/null 2>&1 || true
c run --no-deps -d --name "${project}-ue-a2" ue2 answer "${ue_a2[@]}" --ring 1s --tone 4s >/dev/null
wait_log "${project}-ue-a2" '"msg":"waiting for a call"' || fail=1
if c run --no-deps --rm ue-b1 call "${ue_b1[@]}" --tone 3s tel:+12025550102 >/dev/null 2>&1 &&
	[ "$(timeout 60 docker wait "${project}-ue-a2")" = "0" ]; then
	echo "ok   UE B1 called UE A2 by number, and UE A2 answered"
else
	echo "FAIL UE B1 to UE A2"
	docker logs "${project}-ue-a2" 2>&1 | tail -n 20
	fail=1
fi
docker rm -f "${project}-ue-a2" >/dev/null 2>&1 || true

echo "short message from network A to network B"
docker rm -f "${project}-ue-b1" >/dev/null 2>&1 || true
c run --no-deps -d --name "${project}-ue-b1" ue-b1 register "${ue_b1[@]}" >/dev/null
wait_log "${project}-ue-b1" '"msg":"registered"' || fail=1
# TP-ST 1: the SC forwarded the short message to network B but cannot
# confirm the delivery (TS 23.040 9.2.3.15).
if out="$(c run --no-deps --rm ue sms --status-report +12025550151 "$gsm_text" 2>&1)" &&
	grep -q '"msg":"status report".*"status":1,' <<<"$out"; then
	echo "ok   UE A1 sent UE B1 a short message, and the status report says network B took it"
else
	echo "FAIL UE A1 to UE B1"
	# grep finds nothing when the UE logged none of these lines.
	grep -E '"msg":"(sms sent|submit report|status report|ue failed)"' <<<"$out" || true
	fail=1
fi
if wait_log "${project}-ue-b1" "\"msg\":\"sms received\",.*\"from\":\"+12025550101\",\"text\":\"$gsm_text\"" &&
	wait_log "${project}-ue-b1" '"msg":"delivery report sent"'; then
	echo "ok   UE B1 received the text from UE A1"
else
	echo "FAIL UE B1 did not receive the text"
	fail=1
fi

echo "short message from network B to network A"
# UE B1 sends from a registration of its own, so that its status report
# has one contact to go to. The registration of the section above ends
# first.
docker stop "${project}-ue-b1" >/dev/null 2>&1 || true
docker rm -f "${project}-ue-b1" "${project}-ue-a1" >/dev/null 2>&1 || true
c run --no-deps -d --name "${project}-ue-a1" ue register >/dev/null
wait_log "${project}-ue-a1" '"msg":"registered"' || fail=1
# TP-ST 1 again: the SC of network B forwarded it to network A.
if out="$(c run --no-deps --rm ue-b1 sms "${ue_b1[@]}" "${sc_b[@]}" --status-report +12025550101 "$gsm_text_back" 2>&1)" &&
	grep -q '"msg":"status report".*"status":1,' <<<"$out"; then
	echo "ok   UE B1 sent UE A1 a short message, and the status report says network A took it"
else
	echo "FAIL UE B1 to UE A1"
	# grep finds nothing when the UE logged none of these lines.
	grep -E '"msg":"(sms sent|submit report|status report|ue failed)"' <<<"$out" || true
	fail=1
fi
if wait_log "${project}-ue-a1" "\"msg\":\"sms received\",.*\"from\":\"+12025550151\",\"text\":\"$gsm_text_back\"" &&
	wait_log "${project}-ue-a1" '"msg":"delivery report sent"'; then
	echo "ok   UE A1 received the text from UE B1"
else
	echo "FAIL UE A1 did not receive the text"
	docker logs "${project}-ue-a1" 2>&1 | tail -n 20
	fail=1
fi
docker stop "${project}-ue-a1" >/dev/null 2>&1 || true
docker rm -f "${project}-ue-a1" >/dev/null 2>&1 || true

echo "mismatched profiles"
# UE B1 stays registered for the short messages of the first scenario.
c run --no-deps -d --name "${project}-ue-b1" ue-b1 register "${ue_b1[@]}" >/dev/null
wait_log "${project}-ue-b1" '"msg":"registered"' || fail=1
# 1. MESSAGE: without the agreement on MESSAGE, IBCF-A answers 405 and
# the SC of network A reports TP-ST 98, no response from the SME. "ue
# sms" ends with an error when no status report comes in time; the
# checks read its log, hence "|| true".
use_profile ibcf-a network-b-no-message.yaml
out="$(c run --no-deps --rm ue sms --status-report +12025550151 'Is MESSAGE agreed?' 2>&1 || true)"
if grep -q '"msg":"status report".*"status":98,' <<<"$out"; then
	echo "ok   without MESSAGE in the agreement the short message was not delivered"
else
	echo "FAIL short message without MESSAGE in the agreement: $(tail -n 1 <<<"$out")"
	fail=1
fi
use_profile ibcf-a network-b.yaml
out="$(c run --no-deps --rm ue sms --status-report +12025550151 'MESSAGE is agreed.' 2>&1 || true)"
if grep -q '"msg":"status report".*"status":1,' <<<"$out"; then
	echo "ok   with MESSAGE in the agreement it went through"
else
	echo "FAIL short message with MESSAGE in the agreement: $(tail -n 1 <<<"$out")"
	fail=1
fi
docker stop "${project}-ue-b1" >/dev/null 2>&1 || true
docker rm -f "${project}-ue-b1" >/dev/null 2>&1 || true

# 2. Codecs: network B takes AMR-WB alone, UE A1 offers PCMU: 488 from
# IBCF-B. The UE B1 that waits is still waiting afterwards.
answer_b1
use_profile ibcf-b network-a-amr-wb-only.yaml
status="$(call_status tel:+12025550151)"
if [ "$status" = 488 ]; then
	echo "ok   with no common codec the call failed with 488"
else
	echo "FAIL no common codec: $status, want 488"
	fail=1
fi
use_profile ibcf-b network-a.yaml
status="$(call_status tel:+12025550151)"
if [ "$status" = 200 ]; then
	echo "ok   with PCMU in the agreement the call went through"
else
	echo "FAIL with PCMU in the agreement: $status"
	fail=1
fi

# 3. Number format: a local number crosses as it is, and IBCF-B, whose
# agreement has no local numbers, answers 404; made global at IBCF-A,
# the number is found.
answer_b1
use_profile ibcf-a network-b-local-numbers.yaml
status="$(call_status "$local_number")"
if [ "$status" = 404 ]; then
	echo "ok   a local number that crossed as it is failed with 404"
else
	echo "FAIL local number: $status, want 404"
	fail=1
fi
use_profile ibcf-a network-b-global-numbers.yaml
status="$(call_status "$local_number")"
if [ "$status" = 200 ]; then
	echo "ok   the local number made global at IBCF-A went through"
else
	echo "FAIL local number made global: $status"
	fail=1
fi
use_profile ibcf-a network-b.yaml

docker rm -f "${project}-ue-b1" >/dev/null 2>&1 || true

echo "a conference of network A with UE B1 of network B"
# UE A1 calls UE B1, holds the call, calls UE A2 and merges the two
# calls into a conference (GSMA IR.92 2.3.3). The focus's INVITE to UE
# B1 carries a Replaces of UE B1's call with UE A1 (TS 24.147
# 5.3.2.5.4), which each IBCF passes on naming the dialog of its other
# side (TS 24.229 5.10.5), so UE B1 takes it in the place of that call.
# UE B1 then leaves and comes back with an INVITE to the conference URI
# (TS 24.147 5.3.1.4.1): a request outside a dialog, which crosses
# IBCF-B and IBCF-A and goes from the I-CSCF of network A to the focus
# by DNS. Each UE runs "ue serve" and takes each step from "ue ctl" in
# its container.
a1="${project}-ue-a1" a2="${project}-ue-a2" b1="${project}-ue-b1"
# serve NAME SERVICE FLAGS... starts the UE of SERVICE as a phone that
# stays up, with its control API on port 8090 of its container.
serve() {
	local name="$1" service="$2"
	shift 2
	docker rm -f "$name" >/dev/null 2>&1 || true
	c run --no-deps -d --name "$name" "$service" serve -control 0.0.0.0:8090 "$@" >/dev/null
}
# ctl NAME ACTION... is "ue ctl" in the container of the phone NAME. It
# prints the JSON answer of the control API, and fails on an answer
# other than 2xx.
ctl() {
	local name="$1"
	shift
	docker exec "$name" ue ctl "$@"
}
# phone NAME prints "registered|unregistered CALLS USERS" for the phone:
# CALLS is the state of each call on its line, joined by "+", with
# "/held" for a call it holds, or "idle"; USERS is the number of users
# of its conference that are connected. Until the control API answers,
# it prints "unreachable".
phone() {
	ctl "$1" status 2>/dev/null | "$python" -c '
import json, sys
s = json.load(sys.stdin)
calls = "+".join(c["state"] + ("/held" if (c.get("session") or {}).get("held") else "")
                 for c in s.get("calls") or []) or "idle"
users = sum(1 for u in (s.get("conference") or {}).get("users") or [] if u.get("status") == "connected")
print("registered" if s["registered"] else "unregistered", calls, users)
' 2>/dev/null | tr -d '\r' || echo unreachable
}
phone_is() { [ "$(phone "$1")" = "$2" ]; }
# wait_phone NAME STATE waits up to 30 s for phone to print STATE, and
# says what it printed when it does not.
wait_phone() {
	wait_for 30 phone_is "$1" "$2" && return 0
	echo "FAIL ${1#"$project"-} is \"$(phone "$1")\" after 30 s, want \"$2\""
	return 1
}
# json_field FIELD... prints a field of the JSON on its input, such as
# "uri", or "call" "call_id"; nothing when it has none.
json_field() {
	"$python" -c '
import json, sys
v = json.load(sys.stdin)
for k in sys.argv[1:]:
    v = (v or {}).get(k)
print(v or "")
' "$@" 2>/dev/null | tr -d '\r'
}
serve "$a1" ue
serve "$a2" ue2 "${ue_a2[@]}"
serve "$b1" ue-b1 "${ue_b1[@]}"
registered=1
for name in "$a1" "$a2" "$b1"; do
	wait_phone "$name" "registered idle 0" || registered=0
done
if [ "$registered" = 1 ]; then
	echo "ok   UE A1, UE A2 and UE B1 registered"
else
	fail=1
fi
if ctl "$a1" call tel:+12025550151 >/dev/null && wait_phone "$a1" "registered active 0" &&
	wait_phone "$b1" "registered active 0"; then
	echo "ok   UE A1 called UE B1, which answered"
else
	echo "FAIL UE A1's call to UE B1"
	fail=1
fi
if ctl "$a1" hold >/dev/null && wait_phone "$a1" "registered active/held 0"; then
	echo "ok   UE A1 held its call with UE B1"
else
	echo "FAIL UE A1 did not hold its call"
	fail=1
fi
if ctl "$a1" call tel:+12025550102 >/dev/null && wait_phone "$a1" "registered active/held+active 0" &&
	wait_phone "$a2" "registered active 0"; then
	echo "ok   UE A1 called UE A2, which answered"
else
	echo "FAIL UE A1's call to UE A2"
	fail=1
fi
# The merge answers once the focus has invited both peers. Each peer
# takes the focus's call in the place of its call with UE A1, so each
# has one call again. UE A2 then subscribes to the conference URI of the
# focus's Contact and sees its three users. UE B1 sees none: its
# SUBSCRIBE outside the dialog gets 403 at IBCF-B, and the agreement
# with network A does not let one within the dialog cross either.
if ctl "$a1" merge >/dev/null && wait_phone "$a1" "registered active 3" &&
	wait_phone "$b1" "registered active 0" && wait_phone "$a2" "registered active 3"; then
	echo "ok   UE A1 merged its calls, and UE B1 of network B is in the conference with UE A2"
else
	echo "FAIL the merge: UE A1 is \"$(phone "$a1")\", UE B1 \"$(phone "$b1")\""
	fail=1
fi
# A conference URI is a PSI of the subdomain of the conference URIs,
# with no port (TS 23.003 13.5): it names no node of network A.
conference_uri="$(ctl "$a1" conference 2>/dev/null | json_field uri)" || conference_uri=
if [[ "$conference_uri" =~ ^sip:conf-[^@:]+@conf\.ims\.mnc001\.mcc001\.3gppnetwork\.org$ ]]; then
	echo "ok   the conference URI is $conference_uri, in the subdomain of the conference URIs"
else
	echo "FAIL the conference URI is \"$conference_uri\""
	fail=1
fi
if ctl "$b1" hangup >/dev/null && wait_phone "$b1" "registered idle 0" && wait_phone "$a1" "registered active 2"; then
	echo "ok   UE B1 left the conference"
else
	echo "FAIL UE B1 did not leave the conference: UE B1 is \"$(phone "$b1")\", UE A1 \"$(phone "$a1")\""
	fail=1
fi
if ctl "$b1" call "$conference_uri" >/dev/null && wait_phone "$b1" "registered active 0" &&
	wait_phone "$a1" "registered active 3"; then
	echo "ok   UE B1 called the conference URI from network B, and is in the conference again"
else
	echo "FAIL UE B1 did not come back to the conference: UE B1 is \"$(phone "$b1")\", UE A1 \"$(phone "$a1")\""
	fail=1
fi
b1_call="$(ctl "$b1" status 2>/dev/null | json_field call call_id)" || b1_call=
# The creator leaves, and the focus ends the call of every other user
# (TS 24.147 5.3.2.7), UE B1's across the border too.
if ctl "$a1" hangup >/dev/null && wait_phone "$a1" "registered idle 0" &&
	wait_phone "$b1" "registered idle 0" && wait_phone "$a2" "registered idle 0"; then
	echo "ok   UE A1 left, and the conference ended for all three"
else
	echo "FAIL ending the conference"
	fail=1
fi
for name in "$a1" "$a2" "$b1"; do
	docker stop "$name" >/dev/null 2>&1 || true
	docker logs "$name" >"e2e-out/${name#"$project"-}-conference.log" 2>&1 || true
	docker rm -f "$name" >/dev/null 2>&1 || true
done

echo "requests from an address of no peer network"
# A host on the link that is neither IBCF-B nor a TrGW sends IBCF-A an
# INVITE and a MESSAGE over UDP. IBCF-A finds no peer for the address
# and drops both without any answer, not even the 100 (Trying) of the
# INVITE, so that the host learns nothing about network A.
stranger=172.28.2.99
stranger_from='<sip:+12025550198@example.invalid>;tag=no-peer'
since="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
# stranger_request prints a request of method from the stranger, with
# a Call-ID and a branch of its own, and a body of type when there is
# one.
stranger_request() {
	local method="$1" type="${2:-}" body="${3:-}"
	printf '%s\r\n' \
		"$method sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org;user=phone SIP/2.0" \
		"Via: SIP/2.0/UDP $stranger:5070;branch=z9hG4bK-no-peer-$method;rport" \
		"Max-Forwards: 70" \
		"From: $stranger_from" \
		"To: <sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org;user=phone>" \
		"Call-ID: no-peer-$method-$(date +%s)@$stranger" \
		"CSeq: 1 $method" \
		"Contact: <sip:$stranger:5070>"
	if [ -n "$type" ]; then
		printf 'Content-Type: %s\r\n' "$type"
	fi
	printf 'Content-Length: %d\r\n\r\n%s' "${#body}" "$body"
}
# send_as_stranger sends what comes on its input from port 5070 of the
# stranger, and prints what comes back to that port within two seconds.
# nc may end with an error when nothing comes back, which is the outcome
# wanted here, hence "|| true".
send_as_stranger() {
	docker run --rm -i --network "${project}_nni" --ip "$stranger" --entrypoint nc ims-practice/ims:dev \
		-u -w 2 -p 5070 172.28.2.10 5060 2>&1 || true
}
answers="$(stranger_request INVITE | send_as_stranger)
$(stranger_request MESSAGE text/plain hello | send_as_stranger)"
answers="$(tr -d '\r' <<<"$answers")"
if [ -z "${answers//$'\n'/}" ]; then
	echo "ok   $stranger got no answer to its INVITE and MESSAGE"
else
	echo "FAIL $stranger got an answer: ${answers%%$'\n'*}"
	fail=1
fi
# Empty when IBCF-A logged no discard; the checks below say which is
# missing.
discarded="$(grep '"msg":"discarded a request from an address of no peer network"' <<<"$(c logs --no-color --since "$since" ibcf-a 2>/dev/null)" || true)"
for method in INVITE MESSAGE; do
	if grep -q "\"method\":\"$method\"" <<<"$discarded"; then
		echo "ok   IBCF-A logged the $method from $stranger as discarded"
	else
		echo "FAIL IBCF-A did not log the $method from $stranger as discarded"
		fail=1
	fi
done
same_nodes

echo "TrGW contexts"
# Every call above has ended, failed or never started, so each IBCF has
# released the context it reserved in its TrGW (TS 29.238 Table
# 5.17.2.1.1, Release TrGW Termination). The control API of the TrGW
# lists the contexts it holds; the IBCF releases a context once both
# dialogs of its call have ended, a moment after the UEs exit.
for trgw in trgw-a trgw-b; do
	open=
	for i in $(seq 1 10); do
		open="$(c exec -T "$trgw" wget -qO- http://127.0.0.1:8080/contexts 2>&1 | tr -d '\r\n ')"
		case "$open" in '[]' | null) break ;; esac
		sleep 1
	done
	case "$open" in
	'[]' | null) echo "ok   $trgw holds no context: the IBCF released each one it reserved" ;;
	*)
		echo "FAIL $trgw still holds contexts: $open"
		fail=1
		;;
	esac
done

echo "captures"
pcap_in() {
	c exec -T capture sh -c "ls -t /captures/$1ims-*.pcap | head -n 1" | tr -d '\r'
}
pcap_a="$(pcap_in "")"
pcap_b="$(pcap_in b/)"
pcap_nni="$(pcap_in nni/)"
echo "reading $pcap_a, $pcap_b and $pcap_nni"
# The requests of the host of no peer network are the last frames that
# the checks read.
pcap="$pcap_nni" await "the MESSAGE from the host of no peer network" \
	"sip.Method == \"MESSAGE\" && ip.src == $stranger && ip.dst == 172.28.2.10"

# The checks below read the capture that pcap names, with the helpers of
# scripts/e2e-lib.sh. Each capture has an order of its own, so the
# checks of each start again at its first frame; then they follow the
# scenarios, which ran one after the other, and within a scenario what
# each message causes. RTP flows before tshark sees the SDP of some of
# its ports.
tshark_options=(-o rtp.heuristic_rtp:TRUE)

icscf=172.28.0.20
scscf=172.28.0.30
ipsmgw=172.28.0.70
mmtel=172.28.0.75
ibcf_a=172.28.0.90
ibcf_a_nni=172.28.2.10
ibcf_b_nni=172.28.2.20
trgw_a_nni=172.28.2.11
trgw_b_nni=172.28.2.21
ibcf_b=172.28.1.90
icscf_b=172.28.1.20
scscf_b=172.28.1.30
ipsmgw_b=172.28.1.70
trgw_b=172.28.1.91
ue_b1_ip=172.28.1.100
ue_a1_ip=172.28.0.100
invite='sip.Method == "INVITE"'
ok200='sip.Status-Code == 200 && sip.CSeq.method == "INVITE"'
home_domain=ims.mnc001.mcc001.3gppnetwork.org
domain_b=ims.mnc002.mcc001.3gppnetwork.org
# The nodes of each network, as host names in SIP and SDP. A name counts
# only after a character that cannot be part of a host name, so that a
# longer name that ends the same does not count.
node_names='(pcscf|scscf|icscf|mmtel|xcap|agw|ipsmgw|hss|ibcf|trgw|epc|pcrf|dns|lab)'

echo "network A (ims0)"
pcap="$pcap_a"
last=0
# UE A1 calls UE B1: the S-CSCF finds network B by ENUM and sends the
# INVITE out of network A through IBCF-A.
expect "ENUM gives network B's domain for +1 202 555 0151" \
	'dns.qry.name == "1.5.1.0.5.5.5.2.0.2.1.e164.arpa" && dns.naptr.regex contains "ims.mnc002"'
expect "INVITE S-CSCF -> IBCF-A with the IBCF on top of Route" \
	"$invite && ip.src == $scscf && ip.dst == $ibcf_a && sip.Route contains \"ibcf.ims.mnc001\""
# UE B1 calls UE A2.
expect "INVITE IBCF-A -> I-CSCF from network B" \
	"$invite && ip.src == $ibcf_a && ip.dst == $icscf && sip.r-uri.user == \"+12025550102\""
# UE A1 sends UE B1 a short message.
expect "MESSAGE IP-SM-GW -> IBCF-A from the SC's number" \
	"sip.Method == \"MESSAGE\" && ip.dst == $ibcf_a && sip.P-Asserted-Identity contains \"tel:+12025550100\""
# UE B1 answers with one. IBCF-B put the SC's tel URI into a SIP URI of
# network B's domain, since numbers cross in SIP URIs by the agreement
# (TS 29.165 8.1).
expect "MESSAGE IBCF-A -> I-CSCF from network B's SC" \
	"sip.Method == \"MESSAGE\" && ip.src == $ibcf_a && ip.dst == $icscf && sip.r-uri.user == \"+12025550101\" && sip.P-Asserted-Identity contains \"sip:+12025550150@ims.mnc002\""
expect "MESSAGE from network B reaches network A's IP-SM-GW" \
	"sip.Method == \"MESSAGE\" && ip.src == $scscf && ip.dst == $ipsmgw && sip.P-Served-User contains \"sescase=term\" && gsm_sms.sms_text contains \"UE-A1\""
expect "MESSAGE IP-SM-GW of network A -> UE A1 path, network B's SC in the RP-OA" \
	"sip.Method == \"MESSAGE\" && ip.dst == $ue_a1_ip && gsm_sms.sms_text contains \"UE-A1\" && sip.P-Asserted-Identity contains \"ipsmgw.ims.mnc001\" && gsm_a.dtap.clg_party_bcd_num == \"12025550150\""
# The first mismatch: IBCF-A's agreement leaves MESSAGE out.
expect "405 from IBCF-A for MESSAGE without the agreement" \
	"sip.Status-Code == 405 && ip.src == $ibcf_a && sip.CSeq.method == \"MESSAGE\""

echo "the link between the networks (nni0)"
pcap="$pcap_nni"
last=0
# UE A1 calls UE B1. Each IBCF is a B2BUA: the INVITE on the link starts
# a dialog of IBCF-A's own, with its Via and its Contact only, and no
# Record-Route. The three checks of that INVITE start at the same frame.
mark
expect "INVITE IBCF-A -> IBCF-B with the Via of IBCF-A alone" \
	"$invite && ip.src == $ibcf_a_nni && ip.dst == $ibcf_b_nni && sip.Via contains \"ibcf.nni.mnc001.example.net\" && count(sip.Via) == 1"
back
expect "Contact of the caller is IBCF-A" \
	"$invite && ip.src == $ibcf_a_nni && sip.Contact contains \"sip:ibcf.nni.mnc001.example.net:5060\""
back
expect "SDP of the offer points at TrGW-A" \
	"$invite && ip.src == $ibcf_a_nni && sdp.connection_info.address == $trgw_a_nni"
# The answer and the media of each way follow the offer side by side.
mark
expect "SDP of the answer points at TrGW-B" \
	"sip.Status-Code == 183 && ip.src == $ibcf_b_nni && sdp.connection_info.address == $trgw_b_nni"
back
expect "RTP TrGW-A -> TrGW-B" "rtp && ip.src == $trgw_a_nni && ip.dst == $trgw_b_nni"
back
expect "RTP TrGW-B -> TrGW-A" "rtp && ip.src == $trgw_b_nni && ip.dst == $trgw_a_nni"
# The short messages, from network A and back, come after the call.
back
expect "MESSAGE IBCF-A -> IBCF-B with the short message, RP and TP decoded" \
	"sip.Method == \"MESSAGE\" && ip.src == $ibcf_a_nni && gsm_sms.sms_text contains \"UE-B1\""
expect "MESSAGE IBCF-B -> IBCF-A with the short message" \
	"sip.Method == \"MESSAGE\" && ip.src == $ibcf_b_nni && gsm_sms.sms_text contains \"UE-A1\""
# The mismatches on the link: no common codec, then the local number.
expect "488 from IBCF-B when no codec is left" \
	"sip.Status-Code == 488 && ip.src == $ibcf_b_nni && sip.CSeq.method == \"INVITE\""
expect "404 from IBCF-B for the local number" \
	"sip.Status-Code == 404 && ip.src == $ibcf_b_nni && sip.CSeq.method == \"INVITE\""
mismatched="$last"
# Only a focus's Contact crosses as it came, with the IBCF that passes it
# on in Record-Route (TS 24.229 5.10.5): that of the conference below.
# The responses to the focus's INVITE to UE B1 carry that Record-Route
# too (RFC 3261 12.1.1), with IBCF-B's own Contact.
focus_to_b1="$(tshark_fields "$invite && ip.src == $ibcf_a_nni && ip.dst == $ibcf_b_nni && sip.Replaces" sip.Call-ID | first_line)"
none "Record-Route on the link, other than with the Contact of a focus and in the dialog that its INVITE to UE B1 starts" \
	"sip.Record-Route && !(sip.Contact contains \"isfocus\") && sip.Call-ID != \"$focus_to_b1\""
none "P-Charging-Function-Addresses" 'sip.P-Charging-Function-Addresses'

echo "network B (ims1)"
pcap="$pcap_b"
last=0
# UE A1 calls UE B1.
expect "INVITE IBCF-B -> I-CSCF with the number in a SIP URI" \
	"$invite && ip.src == $ibcf_b && ip.dst == $icscf_b && sip.r-uri.user == \"+12025550151\" && sip.r-uri contains \"user=phone\""
expect "INVITE I-CSCF -> S-CSCF with the tel URI" \
	"$invite && ip.src == $icscf_b && ip.dst == $scscf_b && sip.r-uri == \"tel:+12025550151\""
expect "INVITE to UE B1 with TrGW-B in the SDP" \
	"$invite && ip.dst == $ue_b1_ip && sdp.connection_info.address == $trgw_b"
expect "RTP TrGW-B -> UE B1" "rtp && ip.src == $trgw_b && ip.dst == $ue_b1_ip"
# UE A1 sends UE B1 a short message.
expect "MESSAGE from network A reaches network B's IP-SM-GW" \
	"sip.Method == \"MESSAGE\" && ip.dst == $ipsmgw_b && sip.P-Served-User contains \"sescase=term\""
expect "MESSAGE IP-SM-GW of network B -> UE B1 path" \
	"sip.Method == \"MESSAGE\" && ip.dst == $ue_b1_ip && gsm_sms.sms_text contains \"UE-B1\""
# UE B1 answers with one: the IP-SM-GW of network B finds network A by
# ENUM and sends the short message to IBCF-B.
expect "ENUM gives network A's domain for +1 202 555 0101" \
	'dns.qry.name == "1.0.1.0.5.5.5.2.0.2.1.e164.arpa" && dns.naptr.regex contains "ims.mnc001"'
expect "MESSAGE IP-SM-GW -> IBCF-B from the SC's number" \
	"sip.Method == \"MESSAGE\" && ip.src == $ipsmgw_b && ip.dst == $ibcf_b && sip.Route contains \"ibcf.ims.mnc002\" && sip.P-Asserted-Identity contains \"tel:+12025550150\""

echo "one icid-value in both networks, with the IOIs of each (TS 24.229 5.4.3.2 step 7, 5.4.3.3 step 7)"
# The first call is UE A1's to UE B1. Network A's P-CSCF wrote its
# icid-value, and each network writes its own domain in the IOIs.
pcscf_b=172.28.1.40
icid="$(pcap="$pcap_a" tshark_fields "$invite && ip.src == $scscf && ip.dst == $ibcf_a" sip.P-Charging-Vector | first_line |
	sed -n 's/^icid-value=\([^;]*\);.*/\1/p')"
[ -n "$icid" ] || icid=none
pcap="$pcap_nni"
last=0
expect "INVITE IBCF-A -> IBCF-B with network A's icid-value and orig-ioi" \
	"$invite && ip.src == $ibcf_a_nni && ip.dst == $ibcf_b_nni && sip.P-Charging-Vector == \"icid-value=$icid;orig-ioi=$home_domain\""
expect "200 OK IBCF-B -> IBCF-A with network A's orig-ioi and network B's term-ioi" \
	"sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.src == $ibcf_b_nni && sip.P-Charging-Vector == \"icid-value=$icid;orig-ioi=$home_domain;term-ioi=$domain_b\""
pcap="$pcap_b"
last=0
expect "INVITE S-CSCF -> P-CSCF of network B with the same icid-value and network B's orig-ioi" \
	"$invite && ip.src == $scscf_b && ip.dst == $pcscf_b && sip.P-Charging-Vector == \"icid-value=$icid;orig-ioi=$domain_b\""
pcap="$pcap_b" none "P-Charging-Vector to UE B1" "ip.dst == $ue_b1_ip && sip.P-Charging-Vector"

echo "the dialogs of the B2BUAs"
# call_ids prints the Call-IDs of the INVITEs that filter finds in pcap.
call_ids() {
	pcap="$1" tshark_fields "$invite && $2" sip.Call-ID | grep . | sort -u
}
ids_a="$(call_ids "$pcap_a" "ip.src == $scscf && ip.dst == $ibcf_a")"
ids_nni="$(call_ids "$pcap_nni" "ip.src == $ibcf_a_nni && ip.dst == $ibcf_b_nni")"
ids_b="$(call_ids "$pcap_b" "ip.src == $ibcf_b && ip.dst == $icscf_b")"
if [ -n "$ids_a" ] && [ -n "$ids_nni" ] && [ -n "$ids_b" ] &&
	[ -z "$(comm -12 <(printf '%s\n' "$ids_a") <(printf '%s\n' "$ids_nni"))" ] &&
	[ -z "$(comm -12 <(printf '%s\n' "$ids_nni") <(printf '%s\n' "$ids_b"))" ]; then
	echo "ok   the INVITEs to IBCF-A, between the IBCFs and from IBCF-B have Call-IDs of their own ($(wc -l <<<"$ids_nni") on the link)"
else
	echo "FAIL a Call-ID crossed an IBCF: network A $(wc -w <<<"$ids_a"), link $(wc -w <<<"$ids_nni"), network B $(wc -w <<<"$ids_b")"
	fail=1
fi

# dialog_of prints the Call-ID of the first INVITE that filter finds in
# the capture that pcap names.
dialog_of() {
	tshark_fields "$invite && $1" sip.Call-ID | first_line
}
# in_dialog checks, in the capture that pcap names, the dialog of the
# first call from UE A1 to UE B1 on one side of an IBCF: the INVITE that
# filter finds, then the 200 to it and the ACK, then the BYE, from
# whichever party hung up, and the 200 to the BYE. Each step follows
# from the one before it. from_callee and to_callee are filters for the
# messages that come from the callee's side and go to it.
in_dialog() {
	local where="$1" filter="$2" from_callee="$3" to_callee="$4" id
	id="$(dialog_of "$filter")"
	if [ -z "$id" ]; then
		echo "FAIL $where: no INVITE"
		fail=1
		return
	fi
	local call="sip.Call-ID == \"$id\""
	last=0
	expect "$where: INVITE" "$invite && $call"
	expect "$where: 200 to the INVITE" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && $from_callee && $call"
	expect "$where: ACK of the 200" "sip.Method == \"ACK\" && $to_callee && $call"
	expect "$where: BYE" "sip.Method == \"BYE\" && $call"
	expect "$where: 200 to the BYE" "sip.Status-Code == 200 && sip.CSeq.method == \"BYE\" && $call"
}

echo "the call from network A to network B, dialog by dialog"
# Each IBCF ends the BYE of one dialog and sends one of its own in the
# other, so the BYE shows in each of the three dialogs of the call.
pcap="$pcap_a" in_dialog "network A, S-CSCF and IBCF-A" "ip.src == $scscf && ip.dst == $ibcf_a" \
	"ip.src == $ibcf_a" "ip.dst == $ibcf_a"
pcap="$pcap_nni" in_dialog "the link, IBCF-A and IBCF-B" "ip.src == $ibcf_a_nni && ip.dst == $ibcf_b_nni" \
	"ip.src == $ibcf_b_nni" "ip.dst == $ibcf_b_nni"
pcap="$pcap_b" in_dialog "network B, IBCF-B and the I-CSCF" "ip.src == $ibcf_b && ip.dst == $icscf_b" \
	"ip.dst == $ibcf_b" "ip.src == $ibcf_b"
# Within the dialog on the link, the reliable 183 of the callee's side
# gets its PRACK from IBCF-A, after the caller's PRACK (RFC 3262).
pcap="$pcap_nni"
link_call="sip.Call-ID == \"$(dialog_of "ip.src == $ibcf_a_nni && ip.dst == $ibcf_b_nni")\""
last=0
expect "the link: INVITE" "$invite && $link_call"
expect "the link: reliable 183 IBCF-B -> IBCF-A" "sip.Status-Code == 183 && sip.Require contains \"100rel\" && ip.src == $ibcf_b_nni && $link_call"
expect "the link: PRACK IBCF-A -> IBCF-B" "sip.Method == \"PRACK\" && ip.src == $ibcf_a_nni && $link_call"
expect "the link: 200 to the PRACK" "sip.Status-Code == 200 && sip.CSeq.method == \"PRACK\" && ip.src == $ibcf_b_nni && $link_call"

echo "the host of no peer network"
# The host sent its INVITE and then its MESSAGE after the mismatches.
pcap="$pcap_nni"
last="$mismatched"
expect "INVITE from the host of no peer network on the link" \
	"$invite && ip.src == $stranger && ip.dst == $ibcf_a_nni"
expect "MESSAGE from the host of no peer network on the link" \
	"sip.Method == \"MESSAGE\" && ip.src == $stranger && ip.dst == $ibcf_a_nni"
pcap="$pcap_nni" none "packets to the host of no peer network" "ip.dst == $stranger"
pcap="$pcap_a" none "requests of the host of no peer network inside network A" \
	'sip.From contains "+12025550198"'

echo "the merge takes UE B1's call over (TS 24.147 5.3.2.5.4; RFC 3891 3; TS 24.229 5.7.5.1, 5.10.5)"
# The focus's INVITE to UE B1 is the one INVITE with a Replaces in each
# capture. Its Replaces names UE B1's call with UE A1 by the Call-ID of
# the dialog on that side of each B2BUA: the MMTel AS and IBCF-A in
# network A, the two IBCFs on the link, IBCF-B and UE B1 in network B.
# replaced_call prints the Call-ID in the Replaces of frame $last: the
# part before its first ";".
replaced_call() {
	local value
	value="$(field sip.Replaces | first_line)"
	printf '%s\n' "${value%%;*}"
}
pcap="$pcap_a"
last=0
expect "INVITE S-CSCF -> IBCF-A from the focus, isfocus in Contact and a Replaces" \
	"$invite && ip.src == $scscf && ip.dst == $ibcf_a && sip.Replaces && sip.Contact contains \"isfocus\""
replaced_a="$(replaced_call)"
last=0
expect "the Replaces names UE A1's call with UE B1 as it reached IBCF-A" \
	"$invite && ip.src == $scscf && ip.dst == $ibcf_a && sip.Call-ID == \"$replaced_a\" && !sip.Replaces"
pcap="$pcap_nni"
last=0
expect "INVITE IBCF-A -> IBCF-B from the focus, with a Replaces" \
	"$invite && ip.src == $ibcf_a_nni && ip.dst == $ibcf_b_nni && sip.Replaces"
replaced_nni="$(replaced_call)"
last=0
expect "the Replaces names that call on the link, IBCF-A's own dialog with IBCF-B" \
	"$invite && ip.src == $ibcf_a_nni && ip.dst == $ibcf_b_nni && sip.Call-ID == \"$replaced_nni\" && !sip.Replaces"
pcap="$pcap_b"
last=0
expect "INVITE to UE B1 from the focus, with a Replaces" "$invite && ip.dst == $ue_b1_ip && sip.Replaces"
replaced_b="$(replaced_call)"
taken_over="sip.Call-ID == \"$(field sip.Call-ID | first_line)\""
expect "200 OK from UE B1: it takes the focus's call" "$ok200 && ip.src == $ue_b1_ip && $taken_over"
last=0
expect "the Replaces names UE B1's own call with UE A1" \
	"$invite && ip.dst == $ue_b1_ip && sip.Call-ID == \"$replaced_b\" && !sip.Replaces"
expect "a BYE ends that call" "sip.Method == \"BYE\" && sip.Call-ID == \"$replaced_b\""
if [ -n "$replaced_a" ] && [ -n "$replaced_nni" ] && [ -n "$replaced_b" ] &&
	[ "$replaced_a" != "$replaced_nni" ] && [ "$replaced_nni" != "$replaced_b" ] && [ "$replaced_a" != "$replaced_b" ]; then
	echo "ok   each IBCF passed the Replaces on with the Call-ID of its other side"
else
	echo "FAIL the Replaces crossed an IBCF as it came: network A \"$replaced_a\", link \"$replaced_nni\", network B \"$replaced_b\""
	fail=1
fi

echo "UE B1 joins the conference of network A (TS 24.147 5.3.1.4.1, 5.3.2.4.1; TS 24.229 5.3.2.1 step 3, 5.10.5)"
# Each capture is read from its first frame: the filters name the
# conference URI, which no other scenario uses. The INVITE crosses from
# network B, and the answer of the focus comes back with the conference
# URI and isfocus in Contact.
b1_dialog="sip.Call-ID == \"$b1_call\""
# A Contact with isfocus that names a node or a port: a conference URI
# names neither.
focus_naming_a_node="sip.Contact contains \"isfocus\" && (sip.Contact matches \"@$node_names\\\\.\" || sip.Contact matches \"@[^>]*:[0-9]+>\")"
pcap="$pcap_b"
last=0
expect "INVITE UE B1 -> P-CSCF of network B to the conference URI" \
	"$invite && ip.src == $ue_b1_ip && sip.r-uri == \"$conference_uri\" && $b1_dialog"
expect "INVITE S-CSCF -> IBCF-B: the conference URI is in network A's domain" \
	"$invite && ip.src == $scscf_b && ip.dst == $ibcf_b && sip.r-uri == \"$conference_uri\""
expect "200 OK reaches UE B1: the conference URI with isfocus in Contact, IBCF-B in Record-Route" \
	"$ok200 && ip.dst == $ue_b1_ip && $b1_dialog && sip.Contact contains \"<$conference_uri>\" && sip.Contact contains \"isfocus\" && sip.Record-Route contains \"ibcf.ims.mnc002\""
expect "BYE reaches UE B1: the conference ends when UE A1 leaves" \
	"sip.Method == \"BYE\" && ip.dst == $ue_b1_ip && $b1_dialog"
none "Contacts with isfocus in network B that name a node or a port" "$focus_naming_a_node"
pcap="$pcap_nni"
last=0
expect "INVITE IBCF-B -> IBCF-A to the conference URI" \
	"$invite && ip.src == $ibcf_b_nni && ip.dst == $ibcf_a_nni && sip.r-uri == \"$conference_uri\""
link_conference="sip.Call-ID == \"$(field sip.Call-ID | first_line)\""
expect "200 OK IBCF-A -> IBCF-B: the conference URI with isfocus in Contact, IBCF-A's external name alone in Record-Route" \
	"$ok200 && ip.src == $ibcf_a_nni && $link_conference && sip.Contact contains \"<$conference_uri>\" && sip.Contact contains \"isfocus\" && sip.Record-Route contains \"ibcf.nni.mnc001.example.net\" && count(sip.Record-Route) == 1"
expect "BYE IBCF-A -> IBCF-B: the focus ends UE B1's call" \
	"sip.Method == \"BYE\" && ip.src == $ibcf_a_nni && $link_conference"
none "Contacts with isfocus on the link that name a node or a port" "$focus_naming_a_node"
pcap="$pcap_a"
last=0
expect "INVITE IBCF-A -> I-CSCF to the conference URI" \
	"$invite && ip.src == $ibcf_a && ip.dst == $icscf && sip.r-uri == \"$conference_uri\""
# TS 23.228 5.4.12.3: the I-CSCF finds the AS that hosts the PSI by the
# NAPTR and SRV records of its subdomain (RFC 3263), not by the HSS.
expect "NAPTR query of the I-CSCF for conf.$home_domain" \
	"dns.flags.response == 0 && dns.qry.type == 35 && dns.qry.name == \"conf.$home_domain\" && ip.src == $icscf"
expect "SRV answer to the I-CSCF: _sip._udp.conf.$home_domain is the MMTel AS" \
	"dns.flags.response == 1 && dns.qry.type == 33 && dns.qry.name == \"_sip._udp.conf.$home_domain\" && dns.srv.target == \"mmtel.$home_domain\" && ip.dst == $icscf"
expect "INVITE I-CSCF -> MMTel AS to the conference URI" \
	"$invite && ip.src == $icscf && ip.dst == $mmtel && sip.r-uri == \"$conference_uri\""
expect "200 OK MMTel AS -> I-CSCF: the conference URI with isfocus in Contact" \
	"$ok200 && ip.src == $mmtel && ip.dst == $icscf && sip.Contact contains \"<$conference_uri>\" && sip.Contact contains \"isfocus\""
none "LIRs for the conference URI" "diameter.cmd.code == 302 && diameter.Public-Identity contains \"@conf.\""

echo "topology hiding"
# Over the whole captures, so for the short messages of both directions
# and the conference as much as for the calls.
pcap="$pcap_b" none "addresses of network A in network B's capture" 'ip.addr == 172.28.0.0/24 || frame contains "172.28.0."'
pcap="$pcap_nni" none "addresses of network A on the link" 'ip.addr == 172.28.0.0/24 || frame contains "172.28.0."'
pcap="$pcap_b" none "node names of network A in network B's capture" \
	"(sip || sdp) && frame matches \"[@/: <]$node_names\\\\.ims\\\\.mnc001\""
pcap="$pcap_nni" none "node names of network A on the link" \
	"(sip || sdp) && frame matches \"[@/: <]$node_names\\\\.ims\\\\.mnc001\""
pcap="$pcap_a" none "addresses of network B in network A's capture" 'ip.addr == 172.28.1.0/24 || frame contains "172.28.1."'
pcap="$pcap_nni" none "addresses of network B on the link" 'ip.addr == 172.28.1.0/24 || frame contains "172.28.1."'
pcap="$pcap_a" none "node names of network B in network A's capture" \
	"(sip || sdp) && frame matches \"[@/: <]$node_names\\\\.ims\\\\.mnc002\""
pcap="$pcap_nni" none "node names of network B on the link" \
	"(sip || sdp) && frame matches \"[@/: <]$node_names\\\\.ims\\\\.mnc002\""

# The public identities of network A hold its users' numbers, so no
# identity made from an IMSI of network A leaves it. 001010 begins
# network A's test IMSIs: its private identities carry one, and so does
# the temporary public identity that a phone without an ISIM derives,
# which the operator bars (TS 23.228 E.3.1).
no_imsi_identity() {
	local where="$1" filter='sip && frame contains "sip:001010"' frames
	frames="$(tshark_fields "$filter" | awk NF | tr '\n' ' ')"
	if [ -z "$frames" ]; then
		echo "ok   no identity made from an IMSI of network A in $where"
		return
	fi
	echo "FAIL an identity made from an IMSI of network A is in $where, in frames ${frames% }:" \
		"$(tshark_fields "$filter" sip.r-uri sip.From sip.To sip.P-Asserted-Identity | first_line)"
	fail=1
}
pcap="$pcap_b" no_imsi_identity "network B's capture"
pcap="$pcap_nni" no_imsi_identity "the link"

# Every display filter that the guide shows its readers finds something
# in one of the three captures. The one that finds network A inside the
# captures of network B and of the link must find nothing there.
none_in="$pcap_b $pcap_nni" guide_filters docs/guide/en/interconnect.md "$pcap_a" "$pcap_b" "$pcap_nni"

for pcap in "$pcap_a" "$pcap_b" "$pcap_nni"; do
	none "malformed SIP, SDP, DNS or SMS packets in $pcap" \
		'(sip || sdp || dns || gsm_sms) && (_ws.malformed || _ws.expert.severity >= warning)'
done

if [ "$fail" -ne 0 ]; then
	c logs --no-color | grep -E '"level":"(WARN|ERROR)"' | tail -n 40
fi
exit "$fail"
