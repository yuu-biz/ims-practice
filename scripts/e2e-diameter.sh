#!/usr/bin/env bash
# End-to-end check: the I-CSCF and the S-CSCF open Diameter
# connections to the HSS, and the P-CSCF one to the PCRF in the epc node
#, exchange watchdogs, and disconnect cleanly, a node that is started
# again opens its connection anew, and the capture container records all
# of it.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
export MSYS_NO_PATHCONV=1
# Use the project's own builder when it exists, so the build
# cache stays separate from other projects. CI has no such builder.
if docker buildx inspect ims-practice >/dev/null 2>&1; then
	export BUILDX_BUILDER=ims-practice
fi

# A connection opens at once, or after Tc (30 s) when the node started
# before the HSS accepted connections. The first DWR follows Tw (30 s,
# up to 2 s early or late) after that, so 75 s leaves room for both.
wait_seconds="${WAIT_SECONDS:-75}"

fail=0
docker compose up -d --build
remember_nodes
echo "waiting ${wait_seconds}s for CER/CEA and DWR/DWA"
sleep "$wait_seconds"
docker compose stop scscf
sleep 2
same_nodes scscf

# A node that is started again opens its connection anew, with a CER and
# a CEA of its own. The log of the restarted container still holds the
# lines of its first connection, so only the ones written from here on
# count.
since="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
docker compose start scscf
opened=
for i in $(seq 1 120); do
	if grep -q '"to":"I-Open (OKAY)"' <<<"$(docker compose logs --no-color --since "$since" scscf 2>/dev/null)"; then
		opened=yes
		break
	fi
	sleep 1
done
if [ -n "$opened" ]; then
	echo "ok   the S-CSCF opened its Diameter connection again"
else
	echo "FAIL the S-CSCF did not open its Diameter connection again within 120 s"
	docker compose logs --no-color --since "$since" scscf | tail -n 20
	fail=1
fi
sleep 1

pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
echo "reading $pcap"

# count is the number of frames that pass the filter, and at_least
# checks that there are min or more. The checks here count frames, in
# any order; e2e-mrf.sh has an at_least of its own that counts distinct
# values of a field.
count() {
	local out
	if ! out="$(docker compose exec -T capture tshark -r "$pcap" -Y "$1" 2>&1)"; then
		echo "tshark failed on filter: $1" >&2
		echo "$out" >&2
		exit 2
	fi
	if [ -z "$out" ]; then echo 0; else printf '%s
' "$out" | wc -l | tr -d ' '; fi
}

at_least() {
	local label="$1" filter="$2" min="$3" n
	n="$(count "$filter")"
	if [ "$n" -ge "$min" ]; then
		echo "ok   $label: $n"
	else
		echo "FAIL $label: $n, want at least $min"
		fail=1
	fi
}

hss=172.28.0.10
icscf=172.28.0.20
scscf=172.28.0.30
pcscf=172.28.0.40
pcrf=172.28.0.80
for node in $icscf $scscf $pcscf; do
	at_least "CER from $node" "diameter.cmd.code == 257 && diameter.flags.request == 1 && ip.src == $node" 1
	at_least "CEA 2001 to $node" "diameter.cmd.code == 257 && diameter.flags.request == 0 && diameter.Result-Code == 2001 && ip.dst == $node" 1
done
# The HSS speaks Cx (16777216) with the CSCFs and Sh (16777217) with
# application servers, and advertises nothing else. The two are
# adjacent, so a range finds any other one.
none "CEA of the HSS advertising an application other than Cx and Sh" "diameter.cmd.code == 257 && diameter.flags.request == 0 && ip.src == $hss && (diameter.Auth-Application-Id < 16777216 || diameter.Auth-Application-Id > 16777217)"
at_least "CEA of the HSS advertising Sh" "diameter.cmd.code == 257 && diameter.flags.request == 0 && ip.src == $hss && diameter.Auth-Application-Id == 16777217" 1
# The PCRF speaks Rx (16777236, TS 29.214 5.1) with the P-CSCF only.
none "CEA of the PCRF advertising an application other than Rx" "diameter.cmd.code == 257 && diameter.flags.request == 0 && ip.src == $pcrf && diameter.Auth-Application-Id != 16777236"
at_least "CEA of the PCRF advertising Rx to the P-CSCF" "diameter.cmd.code == 257 && diameter.flags.request == 0 && ip.src == $pcrf && ip.dst == $pcscf && diameter.Auth-Application-Id == 16777236" 1
at_least "DWR" 'diameter.cmd.code == 280 && diameter.flags.request == 1' 2
at_least "DWA" 'diameter.cmd.code == 280 && diameter.flags.request == 0' 2

# The S-CSCF stopped and started again, in that order, so these checks
# follow one another: expect takes the first frame after the one before.
last=0
expect "DPR from the stopped S-CSCF, Disconnect-Cause REBOOTING" "diameter.cmd.code == 282 && diameter.flags.request == 1 && ip.src == $scscf && diameter.Disconnect-Cause == 0"
expect "DPA to the stopped S-CSCF" "diameter.cmd.code == 282 && diameter.flags.request == 0 && ip.dst == $scscf"
expect "CER of the S-CSCF that started again" "diameter.cmd.code == 257 && diameter.flags.request == 1 && ip.src == $scscf"
expect "CEA 2001 to the S-CSCF that started again" "diameter.cmd.code == 257 && diameter.flags.request == 0 && diameter.Result-Code == 2001 && ip.dst == $scscf"

none "malformed Diameter packets" 'diameter && (_ws.malformed || _ws.expert.severity >= warning)'

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color hss icscf scscf pcscf epc | tail -n 60
fi
exit "$fail"
