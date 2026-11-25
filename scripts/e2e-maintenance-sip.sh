#!/usr/bin/env bash
# End-to-end check: the maintenance API takes each node that the SIP
# requests of a registration and of a call go through out of service,
# and back, on the deployment of scripts/e2e-observability.sh. A shut
# down makes the S-CSCF answer a new REGISTER with 503 and Retry-After,
# which the capture shows hop by hop, and an unlock lets the UE register
# again. A shut down of the P-CSCF makes the P-CSCF answer the REGISTER
# itself with 503 and Retry-After, forwarding nothing, and a shut down
# of the I-CSCF makes the I-CSCF answer 503, after which the P-CSCF,
# with no other I-CSCF left, answers the UE 504.
# A shut down of the MMTel AS makes the AS answer 503 to the INVITE that
# the filter criteria send it, which the S-CSCF turns into the 500 that
# ends the call, since the criterion of the originating case says
# SESSION_TERMINATED; after the unlock the call goes through the AS on
# both legs again. A shut down of the MRFC makes the MRFC answer 503
# with Retry-After to the INVITE with which the AS asks it for an
# announcement, so a barred call of UE 1 is rejected without the early
# media of the MRFP; after the unlock the caller hears the announcement
# again. A shut down of the media relay makes it answer the Add of the
# P-CSCF with 503 over its control API, and the P-CSCF, which has no
# other relay in this network, answers the INVITE of UE 1 with 500 and
# forwards nothing; after the unlock the call goes through the relay
# again and the relay lets go of its context. A shut down of the MRFP
# does the same to the MRFC, which then has no MRFP left and answers the
# AS 503, so a barred call of UE 1 is rejected without an announcement
# again.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
. scripts/e2e-observability-lib.sh

start_network
remember_nodes
fail=0

echo "drain of the S-CSCF"
# No transaction is in progress, so the shut down makes the S-CSCF
# LOCKED at once; SHUTTINGDOWN refuses a new REGISTER all the same. A
# request that fails leaves its answer empty, hence "|| true", and the
# check reports it with the state of the node.
answer="$(administrative_state scscf SHUTTINGDOWN || true)"
state="$(node_state scscf || true)"
if grep -Eq '"administrative_state":"(SHUTTINGDOWN|LOCKED)"' <<<"$state"; then
	echo "ok   the maintenance API shut the S-CSCF down"
else
	echo "FAIL the shut down of the S-CSCF: $answer"
	echo "     state of the node: $state"
	fail=1
fi
if docker compose run --no-deps --rm ue register -deregister-after 2s 2>/dev/null; then
	echo "FAIL UE registered with a shut down S-CSCF"
	fail=1
else
	echo "ok   UE could not register while the S-CSCF is shut down"
fi
answer="$(administrative_state scscf UNLOCKED || true)"
state="$(node_state scscf || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the S-CSCF"
else
	echo "FAIL the unlock of the S-CSCF: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# The checks read the capture up to the 200 OK of this registration:
# the one after those that the S-CSCF has sent so far.
pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
registered='sip.CSeq.method == "REGISTER" && sip.Status-Code == 200 && ip.src == 172.28.0.30'
registrations="$(count "$registered")"
if docker compose run --no-deps --rm ue register -deregister-after 2s; then
	echo "ok   UE registered after the unlock of the S-CSCF"
else
	echo "FAIL UE registration after the unlock of the S-CSCF"
	fail=1
fi
await "the 200 OK to the registration after the unlock of the S-CSCF" "$registered" $((registrations + 1))

echo "drain of the P-CSCF"
# The UEs that registered above have deregistered, but the P-CSCF counts
# each as a user until it forgets its IP association, 64*T1 later, so
# the shut down may leave it SHUTTINGDOWN for a while. Either state
# refuses a new REGISTER.
answer="$(administrative_state pcscf SHUTTINGDOWN || true)"
state="$(node_state pcscf || true)"
if grep -Eq '"administrative_state":"(SHUTTINGDOWN|LOCKED)"' <<<"$state"; then
	echo "ok   the maintenance API shut the P-CSCF down"
else
	echo "FAIL the shut down of the P-CSCF: $answer"
	echo "     state of the node: $state"
	fail=1
fi
if docker compose run --no-deps --rm ue register -deregister-after 2s 2>/dev/null; then
	echo "FAIL UE registered with a shut down P-CSCF"
	fail=1
else
	echo "ok   UE could not register while the P-CSCF is shut down"
fi
answer="$(administrative_state pcscf UNLOCKED || true)"
state="$(node_state pcscf || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the P-CSCF"
else
	echo "FAIL the unlock of the P-CSCF: $answer"
	echo "     state of the node: $state"
	fail=1
fi
registrations="$(count "$registered")"
if docker compose run --no-deps --rm ue register -deregister-after 2s; then
	echo "ok   UE registered after the unlock of the P-CSCF"
else
	echo "FAIL UE registration after the unlock of the P-CSCF"
	fail=1
fi
await "the 200 OK to the registration after the unlock of the P-CSCF" "$registered" $((registrations + 1))

echo "drain of the I-CSCF"
# The I-CSCF serves no transaction between requests, so it is LOCKED at
# once.
answer="$(administrative_state icscf SHUTTINGDOWN || true)"
state="$(node_state icscf || true)"
if grep -Eq '"administrative_state":"(SHUTTINGDOWN|LOCKED)"' <<<"$state"; then
	echo "ok   the maintenance API shut the I-CSCF down"
else
	echo "FAIL the shut down of the I-CSCF: $answer"
	echo "     state of the node: $state"
	fail=1
fi
if docker compose run --no-deps --rm ue register -deregister-after 2s 2>/dev/null; then
	echo "FAIL UE registered with a shut down I-CSCF"
	fail=1
else
	echo "ok   UE could not register while the I-CSCF is shut down"
fi
answer="$(administrative_state icscf UNLOCKED || true)"
state="$(node_state icscf || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the I-CSCF"
else
	echo "FAIL the unlock of the I-CSCF: $answer"
	echo "     state of the node: $state"
	fail=1
fi
registrations="$(count "$registered")"
if docker compose run --no-deps --rm ue register -deregister-after 2s; then
	echo "ok   UE registered after the unlock of the I-CSCF"
else
	echo "FAIL UE registration after the unlock of the I-CSCF"
	fail=1
fi
await "the 200 OK to the registration after the unlock of the I-CSCF" "$registered" $((registrations + 1))

echo "drain of the MMTel AS"
# The AS answers the INVITE of each leg it serves, so a call through it
# again after the unlock adds 2xx of its own to the capture. Count what
# is there first.
asAnswers='sip.CSeq.method == "INVITE" && sip.Status-Code == 200 && ip.src == 172.28.0.75'
asAnswered="$(count "$asAnswers")"
# The AS is serving no call, so the shut down makes it LOCKED at once;
# SHUTTINGDOWN refuses a new INVITE just the same. The filter criterion
# of the originating case says SESSION_TERMINATED, so the S-CSCF ends
# the call rather than go on without the AS (TS 24.229 5.4.3.2).
answer="$(administrative_state mmtel SHUTTINGDOWN || true)"
state="$(node_state mmtel || true)"
if grep -Eq '"administrative_state":"(SHUTTINGDOWN|LOCKED)"' <<<"$state"; then
	echo "ok   the maintenance API shut the MMTel AS down"
else
	echo "FAIL the shut down of the MMTel AS: $answer"
	echo "     state of the node: $state"
	fail=1
fi
if docker compose run --no-deps --rm ue call --tone 1s tel:+1-202-555-0102 2>/dev/null; then
	echo "FAIL UE 1 called with a shut down MMTel AS"
	fail=1
else
	echo "ok   UE 1 could not call while the MMTel AS is shut down"
fi
# Ut goes out of service with the AS: the XCAP server answers 503
# instead of the settings (RFC 9110 15.6.4). "ue xcap" needs no
# registration, and ends by itself on the refusal.
if docker compose run --no-deps --rm ue xcap get >/dev/null 2>&1; then
	echo "FAIL Ut answered UE 1 while the MMTel AS is shut down"
	fail=1
else
	echo "ok   Ut refused UE 1 while the MMTel AS is shut down"
fi
answer="$(administrative_state mmtel UNLOCKED || true)"
state="$(node_state mmtel || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the MMTel AS"
else
	echo "FAIL the unlock of the MMTel AS: $answer"
	echo "     state of the node: $state"
	fail=1
fi
if docker compose run --no-deps --rm ue xcap get >/dev/null; then
	echo "ok   Ut served UE 1 after the unlock of the MMTel AS"
else
	echo "FAIL Ut after the unlock of the MMTel AS"
	fail=1
fi
# A call after the unlock goes through the AS on both legs again. UE 2
# waits in the background for it.
docker compose run --no-deps -d --name ims-practice-ue2-mmtel ue2 answer "${ue2[@]}" --ring 500ms --tone 1s >/dev/null
if wait_log ims-practice-ue2-mmtel '"msg":"waiting for a call"'; then
	if docker compose run --no-deps --rm ue call --tone 1s tel:+1-202-555-0102; then
		echo "ok   UE 1 called UE 2 after the unlock of the MMTel AS"
	else
		echo "FAIL the call after the unlock of the MMTel AS"
		fail=1
	fi
else
	fail=1
fi
if [ "$(ended ims-practice-ue2-mmtel)" = "0" ]; then
	echo "ok   UE 2 answered the call after the unlock of the MMTel AS"
else
	echo "FAIL UE 2 did not answer the call after the unlock of the MMTel AS"
	fail=1
fi

await "the 200 OK of the call after the unlock of the MMTel AS" "$asAnswers" $((asAnswered + 1))

echo "drain of the MRFC"
# The MRFC takes its work from the AS, so the drain shows in a call
# whose service needs the MRF: a barred call, before whose rejection the
# AS has the MRFC play an announcement to the caller (TS 24.628 4.2.4).
# The AS answers the INVITE of each leg it serves, and the MRFC answers
# the one of each dialog of its own, so count the 2xx of the MRFC first.
mrfcAnswers='sip.CSeq.method == "INVITE" && sip.Status-Code == 200 && ip.src == 172.28.0.85'
mrfcAnswered="$(count "$mrfcAnswers")"
# UE 1 bars its own outgoing calls over Ut, as the MRF guide has it.
# Every scene after this one, and the checks of the capture, need UE 1's
# calls to go through again, so the barring comes off on the way out of
# this scene whatever happens in between, and the trap goes once the
# scene has taken it off itself.
trap 'docker compose run --no-deps --rm ue xcap set baoc off >/dev/null 2>&1 || true' EXIT
# The drain only shows in a call that the AS rejects, so the two calls
# below say nothing about it unless the barring took. Check that here,
# before the MRFC is shut down. "ue xcap" prints every supplementary
# service of the document, each as "name", "provisioned" and "active"
# in that order, so the check reads the three lines of baoc itself: a
# bare '"active": true' would match any other service.
services="$(docker compose run --no-deps --rm ue xcap set baoc on 2>&1)"
if grep -A 2 '"name": "baoc"' <<<"$services" | grep -q '"active": true'; then
	echo "ok   UE 1 barred its outgoing calls over Ut"
else
	echo "FAIL UE 1 could not bar its outgoing calls, so the calls of this scene say nothing about the drain"
	fail=1
fi
# The MRFC holds no dialog of its own, so the shut down makes it LOCKED
# at once; SHUTTINGDOWN refuses a new INVITE just the same.
answer="$(administrative_state mrfc SHUTTINGDOWN || true)"
state="$(node_state mrfc || true)"
if grep -Eq '"administrative_state":"(SHUTTINGDOWN|LOCKED)"' <<<"$state"; then
	echo "ok   the maintenance API shut the MRFC down"
else
	echo "FAIL the shut down of the MRFC: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# The call is barred either way, so "ue call" exits 1, hence "|| true".
# What the drain changes is the announcement: the AS gets 503 from the
# MRFC and rejects the call without the early media of the MRFP.
out="$(docker compose run --no-deps --rm ue call --tone 1s tel:+1-202-555-0102 2>&1 || true)"
if grep -q '"call failed: 603 ' <<<"$out" && ! grep -q '"msg":"early media authorization"' <<<"$out"; then
	echo "ok   UE 1's barred call was rejected without an announcement while the MRFC is shut down"
else
	echo "FAIL the barred call while the MRFC is shut down: $(tail -n 2 <<<"$out")"
	fail=1
fi
answer="$(administrative_state mrfc UNLOCKED || true)"
state="$(node_state mrfc || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the MRFC"
else
	echo "FAIL the unlock of the MRFC: $answer"
	echo "     state of the node: $state"
	fail=1
fi
out="$(docker compose run --no-deps --rm ue call --tone 1s tel:+1-202-555-0102 2>&1 || true)"
if grep -q '"msg":"early media authorization"' <<<"$out" && grep -q '"call failed: 603 ' <<<"$out"; then
	echo "ok   UE 1 heard the announcement before the rejection after the unlock of the MRFC"
else
	echo "FAIL the barred call after the unlock of the MRFC: $(tail -n 2 <<<"$out")"
	fail=1
fi
services="$(docker compose run --no-deps --rm ue xcap set baoc off 2>&1)"
if grep -A 2 '"name": "baoc"' <<<"$services" | grep -q '"active": false'; then
	echo "ok   UE 1 let its outgoing calls through again"
else
	echo "FAIL UE 1 could not stop barring its outgoing calls"
	fail=1
fi
# The barring is off, so the trap that would take it off has nothing
# left to do.
trap - EXIT

await "the 200 OK of the MRFC after its unlock" "$mrfcAnswers" $((mrfcAnswered + 1))

echo "drain of the media relay"
# The P-CSCF puts the media of each call through a media relay, and the
# Compose project has one (the media_relays of deploy/compose/pcscf.yaml),
# so a drained relay leaves a new call with no node to carry its RTP.
# A commercial network runs several, and the P-CSCF takes the next one of
# its list; what the drain does to the relay itself is the same either
# way.
#
# The scenes above left no call up, so the relay holds no context. Wait
# until it says so, so that the shut down below is the shut down of a
# function with no user, which X.731 7.1.3.3 makes LOCKED at once.
if until_state agw '"users":0'; then
	echo "ok   the media relay holds no context before the shut down"
else
	echo "FAIL the media relay still holds contexts: $(node_state agw || true)"
	fail=1
fi
answer="$(administrative_state agw SHUTTINGDOWN || true)"
state="$(node_state agw || true)"
if grep -q '"administrative_state":"LOCKED"' <<<"$state"; then
	echo "ok   the maintenance API shut the media relay down, which locked it at once"
else
	echo "FAIL the shut down of the media relay: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# The relay answers the Add of the P-CSCF with 503 over its control API,
# and the P-CSCF, with no other relay to take, answers the INVITE 500
# and sends it nowhere. UE 2 is not started: the INVITE never leaves the
# P-CSCF. "ue call" then exits 1, hence "|| true"; the registration
# itself goes through, since no relay carries a REGISTER.
out="$(docker compose run --no-deps --rm ue call --tone 1s tel:+1-202-555-0102 2>&1 || true)"
if grep -q '"msg":"registered"' <<<"$out" && grep -q '"call failed: 500 ' <<<"$out"; then
	echo "ok   UE 1 registered but could not call while the media relay is shut down"
else
	echo "FAIL the call while the media relay is shut down: $(tail -n 2 <<<"$out")"
	fail=1
fi
answer="$(administrative_state agw UNLOCKED || true)"
state="$(node_state agw || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the media relay"
else
	echo "FAIL the unlock of the media relay: $answer"
	echo "     state of the node: $state"
	fail=1
fi
docker compose run --no-deps -d --name ims-practice-ue2-agw ue2 answer "${ue2[@]}" --ring 500ms --tone 1s >/dev/null
if wait_log ims-practice-ue2-agw '"msg":"waiting for a call"'; then
	out="$(docker compose run --no-deps --rm ue call --tone 1s tel:+1-202-555-0102 2>&1 || true)"
	if grep -q '"msg":"call answered"' <<<"$out"; then
		echo "ok   UE 1 called UE 2 through the media relay after its unlock"
	else
		echo "FAIL the call after the unlock of the media relay: $(tail -n 2 <<<"$out")"
		fail=1
	fi
else
	fail=1
fi
if [ "$(ended ims-practice-ue2-agw)" = "0" ]; then
	echo "ok   UE 2 answered and the call ended"
else
	echo "FAIL UE 2 did not answer the call after the unlock of the media relay"
	fail=1
fi
# The relay subtracts the context of the call that has ended, so it
# counts no user again: a shut down now would lock it at once.
if until_state agw '"users":0'; then
	echo "ok   the media relay let go of the context of the call that ended"
else
	echo "FAIL the media relay still holds contexts: $(node_state agw || true)"
	fail=1
fi

echo "drain of the MRFP"
# The MRFC asks an MRFP for the media of each announcement, and the
# Compose project has one (the mrfp of deploy/compose/mrfc.yaml). A
# drained MRFP answers the Add of the MRFC with 503 over the same control
# API, the MRFC asks the next MRFP of its list, and with none left it
# answers the AS 503. The AS then serves the call without the MRF, which
# shows in the barred call of UE 1, as it does while the MRFC itself is
# out of service.
mrfcAnswered="$(count "$mrfcAnswers")"
trap 'docker compose run --no-deps --rm ue xcap set baoc off >/dev/null 2>&1 || true' EXIT
services="$(docker compose run --no-deps --rm ue xcap set baoc on 2>&1)"
if grep -A 2 '"name": "baoc"' <<<"$services" | grep -q '"active": true'; then
	echo "ok   UE 1 barred its outgoing calls over Ut"
else
	echo "FAIL UE 1 could not bar its outgoing calls, so the call of this scene says nothing about the drain"
	fail=1
fi
# The MRFP holds no context of its own here: the announcement of the
# scene above has played and its dialog has ended.
if until_state mrfp '"users":0'; then
	echo "ok   the MRFP holds no context before the shut down"
else
	echo "FAIL the MRFP still holds contexts: $(node_state mrfp || true)"
	fail=1
fi
answer="$(administrative_state mrfp SHUTTINGDOWN || true)"
state="$(node_state mrfp || true)"
if grep -q '"administrative_state":"LOCKED"' <<<"$state"; then
	echo "ok   the maintenance API shut the MRFP down, which locked it at once"
else
	echo "FAIL the shut down of the MRFP: $answer"
	echo "     state of the node: $state"
	fail=1
fi
out="$(docker compose run --no-deps --rm ue call --tone 1s tel:+1-202-555-0102 2>&1 || true)"
if grep -q '"call failed: 603 ' <<<"$out" && ! grep -q '"msg":"early media authorization"' <<<"$out"; then
	echo "ok   UE 1's barred call was rejected without an announcement while the MRFP is shut down"
else
	echo "FAIL the barred call while the MRFP is shut down: $(tail -n 2 <<<"$out")"
	fail=1
fi
answer="$(administrative_state mrfp UNLOCKED || true)"
state="$(node_state mrfp || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the MRFP"
else
	echo "FAIL the unlock of the MRFP: $answer"
	echo "     state of the node: $state"
	fail=1
fi
out="$(docker compose run --no-deps --rm ue call --tone 1s tel:+1-202-555-0102 2>&1 || true)"
if grep -q '"msg":"early media authorization"' <<<"$out" && grep -q '"call failed: 603 ' <<<"$out"; then
	echo "ok   UE 1 heard the announcement before the rejection after the unlock of the MRFP"
else
	echo "FAIL the barred call after the unlock of the MRFP: $(tail -n 2 <<<"$out")"
	fail=1
fi
services="$(docker compose run --no-deps --rm ue xcap set baoc off 2>&1)"
if grep -A 2 '"name": "baoc"' <<<"$services" | grep -q '"active": false'; then
	echo "ok   UE 1 let its outgoing calls through again"
else
	echo "FAIL UE 1 could not stop barring its outgoing calls"
	fail=1
fi
trap - EXIT

await "the 200 OK of the MRFC after the unlock of the MRFP" "$mrfcAnswers" $((mrfcAnswered + 1))
same_nodes

echo "reading $pcap"
last=0
ue=172.28.0.100
pcscf=172.28.0.40
icscf=172.28.0.20
scscf=172.28.0.30
mmtel=172.28.0.75
mrfc=172.28.0.85
mrfp=172.28.0.86
agw=172.28.0.50
register='sip.CSeq.method == "REGISTER"'
invite='sip.CSeq.method == "INVITE"'

echo "the REGISTER while the S-CSCF is shut down"
expect "503 S-CSCF -> I-CSCF with Retry-After 30" "$register && sip.Status-Code == 503 && ip.src == $scscf && ip.dst == $icscf && sip.Retry-After == 30"
# A proxy does not pass a 503 on (RFC 3261 16.7 step 6): the I-CSCF has
# no other S-CSCF to try, so it answers 500 of its own.
expect "500 I-CSCF -> P-CSCF without Retry-After" "$register && sip.Status-Code == 500 && ip.src == $icscf && ip.dst == $pcscf && !sip.Retry-After"
expect "500 P-CSCF -> UE without Retry-After" "$register && sip.Status-Code == 500 && ip.src == $pcscf && ip.dst == $ue && !sip.Retry-After"
echo "the REGISTER after the unlock of the S-CSCF"
expect "401 S-CSCF -> I-CSCF" "$register && sip.Status-Code == 401 && ip.src == $scscf"
expect "200 S-CSCF -> I-CSCF" "$register && sip.Status-Code == 200 && ip.src == $scscf"
echo "the REGISTER while the P-CSCF is shut down"
# The P-CSCF answers itself, with a Retry-After longer than Timer F of
# the UE (TS 24.229 5.1.1.2.1), and sends the REGISTER nowhere.
before="$last"
expect "503 P-CSCF -> UE with Retry-After 300" "$register && sip.Status-Code == 503 && ip.src == $pcscf && ip.dst == $ue && sip.Retry-After == 300"
if [ "$last" != "$before" ]; then
	refused="sip.Call-ID == \"$(field sip.Call-ID)\""
	none "REGISTERs of that call that the P-CSCF sent on" "sip.Method == \"REGISTER\" && ip.src == $pcscf && $refused"
fi
echo "the REGISTER after the unlock of the P-CSCF"
expect "401 P-CSCF -> UE" "$register && sip.Status-Code == 401 && ip.src == $pcscf && ip.dst == $ue"
expect "200 P-CSCF -> UE" "$register && sip.Status-Code == 200 && ip.src == $pcscf && ip.dst == $ue"
echo "the REGISTER while the I-CSCF is shut down"
# The I-CSCF answers 503 without asking the HSS. The P-CSCF has no other
# I-CSCF to try, so it answers the UE 504 (TS 24.229 5.2.2.1 item 7).
before="$last"
expect "503 I-CSCF -> P-CSCF with Retry-After 30" "$register && sip.Status-Code == 503 && ip.src == $icscf && ip.dst == $pcscf && sip.Retry-After == 30"
if [ "$last" != "$before" ]; then
	refused="sip.Call-ID == \"$(field sip.Call-ID)\""
	none "REGISTERs of that call that the I-CSCF sent on" "sip.Method == \"REGISTER\" && ip.src == $icscf && $refused"
fi
expect "504 P-CSCF -> UE without Retry-After" "$register && sip.Status-Code == 504 && ip.src == $pcscf && ip.dst == $ue && !sip.Retry-After"
echo "the REGISTER after the unlock of the I-CSCF"
expect "401 I-CSCF -> P-CSCF" "$register && sip.Status-Code == 401 && ip.src == $icscf && ip.dst == $pcscf"
expect "200 I-CSCF -> P-CSCF" "$register && sip.Status-Code == 200 && ip.src == $icscf && ip.dst == $pcscf"

echo "the INVITE while the MMTel AS is shut down"
# The AS answers 503 and sends nothing on. The S-CSCF has no other
# instance of the AS to try, and the filter criterion of the
# originating case says SESSION_TERMINATED, so it ends the call with a
# 500 of its own: a proxy does not pass a 503 on (RFC 3261 16.7 step 6).
expect "INVITE S-CSCF -> MMTel AS" "sip.Method == \"INVITE\" && ip.src == $scscf && ip.dst == $mmtel"
refusedInvite="$last"
expect "503 MMTel AS -> S-CSCF with Retry-After 30" "$invite && sip.Status-Code == 503 && ip.src == $mmtel && ip.dst == $scscf && sip.Retry-After == 30"
# The AS is a B2BUA, so the INVITE it would have sent on has a Call-ID
# of its own: what shows that it sent none is that no INVITE left it
# between the one it was given and its 503.
none "INVITEs that the AS sent on while it was shut down" "sip.Method == \"INVITE\" && ip.src == $mmtel && frame.number > $refusedInvite && frame.number < $last"
expect "500 S-CSCF -> P-CSCF without Retry-After" "$invite && sip.Status-Code == 500 && ip.src == $scscf && ip.dst == $pcscf && !sip.Retry-After"
expect "500 P-CSCF -> UE without Retry-After" "$invite && sip.Status-Code == 500 && ip.src == $pcscf && ip.dst == $ue && !sip.Retry-After"
echo "the Ut request while the MMTel AS is shut down"
# Ut has the administrative state of the AS, so the XCAP server answers
# 503 with Retry-After, without the challenge of HTTP Digest that it
# would answer an unauthenticated GET with (RFC 9110 15.6.4).
expect "GET UE 1 -> XCAP server" "http.request.method == \"GET\" && ip.src == $ue && ip.dst == $mmtel"
expect "503 XCAP server -> UE 1 with Retry-After 30" "http.response.code == 503 && ip.src == $mmtel && ip.dst == $ue && http.response.line matches \"^retry-after: 30\""
echo "the Ut request after the unlock of the MMTel AS"
expect "401 XCAP server -> UE 1 with the challenge of HTTP Digest" "http.response.code == 401 && ip.src == $mmtel && ip.dst == $ue && http.www_authenticate"
expect "200 XCAP server -> UE 1 with the settings" "http.response.code == 200 && ip.src == $mmtel && ip.dst == $ue"
echo "the call after the unlock of the MMTel AS"
# The AS is the B2BUA of the call again: it answers the INVITE of the
# S-CSCF on one leg and sends a new INVITE back to the S-CSCF on the
# other (TS 24.229 5.7.5).
expect "INVITE S-CSCF -> MMTel AS" "sip.Method == \"INVITE\" && ip.src == $scscf && ip.dst == $mmtel"
expect "INVITE MMTel AS -> S-CSCF of the second leg" "sip.Method == \"INVITE\" && ip.src == $mmtel && ip.dst == $scscf"
expect "200 MMTel AS -> S-CSCF" "$invite && sip.Status-Code == 200 && ip.src == $mmtel && ip.dst == $scscf"

echo "the announcement of the barred call while the MRFC is shut down"
# The AS asks the MRFC for the announcement of RFC 4240 3. The MRFC
# answers 503 with Retry-After and sets up no media, so the AS rejects
# the barred call without the early media it would have authorized, and
# the 603 carries no Reason: the AS adds that only to a rejection the
# caller has heard an announcement before (TS 24.628 4.2.4).
expect "INVITE MMTel AS -> MRFC for the announcement notice" "sip.Method == \"INVITE\" && ip.src == $mmtel && ip.dst == $mrfc && sip.r-uri contains \"annc@\""
refusedAnnouncement="$last"
expect "503 MRFC -> MMTel AS with Retry-After 30" "$invite && sip.Status-Code == 503 && ip.src == $mrfc && ip.dst == $mmtel && sip.Retry-After == 30"
none "2xx of the MRFC while it was shut down" "frame.number > $refusedAnnouncement && frame.number <= $last && $invite && sip.Status-Code == 200 && ip.src == $mrfc"
expect "603 P-CSCF -> UE 1 for the barred call, without a Reason" "sip.Status-Code == 603 && ip.src == $pcscf && ip.dst == $ue && !sip.Reason"
none "responses that authorize early media for UE 1 while the MRFC was shut down" "frame.number > $refusedAnnouncement && frame.number < $last && sip.P-Early-Media && ip.dst == $ue"
echo "the announcement after the unlock of the MRFC"
expect "INVITE MMTel AS -> MRFC for the announcement notice" "sip.Method == \"INVITE\" && ip.src == $mmtel && ip.dst == $mrfc && sip.r-uri contains \"annc@\""
expect "200 MRFC -> MMTel AS with the SDP of the MRFP" "$invite && sip.Status-Code == 200 && ip.src == $mrfc && sdp.connection_info.address == \"$mrfp\""
expect "183 P-CSCF -> UE 1 with P-Early-Media: sendonly" "sip.Status-Code == 183 && ip.dst == $ue && sip.P-Early-Media == \"sendonly\""
expect "RTP of notice media relay -> UE 1" "rtp && ip.src == $agw && ip.dst == $ue"
expect "BYE MRFC -> MMTel AS once notice has played" "sip.Method == \"BYE\" && ip.src == $mrfc && ip.dst == $mmtel"
expect "603 P-CSCF -> UE 1 with Reason: SIP;cause=603" "sip.Status-Code == 603 && ip.src == $pcscf && ip.dst == $ue && sip.Reason contains \"cause=603\""

echo "the call while the media relay is shut down"
# The P-CSCF asks the relay for the context of the call over the control
# API (Reserve AGW Connection Point, TS 29.334 5.17.2.2), and the relay,
# which the operator has taken out of service, answers 503 (Service
# Unavailable). The P-CSCF has no other relay in its list, so it answers
# the INVITE 500 itself and sends it nowhere. Retry-After is for the 503
# of a node that answered it (RFC 3261 21.5.4), so the 500 carries none.
expect "POST /contexts P-CSCF -> media relay" "http.request.method == \"POST\" && ip.src == $pcscf && ip.dst == $agw && http.request.uri == \"/contexts\""
expect "503 media relay -> P-CSCF for the new context" "http.response.code == 503 && ip.src == $agw && ip.dst == $pcscf"
before="$last"
expect "500 P-CSCF -> UE 1 for the call, without Retry-After" "$invite && sip.Status-Code == 500 && ip.src == $pcscf && ip.dst == $ue && !sip.Retry-After"
if [ "$last" != "$before" ]; then
	refused="sip.Call-ID == \"$(field sip.Call-ID)\""
	none "INVITEs of that call that the P-CSCF sent on" "sip.Method == \"INVITE\" && ip.src == $pcscf && $refused"
fi
echo "the call after the unlock of the media relay"
expect "POST /contexts P-CSCF -> media relay" "http.request.method == \"POST\" && ip.src == $pcscf && ip.dst == $agw && http.request.uri == \"/contexts\""
expect "200 P-CSCF -> UE 1 for the call" "$invite && sip.Status-Code == 200 && ip.src == $pcscf && ip.dst == $ue"
expect "RTP UE 1 -> media relay" "rtp && ip.src == $ue && ip.dst == $agw"
# The BYE of the call ends the dialog, and the P-CSCF subtracts the
# context it holds in the relay (TS 29.334 5.17.2.5), so the relay
# counts no user again.
expect "DELETE of the context P-CSCF -> media relay" "http.request.method == \"DELETE\" && ip.src == $pcscf && ip.dst == $agw"

echo "the announcement of the barred call while the MRFP is shut down"
# The MRFC asks the MRFP for the context of the announcement over the
# control API (Reserve IMS Resources, TS 29.333 5.17.2.2) and gets 503,
# and with no other MRFP in its list it answers the AS 503. That 503
# carries no Retry-After: the MRFC is in service, and Retry-After would
# say that the AS should send the MRFC itself nothing for that long
# (RFC 3261 21.5.4). The AS then rejects the barred call without the
# early media of an announcement, and the 603 carries no Reason.
expect "INVITE MMTel AS -> MRFC for the announcement notice" "sip.Method == \"INVITE\" && ip.src == $mmtel && ip.dst == $mrfc && sip.r-uri contains \"annc@\""
expect "503 MRFP -> MRFC for the new context" "http.response.code == 503 && ip.src == $mrfp && ip.dst == $mrfc"
refusedByMRFP="$last"
expect "503 MRFC -> MMTel AS without Retry-After" "$invite && sip.Status-Code == 503 && ip.src == $mrfc && ip.dst == $mmtel && !sip.Retry-After"
none "2xx of the MRFC while the MRFP was shut down" "frame.number > $refusedByMRFP && frame.number <= $last && $invite && sip.Status-Code == 200 && ip.src == $mrfc"
expect "603 P-CSCF -> UE 1 for the barred call, without a Reason" "sip.Status-Code == 603 && ip.src == $pcscf && ip.dst == $ue && !sip.Reason"
echo "the announcement after the unlock of the MRFP"
expect "200 MRFC -> MMTel AS with the SDP of the MRFP" "$invite && sip.Status-Code == 200 && ip.src == $mrfc && sdp.connection_info.address == \"$mrfp\""
expect "RTP of notice media relay -> UE 1" "rtp && ip.src == $agw && ip.dst == $ue"
expect "603 P-CSCF -> UE 1 with Reason: SIP;cause=603" "sip.Status-Code == 603 && ip.src == $pcscf && ip.dst == $ue && sip.Reason contains \"cause=603\""

finish
