#!/usr/bin/env bash
# End-to-end check: SMS over IP with the Service Centre of SMS. Two UE
# simulators stay up and take their orders on their control API, so that
# the scenario can put UE 2 in the states where a delivery fails. The
# capture must show three runs of one short message each:
#
#   a) UE 1 to UE 2 and back: the short message crosses SGd to the
#      Service Centre and comes back over it, with a status report at the
#      end;
#   b) UE 2 takes no short message (480). The Service Centre holds it,
#      the HSS keeps the Service Centre's address, and once UE 2 is back
#      the HSS alerts the Service Centre, which delivers it. UE 2 shows
#      that it is back first with a short message of its own, and the
#      second time by registering again and nothing else;
#   c) UE 2 has no memory (RP-ERROR, RP-Cause 22). The same, set off by
#      the notification that it has memory again.
#
# A short message to a number no subscriber has is refused on the way in.
# Wireshark must decode the RP and TP layers of every short message, with
# the text that was sent.
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

# The CSCFs, the Service Centre and the IP-SM-GW serve requests only
# once their Diameter connections are open. The IP-SM-GW opens two, so
# each one is waited for by the peer it goes to.
hss_host=hss.ims.mnc001.mcc001.3gppnetwork.org
sc_host=sc.ims.mnc001.mcc001.3gppnetwork.org
wait_for_open() {
	local service="$1" peer="$2" i
	# Up to two minutes: a node that tried before its peer listened
	# tries again after Tc, 30 s (RFC 6733 2.1).
	for i in $(seq 1 120); do
		if grep -q "\"peer\":\"$peer\".*\"to\":\"I-Open (OKAY)\"" <<<"$(docker compose logs --no-color "$service" 2>/dev/null)"; then
			return 0
		fi
		sleep 1
	done
	echo "FAIL $service never opened its Diameter connection to $peer" >&2
	exit 1
}
wait_for_open icscf "$hss_host"
wait_for_open scscf "$hss_host"
wait_for_open sc "$hss_host"
wait_for_open ipsmgw "$hss_host"
wait_for_open ipsmgw "$sc_host"

gsm_text='Hello UE-B, this is UE-A.'
ucs2_text='こんにちは、UE-A さん'
away_text='Are you back yet?'
rereg_text='Away again, back by registering?'
room_text='Is there room now?'
remember_nodes
fail=0

# phone_answer UE METHOD PATH [BODY] sends one request to the control
# API of a UE simulator of the phones profile and prints the body of the
# answer, then its status code on a line of its own. The API is on no
# address of the host, so the request goes through the Lab, which
# passes it on to the UE as it is, as the Lab's own screen does. A short
# message is answered once its MESSAGE has a final response, so the
# wait is longer than a SIP transaction.
phone_answer() {
	local data=()
	if [ -n "${4:-}" ]; then
		data=(-H 'Content-Type: application/json' --data-binary "$4")
	fi
	curl -s -m 75 -w '\n%{http_code}' -X "$2" "${data[@]}" "http://localhost:8080/api/ues/$1$3"
}
# phone UE METHOD PATH [BODY] prints the JSON body of that answer, or
# nothing when the answer is not 200, and says on its error output what
# the answer was. The control API answers each request with the state it
# leaves behind, so the caller reads the body.
phone() {
	local answer status
	answer="$(phone_answer "$@")"
	status="$(tail -n 1 <<<"$answer")"
	if [ "${status:-}" != "200" ]; then
		echo "$1 answered ${2:-} ${3:-} with ${status:-no status}: $(sed '$d' <<<"$answer")" >&2
		return 1
	fi
	sed '$d' <<<"$answer"
}
# ue_status UE prints what the UE answers to GET /status, which the
# Lab's list of the UEs carries: whether it is registered, and the short
# messages it sent and received. Nothing when the UE does not answer.
ue_status() {
	curl -fsS -m 10 http://localhost:8080/api/ues | "$python" -c '
import json, sys
for u in json.load(sys.stdin):
    if u["name"] == sys.argv[1] and u["status"]:
        print(json.dumps(u["status"], separators=(",", ":"), ensure_ascii=False))
' "$1" | tr -d '\r'
}
# sms_history UE prints the short messages of the UE from its status.
sms_history() {
	"$python" -c '
import json, sys
print(json.dumps(json.load(sys.stdin).get("sms") or {}, ensure_ascii=False))
' <<<"$(ue_status "$1")"
}
# set_state SERVICE METHOD PATH BODY changes a state of the UE's
# reception of short messages, and fails the run when the control API
# does not take it.
set_state() {
	if phone "$@" >/dev/null; then
		echo "ok   $1 took $3"
	else
		echo "FAIL $1 did not take $3 $4"
		fail=1
	fi
}
# phone_registered SERVICE: the UE says that it is registered.
phone_registered() { grep -q '"registered":true' <<<"$(ue_status "$1")"; }
# received SERVICE TEXT: the UE has received a short message with that
# text. The sent ones carry a text too, so the two lists are read apart.
received() {
	"$python" -c '
import json, sys
text = sys.argv[1]
history = json.load(sys.stdin)
sys.exit(0 if any(m.get("text") == text for m in history.get("received") or []) else 1)
' "$2" <<<"$(sms_history "$1")"
}
# sent_state SERVICE TEXT prints how the short message with that text
# that the UE sent stands: the state, the RP-Cause of an RP-ERROR and the
# TP-FCS of the report in it, and the TP-ST of the status report where
# one has come. Nothing when the UE sent no such message.
sent_state() {
	"$python" -c '
import json, sys
text = sys.argv[1]
for m in json.load(sys.stdin).get("sent") or []:
    if m.get("text") == text:
        report = m.get("status_report") or {}
        print(m["state"], m.get("rp_cause", 0), m.get("failure_cause", 0), report.get("status", "-"))
' "$2" <<<"$(sms_history "$1")" | tr -d '\r'
}
# sent_is SERVICE TEXT STATE waits for sent_state to say STATE.
sent_is() { [ "$(sent_state "$1" "$2")" = "$3" ]; }
# accepted SERVICE TEXT: the submit report said that the Service Centre
# took the short message. Only the state is read, since a status report
# may have come since.
accepted() { [ "$(sent_state "$1" "$2" | cut -d ' ' -f 1)" = accepted ]; }

# The two UEs stay up and hold subscribers 1 and 2, so they must not
# outlive the script.
trap 'docker compose --profile phones rm -sf phone1 phone2 >/dev/null 2>&1' EXIT
docker compose --profile phones up -d --no-deps phone1 phone2
for ue in phone1 phone2; do
	if wait_for 60 phone_registered "$ue"; then
		echo "ok   $ue registered"
	else
		echo "FAIL $ue did not register"
		exit 1
	fi
done

echo "a) a short message from UE 1 to UE 2, with a status report"
# POST /sms answers once the MESSAGE has its 202; the submit report
# comes in a MESSAGE of its own, so the state is read afterwards.
set_state phone1 POST /sms "{\"to\": \"+12025550102\", \"text\": \"$gsm_text\", \"status_report\": true}"
if wait_for 30 accepted phone1 "$gsm_text"; then
	echo "ok   the Service Centre took the short message of UE 1"
else
	echo "FAIL the submit report of UE 1: $(sent_state phone1 "$gsm_text")"
	fail=1
fi
if wait_for 30 received phone2 "$gsm_text"; then
	echo "ok   UE 2 received the text from UE 1"
else
	echo "FAIL UE 2 did not receive the text"
	fail=1
fi
# TP-ST 0 is "short message received by the SME" (TS 23.040 9.2.3.15).
if wait_for 30 sent_is phone1 "$gsm_text" "accepted 0 0 0"; then
	echo "ok   UE 1 got a status report that the short message was delivered"
else
	echo "FAIL UE 1 got no status report: $(sent_state phone1 "$gsm_text")"
	fail=1
fi

echo "a number that no subscriber has"
unknown_text='Nobody has this number.'
set_state phone1 POST /sms "{\"to\": \"+12025550198\", \"text\": \"$unknown_text\"}"
# RP-Cause 1 is "unassigned (unallocated) number" and TP-FCS 0xc3 (195)
# "invalid SME address" (TS 24.011 table 8.4, TS 23.040 9.2.3.22).
if wait_for 30 sent_is phone1 "$unknown_text" "failed 1 195 -"; then
	echo "ok   a short message to an unknown number was refused with RP-Cause 1 and TP-FCS 0xc3"
else
	echo "FAIL a short message to an unknown number: $(sent_state phone1 "$unknown_text")"
	fail=1
fi

echo "b) UE 2 takes no short message, and the alert of the HSS delivers it"
set_state phone2 PUT /sms-availability '{"available": false}'
set_state phone1 POST /sms "{\"to\": \"+12025550102\", \"text\": \"$away_text\"}"
if wait_for 30 accepted phone1 "$away_text"; then
	echo "ok   the Service Centre took the short message for the UE that takes none"
else
	echo "FAIL the submit report of UE 1: $(sent_state phone1 "$away_text")"
	fail=1
fi
# The HSS logs the message waiting data of a user each time it changes,
# so each delivery that the Service Centre holds for an absent UE 2 adds
# one line with its address and the unreachable flag set.
waiting_count() {
	grep -c '"msg":"message waiting data".*"service_centres":\["12025550100"\].*"unri":true' \
		<<<"$(docker compose logs --no-color hss 2>/dev/null)" || true
}
# waiting_more_than N: the HSS has logged that more than N times.
waiting_more_than() { [ "$(waiting_count)" -gt "$1" ]; }
if wait_for 60 waiting_more_than 0; then
	echo "ok   the HSS keeps the Service Centre's address for UE 2, with the unreachable flag"
else
	echo "FAIL the HSS did not keep the Service Centre's address"
	fail=1
fi
if received phone2 "$away_text"; then
	echo "FAIL UE 2 took the short message although it takes none"
	fail=1
else
	echo "ok   UE 2 has not taken the short message"
fi
# UE 2 takes short messages again, and shows it by sending one of its
# own: any request of the user tells the IP-SM-GW that the UE is there
# (TS 23.204 6.5b step 2). This one is in UCS2.
set_state phone2 PUT /sms-availability '{"available": true}'
set_state phone2 POST /sms "{\"to\": \"+12025550101\", \"text\": \"$ucs2_text\"}"
if wait_for 30 accepted phone2 "$ucs2_text"; then
	echo "ok   UE 2 sent a short message of its own, in Japanese"
else
	echo "FAIL the submit report of UE 2: $(sent_state phone2 "$ucs2_text")"
	fail=1
fi
if wait_for 60 received phone2 "$away_text"; then
	echo "ok   the alert of the HSS delivered the short message that the Service Centre held"
else
	echo "FAIL the short message that the Service Centre held was not delivered"
	fail=1
fi
if wait_for 30 received phone1 "$ucs2_text"; then
	echo "ok   UE 1 received the Japanese text from UE 2"
else
	echo "FAIL UE 1 did not receive the Japanese text"
	fail=1
fi

echo "b) again, and this time UE 2 only registers again"
set_state phone2 PUT /sms-availability '{"available": false}'
held_before="$(waiting_count)"
set_state phone1 POST /sms "{\"to\": \"+12025550102\", \"text\": \"$rereg_text\"}"
if wait_for 30 accepted phone1 "$rereg_text"; then
	echo "ok   the Service Centre took the second short message for the UE that takes none"
else
	echo "FAIL the submit report of UE 1: $(sent_state phone1 "$rereg_text")"
	fail=1
fi
if wait_for 60 waiting_more_than "$held_before"; then
	echo "ok   the HSS keeps the Service Centre's address for UE 2 again, with the unreachable flag"
else
	echo "FAIL the HSS did not keep the Service Centre's address the second time"
	fail=1
fi
# A re-registration and nothing else: UE 2 sends no MESSAGE that could
# say it is back (TS 23.204 6.5b step 2a).
set_state phone2 PUT /sms-availability '{"available": true}'
set_state phone2 POST /register ""
if wait_for 60 received phone2 "$rereg_text"; then
	echo "ok   the alert of the HSS delivered the short message once UE 2 had only registered again"
else
	echo "FAIL the short message held for UE 2 was not delivered after its re-registration"
	fail=1
fi

echo "c) UE 2 has no memory, and its notification sets off the alert"
set_state phone2 PUT /sms-memory '{"full": true}'
set_state phone1 POST /sms "{\"to\": \"+12025550102\", \"text\": \"$room_text\"}"
if wait_for 30 accepted phone1 "$room_text"; then
	echo "ok   the Service Centre took the short message for the UE without memory"
else
	echo "FAIL the submit report of UE 1: $(sent_state phone1 "$room_text")"
	fail=1
fi
memory_exceeded() {
	grep -q '"msg":"message waiting data".*"service_centres":\["12025550100"\].*"mcef":true' \
		<<<"$(docker compose logs --no-color hss 2>/dev/null)"
}
if wait_for 60 memory_exceeded; then
	echo "ok   the HSS keeps the Service Centre's address for UE 2, with the memory capacity exceeded flag"
else
	echo "FAIL the HSS did not record that UE 2 has no memory"
	fail=1
fi
# Giving the memory back sends the notification that the UE owes, which
# the IP-SM-GW passes to the HSS before it answers the UE.
# The UE answers once its RP-SMMA has had its 202 Accepted. The flag
# stays set until the RP-ACK comes back in a MESSAGE of its own (TS
# 23.040 10.3), which the capture shows below.
reception="$(phone phone2 PUT /sms-memory '{"full": false}' || true)"
if grep -q '"memory_full":false,"memory_capacity_exceeded_notification":true' <<<"$reception"; then
	echo "ok   UE 2 has memory again and has sent the notification it owes"
else
	echo "FAIL UE 2 did not send the notification it owes: $reception"
	fail=1
fi
if wait_for 60 received phone2 "$room_text"; then
	echo "ok   the alert of the HSS delivered the short message that UE 2 had no memory for"
else
	echo "FAIL the short message that UE 2 had no memory for was not delivered"
	fail=1
fi

echo "UE 2 deregisters, and the IP-SM-GW takes itself out of the HSS"
set_state phone2 POST /deregister ""
unregistered() { grep -q '"registered":false' <<<"$(ue_status phone2)"; }
if wait_for 30 unregistered; then
	echo "ok   UE 2 deregistered"
else
	echo "FAIL UE 2 did not deregister"
	fail=1
fi
same_nodes

pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
echo "reading $pcap"

last=0

ue1=172.28.0.110
ue2ip=172.28.0.111
pcscf=172.28.0.40
icscf=172.28.0.20
scscf=172.28.0.30
hss=172.28.0.10
ipsmgw=172.28.0.70
sc=172.28.0.71
ipsmgw_uri=sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060
message='sip.Method == "MESSAGE"'
sms='sip.Content-Type == "application/vnd.3gpp.sms"'
smsip='sip.Accept-Contact == "*;+g.3gpp.smsip;require;explicit"'
req='diameter.flags.request == 1'
ans='diameter.flags.request == 0'
# The commands of SGd and S6c (TS 29.338 Table 5.3.2.2/1, Table
# 6.3.2.2/1).
ofr="diameter.cmd.code == 8388645 && $req"
ofa="diameter.cmd.code == 8388645 && $ans"
tfr="diameter.cmd.code == 8388646 && $req"
tfa="diameter.cmd.code == 8388646 && $ans"
srr="diameter.cmd.code == 8388647 && $req"
alr="diameter.cmd.code == 8388648 && $req"
rdr="diameter.cmd.code == 8388649 && $req"
# Ready-For-SM, the command of this project's own on S6c with which the
# IP-SM-GW tells the HSS that a user can take short messages again. A
# network sends that notice in MAP-READY-FOR-SM (TS 29.002 12.4.1); the
# command code is the experimental one of RFC 6733 11.2.1.
ready="diameter.cmd.code == 16777214 && diameter.applicationId == 16777312 && $req"
# RP message types (TS 24.011 8.2.2) and TP message types (TS 23.040
# 9.2.3.1). An RP-ACK from the network carries an SMS-SUBMIT-REPORT,
# one from the MS an SMS-DELIVER-REPORT; both have TP-MTI 1 and 0, as
# the direction tells them apart.
rp_data_mo='gsm_a.rp.msg_type == 0x00'
rp_data_mt='gsm_a.rp.msg_type == 0x01'
rp_ack_mo='gsm_a.rp.msg_type == 0x02'
rp_ack_mt='gsm_a.rp.msg_type == 0x03'
rp_error_mo='gsm_a.rp.msg_type == 0x04'
rp_error_mt='gsm_a.rp.msg_type == 0x05'
rp_smma='gsm_a.rp.msg_type == 0x06'

# The checks read the capture up to the second delivery of the short
# message that UE 2 had no memory for.
await "both deliveries of the short message for the UE without memory" \
	"$message && ip.dst == $ue2ip && gsm_sms.sms_text == \"$room_text\"" 2 60

# The IP-SM-GW opens Diameter to the HSS with Sh and S6c, which they
# share, and SGd, on which a Service Centre brings it short messages,
# and with nothing else (RFC 6733 5.3).
cer="diameter.cmd.code == 257 && $req && ip.src == $ipsmgw && ip.dst == $hss"
expect "CER IP-SM-GW -> HSS advertising Sh, S6c and SGd" "$cer && diameter.Auth-Application-Id == 16777217 && diameter.Auth-Application-Id == 16777312 && diameter.Auth-Application-Id == 16777313"
none "CER of the IP-SM-GW to the HSS advertising an application other than Sh, S6c and SGd" "$cer && diameter.Auth-Application-Id in {0..16777216, 16777218..16777311, 16777314..4294967295}"
expect "CEA 2001 HSS -> IP-SM-GW" "diameter.cmd.code == 257 && $ans && ip.src == $hss && ip.dst == $ipsmgw && diameter.Result-Code == 2001"

echo "the Service Centre opens S6c to the HSS, and the IP-SM-GW SGd to it (TS 29.338 6.3.1.1)"
last=0
sc_cer="diameter.cmd.code == 257 && $req && ip.src == $sc && ip.dst == $hss"
expect "CER SC -> HSS advertising S6c and SGd" "$sc_cer && diameter.Auth-Application-Id == 16777312 && diameter.Auth-Application-Id == 16777313"
none "CER of the SC advertising an application other than S6c and SGd" "$sc_cer && diameter.Auth-Application-Id in {0..16777311, 16777314..4294967295}"
expect "CEA 2001 HSS -> SC" "diameter.cmd.code == 257 && $ans && ip.src == $hss && ip.dst == $sc && diameter.Result-Code == 2001"
last=0
expect "CER IP-SM-GW -> SC advertising SGd" "diameter.cmd.code == 257 && $req && ip.src == $ipsmgw && ip.dst == $sc && diameter.Auth-Application-Id == 16777313"
expect "CEA 2001 SC -> IP-SM-GW" "diameter.cmd.code == 257 && $ans && ip.src == $sc && ip.dst == $ipsmgw && diameter.Result-Code == 2001"

echo "UE 2 registers at the IP-SM-GW too (TS 24.229 5.4.1.7, TS 24.341 5.3.3.2)"
last=0
# The S-CSCF answers the UE before it registers the user anywhere else.
expect "200 OK S-CSCF -> I-CSCF to UE 2's REGISTER" "sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ip.src == $scscf && ip.dst == $icscf && sip.Service-Route && sip.To contains \"+12025550102\""
ue2_registered="$last"
expect "third-party REGISTER S-CSCF -> IP-SM-GW with the MSISDN in service-info" "sip.Method == \"REGISTER\" && ip.src == $scscf && ip.dst == $ipsmgw && sip.Content-Type == \"application/3gpp-ims+xml\" && sip.msg_body contains \"<service-info>12025550102</service-info>\" && sip.Expires == 3600"
expect "200 OK IP-SM-GW -> S-CSCF with the binding" "sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ip.src == $ipsmgw && sip.Contact contains \"expires=3600\" && sip.P-Charging-Vector contains \"term-ioi=\""
expect "SUBSCRIBE IP-SM-GW -> I-CSCF to UE 2's registration state, with the entry point in Route" "sip.Method == \"SUBSCRIBE\" && sip.Event == \"reg\" && ip.src == $ipsmgw && ip.dst == $icscf && sip.Route contains \"<sip:ims.mnc001.mcc001.3gppnetwork.org;lr>\" && sip.r-uri.user == \"+12025550102\" && sip.P-Asserted-Identity contains \"ipsmgw\" && sip.Expires == 3761 && sip.P-Charging-Vector contains \"orig-ioi=\""
subscribed="$last"
expect "SUBSCRIBE I-CSCF -> S-CSCF" "sip.Method == \"SUBSCRIBE\" && sip.Event == \"reg\" && ip.src == $icscf && ip.dst == $scscf && sip.P-Asserted-Identity contains \"ipsmgw\""
expect "NOTIFY S-CSCF -> IP-SM-GW: a contact with +g.3gpp.smsip" "sip.Method == \"NOTIFY\" && ip.src == $scscf && ip.dst == $ipsmgw && sip.Subscription-State contains \"active\" && sip.msg_body contains \"+12025550102\" && sip.msg_body contains \"+g.3gpp.smsip\""
notified="$last"
# The IP-SM-GW reads UE 2's IMSI first (TS 29.328 7.6.24): a short
# message over SGd names its recipient by IMSI.
expect "UDR IP-SM-GW -> HSS: IMSI of UE 2" "diameter.cmd.code == 306 && $req && ip.src == $ipsmgw && diameter.Data-Reference == 32"
expect "UDA 2001" "$(answer_to) && diameter.Result-Code == 2001"
expect "PUR IP-SM-GW -> HSS: SMSRegistrationInfo with the IP-SM-GW's number, reachable over S6c" "diameter.cmd.code == 307 && $req && ip.src == $ipsmgw && diameter.Data-Reference == 24 && e164.msisdn == \"12025550102\" && diameter.Sh-User-Data contains \"<IP-SM-GW-Number>12025550199<\" && diameter.Sh-User-Data contains \"<IP-SM-GW-S6cSupportIndication>1<\""
expect "PUA 2001" "$(answer_to) && diameter.Result-Code == 2001"
# The 200 to the SUBSCRIBE comes back through the I-CSCF, and may come
# after the NOTIFY, which the S-CSCF sends straight to the IP-SM-GW. The
# IP-SM-GW answers the NOTIFY and sends the Sh requests together.
last="$subscribed"
expect "200 OK to the SUBSCRIBE, I-CSCF -> IP-SM-GW" "sip.Status-Code == 200 && sip.CSeq.method == \"SUBSCRIBE\" && ip.src == $icscf && ip.dst == $ipsmgw"
last="$notified"
expect "200 OK to the NOTIFY, IP-SM-GW -> S-CSCF" "sip.Status-Code == 200 && sip.CSeq.method == \"NOTIFY\" && ip.src == $ipsmgw && ip.dst == $scscf"

echo "a) a short message from UE 1 to UE 2, GSM 7 bit, with a status report (TS 24.341 B.5, B.6)"
# UE 1 sends once UE 2 has said it is registered, which is after the
# 200 to UE 2's REGISTER. The Sh requests above run on their own and may
# come after.
last="$ue2_registered"
expect "MESSAGE UE 1 -> P-CSCF to the SC: RP-DATA with SMS-SUBMIT" "$message && $sms && ip.src == $ue1 && ip.dst == $pcscf && sip.r-uri == \"tel:+12025550100\" && $rp_data_mo && gsm_sms.tp-mti == 1 && gsm_sms.tp-da == \"12025550102\" && gsm_sms.tp-srr == 1 && gsm_sms.tp-dcs == 0 && gsm_sms.sms_text == \"$gsm_text\""
mo="$(field sip.Call-ID)"
expect "MESSAGE P-CSCF -> S-CSCF" "$message && sip.Call-ID == \"$mo\" && ip.src == $pcscf && ip.dst == $scscf && sip.P-Asserted-Identity"
expect "MESSAGE S-CSCF -> IP-SM-GW by UE 1's originating iFC, with the tel URI asserted" "$message && sip.Call-ID == \"$mo\" && ip.src == $scscf && ip.dst == $ipsmgw && count(sip.Route) == 2 && sip.P-Asserted-Identity contains \"tel:+12025550101\""
# TS 24.229 5.4.3.2 step 5: the AS, then the S-CSCF itself with the
# original dialog identifier, to come back to.
routes="$(field sip.Route)"
if [[ "$routes" == "<$ipsmgw_uri;lr>,<sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr;odi="*">" ]]; then
	echo "ok   Route is the IP-SM-GW, then the S-CSCF with odi"
else
	echo "FAIL Route is $routes"
	fail=1
fi
mo_at_gw="$last"
expect "202 Accepted IP-SM-GW -> S-CSCF" "sip.Status-Code == 202 && sip.Call-ID == \"$mo\" && ip.src == $ipsmgw"
expect "202 Accepted reaches UE 1" "sip.Status-Code == 202 && sip.Call-ID == \"$mo\" && ip.dst == $ue1"
last="$mo_at_gw"

echo "the short message crosses SGd to the Service Centre (TS 23.204 6.3, TS 29.338 6.2.1)"
expect "OFR IP-SM-GW -> SC with the sender's MSISDN and the SMS-SUBMIT" "$ofr && ip.src == $ipsmgw && ip.dst == $sc && e164.msisdn == \"12025550101\" && diameter.SM-RP-UI"
# TS 29.338 6.3.1.1: a serving node that cannot name an SMS-IWMSC gives
# the realm alone, so any instance of the Service Centre may answer.
none "OFR of the IP-SM-GW naming a Destination-Host" "$ofr && ip.src == $ipsmgw && diameter.Destination-Host"
expect "SRR SC -> HSS for UE 2's number (TS 23.040 8.2.2)" "$srr && ip.src == $sc && ip.dst == $hss && e164.msisdn == \"12025550102\""
expect "SRA 2001 with the IP-SM-GW as the serving node and UE 2's IMSI" "$(answer_to) && diameter.Result-Code == 2001 && diameter.IP-SM-GW-Name && diameter.User-Name == \"001010000000002\""
# The Service Centre starts delivering as soon as it has taken the short
# message, so the query of the delivery may come before this answer; both
# are therefore looked for from here.
after_sra="$last"
expect "OFA 2001 SC -> IP-SM-GW with the SMS-SUBMIT-REPORT" "$ofa && ip.src == $sc && ip.dst == $ipsmgw && diameter.Result-Code == 2001 && diameter.SM-RP-UI"
# The submit report and the delivery go out together.
expect "submit report IP-SM-GW -> I-CSCF: RP-ACK in reply to the short message" "$message && $sms && ip.src == $ipsmgw && ip.dst == $icscf && sip.In-Reply-To == \"$mo\" && $rp_ack_mt && $smsip && sip.Request-Disposition == \"fork\""
report="$(field sip.Call-ID)"
expect "submit report reaches UE 1" "$message && sip.Call-ID == \"$report\" && ip.src == $pcscf && ip.dst == $ue1"
expect "200 OK UE 1 -> P-CSCF to the submit report" "sip.Status-Code == 200 && sip.Call-ID == \"$report\" && ip.src == $ue1"
last="$after_sra"

echo "the Service Centre delivers it (TS 23.040 8.1.1, TS 29.338 6.2.2)"
expect "SRR SC -> HSS, the SMS-GMSC's own query before the delivery" "$srr && ip.src == $sc && ip.dst == $hss && e164.msisdn == \"12025550102\""
expect "SRA 2001 with the IP-SM-GW" "$(answer_to) && diameter.Result-Code == 2001 && diameter.IP-SM-GW-Name"
expect "TFR SC -> IP-SM-GW naming UE 2 by IMSI" "$tfr && ip.src == $sc && ip.dst == $ipsmgw && diameter.User-Name == \"001010000000002\" && diameter.SM-RP-UI"
tfr_session="$(field diameter.Session-Id | cut -d, -f1)"
expect "MT MESSAGE IP-SM-GW -> I-CSCF: RP-DATA with SMS-DELIVER, the entry point in Route" "$message && $sms && ip.src == $ipsmgw && ip.dst == $icscf && sip.Route contains \"<sip:ims.mnc001.mcc001.3gppnetwork.org;lr>\" && sip.r-uri == \"tel:+12025550102\" && $rp_data_mt && gsm_sms.tp-mti == 0 && gsm_sms.tp-oa == \"12025550101\" && gsm_sms.sms_text == \"$gsm_text\" && $smsip && sip.Request-Disposition == \"no-fork\""
mt="$(field sip.Call-ID)"
expect "LIR for UE 2's tel URI" "diameter.cmd.code == 302 && $req && ip.src == $icscf && diameter.Public-Identity == \"tel:+12025550102\""
expect "LIA with UE 2's S-CSCF" "$(answer_to) && diameter.Result-Code == 2001 && diameter.Server-Name"
expect "MT MESSAGE I-CSCF -> S-CSCF" "$message && sip.Call-ID == \"$mt\" && ip.src == $icscf && ip.dst == $scscf"
expect "MT MESSAGE S-CSCF -> P-CSCF to the contact with +g.3gpp.smsip" "$message && sip.Call-ID == \"$mt\" && ip.src == $scscf && ip.dst == $pcscf && sip.r-uri.host == \"$ue2ip\" && sip.P-Called-Party-ID"
expect "MT MESSAGE reaches UE 2, text decoded" "$message && sip.Call-ID == \"$mt\" && ip.dst == $ue2ip && gsm_sms.sms_text == \"$gsm_text\""
expect "200 OK UE 2 -> P-CSCF, not yet the delivery report" "sip.Status-Code == 200 && sip.Call-ID == \"$mt\" && ip.src == $ue2ip"
expect "delivery report UE 2 -> P-CSCF: RP-ACK to the IP-SM-GW, in reply to the MT MESSAGE" "$message && $sms && ip.src == $ue2ip && sip.r-uri == \"$ipsmgw_uri\" && sip.In-Reply-To == \"$mt\" && $rp_ack_mo"
dr="$(field sip.Call-ID)"
expect "delivery report S-CSCF -> IP-SM-GW by UE 2's originating iFC" "$message && sip.Call-ID == \"$dr\" && ip.src == $scscf && ip.dst == $ipsmgw && count(sip.Route) == 2"
expect "202 Accepted IP-SM-GW -> S-CSCF for the delivery report" "sip.Status-Code == 202 && sip.Call-ID == \"$dr\" && ip.src == $ipsmgw"
# TS 23.204 6.4 NOTE 4: the answer to the TFR follows the delivery
# report, not the 200 to the MESSAGE that carried the short message.
expect "TFA 2001 IP-SM-GW -> SC for that TFR" "$tfa && ip.src == $ipsmgw && ip.dst == $sc && diameter.Session-Id == \"$tfr_session\" && diameter.Result-Code == 2001"
delivered_frame="$last"

echo "the status report of that short message"
expect "SRR SC -> HSS for the sender" "$srr && ip.src == $sc && ip.dst == $hss && e164.msisdn == \"12025550101\""
expect "TFR SC -> IP-SM-GW with the SMS-STATUS-REPORT" "$tfr && ip.src == $sc && ip.dst == $ipsmgw && diameter.User-Name == \"001010000000001\""
expect "status report IP-SM-GW -> I-CSCF: SMS-STATUS-REPORT, received by the SME" "$message && $sms && ip.src == $ipsmgw && sip.r-uri == \"tel:+12025550101\" && $rp_data_mt && gsm_sms.tp-mti == 2 && gsm_sms.tp-ra == \"12025550102\" && gsm_sms.dis_field.st_error == 0 && gsm_sms.dis.field_st_reason == 0"
sr="$(field sip.Call-ID)"
expect "status report reaches UE 1" "$message && sip.Call-ID == \"$sr\" && ip.dst == $ue1"
expect "delivery report UE 1 -> P-CSCF for the status report" "$message && $sms && ip.src == $ue1 && sip.In-Reply-To == \"$sr\" && $rp_ack_mo"

echo "a short message to a number no one has"
last="$delivered_frame"
expect "MESSAGE UE 1 -> P-CSCF to +12025550198" "$message && $sms && ip.src == $ue1 && gsm_sms.tp-da == \"12025550198\""
mo="$(field sip.Call-ID)"
expect "OFR IP-SM-GW -> SC" "$ofr && ip.src == $ipsmgw && ip.dst == $sc"
expect "SRR SC -> HSS for 12025550198" "$srr && ip.src == $sc && e164.msisdn == \"12025550198\""
expect "SRA DIAMETER_ERROR_USER_UNKNOWN" "$(answer_to) && diameter.Experimental-Result-Code == 5001"
# TS 29.338 6.2.1.1: the SMS-IWMSC refuses the short message with the
# cause that says why, and the TP-FCS of its report tells the UE.
expect "OFA DIAMETER_ERROR_SM_DELIVERY_FAILURE with INVALID_SME-ADDRESS" "$ofa && ip.src == $sc && diameter.Experimental-Result-Code == 5555 && diameter.SM-Enumerated-Delivery-Failure-Cause == 5"
expect "submit report reaches UE 1: RP-ERROR, cause 1, TP-FCS C3" "$message && sip.In-Reply-To == \"$mo\" && ip.dst == $ue1 && $rp_error_mt && gsm_a.rp.cause == 1 && gsm_sms.tp-fcs == 0xc3"
refused_frame="$last"

echo "b) UE 2 takes no short message: the Service Centre holds it (TS 23.204 6.5a)"
last="$refused_frame"
expect "MESSAGE UE 1 -> P-CSCF for the UE that takes none" "$message && $sms && ip.src == $ue1 && gsm_sms.sms_text == \"$away_text\""
expect "TFR SC -> IP-SM-GW" "$tfr && ip.src == $sc && ip.dst == $ipsmgw && diameter.User-Name == \"001010000000002\""
tfr_session="$(field diameter.Session-Id | cut -d, -f1)"
expect "MT MESSAGE IP-SM-GW -> I-CSCF" "$message && $sms && ip.src == $ipsmgw && ip.dst == $icscf && gsm_sms.sms_text == \"$away_text\""
mt="$(field sip.Call-ID)"
expect "480 Temporarily Unavailable UE 2 -> P-CSCF" "sip.Status-Code == 480 && sip.Call-ID == \"$mt\" && ip.src == $ue2ip"
expect "480 reaches the IP-SM-GW" "sip.Status-Code == 480 && sip.Call-ID == \"$mt\" && ip.dst == $ipsmgw"
# TS 29.311 6.1.4.4.1 maps the 480 to an absent subscriber, which TS
# 29.338 7.3.3 carries as DIAMETER_ERROR_ABSENT_USER.
expect "TFA DIAMETER_ERROR_ABSENT_USER for that TFR" "$tfa && ip.src == $ipsmgw && ip.dst == $sc && diameter.Session-Id == \"$tfr_session\" && diameter.Experimental-Result-Code == 5550"
expect "RDR SC -> HSS with SM-Delivery-Cause ABSENT_USER" "$rdr && ip.src == $sc && ip.dst == $hss && e164.msisdn == \"12025550102\" && diameter.SM-Delivery-Cause == 1"
expect "RDA 2001" "$(answer_to) && diameter.Result-Code == 2001"

echo "UE 2 is back, the HSS alerts the Service Centre and it delivers (TS 23.204 6.5b, TS 23.040 3.2.6 item 2c)"
expect "MESSAGE UE 2 -> P-CSCF: the short message of its own, in UCS2" "$message && $sms && ip.src == $ue2ip && $rp_data_mo && gsm_sms.tp-da == \"12025550101\" && gsm_sms.tp-dcs == 8 && gsm_sms.sms_text == \"$ucs2_text\""
back="$last"
expect "Ready-For-SM IP-SM-GW -> HSS for UE 2's number with UE_PRESENT" "$ready && ip.src == $ipsmgw && ip.dst == $hss && e164.msisdn == \"12025550102\" && diameter.Alert-Reason == 0"
# The alert does not wait for the answer to that notice, so the answer
# may come after the ALR.
last="$back"
expect "ALR HSS -> SC for UE 2 (TS 29.338 5.2.2)" "$alr && ip.src == $hss && ip.dst == $sc && e164.msisdn == \"12025550102\""
alerted="$last"
expect "ALA 2001 SC -> HSS" "$(answer_to) && diameter.Result-Code == 2001"
# The Service Centre starts the delivery as soon as it has the alert,
# so its query may come before this answer.
last="$alerted"
expect "SRR SC -> HSS, now that the unreachable flag is clear" "$srr && ip.src == $sc && e164.msisdn == \"12025550102\""
expect "SRA 2001 with the IP-SM-GW again" "$(answer_to) && diameter.Result-Code == 2001 && diameter.IP-SM-GW-Name"
expect "TFR SC -> IP-SM-GW: the short message it held" "$tfr && ip.src == $sc && ip.dst == $ipsmgw && diameter.User-Name == \"001010000000002\""
tfr_session="$(field diameter.Session-Id | cut -d, -f1)"
expect "MT MESSAGE reaches UE 2, the text it did not take before" "$message && ip.dst == $ue2ip && gsm_sms.sms_text == \"$away_text\""
mt="$(field sip.Call-ID)"
# The script goes on as soon as UE 2 has the short message, so what UE 1
# sends next may come before the TFA that ends this delivery.
away_delivered="$last"
expect "delivery report UE 2 -> P-CSCF: RP-ACK" "$message && $sms && ip.src == $ue2ip && sip.In-Reply-To == \"$mt\" && $rp_ack_mo"
expect "TFA 2001 for that TFR" "$tfa && ip.src == $ipsmgw && ip.dst == $sc && diameter.Session-Id == \"$tfr_session\" && diameter.Result-Code == 2001"

echo "b) again: UE 2 only registers again, and that alone sets off the alert (TS 23.204 6.5b step 2a)"
last="$away_delivered"
expect "MESSAGE UE 1 -> P-CSCF for the UE that will only register again" "$message && $sms && ip.src == $ue1 && gsm_sms.sms_text == \"$rereg_text\""
expect "TFR SC -> IP-SM-GW" "$tfr && ip.src == $sc && ip.dst == $ipsmgw && diameter.User-Name == \"001010000000002\""
tfr_session="$(field diameter.Session-Id | cut -d, -f1)"
expect "MT MESSAGE reaches UE 2" "$message && ip.dst == $ue2ip && gsm_sms.sms_text == \"$rereg_text\""
mt="$(field sip.Call-ID)"
expect "480 Temporarily Unavailable UE 2 -> P-CSCF" "sip.Status-Code == 480 && sip.Call-ID == \"$mt\" && ip.src == $ue2ip"
expect "TFA DIAMETER_ERROR_ABSENT_USER for that TFR" "$tfa && ip.src == $ipsmgw && ip.dst == $sc && diameter.Session-Id == \"$tfr_session\" && diameter.Experimental-Result-Code == 5550"
expect "RDR SC -> HSS with SM-Delivery-Cause ABSENT_USER" "$rdr && ip.src == $sc && ip.dst == $hss && e164.msisdn == \"12025550102\" && diameter.SM-Delivery-Cause == 1"
held="$last"
expect "RDA 2001" "$(answer_to) && diameter.Result-Code == 2001"
# UE 2 registers again once the HSS has logged the report, which it does
# before it answers.
last="$held"
expect "REGISTER UE 2 -> P-CSCF: the re-registration" "sip.Method == \"REGISTER\" && ip.src == $ue2ip && ip.dst == $pcscf"
expect "third-party REGISTER S-CSCF -> IP-SM-GW for the re-registration" "sip.Method == \"REGISTER\" && ip.src == $scscf && ip.dst == $ipsmgw && sip.msg_body contains \"<service-info>12025550102</service-info>\" && sip.Expires == 3600"
reregistered="$last"
expect "200 OK IP-SM-GW -> S-CSCF to that REGISTER" "sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ip.src == $ipsmgw && ip.dst == $scscf"
# The refresh of the subscription and the notification go out together.
last="$reregistered"
expect "SUBSCRIBE IP-SM-GW -> S-CSCF: the refresh within the dialog" "sip.Method == \"SUBSCRIBE\" && sip.Event == \"reg\" && ip.src == $ipsmgw && ip.dst == $scscf"
last="$reregistered"
expect "Ready-For-SM IP-SM-GW -> HSS with UE_PRESENT, set off by the re-registration" "$ready && ip.src == $ipsmgw && ip.dst == $hss && e164.msisdn == \"12025550102\" && diameter.Alert-Reason == 0"
told="$last"
none "MESSAGE from UE 2 between the report of the absent user and that Ready-For-SM" "$message && ip.src == $ue2ip && frame.number > $held && frame.number < $told"
expect "ALR HSS -> SC for UE 2" "$alr && ip.src == $hss && ip.dst == $sc && e164.msisdn == \"12025550102\""
alerted="$last"
expect "ALA 2001 SC -> HSS" "$(answer_to) && diameter.Result-Code == 2001"
last="$alerted"
expect "SRR SC -> HSS, now that the unreachable flag is clear" "$srr && ip.src == $sc && e164.msisdn == \"12025550102\""
expect "SRA 2001 with the IP-SM-GW again" "$(answer_to) && diameter.Result-Code == 2001 && diameter.IP-SM-GW-Name"
expect "TFR SC -> IP-SM-GW: the short message it held" "$tfr && ip.src == $sc && ip.dst == $ipsmgw && diameter.User-Name == \"001010000000002\""
tfr_session="$(field diameter.Session-Id | cut -d, -f1)"
expect "MT MESSAGE reaches UE 2, the text it did not take before" "$message && ip.dst == $ue2ip && gsm_sms.sms_text == \"$rereg_text\""
mt="$(field sip.Call-ID)"
rereg_delivered="$last"
expect "delivery report UE 2 -> P-CSCF: RP-ACK" "$message && $sms && ip.src == $ue2ip && sip.In-Reply-To == \"$mt\" && $rp_ack_mo"
expect "TFA 2001 for that TFR" "$tfa && ip.src == $ipsmgw && ip.dst == $sc && diameter.Session-Id == \"$tfr_session\" && diameter.Result-Code == 2001"

echo "c) UE 2 has no memory: the notification sets off the alert (TS 23.204 6.6, TS 23.040 3.2.6 item 2f)"
last="$rereg_delivered"
expect "MESSAGE UE 1 -> P-CSCF for the UE without memory" "$message && $sms && ip.src == $ue1 && gsm_sms.sms_text == \"$room_text\""
expect "TFR SC -> IP-SM-GW" "$tfr && ip.src == $sc && ip.dst == $ipsmgw && diameter.User-Name == \"001010000000002\""
tfr_session="$(field diameter.Session-Id | cut -d, -f1)"
expect "MT MESSAGE reaches UE 2" "$message && ip.dst == $ue2ip && gsm_sms.sms_text == \"$room_text\""
mt="$(field sip.Call-ID)"
expect "200 OK UE 2 -> P-CSCF: the MESSAGE arrived" "sip.Status-Code == 200 && sip.Call-ID == \"$mt\" && ip.src == $ue2ip"
expect "delivery report UE 2 -> P-CSCF: RP-ERROR, cause 22, TP-FCS D3" "$message && $sms && ip.src == $ue2ip && sip.In-Reply-To == \"$mt\" && $rp_error_mo && gsm_a.rp.cause == 22 && gsm_sms.tp-fcs == 0xd3"
expect "TFA DIAMETER_ERROR_SM_DELIVERY_FAILURE with MEMORY_CAPACITY_EXCEEDED" "$tfa && ip.src == $ipsmgw && ip.dst == $sc && diameter.Session-Id == \"$tfr_session\" && diameter.Experimental-Result-Code == 5555 && diameter.SM-Enumerated-Delivery-Failure-Cause == 0"
expect "RDR SC -> HSS with SM-Delivery-Cause UE_MEMORY_CAPACITY_EXCEEDED" "$rdr && ip.src == $sc && ip.dst == $hss && e164.msisdn == \"12025550102\" && diameter.SM-Delivery-Cause == 0"
expect "RDA 2001" "$(answer_to) && diameter.Result-Code == 2001"
expect "MESSAGE UE 2 -> P-CSCF: RP-SMMA, the notification it owes (TS 24.011 7.3.2)" "$message && $sms && ip.src == $ue2ip && $rp_smma"
smma="$(field sip.Call-ID)"
expect "RP-SMMA reaches the IP-SM-GW" "$message && sip.Call-ID == \"$smma\" && ip.dst == $ipsmgw && $rp_smma"
expect "Ready-For-SM IP-SM-GW -> HSS with UE_MEMORY_AVAILABLE" "$ready && ip.src == $ipsmgw && ip.dst == $hss && e164.msisdn == \"12025550102\" && diameter.Alert-Reason == 1"
notified="$last"
expect "Ready-For-SM answer 2001 HSS -> IP-SM-GW" "$(answer_to) && ip.src == $hss && ip.dst == $ipsmgw && diameter.Result-Code == 2001"
after_notify="$last"
# As in b), the alert does not wait for the answer to the notice, so the
# ALR may come before that answer.
last="$notified"
expect "ALR HSS -> SC for UE 2" "$alr && ip.src == $hss && ip.dst == $sc && e164.msisdn == \"12025550102\""
alerted="$last"
expect "ALA 2001 SC -> HSS" "$(answer_to) && diameter.Result-Code == 2001"
last="$alerted"
expect "TFR SC -> IP-SM-GW: the short message it held" "$tfr && ip.src == $sc && ip.dst == $ipsmgw && diameter.User-Name == \"001010000000002\""
tfr_session="$(field diameter.Session-Id | cut -d, -f1)"
expect "MT MESSAGE reaches UE 2 again, with the same text" "$message && ip.dst == $ue2ip && gsm_sms.sms_text == \"$room_text\""
expect "TFA 2001 for that TFR" "$tfa && ip.src == $ipsmgw && ip.dst == $sc && diameter.Session-Id == \"$tfr_session\" && diameter.Result-Code == 2001"
# The RP-ACK for the notification goes out only after the HSS has taken
# it (TS 23.204 6.6), so it comes after the answer to the Ready-For-SM
# and may come after the alert.
last="$after_notify"
expect "RP-ACK for the notification reaches UE 2" "$message && $sms && ip.dst == $ue2ip && sip.In-Reply-To == \"$smma\" && $rp_ack_mt"

# The charging information stays in the network (TS 24.229 5.2.1):
# the P-CSCF sends none of it to a UE. Every UE of this setup has an
# address from 172.28.0.100 up.
none "P-Charging-Vector or P-Charging-Function-Addresses from the P-CSCF to a UE" 'ip.src == 172.28.0.40 && ip.dst >= 172.28.0.100 && (sip.P-Charging-Vector || sip.P-Charging-Function-Addresses)'
# Wireshark warns of a command that its dictionary does not have, which
# Ready-For-SM is: that warning is how the capture shows the command to
# be this project's own. Its AVPs are standard ones, which Wireshark
# decodes like any other.
none "malformed SIP, Diameter, RP or TP packets" "(sip || diameter || gsm_a.rp || gsm_sms) && (_ws.malformed || _ws.expert.severity >= warning) && !(diameter.cmd.code == 16777214)"
last=0
expect "Ready-For-SM marked by Wireshark as a command it does not know" "$ready && diameter.cmd.code.unknown"
none "malformed Ready-For-SM requests and answers, or AVPs in them that Wireshark does not know" "diameter.cmd.code == 16777214 && (_ws.malformed || diameter.avp.code.unknown)"
# No Sh update carries a notice: an Sh update of SMSRegistrationInfo
# registers the IP-SM-GW or takes the registration away, and nothing
# else (TS 29.328 6.1.2.1 step 4b).
none "Sh update carrying anything but the IP-SM-GW's registration" 'diameter.cmd.code == 307 && diameter.Sh-User-Data contains "ReadyForSM"'
none "retransmitted MESSAGEs and their responses" 'sip.CSeq.method == "MESSAGE" && sip.resend == 1'
# RFC 3261 18.1.1: a request over 1300 bytes does not go over UDP.
none "SIP requests over 1300 bytes on UDP" 'sip.Method && udp && udp.length > 1308'
# The Service Centre speaks Diameter with the IP-SM-GW and the HSS, and
# with nobody else.
none "Diameter between the Service Centre and a node other than the IP-SM-GW or the HSS" \
	"diameter && (ip.src == $sc || ip.dst == $sc) && !(ip.addr == $ipsmgw) && !(ip.addr == $hss)"

# Every display filter that the guide shows its readers finds something
# in this capture.
guide_filters docs/guide/en/sms.md

# The ladder of the short messages, the Sh messages and the SGd and S6c
# messages, Ready-For-SM among them, for the guide and the CI artifact.
mkdir -p e2e-out
docker compose exec -T capture tshark -r "$pcap" -T fields -E separator=/t \
	-Y 'sip.CSeq.method == "MESSAGE" || (sip && ip.addr == 172.28.0.70) || diameter.cmd.code in {306, 307, 8388645, 8388646, 8388647, 8388648, 8388649, 16777214}' \
	-e frame.number -e ip.src -e ip.dst -e _ws.col.Info -e gsm_sms.sms_text | tr -d '\r' >e2e-out/sms-ladder.txt

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color | grep -E '"level":"(WARN|ERROR)"' | tail -n 40
fi
exit "$fail"
