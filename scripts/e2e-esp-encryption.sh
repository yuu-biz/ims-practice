#!/usr/bin/env bash
# End-to-end check: IPsec ESP with encryption on the signalling of IMS
# AKA, and the keys that let a reader of the capture see the SIP again.
#
# The deployment is the one of scripts/e2e-esp.sh with
# deploy/esp/encryption.yaml on top: the P-CSCF applies the
# confidentiality policy of TS 33.203 5.1.3 item 2, so the two ends
# agree on AES-CBC in place of null encryption, and it appends the keys
# of every set of security associations to /captures/esp_sa as rows of
# the ESP SAs table of Wireshark. volte12 registers and waits for a
# call, volte10 registers and calls it, and both deregister when they
# stop.
#
# The checks read the kernel's own tables in the P-CSCF's container and
# the capture of the bridge, three times over: with no ESP settings at
# all, with the settings that read the null encryption of
# scripts/e2e-esp.sh, and with the rows of /captures/esp_sa. The first
# two must find no SIP between the protected ports; the third must find
# the REGISTERs and the call.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
export MSYS_NO_PATHCONV=1
export COMPOSE_FILE=compose.yaml:deploy/esp/esp.yaml:deploy/esp/encryption.yaml COMPOSE_PATH_SEPARATOR=:
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
# The protected ports of deploy/esp, which this deployment keeps: port_ps
# and port_pc of the P-CSCF, port_us and port_uc of each UE (TS 33.203
# 7.1).
port_ps=5064
port_pc=5065
port_us=5062
port_uc=5063
# The file of the keys that deploy/esp/pcscf-encryption.yaml names.
keys=/captures/esp_sa
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
# reason. Say so instead, as scripts/e2e-esp.sh does.
if ! out="$(docker compose exec -T pcscf ip xfrm state 2>&1)"; then
	echo "FAIL this machine's kernel has no xfrm, so ESP cannot be set up: $out"
	echo "     (a container shares the kernel of the machine that runs the engine)"
	docker compose down --rmi local -v --remove-orphans >/dev/null 2>&1 || true
	exit 1
fi

no_associations() { [ -z "$(docker compose exec -T pcscf ip xfrm state | tr -d '\r')" ]; }

# A file of keys that an earlier run left in the captures directory
# would be counted below and would name associations that are gone. It
# goes from inside the container, which owns the file; no UE has
# registered yet, so nothing of this run is thrown away with it.
docker compose exec -T capture rm -f "$keys"

echo "a VoLTE call over encrypted ESP: volte10 calls volte12"
docker compose run --no-deps -d --name ims-practice-esp-enc-volte12 volte12 answer "${vb[@]}" \
	--ring 1s --tone 3s >/dev/null
if ! wait_log ims-practice-esp-enc-volte12 '"msg":"waiting for a call"'; then
	echo "FAIL volte12 did not attach and register with encrypted ESP"
	docker logs ims-practice-esp-enc-volte12 2>&1 | tail -n 10
	# A kernel that refuses the associations shows here: the P-CSCF
	# answers the REGISTER 500 and its log says what ip said. grep ends
	# with 1 when it selects no line, which is no reason to stop.
	docker compose logs --no-color pcscf | { grep -E '"level":"(WARN|ERROR)"' || true; } | tail -n 5
	fail=1
fi

# The four security associations of the registration carry AES-CBC
# beside the integrity algorithm, which is what the agreement selected
# (TS 33.203 7.2, Annex H).
states="$(docker compose exec -T pcscf ip xfrm state | tr -d '\r')"
printf '%s\n' "$states" >e2e-out/esp-encryption-xfrm.txt
n="$(grep -c '^src ' <<<"$states" || true)"
if [ "$n" = 4 ]; then
	echo "ok   the P-CSCF holds the four security associations of the registration"
else
	echo "FAIL the P-CSCF holds $n security associations, want 4:"
	sed 's/^/     /' <<<"$states"
	fail=1
fi
if grep -q "auth-trunc hmac(sha256)" <<<"$states" && grep -q "enc cbc(aes)" <<<"$states" &&
	! grep -q "cipher_null" <<<"$states"; then
	echo "ok   they are HMAC-SHA2-256-128 with AES-CBC encryption"
else
	echo "FAIL the security associations do not carry AES-CBC:"
	sed 's/^/     /' <<<"$states"
	fail=1
fi

out="$(timeout 180 docker compose run --no-deps --rm volte10 call "${va[@]}" --tone 2s tel:+12025550112 2>&1 || true)"
printf '%s\n' "$out" >e2e-out/esp-encryption-volte10-call.log
if grep -q '"msg":"call answered"' <<<"$out" && [ "$(ended ims-practice-esp-enc-volte12)" = 0 ]; then
	echo "ok   volte10 called volte12 over encrypted ESP, and volte12 answered"
else
	echo "FAIL the call over encrypted ESP: $(grep -E '"msg":"(call progress|ue failed|media)"' <<<"$out" | tail -n 3)"
	docker logs ims-practice-esp-enc-volte12 2>&1 | tail -n 10 || true
	fail=1
fi

if wait_for 30 no_associations; then
	echo "ok   the P-CSCF kept no security association after the deregistrations"
else
	echo "FAIL the P-CSCF still holds security associations:"
	docker compose exec -T pcscf ip xfrm state | sed 's/^/     /'
	fail=1
fi

same_nodes

# expect_frames checks that the capture holds at least min frames that
# pass the filter.
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

# The rows of the ESP SAs table that the P-CSCF wrote beside the
# capture: four for each of the two registrations, each naming the
# algorithms of the agreement as Wireshark names them. The keys stay in
# the file and out of this output.
table="$(docker compose exec -T capture cat "$keys" 2>/dev/null | tr -d '\r' || true)"
rows="$(grep -c . <<<"$table" || true)"
if [ "$rows" -ge 8 ]; then
	echo "ok   the P-CSCF wrote $rows rows of the ESP SAs table to $keys"
else
	echo "FAIL $keys holds $rows rows, want the four of each of the two registrations"
	fail=1
fi
if grep -q "AES-CBC \[RFC3602\]" <<<"$table"; then
	echo "ok   the rows name AES-CBC [RFC3602], the encryption algorithm of the agreement"
else
	echo "FAIL the rows of $keys name another encryption algorithm"
	fail=1
fi
# The settings a reader of the capture gives Wireshark: one -o for each
# row of the table. SIP is registered on port 5060 and found on any
# other port by a heuristic. Inside ESP the protected ports are named
# here instead, so that a message over TCP between them is read as SIP
# whatever the heuristic makes of it.
esp_sa=(-o esp.enable_encryption_decode:TRUE)
while IFS= read -r row; do
	[ -n "$row" ] || continue
	esp_sa+=(-o "uat:esp_sa:$row")
done <<<"$table"
esp_sa+=(-d "tcp.port==$port_us,sip" -d "tcp.port==$port_ps,sip")

ue_to_pcscf="ip.addr == $pcscf_ip && (ip.addr == $volte10_ip || ip.addr == $volte12_ip)"
protected="($ue_to_pcscf) && (udp.port == $port_ps || udp.port == $port_pc || udp.port == $port_us || udp.port == $port_uc || tcp.port == $port_ps || tcp.port == $port_pc || tcp.port == $port_us || tcp.port == $port_uc)"

await "the ESP of the registrations and the call" "esp && ($ue_to_pcscf)" 20
echo "ok   $(count "esp && ($ue_to_pcscf)") ESP packets between the VoLTE UEs and the P-CSCF"

# Without the keys there is no SIP to be found between the two ends,
# neither in the clear nor inside ESP.
none "SIP in the clear on a protected port" "sip && ($protected)"
none "SIP in the clear between a UE and the P-CSCF outside port 5060" \
	"sip && ($ue_to_pcscf) && !(udp.port == 5060)"
# The settings that read the SIP inside the null encryption of
# scripts/e2e-esp.sh find nothing here: they name the right integrity
# algorithm, so the length of the integrity check value is right, and
# what is left under it is still ciphertext.
tshark_options=(-o esp.enable_encryption_decode:TRUE
	-o 'uat:esp_sa:"IPv4","*","*","*","NULL","","HMAC-SHA-256-128 [RFC4868]",""'
	-d "tcp.port==$port_us,sip" -d "tcp.port==$port_ps,sip")
none "SIP inside ESP read as though its encryption were null" "esp && sip && ($protected)"
tshark_options=()

# The unprotected port carries what TS 33.203 7.1 items 3 and 4 allow
# there and nothing else: the first REGISTER of each UE and its 401.
# Those two carry the security agreement, so the capture shows in the
# clear that each UE offered AES-CBC and that the P-CSCF offered it too
# (TS 33.203 5.1.3, 7.2).
expect_frames "the first REGISTER of each UE, unprotected on port 5060" \
	"sip.Method == \"REGISTER\" && udp.port == 5060 && ($ue_to_pcscf)" 2
expect_frames "the Security-Client of each UE offering AES-CBC" \
	"sip.Security-Client contains \"ealg=aes-cbc\" && udp.port == 5060 && ($ue_to_pcscf)" 2
expect_frames "the Security-Server of each 401 offering AES-CBC" \
	"sip.Security-Server contains \"ealg=aes-cbc\" && udp.port == 5060 && ($ue_to_pcscf)" 2

# With the rows of the ESP SAs table, the same ESP packets show the SIP
# that was encrypted inside them.
tshark_options=("${esp_sa[@]}")
expect_frames "REGISTER decrypted out of ESP" "esp && sip.Method == \"REGISTER\" && ($protected)" 2
expect_frames "INVITE decrypted out of ESP" "esp && sip.Method == \"INVITE\" && ($protected)" 1
expect_frames "the 200 (OK) of the call decrypted out of ESP" \
	"esp && sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ($protected)" 1
expect_frames "BYE decrypted out of ESP" "esp && sip.Method == \"BYE\" && ($protected)" 1
# Each UE registers and deregisters over its associations, and the
# P-CSCF answers both over them: it lets the associations go only once
# the 200 (OK) of the deregistration has left (TS 33.203 7.4.2a).
expect_frames "the 200 (OK) of a protected REGISTER, the deregistrations among them" \
	"esp && sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ($protected)" 4
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
