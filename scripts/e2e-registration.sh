#!/usr/bin/env bash
# End-to-end check: a UE registers with SIP Digest through the
# P-CSCF, the I-CSCF and the S-CSCF, and the capture holds the whole
# registration in order, with the subscriptions of the P-CSCF and the
# UE to the registration state. A second UE with a wrong password must
# not register.
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

# The CSCFs can only serve a REGISTER once their Diameter connections to
# the HSS are open.
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
	docker compose logs --no-color "$service" | tail -n 30 >&2
	exit 1
}
wait_for_open icscf
wait_for_open scscf

remember_nodes
fail=0
domain=ims.mnc001.mcc001.3gppnetwork.org
if docker compose run --no-deps --rm ue register --deregister-after 2s; then
	echo "ok   UE registered and deregistered"
else
	echo "FAIL UE registration"
	fail=1
fi
if docker compose run --no-deps --rm ue register --password wrong-password 2>/dev/null; then
	echo "FAIL UE with a wrong password registered"
	fail=1
else
	echo "ok   UE with a wrong password was refused"
fi
# A MESSAGE from the UE's address after it has deregistered: the
# P-CSCF has no IP association for it any more, so it discards the
# request and sends nothing back, not even a 100 (TS 24.229
# 5.2.6.3.2A). The UE sends it twice, as a UE retransmits over UDP.
unregistered_call_id="unregistered-$(date +%s)@172.28.0.100"
unregistered_message="MESSAGE sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org SIP/2.0\r
Via: SIP/2.0/UDP 172.28.0.100:5060;branch=z9hG4bK-unregistered-$(date +%s);rport\r
Max-Forwards: 70\r
Route: <sip:pcscf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr>\r
From: <sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org>;tag=unregistered\r
To: <sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org>\r
Call-ID: $unregistered_call_id\r
CSeq: 1 MESSAGE\r
Content-Type: text/plain\r
Content-Length: 5\r
\r
hello"
reply="$(docker compose run --no-deps --rm -T --entrypoint sh ue -c \
	'for i in 1 2; do printf "$1" | nc -u -p 5060 -w 2 172.28.0.40 5060; done' sh "$unregistered_message" || true)"
if [ -z "$reply" ]; then
	echo "ok   a MESSAGE from the deregistered UE got no answer"
else
	echo "FAIL a MESSAGE from the deregistered UE got an answer: $(head -n 1 <<<"$reply")"
	fail=1
fi

# Removing the subscription of a registered user deregisters the user as
# well: the HSS sends an RTR, and the S-CSCF ends the registration with
# a NOTIFY whose contact is rejected (TS 29.228 6.1.3). Subscriber 3 is
# the one that no other check of this scenario registers, so taking it
# away leaves the rest of the run as it was. The DELETE goes to the Lab,
# which passes it to the HSS provisioning API, as its Subscribers panel
# does.
ue3=(--impi "001010000000003@$domain" --impu "sip:+12025550103@$domain" --password test-password-3)
docker compose run --no-deps -d --name ims-practice-ue3-rtr ue3 register "${ue3[@]}" >/dev/null
# The UE needs its own reg event subscription before the removal, or the
# NOTIFY that rejects its registration would have nowhere to go. The UE
# logs "reg event" once the first NOTIFY of that subscription is in.
subscribed=
for i in $(seq 1 30); do
	grep -q '"msg":"reg event"' <<<"$(docker logs ims-practice-ue3-rtr 2>/dev/null)" && {
		subscribed=yes
		break
	}
	sleep 1
done
if [ -z "$subscribed" ]; then
	echo "FAIL UE 3 did not register and subscribe to its registration state within 30 s"
	docker logs ims-practice-ue3-rtr 2>&1 | tail -n 5
	fail=1
else
	# The HSS answers the DELETE only once the RTR is over, so there is
	# nothing else to wait for. The status code is read from the end of
	# the output, since curl on Windows cannot write to /dev/null when
	# MSYS_NO_PATHCONV is set.
	status="$(curl -s -w '
%{http_code}' -X DELETE "http://localhost:8080/api/subscribers/001010000000003%40$domain" | tail -n 1)"
	if [ "$status" = 204 ]; then
		echo "ok   the HSS removed the subscription of UE 3"
	else
		echo "FAIL removing the subscription of UE 3: HTTP $status"
		fail=1
	fi
fi
# On event="rejected" the UE does not register again (TS 24.229
# 5.1.1.7): "ue register" ends with an error, which is exit status 1.
if [ "$(timeout 30 docker wait ims-practice-ue3-rtr 2>/dev/null || echo timeout)" = 1 ] &&
	grep -q 'deregistered by the network, event rejected' <<<"$(docker logs ims-practice-ue3-rtr 2>&1)"; then
	echo "ok   UE 3 ended, deregistered by the network"
else
	echo "FAIL UE 3 after the removal of its subscription: $(docker logs ims-practice-ue3-rtr 2>&1 | tail -n 2)"
	fail=1
fi
docker rm -f ims-practice-ue3-rtr >/dev/null

same_nodes
sleep 1

pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
echo "reading $pcap"

# expect finds the first frame after the previous match that passes the
# filter, so the checks below assert the order of the registration.
last=0

ue=172.28.0.100
ue3ip=172.28.0.103
pcscf=172.28.0.40
icscf=172.28.0.20
scscf=172.28.0.30
hss=172.28.0.10
dns=172.28.0.2
register='sip.Method == "REGISTER"'
uar='diameter.cmd.code == 300'
sar='diameter.cmd.code == 301'
mar='diameter.cmd.code == 303'
rtr='diameter.cmd.code == 304'
req='diameter.flags.request == 1'
ans='diameter.flags.request == 0'

echo "first REGISTER, answered with a challenge"
expect "REGISTER UE -> P-CSCF" "$register && ip.src == $ue && ip.dst == $pcscf"
expect "NAPTR query for the home domain" "dns.qry.type == 35 && dns.flags.response == 0 && ip.dst == $dns"
expect "REGISTER P-CSCF -> I-CSCF with Path" "$register && ip.src == $pcscf && ip.dst == $icscf && sip.Path"
expect "UAR I-CSCF -> HSS" "$uar && $req && ip.src == $icscf && ip.dst == $hss && diameter.Public-Identity"
expect "UAA first registration" "$uar && $ans && ip.src == $hss && diameter.Experimental-Result-Code == 2001"
expect "REGISTER I-CSCF -> S-CSCF" "$register && ip.src == $icscf && ip.dst == $scscf"
# Wireshark names the TS 29.229 AVPs 3GPP-SIP-Authentication-Scheme and
# Cx-User-Data.
expect "MAR S-CSCF -> HSS, scheme left to the HSS" "$mar && $req && ip.src == $scscf && diameter.3GPP-SIP-Authentication-Scheme == \"Unknown\""
expect "MAA with SIP Digest and H(A1)" "$mar && $ans && ip.src == $hss && diameter.Result-Code == 2001 && diameter.3GPP-SIP-Authentication-Scheme == \"SIP Digest\" && diameter.Digest-HA1"
expect "401 S-CSCF -> I-CSCF" "sip.Status-Code == 401 && ip.src == $scscf && sip.WWW-Authenticate"
expect "401 P-CSCF -> UE, without P-Charging-Vector" "sip.Status-Code == 401 && ip.src == $pcscf && ip.dst == $ue && !sip.P-Charging-Vector"

echo "second REGISTER with credentials"
expect "REGISTER UE -> P-CSCF with a response" "$register && ip.src == $ue && sip.Authorization contains \"response=\\\"\" && !(sip.Authorization contains \"response=\\\"\\\"\")"
expect "UAR I-CSCF -> HSS" "$uar && $req && ip.src == $icscf"
expect "UAA subsequent registration with Server-Name" "$uar && $ans && ip.src == $hss && diameter.Experimental-Result-Code == 2002 && diameter.Server-Name"
expect "REGISTER I-CSCF -> S-CSCF" "$register && ip.src == $icscf && ip.dst == $scscf"
expect "SAR REGISTRATION" "$sar && $req && ip.src == $scscf && diameter.Server-Assignment-Type == 1"
expect "SAA with User-Data" "$sar && $ans && ip.src == $hss && diameter.Result-Code == 2001 && diameter.Cx-User-Data"
expect "200 OK S-CSCF -> I-CSCF with Service-Route" "sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ip.src == $scscf && sip.Service-Route"
expect "200 OK P-CSCF -> UE with P-Associated-URI, without P-Charging-Vector" "sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ip.src == $pcscf && ip.dst == $ue && sip.P-Associated-URI && !sip.P-Charging-Vector"
registered="$last"

# The P-CSCF subscribes to the reg event package after the 200 OK (TS
# 24.229 5.2.3). This exchange runs alongside others, so it is checked in
# a pass of its own that starts at the 200 OK.
subscribe='sip.Method == "SUBSCRIBE" && sip.Event == "reg"'
notify='sip.Method == "NOTIFY" && sip.Event == "reg"'
lir='diameter.cmd.code == 302'
echo "reg event subscription of the P-CSCF"
expect "SUBSCRIBE P-CSCF -> I-CSCF asserting the Path URI" "$subscribe && ip.src == $pcscf && ip.dst == $icscf && sip.P-Asserted-Identity contains \"sip:term@\" && sip.Expires == 3761"
expect "LIR I-CSCF -> HSS" "$lir && $req && ip.src == $icscf && ip.dst == $hss"
expect "LIA with Server-Name" "$lir && $ans && ip.src == $hss && diameter.Server-Name"
expect "SUBSCRIBE I-CSCF -> S-CSCF" "$subscribe && ip.src == $icscf && ip.dst == $scscf"
expect "200 OK to the SUBSCRIBE, S-CSCF -> I-CSCF" "sip.Status-Code == 200 && sip.CSeq.method == \"SUBSCRIBE\" && ip.src == $scscf && ip.dst == $icscf"
expect "NOTIFY S-CSCF -> P-CSCF with reginfo, active" "$notify && ip.src == $scscf && ip.dst == $pcscf && sip.Content-Type == \"application/reginfo+xml\" && sip.Subscription-State contains \"active\""
expect "200 OK to the NOTIFY, P-CSCF -> S-CSCF" "sip.Status-Code == 200 && sip.CSeq.method == \"NOTIFY\" && ip.src == $pcscf && ip.dst == $scscf"
last="$registered"

# The UE subscribes along its Service-Route (TS 24.229 5.1.1.3). Its
# NOTIFY carries a Route; the P-CSCF's own does not. Only the S-CSCF
# sends the 200 before the NOTIFY for sure; past the P-CSCF the two may
# swap.
echo "reg event subscription of the UE"
expect "SUBSCRIBE UE -> P-CSCF" "$subscribe && ip.src == $ue && ip.dst == $pcscf && sip.Expires == 600000"
expect "SUBSCRIBE P-CSCF -> S-CSCF, asserted and record-routed" "$subscribe && ip.src == $pcscf && ip.dst == $scscf && sip.P-Asserted-Identity && sip.Record-Route"
expect "200 OK to the SUBSCRIBE, S-CSCF -> P-CSCF" "sip.Status-Code == 200 && sip.CSeq.method == \"SUBSCRIBE\" && ip.src == $scscf && ip.dst == $pcscf"
expect "NOTIFY S-CSCF -> P-CSCF along the UE's route" "$notify && ip.src == $scscf && ip.dst == $pcscf && sip.Route"
expect "NOTIFY P-CSCF -> UE with reginfo, record-routed" "$notify && ip.src == $pcscf && ip.dst == $ue && sip.Content-Type == \"application/reginfo+xml\" && sip.Record-Route && sip.Subscription-State contains \"active\""
expect "200 OK to the NOTIFY, UE -> P-CSCF" "sip.Status-Code == 200 && sip.CSeq.method == \"NOTIFY\" && ip.src == $ue && ip.dst == $pcscf"
last="$registered"

echo "deregistration"
expect "REGISTER Expires 0" "$register && ip.src == $ue && sip.Contact contains \"expires=0\""
expect "UAR DE_REGISTRATION" "$uar && $req && diameter.User-Authorization-Type == 1"
# TS 23.228 5.3.1 steps 7 to 9: the S-CSCF has the SAA before it
# answers 200. This check only asks that the SAR follow the UAR; the
# order of the SAR and the 200 is tested in internal/scscf.
# The NOTIFYs that end the two subscriptions follow the 200 out of the
# S-CSCF in either order. The P-CSCF keeps the ended IP association
# long enough to pass the UE's NOTIFY on (TS 24.229 5.2.5.1 item 2), and
# the UE waits a second to answer it.
mark="$last"
expect "200 OK S-CSCF -> I-CSCF with expires=0" "sip.Status-Code == 200 && ip.src == $scscf && sip.Contact contains \"expires=0\""
after_200="$last"
expect "NOTIFY S-CSCF -> P-CSCF, the P-CSCF's subscription terminated" "$notify && ip.src == $scscf && ip.dst == $pcscf && !sip.Route && sip.Subscription-State contains \"terminated\""
last="$after_200"
expect "NOTIFY S-CSCF -> P-CSCF, the UE's subscription terminated" "$notify && ip.src == $scscf && ip.dst == $pcscf && sip.Route && sip.Subscription-State contains \"terminated\""
expect "NOTIFY P-CSCF -> UE, terminated" "$notify && ip.src == $pcscf && ip.dst == $ue && sip.Subscription-State contains \"terminated\""
expect "200 OK to the last NOTIFY, UE -> P-CSCF" "sip.Status-Code == 200 && sip.CSeq.method == \"NOTIFY\" && ip.src == $ue && ip.dst == $pcscf"
last="$mark"
expect "SAR USER_DEREGISTRATION" "$sar && $req && diameter.Server-Assignment-Type == 5"

# The UE with the wrong password starts once the first UE has ended,
# which is after the 200 to its deregistration, and so after the SAR
# above. The NOTIFYs that end the subscriptions need not come before:
# the first UE waits for its own only a second.
echo "wrong password"
expect "SAR AUTHENTICATION_FAILURE" "$sar && $req && diameter.Server-Assignment-Type == 9"
expect "403 to the UE with the wrong password" "sip.Status-Code == 403 && ip.dst == $ue"

# UE 3 registered after the UE with the wrong password had ended, and
# its subscription was removed from the HSS after that, so this flow is
# the last of the capture.
echo "deregistration by the HSS"
rejected="sip.msg_body contains \"event=\\\"rejected\\\"\""
expect "200 OK to the REGISTER of UE 3" "sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ip.dst == $ue3ip"
# TS 29.228 6.1.3.1: the private identity goes with all of its public
# identities, so the RTR carries no Public-Identity, and
# Destination-Host is the Origin-Host of that S-CSCF's SAR.
expect "RTR HSS -> S-CSCF, PERMANENT_TERMINATION without a Public-Identity" "$rtr && $req && ip.src == $hss && diameter.Destination-Host == \"scscf.$domain\" && diameter.User-Name == \"001010000000003@$domain\" && diameter.Reason-Code == 0 && !diameter.Public-Identity"
removed="$last"
# The S-CSCF sends the NOTIFY of every subscription to the registration
# before it answers the RTR, and the subscribers answer in their own
# time, so each branch starts at the RTR.
mark
expect "NOTIFY S-CSCF -> P-CSCF, the P-CSCF's subscription, contact rejected" "$notify && ip.src == $scscf && ip.dst == $pcscf && !sip.Route && sip.Subscription-State contains \"terminated\" && $rejected"
back
expect "NOTIFY S-CSCF -> P-CSCF along UE 3's route, contact rejected" "$notify && ip.src == $scscf && ip.dst == $pcscf && sip.Route && sip.Subscription-State contains \"terminated\" && $rejected"
expect "NOTIFY P-CSCF -> UE 3, contact rejected" "$notify && ip.src == $pcscf && ip.dst == $ue3ip && $rejected"
back
expect "RTA DIAMETER_SUCCESS" "$rtr && $ans && ip.src == $scscf && diameter.Result-Code == 2001"
# TS 29.228 A.4.4.2: the HSS has deregistered the user already, so the
# S-CSCF tells it nothing more.
none "SAR of the S-CSCF for the removed subscription" "frame.number > $removed && $sar && $req && diameter.User-Name == \"001010000000003@$domain\""

echo "request without an IP association"
some_from_ue="$(tshark_fields "sip.Call-ID == \"$unregistered_call_id\" && ip.src == $ue && ip.dst == $pcscf" | grep -c . || true)"
if [ "$some_from_ue" -ge 2 ]; then
	echo "ok   the MESSAGE and its retransmission reached the P-CSCF ($some_from_ue frames)"
else
	echo "FAIL the MESSAGE from the deregistered UE: $some_from_ue frames at the P-CSCF, want 2"
	fail=1
fi
none "messages of the P-CSCF for the discarded MESSAGE" "sip.Call-ID == \"$unregistered_call_id\" && ip.src == $pcscf"
# The charging information stays in the network (TS 24.229 5.2.1):
# the P-CSCF sends none of it to a UE. Every UE of this setup has an
# address from 172.28.0.100 up.
none "P-Charging-Vector or P-Charging-Function-Addresses from the P-CSCF to a UE" 'ip.src == 172.28.0.40 && ip.dst >= 172.28.0.100 && (sip.P-Charging-Vector || sip.P-Charging-Function-Addresses)'
none "malformed SIP, DNS or Diameter packets" '(sip || dns || diameter) && (_ws.malformed || _ws.expert.severity >= warning)'

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color | tail -n 80
fi
exit "$fail"
