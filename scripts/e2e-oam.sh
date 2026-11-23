#!/usr/bin/env bash
# End-to-end check: an operator runs the network from the OAM. The OAM
# reads the state of every node of the deployment, provisions a file of
# test subscribers in bulk through the provisioning API of the HSS, and
# backs the subscriptions up and puts them back. One of the subscribers
# the OAM created then registers, and the checks read its UAR, MAR and
# SAR in the capture, so that the registration the capture shows is
# that of a subscriber no configuration file holds.
#
# The deployment is the one of compose.yaml. The OAM API is not
# published on the host: it changes the configuration of the nodes and
# hands out backups that hold the credentials of every subscription,
# and asks for no password, so every request below goes from inside the
# container of the OAM node, as the guides show.
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
# The subscribers of the file: private identities 001010000000120 to
# 001010000000139 of the test PLMN 001/01, and the numbers
# +1-202-555-0120 to 0139, which are in the 555-0100 to 555-0199 range
# that the North American Numbering Plan keeps for fiction. No
# configuration file of the deployment holds any of them.
first=20
count=20
impi_of() { printf '0010100000001%02d@%s' "$1" "$domain"; }
number_of() { printf '+120255501%02d' "$1"; }
password_of() { printf 'test-password-01%02d' "$1"; }

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

# oam_request METHOD PATH [BODY] sends one request to the OAM API and
# prints the whole answer, the HTTP headers included. BusyBox wget sends
# no method but GET and POST and no body of its own, hence the request
# written out to nc, as the guides do for the maintenance API. The
# answer of an operation that streams comes line by line until the OAM
# closes the connection, which it does at the end of an HTTP/1.0 answer.
oam_request() {
	docker compose exec -T oam sh -c '
		if [ -n "$3" ]; then
			printf "%s %s HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#3}\r\n\r\n%s" "$1" "$2" "$3"
		else
			printf "%s %s HTTP/1.0\r\n\r\n" "$1" "$2"
		fi | nc -w 30 127.0.0.1 8080' sh "$1" "$2" "${3:-}" | tr -d '\r'
}

# oam_provision sends a file of records to the bulk provisioning of the
# OAM. The file goes into the container of the OAM node, since the API
# is reachable from the network of the nodes alone.
oam_provision() {
	local query="${2:-}"
	docker compose cp "$1" oam:/tmp/records.jsonl >/dev/null
	docker compose exec -T oam sh -c '
		length="$(wc -c < /tmp/records.jsonl | tr -d " ")"
		{
			printf "POST /oam/v1/provisioning%s HTTP/1.0\r\nContent-Type: application/x-ndjson\r\nContent-Length: %s\r\n\r\n" "$1" "$length"
			cat /tmp/records.jsonl
		} | nc -w 60 127.0.0.1 8080
		status=$?
		# The records hold passwords, so they do not stay behind.
		rm -f /tmp/records.jsonl
		exit $status' sh "$query" | tr -d '\r'
}

# summarize reads an answer that streams (the HTTP headers, then one
# JSON object per line) and prints the status code and the counts of the
# summary, such as "200 lines=20 created=20". A record that the OAM or
# the HSS refused is printed with its line number and the reason, never
# with the record itself, which holds credentials.
summarize() {
	"$python" -c '
import json, sys

text = sys.stdin.read()
head, _, body = text.partition("\n\n")
lines = head.splitlines()
out = [lines[0].split(" ")[1] if lines and " " in lines[0] else "no answer"]
for line in body.splitlines():
    line = line.strip()
    if not line:
        continue
    try:
        value = json.loads(line)
    except ValueError:
        out.append("unreadable: " + line[:60])
        continue
    if "summary" in value:
        summary = value["summary"]
        out.append("lines=%d" % summary["lines"])
        for name in ("created", "updated", "unchanged", "deleted", "rejected", "failed"):
            if summary[name]:
                out.append("%s=%d" % (name, summary[name]))
    elif value.get("error"):
        out.append("error: " + value["error"])
    elif value.get("outcome", {}).get("result") in ("rejected", "failed"):
        outcome = value["outcome"]
        out.append("line %d %s: %s" % (outcome.get("line", 0), outcome["result"], outcome.get("reason", "")))
print(" ".join(out))
'
}

# The OAM answers once its listener is up. Its inventory comes from its
# configuration, so this only says that the role started.
for i in $(seq 1 60); do
	grep -q '"nodes"' <<<"$(oam_request GET /oam/v1/inventory 2>/dev/null || true)" && break
	sleep 1
done

mkdir -p e2e-out
remember_nodes
fail=0

# Every node of the inventory answers the OAM, and none of them is out
# of service. The OAM asks each node for its state, so this is the one
# check that the OAM reaches all of them. node_states prints how many
# answered and the administrative state of each role, or why a node
# failed.
node_states() {
	oam_request GET /oam/v1/state | "$python" -c '
import json, sys
_, _, body = sys.stdin.read().partition("\n\n")
answer = json.loads(body)
states = []
for node in answer["nodes"]:
    functions = (node.get("result") or {}).get("functions") or []
    states.append("%s=%s" % (node["node"], ",".join(
        f["administrative_state"] for f in functions)
        or (node.get("error") or {}).get("error", "no role")))
print("%d/%d %s" % (answer["succeeded"], answer["succeeded"] + answer["failed"], " ".join(sorted(states))))
'
}
want_states="9/9 agw=UNLOCKED hss=UNLOCKED icscf=UNLOCKED ipsmgw=UNLOCKED mmtel=UNLOCKED mrfc=UNLOCKED mrfp=UNLOCKED pcscf=UNLOCKED scscf=UNLOCKED"
# Up to a minute: a node whose listener was not up yet when the OAM
# asked is a failed entry, and the OAM asks again at the next request.
all_unlocked() { [ "$(node_states 2>/dev/null)" = "$want_states" ]; }
if wait_for 60 all_unlocked; then
	echo "ok   every node of the inventory answers the OAM and is unlocked"
else
	echo "FAIL the state the OAM reads is: $(node_states)"
	echo "     want: $want_states"
	exit 1
fi

# The file of subscribers, one JSON object per line, as an operator
# hands it to the OAM.
: >e2e-out/subscribers.jsonl
for i in $(seq 0 $((count - 1))); do
	n=$((first + i))
	printf '{"private_identity":"%s","public_identities":["sip:%s@%s"],"tel":"tel:%s","password":"%s","test_user":true}\n' \
		"$(impi_of "$n")" "$(number_of "$n")" "$domain" "$(number_of "$n")" "$(password_of "$n")" \
		>>e2e-out/subscribers.jsonl
done

# A dry run asks the HSS what each record would do and changes nothing.
report="$(oam_provision e2e-out/subscribers.jsonl '?dry_run=true')"
if [ "$(summarize <<<"$report")" = "200 lines=$count created=$count" ]; then
	echo "ok   a dry run of the file would create all $count subscriptions"
else
	echo "FAIL the dry run: $(summarize <<<"$report")"
	fail=1
fi
if grep -q '"error"' <<<"$(oam_request GET "/oam/v1/subscriptions/$(impi_of "$first")")"; then
	echo "ok   the dry run left the HSS without the subscriptions"
else
	echo "FAIL the dry run created a subscription in the HSS"
	fail=1
fi

report="$(oam_provision e2e-out/subscribers.jsonl)"
if [ "$(summarize <<<"$report")" = "200 lines=$count created=$count" ]; then
	echo "ok   the OAM created all $count subscriptions in the HSS"
else
	echo "FAIL the bulk provisioning: $(summarize <<<"$report")"
	docker compose logs --no-color oam | tail -n 20 >&2
	exit 1
fi

# The HSS itself, not the OAM's report, is asked for them: its own
# provisioning API lists the subscriptions, and every identity of the
# file is among them.
listed="$(docker compose exec -T hss wget -qO- 'http://127.0.0.1:8081/subscriptions?limit=1000' | tr -d '\r')"
missing=0
for i in $(seq 0 $((count - 1))); do
	grep -q "\"$(impi_of $((first + i)))\"" <<<"$listed" || missing=$((missing + 1))
done
if [ "$missing" -eq 0 ]; then
	echo "ok   the HSS has every subscription of the file"
else
	echo "FAIL the HSS is missing $missing of the $count subscriptions"
	fail=1
fi

# The mark that a line of the file set is on the subscription in the
# HSS, and no answer but the export hands out a credential.
one="$(oam_request GET "/oam/v1/subscriptions/$(impi_of "$first")")"
if grep -q '"test_user":true' <<<"$one" && ! grep -qi 'password\|"k"\|"opc"' <<<"$one"; then
	echo "ok   the OAM shows the subscription as a test user and no credential of it"
else
	echo "FAIL the subscription the OAM shows: $(tail -n 1 <<<"$one")"
	fail=1
fi

# The same file again changes nothing: the HSS has each subscription
# already, credentials included.
report="$(oam_provision e2e-out/subscribers.jsonl)"
if [ "$(summarize <<<"$report")" = "200 lines=$count unchanged=$count" ]; then
	echo "ok   the same file again left all $count subscriptions unchanged"
else
	echo "FAIL the file sent again: $(summarize <<<"$report")"
	fail=1
fi

# A subscriber the OAM created registers. The UE simulator is the one of
# the ue service, with the identities and the password of the file in
# place of its own.
impi="$(impi_of "$first")"
ue_args=(--impi "$impi" --impu "sip:$(number_of "$first")@$domain" --password "$(password_of "$first")")
if docker compose run --no-deps --rm ue register "${ue_args[@]}" --deregister-after 2s; then
	echo "ok   a subscriber that the OAM provisioned registered and deregistered"
else
	echo "FAIL the provisioned subscriber did not register"
	docker compose logs --no-color scscf | tail -n 20 >&2
	fail=1
fi

# The operator backs the network up. The answer streams each step and
# ends with the manifest.
backup="$(oam_request POST /oam/v1/backups '{"operator": "e2e", "comment": "end-to-end check"}')"
read -r backup_id backup_state backup_records <<<"$("$python" -c '
import json, sys
_, _, body = sys.stdin.read().partition("\n\n")
for line in body.splitlines():
    value = json.loads(line) if line.strip() else {}
    if "manifest" in value:
        m = value["manifest"]
        print(m["id"], m["state"], (m.get("records") or {}).get("hss.subscriptions", 0))
        break
else:
    print("none failed 0")
' <<<"$backup")"
# The deployment starts with subscriptions of its own, so the archive
# holds those and the ones of the file.
if [ "$backup_state" = completed ] && [ "$backup_records" -gt "$count" ]; then
	echo "ok   the backup $backup_id completed with $backup_records subscriptions"
else
	echo "FAIL the backup is $backup_id $backup_state with $backup_records subscriptions"
	docker compose logs --no-color oam | tail -n 20 >&2
	exit 1
fi

# Every identity of the file is in the archive. Only the number of
# matching lines is read: a line of the archive holds the password and
# the keys of its subscription.
in_archive="$(docker compose exec -T oam sh -c '
	n=0
	i="$2"
	while [ "$i" -lt "$3" ]; do
		identity="$(printf "0010100000001%02d@%s" "$i" "$4")"
		if grep -q "\"$identity\"" "/var/lib/ims/oam/backups/$1.jsonl"; then
			n=$((n + 1))
		fi
		i=$((i + 1))
	done
	echo "$n"' sh "$backup_id" "$first" "$((first + count))" "$domain" | tr -d '\r')"
if [ "$in_archive" = "$count" ]; then
	echo "ok   the archive of the backup holds all $count subscriptions of the file"
else
	echo "FAIL the archive holds $in_archive of the $count subscriptions"
	fail=1
fi

# Putting the backup back changes nothing: every subscription is in
# place already, credentials, SQN_HE and repository data included.
restore="$(oam_request POST "/oam/v1/backups/$backup_id/restore" '{"operator": "e2e"}')"
if [ "$(summarize <<<"$restore")" = "200 lines=$backup_records unchanged=$backup_records" ]; then
	echo "ok   the restore left all $backup_records subscriptions unchanged"
else
	echo "FAIL the restore: $(summarize <<<"$restore")"
	fail=1
fi

# The operator removes the subscribers of the file again, so that a
# deployment left running here serves the subscribers the guides
# describe. A line that deletes carries the private identity and
# nothing else.
: >e2e-out/removals.jsonl
for i in $(seq 0 $((count - 1))); do
	printf '{"private_identity":"%s","delete":true}\n' "$(impi_of $((first + i)))" >>e2e-out/removals.jsonl
done
report="$(oam_provision e2e-out/removals.jsonl)"
if [ "$(summarize <<<"$report")" = "200 lines=$count deleted=$count" ]; then
	echo "ok   the OAM removed all $count subscriptions again"
else
	echo "FAIL removing the subscriptions: $(summarize <<<"$report")"
	fail=1
fi
if grep -q '"error"' <<<"$(oam_request GET "/oam/v1/subscriptions/$impi")"; then
	echo "ok   the HSS no longer has the removed subscription"
else
	echo "FAIL the removed subscription is still in the HSS"
	fail=1
fi

same_nodes

pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
echo "reading $pcap"
last=0

ue=172.28.0.100
pcscf=172.28.0.40
icscf=172.28.0.20
scscf=172.28.0.30
hss=172.28.0.10
register='sip.Method == "REGISTER"'
uar='diameter.cmd.code == 300'
sar='diameter.cmd.code == 301'
mar='diameter.cmd.code == 303'
req='diameter.flags.request == 1'
ans='diameter.flags.request == 0'
# Every Cx request of the registration carries the private identity in
# User-Name, so this picks the provisioned subscriber's out.
user="diameter.User-Name == \"$impi\""

# The SAR of the deregistration is the last frame that these checks
# read; it follows the whole registration.
await "the SAR of the deregistration" "$sar && $req && $user && diameter.Server-Assignment-Type == 5"

echo "the registration of the subscriber that the OAM provisioned"
expect "REGISTER UE -> P-CSCF" "$register && ip.src == $ue && ip.dst == $pcscf && sip.Authorization contains \"$impi\""
expect "UAR I-CSCF -> HSS" "$uar && $req && $user && ip.src == $icscf && ip.dst == $hss"
expect "UAA first registration" "$uar && $ans && ip.src == $hss && diameter.Experimental-Result-Code == 2001"
expect "MAR S-CSCF -> HSS" "$mar && $req && $user && ip.src == $scscf"
expect "MAA with SIP Digest and H(A1)" "$mar && $ans && ip.src == $hss && diameter.Result-Code == 2001 && diameter.Digest-HA1"
expect "401 P-CSCF -> UE" "sip.Status-Code == 401 && ip.src == $pcscf && ip.dst == $ue"
expect "REGISTER with a response" "$register && ip.src == $ue && sip.Authorization contains \"response=\\\"\" && !(sip.Authorization contains \"response=\\\"\\\"\")"
expect "UAR I-CSCF -> HSS" "$uar && $req && $user && ip.src == $icscf"
expect "SAR REGISTRATION" "$sar && $req && $user && ip.src == $scscf && diameter.Server-Assignment-Type == 1"
# Wireshark names the TS 29.229 AVP that carries the user profile
# Cx-User-Data.
expect "SAA with the user profile" "$sar && $ans && ip.src == $hss && diameter.Result-Code == 2001 && diameter.Cx-User-Data"
expect "200 OK P-CSCF -> UE" "sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ip.src == $pcscf && ip.dst == $ue"

# The OAM speaks to the nodes in the clear, like every interface here,
# so the capture holds its requests too. Wireshark decodes HTTP on its
# own ports only, and the maintenance listener is on none of them, so
# the check reads the bytes of the request.
oam=172.28.0.65
if [ "$(count "tcp.dstport == 8085 && ip.src == $oam && tcp contains \"GET /maintenance/v1/state\"")" -gt 0 ]; then
	echo "ok   the capture holds the requests of the OAM to the nodes"
else
	echo "FAIL no request of the OAM to a node in the capture"
	fail=1
fi

exit "$fail"
