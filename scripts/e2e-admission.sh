#!/usr/bin/env bash
# End-to-end check: the admission policy of the HSS. With
# hss.admission set to test_users, the HSS refuses the UAR of a
# subscriber that is not marked test_user with
# DIAMETER_AUTHORIZATION_REJECTED, and the I-CSCF turns that negative
# UAA into 403 (TS 29.228 6.1.1.1 step 5, TS 24.229 5.3.1.3). A
# subscriber that the provisioning API marks registers as before. The
# checks read the capture.
#
# The deployment is the one of compose.yaml, which starts on the policy
# all, and the script narrows the policy on the maintenance API of the
# HSS node, as an operator does through the OAM. That is also what the
# registration guide shows, and it needs no second copy of the
# configuration and no restart: an HSS that was recreated would have
# lost its Diameter connections and the marks that the provisioning API
# had set.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
export MSYS_NO_PATHCONV=1
# Use the project's own builder when it exists, so the build
# cache stays separate from other projects. CI has no such builder.
if docker buildx inspect ims-practice >/dev/null 2>&1; then
	export BUILDX_BUILDER=ims-practice
fi

domain=ims.mnc001.mcc001.3gppnetwork.org

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

# The operator narrows the policy to the test users. The maintenance
# API is not published on the host, so the request goes from inside the
# container of the node, on the port of its maintenance listener. The
# answer carries the record of the change, with the policy before and
# after: a request that named a setting the HSS does not have would be
# refused, and the scenario would otherwise run on the policy all and
# pass every check below for the wrong reason.
change="$(docker compose exec -T hss sh -c '
	printf "PATCH /maintenance/v1/config HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#1}\r\n\r\n%s" "$1" |
		nc -w 5 127.0.0.1 8085' sh '{"settings": {"hss.admission": "test_users"}, "operator": "e2e"}')" || true
if grep -q '"name":"hss.admission","previous":"all","value":"test_users"' <<<"$change"; then
	echo "ok   the operator narrowed the policy to the test users"
else
	echo "FAIL narrowing the admission policy: $(first_line <<<"$change")"
	docker compose logs --no-color hss | tail -n 20 >&2
	exit 1
fi

# No subscription of the file is marked, so every subscriber is outside
# the policy now.
if docker compose run --no-deps --rm ue register --deregister-after 2s 2>/dev/null; then
	echo "FAIL a subscriber that is not a test user registered"
	fail=1
else
	echo "ok   a subscriber that is not a test user was refused"
fi

# The operator marks subscriber 2 as a test user. The provisioning API
# is not published on the host, so the request goes from inside the
# container of the HSS; BusyBox wget cannot send a PATCH, hence the
# request written out to nc.
reply="$(docker compose exec -T hss sh -c '
	printf "PATCH /subscriptions/$1 HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#2}\r\n\r\n%s" "$2" |
		nc -w 5 127.0.0.1 8081' sh "001010000000002@$domain" '{"test_user": true}')" || true
if grep -qi '^HTTP/1\.[01] 200' <<<"$reply"; then
	echo "ok   the HSS marked subscriber 2 as a test user"
else
	echo "FAIL marking subscriber 2 as a test user: $(first_line <<<"$reply")"
	fail=1
fi

ue2=(--impi "001010000000002@$domain" --impu "sip:+12025550102@$domain" --password test-password-2)
if docker compose run --no-deps --rm ue2 register "${ue2[@]}" --deregister-after 2s; then
	echo "ok   the test user registered and deregistered"
else
	echo "FAIL the test user did not register"
	fail=1
fi

# Widen the policy again, before the checks that may stop the script, so
# that a deployment left running here serves every subscriber as the rest
# of the guides describe it. The mark on subscriber 2 stays: with the
# policy all it decides nothing, since both subscriptions take the same
# service profile.
widened="$(docker compose exec -T hss sh -c '
	printf "PATCH /maintenance/v1/config HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#1}\r\n\r\n%s" "$1" |
		nc -w 5 127.0.0.1 8085' sh '{"settings": {"hss.admission": "all"}, "operator": "e2e"}')" || true
if grep -q '"name":"hss.admission","previous":"test_users","value":"all"' <<<"$widened"; then
	echo "ok   the operator widened the policy again"
else
	echo "FAIL widening the admission policy again: $(first_line <<<"$widened")"
	fail=1
fi

same_nodes

pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
echo "reading $pcap"

last=0
ue=172.28.0.100
ue2ip=172.28.0.101
pcscf=172.28.0.40
icscf=172.28.0.20
scscf=172.28.0.30
hss=172.28.0.10
register='sip.Method == "REGISTER"'
uar='diameter.cmd.code == 300'
mar='diameter.cmd.code == 303'
sar='diameter.cmd.code == 301'
req='diameter.flags.request == 1'
ans='diameter.flags.request == 0'

# The test user deregistered last, so its SAR of USER_DEREGISTRATION is
# the last frame these checks read.
await "the SAR of the test user's deregistration" "$sar && $req && diameter.Server-Assignment-Type == 5"

echo "the subscriber that the policy does not admit"
expect "REGISTER UE -> P-CSCF" "$register && ip.src == $ue && ip.dst == $pcscf"
expect "REGISTER P-CSCF -> I-CSCF" "$register && ip.src == $pcscf && ip.dst == $icscf"
# TS 29.229 6.3.24: REGISTRATION is User-Authorization-Type 0, and the
# default value when the AVP is absent.
expect "UAR I-CSCF -> HSS for a registration" "$uar && $req && ip.src == $icscf && ip.dst == $hss && diameter.User-Authorization-Type == 0"
# DIAMETER_AUTHORIZATION_REJECTED belongs to the base protocol (RFC 6733
# 7.1.5), so it comes in Result-Code, and the answer carries neither an
# S-CSCF name nor its capabilities.
expect "UAA DIAMETER_AUTHORIZATION_REJECTED, without a Server-Name or capabilities" \
	"$uar && $ans && ip.src == $hss && ip.dst == $icscf && diameter.Result-Code == 5003 && !diameter.Server-Name && !diameter.Server-Capabilities"
expect "403 I-CSCF -> P-CSCF" "sip.Status-Code == 403 && sip.CSeq.method == \"REGISTER\" && ip.src == $icscf && ip.dst == $pcscf"
expect "403 P-CSCF -> UE" "sip.Status-Code == 403 && sip.CSeq.method == \"REGISTER\" && ip.src == $pcscf && ip.dst == $ue"
# The I-CSCF answered the REGISTER itself, so the S-CSCF never saw it:
# no MAR and no SAR went out for this subscriber, and the UE was never
# challenged. The REGISTER of subscriber 1 names its default public
# identity in To (TS 24.229 5.1.1.2.1).
none "REGISTER I-CSCF -> S-CSCF for the refused subscriber" "$register && ip.src == $icscf && ip.dst == $scscf && sip.To contains \"+12025550101\""
none "MAR for the refused subscriber" "$mar && $req && diameter.User-Name == \"001010000000001@$domain\""
none "SAR for the refused subscriber" "$sar && $req && diameter.User-Name == \"001010000000001@$domain\""
none "401 to the refused subscriber" "sip.Status-Code == 401 && ip.dst == $ue"

# The test user registered after the PATCH, which came after the refusal
# above, so its flows follow in the capture.
echo "the test user"
expect "REGISTER UE 2 -> P-CSCF" "$register && ip.src == $ue2ip && ip.dst == $pcscf"
expect "UAA DIAMETER_FIRST_REGISTRATION with the capabilities" "$uar && $ans && ip.src == $hss && diameter.Experimental-Result-Code == 2001 && diameter.Mandatory-Capability == 1"
# The same filters as the checks of the refused subscriber, which find
# the frames of this one: they would find nothing if the To or the
# User-Name were written otherwise.
expect "REGISTER I-CSCF -> S-CSCF for the test user" "$register && ip.src == $icscf && ip.dst == $scscf && sip.To contains \"+12025550102\""
expect "MAR for the test user" "$mar && $req && diameter.User-Name == \"001010000000002@$domain\""
expect "MAA with SIP Digest and H(A1)" "$mar && $ans && ip.src == $hss && diameter.Result-Code == 2001 && diameter.Digest-HA1"
expect "SAR REGISTRATION" "$sar && $req && ip.src == $scscf && diameter.Server-Assignment-Type == 1"
expect "SAA with User-Data" "$sar && $ans && ip.src == $hss && diameter.Result-Code == 2001 && diameter.Cx-User-Data"
expect "200 OK P-CSCF -> UE 2 with P-Associated-URI" "sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ip.src == $pcscf && ip.dst == $ue2ip && sip.P-Associated-URI"
none "403 to the test user" "sip.Status-Code == 403 && ip.dst == $ue2ip"
none "malformed SIP, DNS or Diameter packets" '(sip || dns || diameter) && (_ws.malformed || _ws.expert.severity >= warning)'

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color | tail -n 80
fi
exit "$fail"
