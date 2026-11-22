#!/usr/bin/env bash
# End-to-end check: SMS over IP. UE 2 registers, and the
# capture shows the S-CSCF registering it at the IP-SM-GW too, the
# IP-SM-GW's subscription to its registration state and the PUR to the
# HSS. UE 1 then sends UE 2 a short message in the GSM 7 bit alphabet
# with a status report request, and UE 2 sends UE 1 one in Japanese
# (UCS2). Last, a short message to a number no one has gets RP-ERROR,
# and one to a user who is not registered gets a status report that
# says it was not delivered. Wireshark must decode the RP and TP layers
# of every short message, with the text that was sent.
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

# The CSCFs and the IP-SM-GW serve requests only once their Diameter
# connections to the HSS are open.
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
wait_for_open ipsmgw

ue2=(--impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org
	--impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org --password test-password-2)
gsm_text='Hello UE-B, this is UE-A.'
ucs2_text='こんにちは、UE-A さん'
remember_nodes
fail=0

# wait_log waits until the log of a container started with "run -d"
# matches pattern.
wait_log() {
	local name="$1" pattern="$2" i
	for i in $(seq 1 20); do
		grep -q "$pattern" <<<"$(docker logs "$name" 2>/dev/null)" && return 0
		sleep 1
	done
	return 1
}
# stop_ue stops a UE that "ue register" keeps registered; it
# deregisters on SIGTERM.
stop_ue() {
	docker stop "$1" >/dev/null
	docker rm "$1" >/dev/null
}

# UE 2 stays registered and takes short messages while UE 1 sends one.
# ue and ue2 have fixed addresses, so each runs once at a time.
docker compose run --no-deps -d --name ims-practice-ue2-sms ue2 register "${ue2[@]}" >/dev/null
if wait_log ims-practice-ue2-sms '"msg":"registered"'; then
	echo "ok   UE 2 registered"
else
	echo "FAIL UE 2 did not register"
	fail=1
fi
if out="$(docker compose run --no-deps --rm ue sms --status-report +12025550102 "$gsm_text" 2>&1)" &&
	grep -q '"msg":"status report".*"status":0,' <<<"$out"; then
	echo "ok   UE 1 sent a short message to UE 2, and got a status report that it was delivered"
else
	echo "FAIL UE 1 to UE 2"
	grep -E '"msg":"(sms sent|submit report|status report|ue failed)"' <<<"$out" || true
	fail=1
fi
if grep -q "\"msg\":\"sms received\",.*\"from\":\"+12025550101\",\"text\":\"$gsm_text\"" <<<"$(docker logs ims-practice-ue2-sms 2>&1)"; then
	echo "ok   UE 2 received the text from UE 1"
else
	echo "FAIL UE 2 did not receive the text"
	fail=1
fi
stop_ue ims-practice-ue2-sms

docker compose run --no-deps -d --name ims-practice-ue1-sms ue register >/dev/null
wait_log ims-practice-ue1-sms '"msg":"registered"' || true
if docker compose run --no-deps --rm ue2 sms "${ue2[@]}" +12025550101 "$ucs2_text" >/dev/null 2>&1; then
	echo "ok   UE 2 sent a short message in Japanese to UE 1"
else
	echo "FAIL UE 2 to UE 1"
	fail=1
fi
# The UE sends its delivery report after it logs the short message.
if wait_log ims-practice-ue1-sms "\"msg\":\"sms received\",.*\"from\":\"+12025550102\",\"text\":\"$ucs2_text\"" &&
	wait_log ims-practice-ue1-sms '"msg":"delivery report sent"'; then
	echo "ok   UE 1 received the Japanese text from UE 2"
else
	echo "FAIL UE 1 did not receive the Japanese text"
	fail=1
fi
stop_ue ims-practice-ue1-sms

# A number that no subscriber has (TS 24.011 E-2, cause 1).
out="$(docker compose run --no-deps --rm ue sms +12025550198 'Nobody has this number.' 2>&1 || true)"
if grep -q 'the SC refused the short message: RP-Cause 1, TP-FCS 0xc3' <<<"$out"; then
	echo "ok   a short message to an unknown number was refused with RP-Cause 1"
else
	echo "FAIL a short message to an unknown number: $(tail -n 1 <<<"$out")"
	fail=1
fi
# Subscriber 3 exists but is never registered. TP-ST 0x62 (98) is
# "no response from SME", after which the SC tries no more (TS 23.040
# 9.2.3.15).
out="$(docker compose run --no-deps --rm ue sms --status-report +12025550103 'Are you there?' 2>&1 || true)"
if grep -q '"msg":"status report".*"status":98,' <<<"$out"; then
	echo "ok   a short message to a user who is not registered got a status report that it was not delivered"
else
	echo "FAIL a short message to a user who is not registered: $(tail -n 1 <<<"$out")"
	fail=1
fi
same_nodes
sleep 1

pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
echo "reading $pcap"

last=0

ue1=172.28.0.100
ue2ip=172.28.0.101
pcscf=172.28.0.40
icscf=172.28.0.20
scscf=172.28.0.30
hss=172.28.0.10
ipsmgw=172.28.0.70
ipsmgw_uri=sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060
message='sip.Method == "MESSAGE"'
sms='sip.Content-Type == "application/vnd.3gpp.sms"'
smsip='sip.Accept-Contact == "*;+g.3gpp.smsip;require;explicit"'
req='diameter.flags.request == 1'
ans='diameter.flags.request == 0'
# RP message types (TS 24.011 8.2.2) and TP message types (TS 23.040
# 9.2.3.1). An RP-ACK from the network carries an SMS-SUBMIT-REPORT,
# one from the MS an SMS-DELIVER-REPORT; both have TP-MTI 1 and 0, as
# the direction tells them apart.
rp_data_mo='gsm_a.rp.msg_type == 0x00'
rp_data_mt='gsm_a.rp.msg_type == 0x01'
rp_ack_mo='gsm_a.rp.msg_type == 0x02'
rp_ack_mt='gsm_a.rp.msg_type == 0x03'
rp_error_mt='gsm_a.rp.msg_type == 0x05'

# The IP-SM-GW opens Sh, and nothing else, to the HSS.
cer="diameter.cmd.code == 257 && $req && ip.src == $ipsmgw"
expect "CER IP-SM-GW -> HSS advertising Sh" "$cer && diameter.Auth-Application-Id == 16777217"
none "CER of the IP-SM-GW advertising an application other than Sh" "$cer && (diameter.Auth-Application-Id < 16777217 || diameter.Auth-Application-Id > 16777217)"
expect "CEA 2001 HSS -> IP-SM-GW" "diameter.cmd.code == 257 && $ans && ip.dst == $ipsmgw && diameter.Result-Code == 2001"

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
expect "PUR IP-SM-GW -> HSS: SMSRegistrationInfo with the IP-SM-GW's number" "diameter.cmd.code == 307 && $req && ip.src == $ipsmgw && diameter.Data-Reference == 24 && e164.msisdn == \"12025550102\" && diameter.Sh-User-Data contains \"<IP-SM-GW-Number>12025550199<\""
expect "PUA 2001" "$(answer_to) && diameter.Result-Code == 2001"
# The 200 to the SUBSCRIBE comes back through the I-CSCF, and may come
# after the NOTIFY, which the S-CSCF sends straight to the IP-SM-GW. The
# IP-SM-GW answers the NOTIFY and sends the PUR together.
last="$subscribed"
expect "200 OK to the SUBSCRIBE, I-CSCF -> IP-SM-GW" "sip.Status-Code == 200 && sip.CSeq.method == \"SUBSCRIBE\" && ip.src == $icscf && ip.dst == $ipsmgw"
last="$notified"
expect "200 OK to the NOTIFY, IP-SM-GW -> S-CSCF" "sip.Status-Code == 200 && sip.CSeq.method == \"NOTIFY\" && ip.src == $ipsmgw && ip.dst == $scscf"

echo "a short message from UE 1 to UE 2, GSM 7 bit, with a status report (TS 24.341 B.5, B.6)"
# UE 1 sends once UE 2 has said it is registered, which is after the
# 200 to UE 2's REGISTER. The IP-SM-GW's PUR above runs on its own and
# may come after.
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
# The SC stub finds the recipient by number over Sh.
expect "UDR IP-SM-GW -> HSS: IMSPublicIdentity of MSISDN 12025550102" "diameter.cmd.code == 306 && $req && ip.src == $ipsmgw && diameter.Data-Reference == 10 && e164.msisdn == \"12025550102\""
expect "UDA 2001" "$(answer_to) && diameter.Result-Code == 2001"
after_udr="$last"
# The submit report and the delivery go out together.
expect "submit report IP-SM-GW -> I-CSCF: RP-ACK in reply to the short message" "$message && $sms && ip.src == $ipsmgw && ip.dst == $icscf && sip.In-Reply-To == \"$mo\" && $rp_ack_mt && $smsip && sip.Request-Disposition == \"fork\""
report="$(field sip.Call-ID)"
expect "submit report reaches UE 1" "$message && sip.Call-ID == \"$report\" && ip.src == $pcscf && ip.dst == $ue1"
expect "200 OK UE 1 -> P-CSCF to the submit report" "sip.Status-Code == 200 && sip.Call-ID == \"$report\" && ip.src == $ue1"
last="$after_udr"
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
expect "status report IP-SM-GW -> I-CSCF: SMS-STATUS-REPORT, received by the SME" "$message && $sms && ip.src == $ipsmgw && sip.r-uri == \"tel:+12025550101\" && $rp_data_mt && gsm_sms.tp-mti == 2 && gsm_sms.tp-ra == \"12025550102\" && gsm_sms.dis_field.st_error == 0 && gsm_sms.dis.field_st_reason == 0"
sr="$(field sip.Call-ID)"
expect "status report reaches UE 1" "$message && sip.Call-ID == \"$sr\" && ip.dst == $ue1"
# UE 1 ends once it has the status report, and only then does the
# script go on: what follows comes after this frame. UE 1's delivery
# report below need not arrive before it.
reported="$last"
expect "delivery report UE 1 -> P-CSCF for the status report" "$message && $sms && ip.src == $ue1 && sip.In-Reply-To == \"$sr\" && $rp_ack_mo"
expect "delivery report reaches the IP-SM-GW" "$message && sip.In-Reply-To == \"$sr\" && ip.dst == $ipsmgw"

echo "UE 2 deregisters, and the IP-SM-GW takes itself out of the HSS"
last="$reported"
expect "NOTIFY S-CSCF -> IP-SM-GW: UE 2's subscription terminated" "sip.Method == \"NOTIFY\" && ip.src == $scscf && ip.dst == $ipsmgw && sip.Subscription-State contains \"terminated\" && sip.msg_body contains \"+12025550102\""
expect "PUR IP-SM-GW -> HSS with an empty IP-SM-GW number" "diameter.cmd.code == 307 && $req && e164.msisdn == \"12025550102\" && diameter.Sh-User-Data contains \"<IP-SM-GW-Number></IP-SM-GW-Number>\""
mark="$last"

echo "a short message from UE 2 to UE 1 in Japanese, UCS2"
# UE 2 registers again for this one, so the PUR above, which runs on its
# own after the NOTIFY, need not come before.
last="$reported"
expect "MESSAGE UE 2 -> P-CSCF: SMS-SUBMIT in UCS2" "$message && $sms && ip.src == $ue2ip && $rp_data_mo && gsm_sms.tp-da == \"12025550101\" && gsm_sms.tp-dcs == 8 && gsm_sms.tp-srr == 0 && gsm_sms.sms_text == \"$ucs2_text\""
mo="$(field sip.Call-ID)"
mark="$last"
# The 202 goes back along the MESSAGE's Via; the submit report and the
# delivery are MESSAGEs of their own, through the I-CSCF. Nothing orders
# them, and a busy P-CSCF has passed the 202 on last.
expect "202 Accepted reaches UE 2" "sip.Status-Code == 202 && sip.Call-ID == \"$mo\" && ip.dst == $ue2ip"
last="$mark"
expect "submit report reaches UE 2" "$message && sip.In-Reply-To == \"$mo\" && ip.dst == $ue2ip && $rp_ack_mt"
last="$mark"
expect "MT MESSAGE reaches UE 1: SMS-DELIVER in UCS2, text decoded" "$message && $sms && ip.dst == $ue1 && $rp_data_mt && gsm_sms.tp-oa == \"12025550102\" && gsm_sms.tp-dcs == 8 && gsm_sms.sms_text == \"$ucs2_text\""
mt="$(field sip.Call-ID)"
received="$last"
expect "delivery report from UE 1 reaches the IP-SM-GW" "$message && sip.In-Reply-To == \"$mt\" && ip.dst == $ipsmgw && $rp_ack_mo"

echo "a short message to a number no one has"
# The script goes on once UE 1 has logged the delivery report as sent,
# not once it has arrived.
last="$received"
expect "MESSAGE UE 1 -> P-CSCF to +12025550198" "$message && $sms && ip.src == $ue1 && gsm_sms.tp-da == \"12025550198\""
mo="$(field sip.Call-ID)"
expect "UDR for 12025550198" "diameter.cmd.code == 306 && $req && e164.msisdn == \"12025550198\""
expect "UDA DIAMETER_ERROR_USER_UNKNOWN" "$(answer_to) && diameter.Experimental-Result-Code == 5001"
expect "submit report reaches UE 1: RP-ERROR, cause 1, TP-FCS C3" "$message && sip.In-Reply-To == \"$mo\" && ip.dst == $ue1 && $rp_error_mt && gsm_a.rp.cause == 1 && gsm_sms.tp-fcs == 0xc3"

echo "a short message to a user who is not registered"
expect "MESSAGE UE 1 -> P-CSCF to +12025550103 with a status report request" "$message && $sms && ip.src == $ue1 && gsm_sms.tp-da == \"12025550103\" && gsm_sms.tp-srr == 1"
mark="$last"
expect "MT MESSAGE IP-SM-GW -> I-CSCF to tel:+12025550103" "$message && ip.src == $ipsmgw && sip.r-uri == \"tel:+12025550103\" && $rp_data_mt"
mt="$(field sip.Call-ID)"
expect "LIR for tel:+12025550103" "diameter.cmd.code == 302 && $req && diameter.Public-Identity == \"tel:+12025550103\""
# Every subscriber has a service for the unregistered state, the MMTel
# AS's criterion for TERMINATING_UNREGISTERED, so the HSS has the I-CSCF
# pick an S-CSCF (TS 29.228 6.1.4.1 step 3). That S-CSCF takes the
# user's profile, finds no criterion for a MESSAGE to the user, and
# answers 480 (TS 24.229 5.4.3.3).
expect "LIA DIAMETER_UNREGISTERED_SERVICE with the capabilities of an S-CSCF" "$(answer_to) && diameter.Experimental-Result-Code == 2003 && diameter.Server-Capabilities"
expect "MT MESSAGE I-CSCF -> S-CSCF" "$message && sip.Call-ID == \"$mt\" && ip.src == $icscf && ip.dst == $scscf"
expect "SAR UNREGISTERED_USER for tel:+12025550103" "diameter.cmd.code == 301 && $req && diameter.Server-Assignment-Type == 3 && diameter.Public-Identity == \"tel:+12025550103\""
expect "SAA 2001" "$(answer_to) && diameter.Result-Code == 2001"
expect "480 S-CSCF -> I-CSCF" "sip.Status-Code == 480 && sip.Call-ID == \"$mt\" && ip.src == $scscf && ip.dst == $icscf"
expect "480 I-CSCF -> IP-SM-GW" "sip.Status-Code == 480 && sip.Call-ID == \"$mt\" && ip.dst == $ipsmgw"
expect "status report reaches UE 1: no response from the SME, no more attempts (TP-ST 0x62)" "$message && ip.dst == $ue1 && gsm_sms.tp-mti == 2 && gsm_sms.tp-ra == \"12025550103\" && gsm_sms.dis_field.st_error == 3 && gsm_sms.dis.field_st_reason == 2"

# The charging information stays in the network (TS 24.229 5.2.1):
# the P-CSCF sends none of it to a UE. Every UE of this setup has an
# address from 172.28.0.100 up.
none "P-Charging-Vector or P-Charging-Function-Addresses from the P-CSCF to a UE" 'ip.src == 172.28.0.40 && ip.dst >= 172.28.0.100 && (sip.P-Charging-Vector || sip.P-Charging-Function-Addresses)'
none "malformed SIP, Diameter, RP or TP packets" '(sip || diameter || gsm_a.rp || gsm_sms) && (_ws.malformed || _ws.expert.severity >= warning)'
none "retransmitted MESSAGEs and their responses" 'sip.CSeq.method == "MESSAGE" && sip.resend == 1'
# RFC 3261 18.1.1: a request over 1300 bytes does not go over UDP.
none "SIP requests over 1300 bytes on UDP" 'sip.Method && udp && udp.length > 1308'

# The ladder of the short messages and the Sh messages, for the guide
# and the CI artifact.
mkdir -p e2e-out
docker compose exec -T capture tshark -r "$pcap" -T fields -E separator=/t \
	-Y 'sip.CSeq.method == "MESSAGE" || (sip && ip.addr == 172.28.0.70) || diameter.cmd.code == 306 || diameter.cmd.code == 307' \
	-e frame.number -e ip.src -e ip.dst -e _ws.col.Info -e gsm_sms.sms_text | tr -d '\r' >e2e-out/sms-ladder.txt

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color | grep -E '"level":"(WARN|ERROR)"' | tail -n 40
fi
exit "$fail"
