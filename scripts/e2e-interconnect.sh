#!/usr/bin/env bash
# End-to-end check: two IMS networks joined by their IBCFs and
# TrGWs (deploy/interconnect). Network A is compose.yaml, network B the
# peer network of deploy/interconnect/compose.yaml. A UE of each network
# registers; UE A1 calls UE B1 by number, and UE B1 calls UE A2; UE A1
# sends UE B1 a short message, and UE B1 answers UE A1 with one. Then
# the mismatch scenarios switch the interconnect profiles: a mismatch
# fails with the status it should, and the adjusted profile passes. A
# host on the link that belongs to no peer sends IBCF-A requests, which
# get no answer and do not get into network A. Last, the captures of
# the three bridges are read: each IBCF is a B2BUA, so the INVITEs on
# either side of it have Call-IDs of their own; network B's capture
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
# stay in captures/, captures/peer/ and captures/nni/.
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
for service in icscf scscf ipsmgw peer-icscf peer-scscf peer-ipsmgw; do
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
# deploy/interconnect/peer/hss.yaml, behind network B's P-CSCF.
peer=(--domain ims.mnc002.mcc001.3gppnetwork.org
	--pcscf 172.28.1.40:5060 --pcscf-uri sip:pcscf.ims.mnc002.mcc001.3gppnetwork.org:5060)
# Only "ue sms" has -sc: the SC that UE B1's short messages go to.
sc_b=(--sc +12025550150)
ue_b1=("${peer[@]}" --impi 001020000000001@ims.mnc002.mcc001.3gppnetwork.org
	--impu sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org --password test-password-b1)
ue_a2=(--impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org
	--impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org --password test-password-2)
gsm_text='Hello UE-B1, this is UE-A1 in another network.'
gsm_text_back='Hello UE-A1, this is UE-B1 answering.'
local_number='sip:2025550151;phone-context=+1@ims.mnc002.mcc001.3gppnetwork.org;user=phone'
rm -f captures/interconnect-a1-sent.raw captures/interconnect-b1-received.raw
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
	c run --no-deps -d --name "${project}-ue-b1" peer-ue answer "${ue_b1[@]}" --ring 1s --tone 4s "$@" >/dev/null
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
if c run --no-deps --rm peer-ue call "${ue_b1[@]}" --tone 3s tel:+12025550102 >/dev/null 2>&1 &&
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
c run --no-deps -d --name "${project}-ue-b1" peer-ue register "${ue_b1[@]}" >/dev/null
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
if out="$(c run --no-deps --rm peer-ue sms "${ue_b1[@]}" "${sc_b[@]}" --status-report +12025550101 "$gsm_text_back" 2>&1)" &&
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
c run --no-deps -d --name "${project}-ue-b1" peer-ue register "${ue_b1[@]}" >/dev/null
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

echo "requests from an address of no peer network"
# A host on the link that is neither IBCF-B nor a TrGW sends IBCF-A an
# INVITE and a MESSAGE over UDP. IBCF-A finds no peer for the address
# and drops both without any answer, not even the 100 (Trying) of the
# INVITE, so that the host learns nothing about network A.
stranger=172.28.2.99
stranger_from='<sip:+12025550199@example.invalid>;tag=no-peer'
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
home_pcap="$(pcap_in "")"
peer_pcap="$(pcap_in peer/)"
nni_pcap="$(pcap_in nni/)"
echo "reading $home_pcap, $peer_pcap and $nni_pcap"
# The requests of the host of no peer network are the last frames that
# the checks read.
pcap="$nni_pcap" await "the MESSAGE from the host of no peer network" \
	"sip.Method == \"MESSAGE\" && ip.src == $stranger && ip.dst == 172.28.2.10"

# The checks below read the capture that pcap names, with the helpers of
# scripts/e2e-lib.sh. RTP flows before tshark sees the SDP of some of
# its ports.
tshark_options=(-o rtp.heuristic_rtp:TRUE)
last=0
# some checks that the capture has a frame that passes the filter,
# anywhere in it: the checks of this section look at the messages of
# separate scenarios, in no order between them.
some() {
	last=0
	expect "$@"
}

icscf=172.28.0.20
scscf=172.28.0.30
ipsmgw=172.28.0.70
ibcf_a=172.28.0.90
ibcf_a_nni=172.28.2.10
ibcf_b_nni=172.28.2.20
trgw_a_nni=172.28.2.11
trgw_b_nni=172.28.2.21
ibcf_b=172.28.1.90
peer_icscf=172.28.1.20
peer_scscf=172.28.1.30
peer_ipsmgw=172.28.1.70
trgw_b=172.28.1.91
ue_b1_ip=172.28.1.100
ue_a1_ip=172.28.0.100
invite='sip.Method == "INVITE"'

echo "network A (ims0)"
pcap="$home_pcap" some "ENUM gives network B's domain for +1 202 555 0151" \
	'dns.qry.name == "1.5.1.0.5.5.5.2.0.2.1.e164.arpa" && dns.naptr.regex contains "ims.mnc002"'
pcap="$home_pcap" some "INVITE S-CSCF -> IBCF-A with the IBCF on top of Route" \
	"$invite && ip.src == $scscf && ip.dst == $ibcf_a && sip.Route contains \"ibcf.ims.mnc001\""
pcap="$home_pcap" some "MESSAGE IP-SM-GW -> IBCF-A from the SC's number" \
	"sip.Method == \"MESSAGE\" && ip.dst == $ibcf_a && sip.P-Asserted-Identity contains \"tel:+12025550100\""
pcap="$home_pcap" some "INVITE IBCF-A -> I-CSCF from network B" \
	"$invite && ip.src == $ibcf_a && ip.dst == $icscf && sip.r-uri.user == \"+12025550102\""
# IBCF-B put the SC's tel URI into a SIP URI of network B's domain,
# since numbers cross in SIP URIs by the agreement (TS 29.165 8.1).
pcap="$home_pcap" some "MESSAGE IBCF-A -> I-CSCF from network B's SC" \
	"sip.Method == \"MESSAGE\" && ip.src == $ibcf_a && ip.dst == $icscf && sip.r-uri.user == \"+12025550101\" && sip.P-Asserted-Identity contains \"sip:+12025550150@ims.mnc002\""
pcap="$home_pcap" some "MESSAGE from network B reaches network A's IP-SM-GW" \
	"sip.Method == \"MESSAGE\" && ip.src == $scscf && ip.dst == $ipsmgw && sip.P-Served-User contains \"sescase=term\" && gsm_sms.sms_text contains \"UE-A1\""
pcap="$home_pcap" some "MESSAGE IP-SM-GW of network A -> UE A1 path, network B's SC in the RP-OA" \
	"sip.Method == \"MESSAGE\" && ip.dst == $ue_a1_ip && gsm_sms.sms_text contains \"UE-A1\" && sip.P-Asserted-Identity contains \"ipsmgw.ims.mnc001\" && gsm_a.dtap.clg_party_bcd_num == \"12025550150\""
pcap="$home_pcap" some "405 from IBCF-A for MESSAGE without the agreement" \
	"sip.Status-Code == 405 && ip.src == $ibcf_a && sip.CSeq.method == \"MESSAGE\""

echo "the link between the networks (nni0)"
# Each IBCF is a B2BUA: the INVITE on the link starts a dialog of
# IBCF-A's own, with its Via and its Contact only, and no Record-Route.
pcap="$nni_pcap" some "INVITE IBCF-A -> IBCF-B with the Via of IBCF-A alone" \
	"$invite && ip.src == $ibcf_a_nni && ip.dst == $ibcf_b_nni && sip.Via contains \"ibcf.nni.mnc001.example.net\" && count(sip.Via) == 1"
pcap="$nni_pcap" none "Record-Route on the link" 'sip.Record-Route'
pcap="$nni_pcap" some "Contact of the caller is IBCF-A" \
	"$invite && ip.src == $ibcf_a_nni && sip.Contact contains \"sip:ibcf.nni.mnc001.example.net:5060\""
pcap="$nni_pcap" some "SDP of the offer points at TrGW-A" \
	"$invite && ip.src == $ibcf_a_nni && sdp.connection_info.address == $trgw_a_nni"
pcap="$nni_pcap" some "SDP of the answer points at TrGW-B" \
	"sip.Status-Code == 183 && ip.src == $ibcf_b_nni && sdp.connection_info.address == $trgw_b_nni"
pcap="$nni_pcap" some "RTP TrGW-A -> TrGW-B" "rtp && ip.src == $trgw_a_nni && ip.dst == $trgw_b_nni"
pcap="$nni_pcap" some "RTP TrGW-B -> TrGW-A" "rtp && ip.src == $trgw_b_nni && ip.dst == $trgw_a_nni"
pcap="$nni_pcap" some "MESSAGE IBCF-A -> IBCF-B with the short message, RP and TP decoded" \
	"sip.Method == \"MESSAGE\" && ip.src == $ibcf_a_nni && gsm_sms.sms_text contains \"UE-B1\""
pcap="$nni_pcap" some "MESSAGE IBCF-B -> IBCF-A with the short message" \
	"sip.Method == \"MESSAGE\" && ip.src == $ibcf_b_nni && gsm_sms.sms_text contains \"UE-A1\""
pcap="$nni_pcap" some "404 from IBCF-B for the local number" \
	"sip.Status-Code == 404 && ip.src == $ibcf_b_nni && sip.CSeq.method == \"INVITE\""
pcap="$nni_pcap" some "488 from IBCF-B when no codec is left" \
	"sip.Status-Code == 488 && ip.src == $ibcf_b_nni && sip.CSeq.method == \"INVITE\""
pcap="$nni_pcap" none "P-Charging-Function-Addresses" 'sip.P-Charging-Function-Addresses'

echo "network B (ims1)"
pcap="$peer_pcap" some "INVITE IBCF-B -> I-CSCF with the number in a SIP URI" \
	"$invite && ip.src == $ibcf_b && ip.dst == $peer_icscf && sip.r-uri.user == \"+12025550151\" && sip.r-uri contains \"user=phone\""
pcap="$peer_pcap" some "INVITE I-CSCF -> S-CSCF with the tel URI" \
	"$invite && ip.src == $peer_icscf && ip.dst == $peer_scscf && sip.r-uri == \"tel:+12025550151\""
pcap="$peer_pcap" some "INVITE to UE B1 with TrGW-B in the SDP" \
	"$invite && ip.dst == $ue_b1_ip && sdp.connection_info.address == $trgw_b"
pcap="$peer_pcap" some "RTP TrGW-B -> UE B1" "rtp && ip.src == $trgw_b && ip.dst == $ue_b1_ip"
pcap="$peer_pcap" some "MESSAGE from network A reaches network B's IP-SM-GW" \
	"sip.Method == \"MESSAGE\" && ip.dst == $peer_ipsmgw && sip.P-Served-User contains \"sescase=term\""
pcap="$peer_pcap" some "MESSAGE IP-SM-GW of network B -> UE B1 path" \
	"sip.Method == \"MESSAGE\" && ip.dst == $ue_b1_ip && gsm_sms.sms_text contains \"UE-B1\""
pcap="$peer_pcap" some "ENUM gives network A's domain for +1 202 555 0101" \
	'dns.qry.name == "1.0.1.0.5.5.5.2.0.2.1.e164.arpa" && dns.naptr.regex contains "ims.mnc001"'
pcap="$peer_pcap" some "MESSAGE IP-SM-GW -> IBCF-B from the SC's number" \
	"sip.Method == \"MESSAGE\" && ip.src == $peer_ipsmgw && ip.dst == $ibcf_b && sip.Route contains \"ibcf.ims.mnc002\" && sip.P-Asserted-Identity contains \"tel:+12025550150\""

echo "one icid-value in both networks, with the IOIs of each (TS 24.229 5.4.3.2 step 7, 5.4.3.3 step 7)"
# The first call is UE A1's to UE B1. Network A's P-CSCF wrote its
# icid-value, and each network writes its own domain in the IOIs.
home_domain=ims.mnc001.mcc001.3gppnetwork.org
peer_domain=ims.mnc002.mcc001.3gppnetwork.org
peer_pcscf=172.28.1.40
icid="$(pcap="$home_pcap" tshark_fields "$invite && ip.src == $scscf && ip.dst == $ibcf_a" sip.P-Charging-Vector | first_line |
	sed -n 's/^icid-value=\([^;]*\);.*/\1/p')"
[ -n "$icid" ] || icid=none
pcap="$nni_pcap" some "INVITE IBCF-A -> IBCF-B with network A's icid-value and orig-ioi" \
	"$invite && ip.src == $ibcf_a_nni && ip.dst == $ibcf_b_nni && sip.P-Charging-Vector == \"icid-value=$icid;orig-ioi=$home_domain\""
pcap="$nni_pcap" some "200 OK IBCF-B -> IBCF-A with network A's orig-ioi and network B's term-ioi" \
	"sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.src == $ibcf_b_nni && sip.P-Charging-Vector == \"icid-value=$icid;orig-ioi=$home_domain;term-ioi=$peer_domain\""
pcap="$peer_pcap" some "INVITE S-CSCF -> P-CSCF of network B with the same icid-value and network B's orig-ioi" \
	"$invite && ip.src == $peer_scscf && ip.dst == $peer_pcscf && sip.P-Charging-Vector == \"icid-value=$icid;orig-ioi=$peer_domain\""
pcap="$peer_pcap" none "P-Charging-Vector to UE B1" "ip.dst == $ue_b1_ip && sip.P-Charging-Vector"

echo "the dialogs of the B2BUAs"
# call_ids prints the Call-IDs of the INVITEs that filter finds in pcap.
call_ids() {
	pcap="$1" tshark_fields "$invite && $2" sip.Call-ID | grep . | sort -u
}
home_ids="$(call_ids "$home_pcap" "ip.src == $scscf && ip.dst == $ibcf_a")"
nni_ids="$(call_ids "$nni_pcap" "ip.src == $ibcf_a_nni && ip.dst == $ibcf_b_nni")"
peer_ids="$(call_ids "$peer_pcap" "ip.src == $ibcf_b && ip.dst == $peer_icscf")"
if [ -n "$home_ids" ] && [ -n "$nni_ids" ] && [ -n "$peer_ids" ] &&
	[ -z "$(comm -12 <(printf '%s\n' "$home_ids") <(printf '%s\n' "$nni_ids"))" ] &&
	[ -z "$(comm -12 <(printf '%s\n' "$nni_ids") <(printf '%s\n' "$peer_ids"))" ]; then
	echo "ok   the INVITEs to IBCF-A, between the IBCFs and from IBCF-B have Call-IDs of their own ($(wc -l <<<"$nni_ids") on the link)"
else
	echo "FAIL a Call-ID crossed an IBCF: network A $(wc -w <<<"$home_ids"), link $(wc -w <<<"$nni_ids"), network B $(wc -w <<<"$peer_ids")"
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
pcap="$home_pcap" in_dialog "network A, S-CSCF and IBCF-A" "ip.src == $scscf && ip.dst == $ibcf_a" \
	"ip.src == $ibcf_a" "ip.dst == $ibcf_a"
pcap="$nni_pcap" in_dialog "the link, IBCF-A and IBCF-B" "ip.src == $ibcf_a_nni && ip.dst == $ibcf_b_nni" \
	"ip.src == $ibcf_b_nni" "ip.dst == $ibcf_b_nni"
pcap="$peer_pcap" in_dialog "network B, IBCF-B and the I-CSCF" "ip.src == $ibcf_b && ip.dst == $peer_icscf" \
	"ip.dst == $ibcf_b" "ip.src == $ibcf_b"
# Within the dialog on the link, the reliable 183 of the callee's side
# gets its PRACK from IBCF-A, after the caller's PRACK (RFC 3262).
pcap="$nni_pcap"
link_call="sip.Call-ID == \"$(dialog_of "ip.src == $ibcf_a_nni && ip.dst == $ibcf_b_nni")\""
last=0
expect "the link: INVITE" "$invite && $link_call"
expect "the link: reliable 183 IBCF-B -> IBCF-A" "sip.Status-Code == 183 && sip.Require contains \"100rel\" && ip.src == $ibcf_b_nni && $link_call"
expect "the link: PRACK IBCF-A -> IBCF-B" "sip.Method == \"PRACK\" && ip.src == $ibcf_a_nni && $link_call"
expect "the link: 200 to the PRACK" "sip.Status-Code == 200 && sip.CSeq.method == \"PRACK\" && ip.src == $ibcf_b_nni && $link_call"

echo "the host of no peer network"
pcap="$nni_pcap" some "INVITE from the host of no peer network on the link" \
	"$invite && ip.src == $stranger && ip.dst == $ibcf_a_nni"
pcap="$nni_pcap" some "MESSAGE from the host of no peer network on the link" \
	"sip.Method == \"MESSAGE\" && ip.src == $stranger && ip.dst == $ibcf_a_nni"
pcap="$nni_pcap" none "packets to the host of no peer network" "ip.dst == $stranger"
pcap="$home_pcap" none "requests of the host of no peer network inside network A" \
	'sip.From contains "+12025550199"'

echo "topology hiding"
# Over the whole captures, so for the short messages of both directions
# as much as for the calls.
# The nodes of each network, as host names in SIP and SDP. A name counts
# only after a character that cannot be part of a host name, so that a
# longer name that ends the same does not count.
node_names='(pcscf|scscf|icscf|mmtel|xcap|agw|ipsmgw|hss|ibcf|trgw|epc|pcrf|dns|lab)'
pcap="$peer_pcap" none "addresses of network A in network B's capture" 'ip.addr == 172.28.0.0/24 || frame contains "172.28.0."'
pcap="$nni_pcap" none "addresses of network A on the link" 'ip.addr == 172.28.0.0/24 || frame contains "172.28.0."'
pcap="$peer_pcap" none "node names of network A in network B's capture" \
	"(sip || sdp) && frame matches \"[@/: <]$node_names\\\\.ims\\\\.mnc001\""
pcap="$nni_pcap" none "node names of network A on the link" \
	"(sip || sdp) && frame matches \"[@/: <]$node_names\\\\.ims\\\\.mnc001\""
pcap="$home_pcap" none "addresses of network B in network A's capture" 'ip.addr == 172.28.1.0/24 || frame contains "172.28.1."'
pcap="$nni_pcap" none "addresses of network B on the link" 'ip.addr == 172.28.1.0/24 || frame contains "172.28.1."'
pcap="$home_pcap" none "node names of network B in network A's capture" \
	"(sip || sdp) && frame matches \"[@/: <]$node_names\\\\.ims\\\\.mnc002\""
pcap="$nni_pcap" none "node names of network B on the link" \
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
pcap="$peer_pcap" no_imsi_identity "network B's capture"
pcap="$nni_pcap" no_imsi_identity "the link"

for pcap in "$home_pcap" "$peer_pcap" "$nni_pcap"; do
	none "malformed SIP, SDP, DNS or SMS packets in $pcap" \
		'(sip || sdp || dns || gsm_sms) && (_ws.malformed || _ws.expert.severity >= warning)'
done

if [ "$fail" -ne 0 ]; then
	c logs --no-color | grep -E '"level":"(WARN|ERROR)"' | tail -n 40
fi
exit "$fail"
