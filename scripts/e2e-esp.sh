#!/usr/bin/env bash
# End-to-end check: IPsec ESP with null encryption on the signalling of
# IMS AKA.
#
# The deployment is the one of compose.yaml with deploy/esp/esp.yaml on
# top: the P-CSCF and the two VoLTE UEs bind a protected client port and
# put the security associations of each authenticated registration into
# the kernel. volte12 registers and waits for a call, volte10 registers
# and calls it, and both deregister when they stop.
#
# The checks read the capture of the bridge, where the packets are
# already protected: ESP is applied between the UE's and the P-CSCF's
# protected ports, so nothing of the SIP between them crosses the bridge
# in the clear. They also read the kernel's own tables in the P-CSCF's
# container, which is where the associations live.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
export MSYS_NO_PATHCONV=1
export COMPOSE_FILE=compose.yaml:deploy/esp/esp.yaml COMPOSE_PATH_SEPARATOR=:
if docker buildx inspect ims-practice >/dev/null 2>&1; then
	export BUILDX_BUILDER=ims-practice
fi

docker compose up -d --build

wait_for_open() {
	local service="$1" i
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
wait_for_open pcscf
remember_nodes
pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"

domain=ims.mnc001.mcc001.3gppnetwork.org
epc_api=http://172.28.0.80:8080
pcscf_ip=172.28.0.40
volte10_ip=172.28.0.120
volte12_ip=172.28.0.121
# The protected ports of deploy/esp: port_ps and port_pc of the P-CSCF,
# port_us and port_uc of each UE (TS 33.203 7.1).
port_ps=5064
port_pc=5065
port_us=5062
port_uc=5063
# "docker compose run" replaces the command of the service, so the UEs
# started here take -port-c of deploy/esp/esp.yaml again.
va=(-epc "$epc_api" -impi "001010000000010@$domain" -impu "sip:001010000000010@$domain"
	-k 465b5ce8b199b49faa5f0a2ee238a6bc -opc cd63cb71954a9f4e48a5994e37a02baf -port-c "$port_uc")
vb=(-epc "$epc_api" -impi "001010000000012@$domain" -impu "sip:001010000000012@$domain"
	-k fec86ba6eb707ed08905757b1bb44b8f -opc 1006020f0a478bf6b699f15c062e42b3 -port-c "$port_uc")
fail=0
mkdir -p e2e-out

# Without xfrm in the kernel that the containers share, no node can set
# an association up, and every check below would fail for that one
# reason. Say so instead. A kernel that lists the associations may still
# refuse to add one; the P-CSCF then says so in its log and answers the
# REGISTER 500, which the check below reports.
if ! out="$(docker compose exec -T pcscf ip xfrm state 2>&1)"; then
	echo "FAIL this machine's kernel has no xfrm, so ESP cannot be set up: $out"
	echo "     (a container shares the kernel of the machine that runs the engine)"
	docker compose down --rmi local -v --remove-orphans >/dev/null 2>&1 || true
	exit 1
fi

# no_associations: the kernel of the P-CSCF's container holds none. A
# command for wait_for, which runs it again and again, so the kernel is
# read each time.
no_associations() { [ -z "$(docker compose exec -T pcscf ip xfrm state | tr -d '\r')" ]; }

echo "a VoLTE call over ESP: volte10 calls volte12"
docker compose run --no-deps -d --name ims-practice-esp-volte12 volte12 answer "${vb[@]}" \
	--ring 1s --tone 3s >/dev/null
if ! wait_log ims-practice-esp-volte12 '"msg":"waiting for a call"'; then
	echo "FAIL volte12 did not attach and register with ESP"
	docker logs ims-practice-esp-volte12 2>&1 | tail -n 10
	# A kernel that refuses the associations shows here: the P-CSCF
	# answers the REGISTER 500 and its log says what ip said. grep ends
	# with 1 when it selects no line, which is no reason to stop.
	docker compose logs --no-color pcscf | { grep -E '"level":"(WARN|ERROR)"' || true; } | tail -n 5
	fail=1
fi

# While a UE is registered, the P-CSCF holds the four security
# associations of TS 33.203 7.1 and, for each of them, one policy for
# UDP and one for TCP, in each direction.
states="$(docker compose exec -T pcscf ip xfrm state | tr -d '\r')"
policies="$(docker compose exec -T pcscf ip xfrm policy | tr -d '\r')"
printf '%s\n%s\n' "$states" "$policies" >e2e-out/esp-xfrm.txt
n="$(grep -c '^src ' <<<"$states" || true)"
if [ "$n" = 4 ]; then
	echo "ok   the P-CSCF holds the four security associations of the registration"
else
	echo "FAIL the P-CSCF holds $n security associations, want 4:"
	sed 's/^/     /' <<<"$states"
	fail=1
fi
if grep -q "transport" <<<"$states" && grep -q "auth-trunc hmac(sha256)" <<<"$states" &&
	grep -q "enc ecb(cipher_null)" <<<"$states"; then
	echo "ok   they are ESP in transport mode, HMAC-SHA2-256-128 with null encryption"
else
	echo "FAIL the security associations are not what the agreement says:"
	sed 's/^/     /' <<<"$states"
	fail=1
fi
outbound="$(grep -c "dir out" <<<"$policies" || true)"
inbound="$(grep -c "dir in" <<<"$policies" || true)"
if [ "$outbound" = 4 ] && [ "$inbound" = 4 ]; then
	echo "ok   the P-CSCF holds a policy for UDP and one for TCP in each direction"
else
	echo "FAIL the P-CSCF holds $outbound outbound and $inbound inbound policies, want 4 and 4:"
	sed 's/^/     /' <<<"$policies"
	fail=1
fi

out="$(docker compose run --no-deps --rm volte10 call "${va[@]}" --tone 2s tel:+12025550112 2>&1 || true)"
printf '%s\n' "$out" >e2e-out/esp-volte10-call.log
if grep -q '"msg":"call answered"' <<<"$out" && [ "$(ended ims-practice-esp-volte12)" = 0 ]; then
	echo "ok   volte10 called volte12 over ESP, and volte12 answered"
else
	echo "FAIL the call over ESP: $(grep -E '"msg":"(call progress|ue failed|media)"' <<<"$out" | tail -n 3)"
	docker logs ims-practice-esp-volte12 2>&1 | tail -n 10 || true
	fail=1
fi

# Both UEs have deregistered, which takes the associations out of the
# kernel as well as out of the SA table (TS 33.203 7.4).
if wait_for 30 no_associations; then
	echo "ok   the P-CSCF kept no security association after the deregistrations"
else
	echo "FAIL the P-CSCF still holds security associations:"
	docker compose exec -T pcscf ip xfrm state | sed 's/^/     /'
	fail=1
fi

same_nodes

# The checks of the capture. tshark reads the SIP inside ESP when its
# settings name the integrity algorithm, which gives it the length of
# the integrity check value; nothing is encrypted, so it needs no key.
# The heuristic of esp.enable_null_encryption_decode_heuristic assumes
# the 12 octets of HMAC-SHA-1-96 and cannot read the 16 of
# HMAC-SHA2-256-128, so this names the algorithm instead, for every SA
# of the capture.
esp_sa=(-o esp.enable_encryption_decode:TRUE
	-o 'uat:esp_sa:"IPv4","*","*","*","NULL","","HMAC-SHA-256-128 [RFC4868]",""')
ue_to_pcscf="ip.addr == $pcscf_ip && (ip.addr == $volte10_ip || ip.addr == $volte12_ip)"
protected="($ue_to_pcscf) && (udp.port == $port_ps || udp.port == $port_pc || udp.port == $port_us || udp.port == $port_uc || tcp.port == $port_ps || tcp.port == $port_pc || tcp.port == $port_us || tcp.port == $port_uc)"

await "the ESP of the registrations and the call" "esp && ($ue_to_pcscf)" 20
echo "ok   $(count "esp && ($ue_to_pcscf)") ESP packets between the VoLTE UEs and the P-CSCF"

# Nothing of the SIP that the security associations cover crosses the
# bridge in the clear. Without the settings above, a SIP frame here is a
# frame that tshark read without ESP.
none "SIP in the clear on a protected port" "sip && ($protected)"
none "SIP in the clear between a UE and the P-CSCF outside port 5060" \
	"sip && ($ue_to_pcscf) && !(udp.port == 5060)"

# The unprotected port carries what TS 33.203 7.1 items 3 and 4 allow
# there and nothing else: the first REGISTER of each UE and its 401.
expect_frames() {
	local label="$1" filter="$2" min="$3" n
	n="$(count "$filter")"
	if [ "$n" -ge "$min" ]; then
		echo "ok   $label: $n frames"
	else
		echo "FAIL $label: $n frames, want $min or more"
		fail=1
	fi
}
expect_frames "the first REGISTER of each UE, unprotected on port 5060" \
	"sip.Method == \"REGISTER\" && udp.port == 5060 && ($ue_to_pcscf)" 2

# With the ESP settings, the same ESP packets show the SIP inside them.
tshark_options=("${esp_sa[@]}")
expect_frames "REGISTER inside ESP" "esp && sip.Method == \"REGISTER\" && ($protected)" 2
expect_frames "INVITE inside ESP" "esp && sip.Method == \"INVITE\" && ($protected)" 1
expect_frames "the 200 (OK) of the call inside ESP" \
	"esp && sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ($protected)" 1
expect_frames "BYE inside ESP" "esp && sip.Method == \"BYE\" && ($protected)" 1
# Each pair of security associations carries the requests of one end
# from its protected client port to the other end's protected server
# port (TS 33.203 7.1 rule 1).
expect_frames "requests of a UE from its protected client port" \
	"esp && sip.Method && ((udp.srcport == $port_uc && udp.dstport == $port_ps) || (tcp.srcport == $port_uc && tcp.dstport == $port_ps))" 1
expect_frames "requests of the P-CSCF from its protected client port" \
	"esp && sip.Method && ((udp.srcport == $port_pc && udp.dstport == $port_us) || (tcp.srcport == $port_pc && tcp.dstport == $port_us))" 1
tshark_options=()

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color | { grep -E '"level":"(WARN|ERROR)"' || true; } | tail -n 40
fi
exit "$fail"
