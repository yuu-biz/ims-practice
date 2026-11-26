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
# nc may end with an error when nothing comes back within its 2 s, which
# is the outcome wanted here, hence "|| true"; the check reads what it
# printed.
reply="$(docker compose run --no-deps --rm -T --entrypoint sh ue -c \
	'for i in 1 2; do printf "$1" | nc -u -p 5060 -w 2 172.28.0.40 5060; done' sh "$unregistered_message" || true)"
if [ -z "$reply" ]; then
	echo "ok   a MESSAGE from the deregistered UE got no answer"
else
	echo "FAIL a MESSAGE from the deregistered UE got an answer: $(head -n 1 <<<"$reply")"
	fail=1
fi

# pushes_to_ue3 counts the user profiles that the HSS has pushed to an
# S-CSCF for UE 3: it logs one line per PPR that was answered.
pushes_to_ue3() {
	grep -c "pushed the user profile.*001010000000003@$domain" \
		<<<"$(docker compose logs --no-color hss 2>/dev/null)" || true
}
pushed_to_ue3() { [ "$(pushes_to_ue3)" -ge "$1" ]; }

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
if ! wait_log ims-practice-ue3-rtr '"msg":"reg event"'; then
	fail=1
else
	# A change of the setting that names the service profile of every
	# subscription reaches the users registered at that moment, and not
	# only the one subscription that a provisioning request names (TS
	# 29.228 6.6.1). UE 3 is the only user registered now and names no
	# service_profile of its own, so the default setting decides for it:
	# these two changes give it the criteria of no-services and then the
	# ones it had back, with one PPR each. The maintenance API is not
	# published on the host either, so the requests go from inside the
	# container of the HSS, on the port of its maintenance listener.
	for profile in no-services default; do
		record="$(docker compose exec -T hss sh -c '
			printf "PATCH /maintenance/v1/config HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#1}\r\n\r\n%s" "$1" |
				nc -w 5 127.0.0.1 8085' sh "{\"settings\": {\"hss.default_service_profile\": \"$profile\"}, \"operator\": \"e2e\"}")" || true
		if grep -q "\"name\":\"hss.default_service_profile\",\"previous\":\"[a-z-]*\",\"value\":\"$profile\"" <<<"$record"; then
			echo "ok   the operator named the service profile $profile for every subscription"
		else
			echo "FAIL naming the service profile $profile for every subscription: $(first_line <<<"$record")"
			fail=1
		fi
	done
	# The pushes follow the answer to the maintenance request, so both of
	# them are waited for here: the PPR of the one subscription below
	# comes after them in the capture, which is the order the checks of
	# the capture read.
	if wait_for 30 pushed_to_ue3 2; then
		echo "ok   the HSS pushed the profile of UE 3 for both changes of the setting"
	else
		echo "FAIL the HSS pushed the profile of UE 3 $(pushes_to_ue3) times within 30 s, want 2"
		fail=1
	fi

	# Giving a registered user another service profile pushes the new
	# profile to its S-CSCF with a PPR, instead of leaving the S-CSCF with
	# the profile of the registration (TS 29.228 6.6.1). The HSS answers
	# the request only once the PPA is in. The profile no-services has no
	# criteria, so from here on the S-CSCF calls no application server for
	# UE 3. The provisioning API is not published on the host, so the
	# request goes from inside the container of the HSS; the Lab passes
	# on no change of a subscription at all.
	reply="$(docker compose exec -T hss sh -c '
		printf "PATCH /subscriptions/$1 HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#2}\r\n\r\n%s" "$2" |
			nc -w 5 127.0.0.1 8081' sh "001010000000003@$domain" '{"service_profile": "no-services"}')" || true
	if grep -qi '^HTTP/1\.[01] 200' <<<"$reply"; then
		echo "ok   the HSS took the new service profile of UE 3"
	else
		echo "FAIL changing the service profile of UE 3: $(head -n 1 <<<"${reply:-no answer}")"
		fail=1
	fi
	# The HSS answers the DELETE only once the RTR is over, so there is
	# nothing else to wait for. BusyBox wget cannot send a DELETE
	# either, hence this request written out to nc as well.
	reply="$(docker compose exec -T hss sh -c '
		printf "DELETE /subscriptions/$1 HTTP/1.0\r\n\r\n" |
			nc -w 5 127.0.0.1 8081' sh "001010000000003@$domain")" || true
	if grep -qi '^HTTP/1\.[01] 204' <<<"$reply"; then
		echo "ok   the HSS removed the subscription of UE 3"
	else
		echo "FAIL removing the subscription of UE 3: $(head -n 1 <<<"${reply:-no answer}")"
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

# The S-CSCF counted the PPRs and the RTR of the scene above: each on
# its receipt (TS 32.409 4.1.1.3.2.1, 4.1.7.1) and the answer it sent
# back with the result that answer carried (4.1.1.3.2.2, 4.1.7.2). The
# counters are on the maintenance listener of the node, which serves
# /metrics whether or not a Prometheus scrapes it, so this scenario
# reads them without one. How many PPRs there were depends on who was
# registered when the setting changed, so each series is only asked to
# have counted something. Under "set -e" and pipefail, a command
# substitution that fails ends the script on the spot, before any FAIL
# line says why; hence the "|| true" on the reads here and above, whose
# checks then report what is missing.
scscf_metrics="$(docker compose exec -T scscf wget -qO- http://127.0.0.1:8085/metrics | tr -d '\r')" || true
for series in 'ims_diameter_requests_total{role="scscf",direction="received",command="PPR"}' \
	'ims_diameter_answers_total{role="scscf",direction="sent",command="PPR",result="DIAMETER_SUCCESS"}' \
	'ims_diameter_requests_total{role="scscf",direction="received",command="RTR"}' \
	'ims_diameter_answers_total{role="scscf",direction="sent",command="RTR",result="DIAMETER_SUCCESS"}'; do
	# The value is the last field of the sample line, and a counter that
	# has counted nothing has no line at all.
	line="$(grep -F "$series " <<<"$scscf_metrics" | tail -n 1)" || true
	case "${line##* }" in
	"" | 0)
		echo "FAIL the S-CSCF counted no $series: ${line:-no sample line}"
		fail=1
		;;
	*)
		echo "ok   the S-CSCF has $series = ${line##* }"
		;;
	esac
done

# A UE on the host, which reaches the P-CSCF through Docker's NAT, as
# "Register from your own machine through NAT" in the guide shows.
host_registered=
if host_ue; then
	if "$host_ue_bin" register -pcscf 127.0.0.1:5060 -listen 127.0.0.1:0 -deregister-after 2s; then
		echo "ok   a UE on the host registered and deregistered through the NAT"
		host_registered=yes
	else
		echo "FAIL the UE on the host"
		fail=1
	fi
fi

same_nodes

pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')" || true
if [ -z "$pcap" ]; then
	echo "FAIL the capture service holds no capture file to read"
	docker compose logs --no-color capture | tail -n 20
	exit 1
fi
echo "reading $pcap"

# expect finds the first frame after the previous match that passes the
# filter, so the checks below assert the order of the registration.
last=0

ue=172.28.0.100
ue3ip=172.28.0.103
# The gateway of the compose bridge, from which a UE on the host arrives.
gateway=172.28.0.1
pcscf=172.28.0.40
icscf=172.28.0.20
scscf=172.28.0.30
hss=172.28.0.10
ipsmgw=172.28.0.70
dns=172.28.0.2
register='sip.Method == "REGISTER"'
uar='diameter.cmd.code == 300'
sar='diameter.cmd.code == 301'
mar='diameter.cmd.code == 303'
rtr='diameter.cmd.code == 304'
ppr='diameter.cmd.code == 305'
req='diameter.flags.request == 1'
ans='diameter.flags.request == 0'

# The checks read the capture up to the RTA and the NOTIFY that ends UE
# 3's registration, and, when a UE ran on the host, up to its answer to
# the NOTIFY of its registration.
await "the RTA of the S-CSCF" "$rtr && $ans"
await "the NOTIFY that rejects UE 3's contact" "sip.Method == \"NOTIFY\" && ip.src == $pcscf && ip.dst == $ue3ip && sip.msg_body contains \"event=\\\"rejected\\\"\""
if [ -n "$host_registered" ]; then
	await "the host's 200 OK to a NOTIFY" "sip.Status-Code == 200 && sip.CSeq.method == \"NOTIFY\" && ip.src == $gateway"
fi

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

# UE 3 registered after the UE with the wrong password had ended, its
# service profile changed while it was registered, and its subscription
# was removed from the HSS after that, so these flows are the last of the
# capture.
echo "a changed service profile, pushed to the S-CSCF"
rejected="sip.msg_body contains \"event=\\\"rejected\\\"\""
expect "200 OK to the REGISTER of UE 3" "sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ip.dst == $ue3ip"
# The two changes of the setting came first, each with a PPR of its own:
# the HSS reads the users that the setting decides for from its index of
# the users whose profile an S-CSCF holds, and pushes to each of them.
# Each change has a round of PPRs that carries its profile, and the
# round of the second change follows that of the first, so these read
# them as two pushes of this user's profile in that order.
for setting_change in first second; do
	expect "PPR of the $setting_change change of the setting, HSS -> S-CSCF" \
		"$ppr && $req && ip.src == $hss && diameter.Destination-Host == \"scscf.$domain\" && diameter.User-Name == \"001010000000003@$domain\" && diameter.Cx-User-Data"
	expect "its PPA DIAMETER_SUCCESS" "$ppr && $ans && ip.src == $scscf && diameter.Result-Code == 2001"
done
# TS 29.228 Table 6.2.2.1: User-Name is the private identity,
# Destination-Host the S-CSCF that stored the server name, and User-Data
# the whole profile of the implicit registration set (6.5.2.1).
expect "PPR HSS -> S-CSCF with the whole profile" "$ppr && $req && ip.src == $hss && diameter.Destination-Host == \"scscf.$domain\" && diameter.User-Name == \"001010000000003@$domain\" && diameter.Cx-User-Data"
pushed="$last"
expect "PPA DIAMETER_SUCCESS" "$ppr && $ans && ip.src == $scscf && diameter.Result-Code == 2001"
# The profile that arrived has no criteria, so the S-CSCF calls no
# application server for UE 3 any more: the deregistration below sends no
# third-party REGISTER to the IP-SM-GW, which the registration did.
third_party="$register && ip.dst == $ipsmgw && sip.To contains \"+12025550103\""
n="$(count "frame.number < $pushed && $third_party")" || exit 2
if [ "$n" -ge 1 ]; then
	echo "ok   third-party REGISTER for UE 3 before the push: $n"
else
	echo "FAIL third-party REGISTER for UE 3 before the push: none, want the one of its registration"
	fail=1
fi
none "third-party REGISTER for UE 3 after the push" "frame.number > $pushed && $third_party"

echo "deregistration by the HSS"
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

# The UE on the host registered last of all, so these frames are the
# last of the capture too.
if [ -n "$host_registered" ]; then
	echo "registration from the host through the NAT"
	expect "REGISTER from the bridge gateway, its Via naming the host's own address" "$register && ip.src == $gateway && ip.dst == $pcscf && sip.Via.sent-by.address == \"127.0.0.1\""
	# RFC 3581 4: the UE asked for rport, so the P-CSCF writes the
	# address and port the request really came from into the UE's Via.
	expect "REGISTER P-CSCF -> I-CSCF, received and rport in the UE's Via" "$register && ip.src == $pcscf && ip.dst == $icscf && sip.Via.sent-by.address == \"127.0.0.1\" && sip.Via.received == \"$gateway\""
	expect "401 to the UE on the host" "sip.Status-Code == 401 && ip.src == $pcscf && ip.dst == $gateway && sip.WWW-Authenticate"
	expect "200 OK to the UE on the host, with P-Associated-URI" "sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ip.src == $pcscf && ip.dst == $gateway && sip.P-Associated-URI"
	# The requests of the network find their way back through the NAT as
	# well: the P-CSCF sends the NOTIFY of the UE's own subscription to
	# the address the REGISTER came from, not to the Contact.
	expect "NOTIFY P-CSCF -> the UE on the host" "$notify && ip.src == $pcscf && ip.dst == $gateway && sip.Content-Type == \"application/reginfo+xml\""
	expect "200 OK to that NOTIFY from the host" "sip.Status-Code == 200 && sip.CSeq.method == \"NOTIFY\" && ip.src == $gateway && ip.dst == $pcscf"
fi

echo "request without an IP association"
some_from_ue="$(count "sip.Call-ID == \"$unregistered_call_id\" && ip.src == $ue && ip.dst == $pcscf")"
if [ "$some_from_ue" -ge 2 ]; then
	echo "ok   the MESSAGE and its retransmission reached the P-CSCF ($some_from_ue frames)"
else
	echo "FAIL the MESSAGE from the deregistered UE: $some_from_ue frames at the P-CSCF, want 2"
	fail=1
fi
none "messages of the P-CSCF for the discarded MESSAGE" "sip.Call-ID == \"$unregistered_call_id\" && ip.src == $pcscf"
# The charging information stays in the network (TS 24.229 5.2.1):
# the P-CSCF sends none of it to a UE. Every UE in a container of this
# setup has an address from 172.28.0.100 up, and a UE on the host comes
# from the gateway of the bridge.
none "P-Charging-Vector or P-Charging-Function-Addresses from the P-CSCF to a UE" "ip.src == $pcscf && (ip.dst >= 172.28.0.100 || ip.dst == $gateway) && (sip.P-Charging-Vector || sip.P-Charging-Function-Addresses)"
none "malformed SIP, DNS or Diameter packets" '(sip || dns || diameter) && (_ws.malformed || _ws.expert.severity >= warning)'

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color | tail -n 80
fi
exit "$fail"
