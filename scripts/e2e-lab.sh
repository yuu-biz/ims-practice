#!/usr/bin/env bash
# End-to-end check: UE 1 calls UE 2, and the Lab, which
# sees only the HEP copies the nodes send, shows that call. Every SIP
# message of the call in the pcap must appear in the Lab's message
# list, hop by hop, and nothing else. The Lab also shows what a
# node sends to itself, which the capture on the bridge cannot see.
# Then the experiments of the Lab guide on the subscription of a user:
# an operator changes it through the OAM, and the Lab shows what the
# change did. Last, the two UE simulators of the phones profile make a
# call that the script drives through the Lab alone.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
export MSYS_NO_PATHCONV=1
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
for i in $(seq 1 30); do
	curl -fsS http://localhost:8080/api/calls >/dev/null 2>&1 && break
	sleep 1
done
# The subscription of the experiments below is provisioned through the
# OAM, as the guide does it.
wait_for_oam || exit 1

ue2=(--impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org
	--impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org --password test-password-2)
remember_nodes
fail=0

docker compose run --no-deps -d --name ims-practice-ue2-call ue2 answer "${ue2[@]}" --ring 1s --tone 2s >/dev/null
wait_log ims-practice-ue2-call '"msg":"waiting for a call"' || fail=1
if docker compose run --no-deps --rm ue call --tone 2s tel:+1-202-555-0102 >/dev/null; then
	echo "ok   UE 1 called UE 2"
else
	echo "FAIL the call"
	fail=1
fi
if ! timeout 60 docker wait ims-practice-ue2-call >/dev/null; then
	echo "FAIL UE 2 did not end within 60 s of the call"
	fail=1
fi
docker rm -f ims-practice-ue2-call >/dev/null

# The MMTel AS of each user is a routeing B2BUA, so the call has a
# dialog between UE 1 and its AS, one between the two ASs and one
# between the second AS and UE 2, each with a Call-ID of its own. The
# Lab lists them as one call, the caller's, by the icid-value they
# share (TS 24.229 5.7.5.1). The HEP copies of the call's end land a
# moment after UE 2 has ended, so the script asks until the Lab lists
# the call as terminated, for up to 30 s.
terminated_call() {
	calls="$(curl -fsS http://localhost:8080/api/calls)" || return 1
	legs="$("$python" -c '
import json, sys
calls = [c for c in json.load(sys.stdin) if c["state"] == "terminated"]
print(" ".join(calls[-1]["legs"]) if calls else "")
' <<<"$calls")"
	[ -n "$legs" ] && [ "$(wc -w <<<"$legs")" -eq 3 ]
}
if wait_for 30 terminated_call; then
	echo "ok   the Lab lists the call as terminated, with its three dialogs"
else
	echo "FAIL the Lab has no terminated call of three dialogs: $calls"
	exit 1
fi

# The capture container owns captures/ (as root on Linux), so the
# script keeps its own files apart.
mkdir -p e2e-out
# $legs is unquoted on purpose: one argument per Call-ID.
query="$("$python" -c 'import sys, urllib.parse; print("&".join("call_id=" + urllib.parse.quote(c, safe="") for c in sys.argv[1:]))' $legs)"
pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
of_the_call="$(sed 's/\([^ ]*\)/sip.Call-ID == "\1"/g; s/" sip/" || sip/g' <<<"$legs")"

# A hop is "source>destination first-line". The pcap holds each hop once;
# the Lab holds a copy from the sender and one from the receiver,
# which count as one hop. hops_agree reads the call's messages from the
# Lab and from the pcap, and compares them.
hops_agree() {
	curl -fsS "http://localhost:8080/api/messages?$query" >e2e-out/lab-messages.json &&
		docker compose exec -T capture tshark -r "$pcap" -Y "$of_the_call" -T fields -E separator='|' \
			-e ip.src -e udp.srcport -e tcp.srcport -e ip.dst -e udp.dstport -e tcp.dstport \
			-e sip.Request-Line -e sip.Status-Line >e2e-out/pcap-messages.txt &&
		"$python" - e2e-out/lab-messages.json e2e-out/pcap-messages.txt >e2e-out/lab-hops.txt <<'EOF'
import collections, json, sys

with open(sys.argv[1], encoding="utf-8") as f:
    messages = json.load(f)
lab = set()
# The Cx messages that the call caused share its call_id; they are
# checked below.
for m in messages:
    if m["protocol"] != "sip":
        continue
    first = m["payload"].split("\r\n", 1)[0]
    lab.add((m["src"], m["dst"], first, m["payload"]))
# A node that sends to itself, as the S-CSCF does when it serves both
# ends of a call, never puts the message on the bridge, so only the
# Lab sees it.
inside = collections.Counter((s, d, first) for s, d, first, _ in lab if s == d)
lab = collections.Counter((s, d, first) for s, d, first, _ in lab if s != d)

pcap = collections.Counter()
with open(sys.argv[2], encoding="utf-8") as f:
    for line in f:
        f_ = line.rstrip("\r\n").split("|")
        if len(f_) < 8:
            continue
        src = f_[0] + ":" + (f_[1] or f_[2])
        dst = f_[3] + ":" + (f_[4] or f_[5])
        pcap[(src, dst, f_[6] or f_[7])] += 1

missing = pcap - lab
extra = lab - pcap
for hop in sorted(missing):
    print("  missing from the Lab:", hop)
for hop in sorted(extra):
    print("  only in the Lab:", hop)
print(f"  {sum(pcap.values())} hops in the pcap, {sum(lab.values())} in the Lab, {sum(inside.values())} inside a node")
sys.exit(1 if missing or extra or not pcap else 0)
EOF
}
# The last HEP copies and frames of the call may land after the Lab has
# listed it as terminated, so the script reads both again until they
# agree, for up to 30 s, and shows the comparison it ended with.
: >e2e-out/lab-hops.txt
if wait_for 30 hops_agree; then
	cat e2e-out/lab-hops.txt
	echo "ok   every SIP hop of the call in the pcap is in the Lab, and no other"
else
	cat e2e-out/lab-hops.txt
	echo "FAIL the Lab and the pcap differ"
	fail=1
fi

# The I-CSCF's LIR for the callee belongs to the call (TS 24.229 5.3.2.1),
# on the dialog between the two ASs.
# The LIR for the P-CSCF's reg event SUBSCRIBE, sent as the call starts,
# belongs to the SUBSCRIBE and must not show up here.
cx="$("$python" -c '
import json, sys
print(" ".join(sorted({m["summary"] for m in json.load(sys.stdin) if m["protocol"] == "diameter"})))
' <e2e-out/lab-messages.json)"
if [ "$cx" = "LIA 2001 LIR" ]; then
	echo "ok   the call's Cx messages are its LIR and LIA"
else
	echo "FAIL the call's Cx messages are: $cx"
	fail=1
fi

registrations="$(curl -fsS http://localhost:8080/api/registrations)"
if grep -q '"impu":"sip:+12025550101@' <<<"$registrations"; then
	echo "ok   the Lab lists UE 1's registration"
else
	echo "FAIL no registration for UE 1: $registrations"
	fail=1
fi
if grep -qi '<html' <<<"$(curl -fsS http://localhost:8080/)"; then
	echo "ok   the Lab serves its page"
else
	echo "FAIL no page at /"
	fail=1
fi

# The experiments of the Lab guide on the subscription of a user: an
# operator adds a subscription through the OAM, a UE registers it, the
# operator gives it another service profile and then removes it. The
# profile reaches its S-CSCF in a PPR (TS 29.228 6.6.1). The removal
# reaches it in an RTR, the S-CSCF tells the UE with a reg-event NOTIFY,
# and the UE can no longer register. The Lab shows what each change did
# and changes no subscription itself.
impi5=001010000000005@ims.mnc001.mcc001.3gppnetwork.org
ue5=(--impi "$impi5" --impu sip:$impi5 --password test-password-5)
subscription5() { printf '{"public_identities":["sip:%s"],"password":"test-password-5"%s}' "$impi5" "$1"; }
status="$(oam_request PUT "/oam/v1/subscriptions/$impi5" "$(subscription5 "")" | oam_status)"
if [ "$status" = 201 ]; then
	echo "ok   a subscription was added through the OAM"
else
	echo "FAIL adding a subscription answered $status"
	fail=1
fi
docker compose run --no-deps -d --name ims-practice-ue5 ue register "${ue5[@]}" >/dev/null
wait_log ims-practice-ue5 '"msg":"registered"' || fail=1
if grep -q '"registration_state":"Registered"' <<<"$(curl -fsS http://localhost:8080/api/subscribers)"; then
	echo "ok   the Lab lists the new subscription as registered"
else
	echo "FAIL the new subscription is not registered in the Lab's list"
	fail=1
fi
# The subscription named no service profile, so it had the one that the
# settings of the HSS name for any other subscription. Sending it again
# with the profile that holds no criteria at all changes the user
# profile its S-CSCF holds.
status="$(oam_request PUT "/oam/v1/subscriptions/$impi5" "$(subscription5 ',"service_profile":"no-services"')" | oam_status)"
subscribers="$(curl -fsS http://localhost:8080/api/subscribers)"
if [ "$status" = 200 ] && grep -q '"service_profile":"no-services","effective_service_profile":"no-services"' <<<"$subscribers"; then
	echo "ok   the OAM gave the registered subscription another service profile"
else
	echo "FAIL the replacement with another service profile answered $status: $subscribers"
	fail=1
fi
# The OAM answers the removal with the outcome of the record, not 204.
status="$(oam_request DELETE "/oam/v1/subscriptions/$impi5" | oam_status)"
if [ "$status" = 200 ] && [ "$(timeout 30 docker wait ims-practice-ue5)" = "1" ] &&
	grep -q 'rejected' <<<"$(docker logs ims-practice-ue5 2>&1)"; then
	echo "ok   removing the subscription ended its registration"
else
	echo "FAIL removing the subscription (DELETE $status)"
	docker logs ims-practice-ue5 2>&1 | tail -n 5
	fail=1
fi
docker rm -f ims-practice-ue5 >/dev/null
# The UE exits 1 when it is refused, as it should be here; the check
# reads its log, hence "|| true".
out="$(docker compose run --no-deps --rm ue register "${ue5[@]}" 2>/dev/null || true)"
if grep -q '403' <<<"$out"; then
	echo "ok   the user of the removed subscription gets 403"
else
	echo "FAIL the user of the removed subscription was not refused: $(tail -n 2 <<<"$out")"
	fail=1
fi
pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
# The RTA is the last frame that this check reads.
await "the RTA of the S-CSCF" 'diameter.cmd.code == 304 && diameter.flags.request == 0'
rtr="$(docker compose exec -T capture tshark -r "$pcap" -Y 'diameter.cmd.code == 304' -T fields -E separator=, -e ip.src -e ip.dst | tr -d '\r\n')"
if [ "$rtr" = "172.28.0.10,172.28.0.30172.28.0.30,172.28.0.10" ]; then
	echo "ok   the capture holds the RTR from the HSS to the S-CSCF and its RTA"
else
	echo "FAIL no RTR and RTA in the capture: $rtr"
	fail=1
fi
# The PPR of the service profile went out before that RTR, so the
# capture holds it by now.
ppr="$(docker compose exec -T capture tshark -r "$pcap" -Y 'diameter.cmd.code == 305' -T fields -E separator=, -e ip.src -e ip.dst | tr -d '\r\n')"
if [ "$ppr" = "172.28.0.10,172.28.0.30172.28.0.30,172.28.0.10" ]; then
	echo "ok   the capture holds the PPR from the HSS to the S-CSCF and its PPA"
else
	echo "FAIL no PPR and PPA in the capture: $ppr"
	fail=1
fi

# The UE simulators of the phones profile, driven through the Lab.
# They are subscribers 1 and 2, as ue and ue2 above, whose containers
# have all ended by now.
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

# The VoLTE UEs of the volte profile are listed too, and so is phone3,
# the third UE of the phones profile for conferences; they stay down here.
volte="volte10 unreachable idle,volte12 unreachable idle,phone3 unreachable idle"
if [ "$(ues)" = "phone1 unreachable idle,phone2 unreachable idle,$volte" ]; then
	echo "ok   the Lab lists the UE simulators as unreachable before they start"
else
	echo "FAIL the UE simulators before they start: $(ues)"
	fail=1
fi
# They restart by themselves and hold subscribers 1 and 2, so they must
# not outlive the script.
trap 'docker compose --profile phones rm -sf phone1 phone2 >/dev/null 2>&1' EXIT
docker compose --profile phones up -d --no-deps phone1 phone2
if wait_ues "phone1 registered idle,phone2 registered idle,$volte"; then
	echo "ok   both UE simulators report through the Lab that they are registered"
else
	echo "FAIL the UE simulators did not register: $(ues)"
	exit 1
fi

status="$(ue_post phone2/answer-policy '{"mode":"manual"}' PUT)"
call_status="$(ue_post phone1/call '{"target":"tel:+12025550102"}')"
if [ "$status" = 200 ] && [ "$call_status" = 200 ] && wait_ues "*phone2 registered ringing,*"; then
	echo "ok   phone1 called phone2 through the Lab, and phone2 rings"
else
	echo "FAIL the call from phone1 (policy $status, call $call_status): $(ues)"
	exit 1
fi
status="$(ue_post phone2/answer)"
if [ "$status" = 200 ] && wait_ues "phone1 registered active,phone2 registered active,$volte"; then
	echo "ok   phone2 answered through the Lab, and both UEs are in the call"
else
	echo "FAIL answering (status $status): $(ues)"
	exit 1
fi
phones_call_id="$(curl -fsS http://localhost:8080/api/ues | "$python" -c '
import json, sys
print(json.load(sys.stdin)[0]["status"]["call"]["call_id"])
' | tr -d '\r')"
status="$(ue_post phone1/hangup)"
if [ "$status" = 200 ] && wait_ues "phone1 registered idle,phone2 registered idle,$volte"; then
	echo "ok   phone1 hung up through the Lab"
else
	echo "FAIL hanging up (status $status): $(ues)"
	fail=1
fi

# The HEP copies of the BYE land a moment after the UEs are idle, so
# the script asks until the Lab lists the call as terminated, for up to
# 30 s.
phones_terminated() {
	state="$(curl -fsS http://localhost:8080/api/calls | "$python" -c '
import json, sys
print(" ".join(c["state"] for c in json.load(sys.stdin) if c["call_id"] == sys.argv[1]))
' "$phones_call_id" | tr -d '\r')"
	[ "$state" = terminated ]
}
if wait_for 30 phones_terminated; then
	echo "ok   the Lab lists the call $phones_call_id as terminated"
else
	echo "FAIL the call $phones_call_id is \"$state\" in the Lab"
	fail=1
fi
same_nodes
pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
# The 200 to phone1's BYE is the last frame that this check reads.
await "the 200 OK to phone1's BYE" "sip.Call-ID == \"$phones_call_id\" && sip.CSeq.method == \"BYE\" && sip.Status-Code == 200 && ip.dst == 172.28.0.110"
bye="$(docker compose exec -T capture tshark -r "$pcap" \
	-Y "sip.Call-ID == \"$phones_call_id\" && sip.CSeq.method == \"BYE\" && (ip.src == 172.28.0.110 || ip.dst == 172.28.0.110)" \
	-T fields -E separator=, -e ip.src -e ip.dst -e sip.Method -e sip.Status-Code | tr -d '\r' | tr '\n' ' ')"
if [ "$bye" = "172.28.0.110,172.28.0.40,BYE, 172.28.0.40,172.28.0.110,,200 " ]; then
	echo "ok   the capture holds phone1's BYE and its 200"
else
	echo "FAIL phone1's BYE and its 200 are not in the capture: $bye"
	fail=1
fi

exit "$fail"
