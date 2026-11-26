#!/usr/bin/env bash
# End-to-end check: an OSS supervises the faults of the network through
# the OAM. The OAM is the element manager, and towards the OSS the MnS
# producer of fault supervision (TS 28.111): the OSS stand-in of the oss
# service subscribes to the alarm notifications with an
# NtfSubscriptionControl, reads the AlarmList of the SubNetwork with a
# filter, and acknowledges an alarm with a JSON Merge Patch, all with
# the ProvMnS operations of TS 28.532 under
# http://oam:8080/3GPPManagement/ProvMnS/v19.
#
# The faults are real ones. A paused media relay stops answering the
# OAM, which makes its part of the alarm list unreliable: the OSS is
# sent notifyPotentialFaultyAlarmList, reads the node in
# unreliableAlarmScope, and is sent notifyAlarmListRebuilt once the node
# answers again. The HSS is then shut down through its maintenance API,
# as the registration guide does. Once their next attempt to connect is
# refused, the five nodes that reach it over Diameter raise the alarm
# "Diameter peer down", and the OSS is sent notifyNewAlarm for each.
# The OSS acknowledges the alarm of the S-CSCF and is sent
# notifyAckStateChanged; after the unlock each node clears its alarm,
# the OSS is sent notifyClearedAlarm, and the record of the S-CSCF,
# cleared and acknowledged, leaves the list. Once the OSS has deleted
# its subscription, it is sent nothing more. The capture then shows each
# of these HTTP exchanges between the OSS and the OAM, in the clear.
#
# The deployment is the one of compose.yaml, with the profile oss.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
export MSYS_NO_PATHCONV=1
# The OSS stand-in is in the profile oss. With the profile set here,
# every docker compose command below sees its service.
export COMPOSE_PROFILES=oss
# Use the project's own builder when it exists, so the build
# cache stays separate from other projects. CI has no such builder.
if docker buildx inspect ims-practice >/dev/null 2>&1; then
	export BUILDX_BUILDER=ims-practice
fi

domain=ims.mnc001.mcc001.3gppnetwork.org
hss_host=hss.$domain
# The nodes that reach the HSS over Diameter, which are also their
# names in the inventory of the OAM and the names of their roles.
peers="icscf scscf ipsmgw mmtel sc"

docker compose up -d --build

# wait_for_open waits up to two minutes until the service $1 has opened
# its Diameter connection to the HSS: a node that tried before the HSS
# listened tries again after Tc, 30 s (RFC 6733 2.1).
opened() { grep -q '"to":"I-Open (OKAY)"' <<<"$(docker compose logs --no-color "$1" 2>/dev/null)"; }
wait_for_open() {
	wait_for 120 opened "$1" && return 0
	echo "FAIL $1 never opened its Diameter connection" >&2
	docker compose logs --no-color "$1" | tail -n 30 >&2
	exit 1
}
for service in $peers; do
	wait_for_open "$service"
done

# oss runs a command of the OSS stand-in in its own container, where the
# defaults of its flags are the addresses of this deployment: the
# {MnSRoot} http://oam:8080/3GPPManagement, the SubNetwork ims, and the
# notificationRecipientAddress http://oss:8080/notifications.
oss() { docker compose exec -T oss oss "$@" | tr -d '\r'; }

# alarm_list [FILTER] reads the AlarmList with "oss alarms" and prints
# one line for the list, "unreliableAlarmScope=<DN or none>
# numOfAlarmRecords=<n>", then one line for each alarm record it holds,
# "<alarm id> <perceivedSeverity> <ackState> <objectInstance>", sorted.
alarm_list() {
	local args=()
	[ -z "${1:-}" ] || args=(-filter "$1")
	# shellcheck disable=SC2154 # python is set by scripts/e2e-lib.sh
	oss alarms ${args[@]+"${args[@]}"} | "$python" -c '
import json, sys
a = json.load(sys.stdin)["attributes"]
print("unreliableAlarmScope=%s numOfAlarmRecords=%s" % (a.get("unreliableAlarmScope", "none"), a.get("numOfAlarmRecords")))
for key, r in sorted((a.get("alarmRecords") or {}).items()):
    print(key, r["perceivedSeverity"], r["ackState"], r["objectInstance"])'
}
records() { tail -n +2; }

# received TYPE [NAME=VALUE]... prints the first notification of that
# notificationType that the OSS took whose attributes have those values,
# and fails when it took none.
received() {
	docker compose exec -T oss wget -qO- "http://127.0.0.1:8080/received?notification_type=$1" 2>/dev/null | tr -d '\r' |
		"$python" -c '
import json, sys
want = dict(a.split("=", 1) for a in sys.argv[1:])
for n in json.load(sys.stdin):
    body = n["notification"]
    if all(k in body and str(body[k]) == v for k, v in want.items()):
        print(json.dumps(body))
        sys.exit(0)
sys.exit(1)' "${@:2}"
}
has_received() { received "$@" >/dev/null; }

# await_notification SECONDS LABEL TYPE [NAME=VALUE]... waits for such
# a notification, and says what it waited for when it does not come.
await_notification() {
	local seconds="$1" label="$2"
	shift 2
	if wait_for "$seconds" has_received "$@"; then
		echo "ok   the OSS received $label"
	else
		echo "FAIL the OSS received no $label within $seconds s"
		echo "     wanted $*"
		echo "     it received: $(docker compose exec -T oss wget -qO- http://127.0.0.1:8080/received 2>/dev/null | tr -d '\r' | tail -c 2000)"
		fail=1
	fi
}

# href_of NODE is the href of the notifications about the alarm of the
# node NODE about the HSS: the canonical URI of the alarmed object, the
# host of the OAM then the DN of the object (TS 32.158 4.2.4).
href_of() { echo "http://oam:8080/SubNetwork=ims/ManagedElement=$1/ManagedFunction=$1/Peer=$hss_host"; }
object_of() { echo "SubNetwork=ims,ManagedElement=$1,ManagedFunction=$1,Peer=$hss_host"; }

# hss_administrative_state asks the maintenance API of the HSS node for
# the administrative state $1, as the registration guide does, from
# inside its container.
hss_administrative_state() {
	docker compose exec -T hss sh -c '
body="{\"administrative_state\":\"$1\",\"operator\":\"e2e\"}"
printf "PUT /maintenance/v1/functions/hss/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
	nc -w 5 127.0.0.1 8085' sh "$1" | tr -d '\r'
}

wait_for_oam || exit 1
oss_serves() { docker compose exec -T oss wget -qO- http://127.0.0.1:8080/received >/dev/null 2>&1; }
if ! wait_for 30 oss_serves; then
	echo "FAIL the OSS stand-in does not serve the notifications it received" >&2
	docker compose logs --no-color oss | tail -n 20 >&2
	exit 1
fi

mkdir -p e2e-out
remember_nodes
fail=0

echo "the alarm list before any fault"
# Every node answers the OAM, and the instance of the OAM that watches
# each node for its alarm events has found it answering: until both,
# the part of the list of a node is unreliable.
reliable() { grep -q '^unreliableAlarmScope=none ' <<<"$(alarm_list 2>/dev/null)"; }
if wait_for 90 reliable; then
	echo "ok   the AlarmList of the SubNetwork is reliable: no unreliableAlarmScope"
else
	echo "FAIL the AlarmList stayed unreliable: $(alarm_list 2>&1 | first_line)"
	docker compose logs --no-color oam | tail -n 20
	exit 1
fi

# A node that started before the HSS listened raised the alarm at its
# first attempt and cleared it at the CEA, and the cleared record stays
# until it is acknowledged (TS 28.111 6.14). The same alarm raised again
# would update that record, which is notifyChangedAlarmGeneral (6.12),
# not notifyNewAlarm. So the OSS acknowledges those first, with one
# patch for each node: a patch never spans two.
# The filters select alarm records with a Jex conditions expression
# (TS 32.161 7.6), in the form of TS 28.111 A.1.4.
records_where="/AlarmList[id=\"1\"]/attributes/alarmRecords/*"
about_hss="specificProblem=\"Diameter peer down\" and contains(objectInstance,\"Peer=$hss_host\")"
hss_alarms="${records_where}[$about_hss]"
before="$(alarm_list "$hss_alarms" | records)"
if awk 'NF && $2 != "CLEARED" { bad = 1 } END { exit bad }' <<<"$before"; then
	echo "ok   the only alarms about the HSS before the drain are cleared: $(grep -c . <<<"$before" || true)"
else
	echo "FAIL an alarm about the HSS is not cleared before the drain:"
	echo "$before"
	exit 1
fi
for node in $(cut -d. -f1 <<<"$before" | sort -u); do
	# shellcheck disable=SC2046 # one argument per alarm id
	oss ack -user e2e $(awk -v n="$node" 'index($1, n ".") == 1 { print $1 }' <<<"$before")
done
if [ -z "$(alarm_list "$hss_alarms" | records)" ]; then
	echo "ok   the OSS acknowledged the cleared alarms about the HSS, which left the list"
else
	echo "FAIL alarms about the HSS are left after the acknowledgement: $(alarm_list "$hss_alarms" | records)"
	exit 1
fi

echo "the subscription"
subscription="$(oss subscribe)"
if grep -q '"notificationRecipientAddress": "http://oss:8080/notifications"' <<<"$subscription" &&
	grep -q '"objectInstance": "SubNetwork=ims,NtfSubscriptionControl=1"' <<<"$subscription"; then
	echo "ok   the OSS created the NtfSubscriptionControl=1 with its notificationRecipientAddress"
else
	echo "FAIL the subscription: $subscription"
	exit 1
fi
subscription_uri=http://oam:8080/3GPPManagement/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl=1
read_back="$(docker compose exec -T oss wget -qO- "$subscription_uri" | tr -d '\r')"
if grep -q '"objectClass":"NtfSubscriptionControl"' <<<"$read_back"; then
	echo "ok   the OAM reads the subscription back"
else
	echo "FAIL the GET of the subscription: $read_back"
	fail=1
fi

echo "a node that does not answer the OAM"
# A paused container keeps its connections and answers nothing, so the
# request of the OAM for the events of the node runs out of time.
docker compose pause mrfp >/dev/null
trap 'docker compose unpause mrfp >/dev/null 2>&1 || true' EXIT
mrfp_dn=SubNetwork=ims,ManagedElement=mrfp
mrfp_href=http://oam:8080/SubNetwork=ims/ManagedElement=mrfp
await_notification 40 "notifyPotentialFaultyAlarmList about the MRFP" notifyPotentialFaultyAlarmList \
	"href=$mrfp_href" "systemDN=SubNetwork=ims" "reason=serviceprovider-NE communication error"
unreliable_mrfp() { grep -q "^unreliableAlarmScope=$mrfp_dn " <<<"$(alarm_list 2>/dev/null)"; }
if wait_for 30 unreliable_mrfp; then
	echo "ok   the AlarmList names the MRFP in unreliableAlarmScope"
else
	echo "FAIL the AlarmList while the MRFP does not answer: $(alarm_list 2>&1 | first_line)"
	fail=1
fi
docker compose unpause mrfp >/dev/null
trap - EXIT
await_notification 40 "notifyAlarmListRebuilt about the MRFP" notifyAlarmListRebuilt \
	"href=$mrfp_href" "reason=System-NE communication error" "alarmListAlignmentRequirement=ALIGNMENT_REQUIRED"
if wait_for 30 reliable; then
	echo "ok   the AlarmList is reliable again once the MRFP answers"
else
	echo "FAIL the AlarmList stayed unreliable after the MRFP came back: $(alarm_list 2>&1 | first_line)"
	fail=1
fi

echo "the drain of the HSS"
answer="$(hss_administrative_state SHUTTINGDOWN || true)"
if grep -Eq '"administrative_state":"(SHUTTINGDOWN|LOCKED)"' <<<"$answer"; then
	echo "ok   the maintenance API shut the HSS down"
else
	echo "FAIL the shut down of the HSS: $answer"
	exit 1
fi
# The DPR alone raises no alarm, as REBOOTING asks the peer to come
# back. The alarm comes when the next attempt, Tc later, is refused.
declare -A alarm_id
for node in $peers; do
	await_notification 90 "notifyNewAlarm about the HSS from $node" notifyNewAlarm \
		"href=$(href_of "$node")" "systemDN=SubNetwork=ims" "alarmType=COMMUNICATIONS_ALARM" \
		"probableCause=COMMUNICATIONS_SUBSYSTEM_FAILURE" "specificProblem=Diameter peer down" "perceivedSeverity=MAJOR"
	alarm_id[$node]="$(received notifyNewAlarm "href=$(href_of "$node")" | "$python" -c 'import json, sys; print(json.load(sys.stdin)["alarmId"])' || true)"
done
scscf_alarm="${alarm_id[scscf]}"

# The filter of the read selects the alarm records, as in TS 28.111
# A.1.4: the five alarms about the HSS, each under the ManagedElement
# that the inventory of the OAM names, and keyed by the alarmId of its
# notification.
want=""
for node in $peers; do
	want+="${alarm_id[$node]} MAJOR UNACKNOWLEDGED $(object_of "$node")"$'\n'
done
want="$(sort <<<"$want" | sed '/^$/d')"
got="$(alarm_list "${records_where}[$about_hss and perceivedSeverity=\"MAJOR\"]" | records)"
if [ "$got" = "$want" ]; then
	echo "ok   the filtered AlarmList holds the five alarms about the HSS, under the ids of their notifications"
else
	echo "FAIL the filtered AlarmList:"
	diff <(echo "$want") <(echo "$got") || true
	fail=1
fi
critical="$(alarm_list "${records_where}[perceivedSeverity=\"CRITICAL\"]" | records)"
if [ -z "$critical" ]; then
	echo "ok   a filter for CRITICAL selects no record"
else
	echo "FAIL a filter for CRITICAL selected records: $critical"
	fail=1
fi

echo "the acknowledgement"
if [ -n "$scscf_alarm" ] && oss ack "$scscf_alarm"; then
	echo "ok   the OSS acknowledged $scscf_alarm"
else
	echo "FAIL the OSS could not acknowledge the alarm of the S-CSCF: '$scscf_alarm'"
	fail=1
fi
await_notification 30 "notifyAckStateChanged for the alarm of the S-CSCF" notifyAckStateChanged \
	"alarmId=$scscf_alarm" "href=$(href_of scscf)" "ackState=ACKNOWLEDGED" "ackUserId=noc-operator" "ackSystemId=oss-1" \
	"perceivedSeverity=MAJOR"
if grep -qx "$scscf_alarm MAJOR ACKNOWLEDGED $(object_of scscf)" <<<"$(alarm_list "$hss_alarms" | records)"; then
	echo "ok   the AlarmList shows the alarm of the S-CSCF acknowledged and still MAJOR"
else
	echo "FAIL the AlarmList after the acknowledgement: $(alarm_list "$hss_alarms" | records)"
	fail=1
fi

echo "the unlock of the HSS"
answer="$(hss_administrative_state UNLOCKED || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$answer"; then
	echo "ok   the maintenance API unlocked the HSS"
else
	echo "FAIL the unlock of the HSS: $answer"
	fail=1
fi
# Each node connects again at its next attempt, up to Tc later, and the
# CEA clears its alarm.
for node in $peers; do
	await_notification 90 "notifyClearedAlarm about the HSS from $node" notifyClearedAlarm \
		"alarmId=${alarm_id[$node]}" "href=$(href_of "$node")" "perceivedSeverity=CLEARED"
done
# Cleared and acknowledged, the record of the S-CSCF leaves the list
# (TS 28.111 6.14); the four others stay, cleared, until acknowledged.
want=""
for node in $peers; do
	[ "$node" = scscf ] || want+="${alarm_id[$node]} CLEARED UNACKNOWLEDGED $(object_of "$node")"$'\n'
done
want="$(sort <<<"$want" | sed '/^$/d')"
got="$(alarm_list "$hss_alarms" | records)"
if [ "$got" = "$want" ]; then
	echo "ok   the cleared and acknowledged record left the list; the four others are cleared and unacknowledged"
else
	echo "FAIL the AlarmList after the unlock:"
	diff <(echo "$want") <(echo "$got") || true
	fail=1
fi

echo "the end of the subscription"
if oss unsubscribe; then
	echo "ok   the OSS deleted its NtfSubscriptionControl"
else
	echo "FAIL the OSS could not delete its subscription"
	fail=1
fi
if gone="$(docker compose exec -T oss wget -qO /dev/null "$subscription_uri" 2>&1)"; then
	echo "FAIL the OAM still reads the deleted subscription"
	fail=1
elif grep -q 404 <<<"$gone"; then
	echo "ok   the deleted subscription is 404"
else
	echo "FAIL the GET of the deleted subscription: $gone"
	fail=1
fi
# The OSS acknowledges the four cleared alarms, one node at a time, so
# that a deployment left running holds no alarm about the HSS. The
# notifications of those changes go to no subscription now.
others=""
for node in $peers; do
	[ "$node" = scscf ] && continue
	others+=" ${alarm_id[$node]}"
	oss ack "${alarm_id[$node]}" || {
		echo "FAIL the OSS could not acknowledge ${alarm_id[$node]}"
		fail=1
	}
done
if [ -z "$(alarm_list "$hss_alarms" | records)" ]; then
	echo "ok   no alarm about the HSS is left in the list"
else
	echo "FAIL alarms about the HSS are left: $(alarm_list "$hss_alarms" | records)"
	fail=1
fi
# The OAM sends the notification of an event as soon as the node has
# it, so ten seconds are more than enough for one that would come.
sleep 10
for id in $others; do
	if has_received notifyAckStateChanged "alarmId=$id"; then
		echo "FAIL the OSS was sent notifyAckStateChanged for $id after it unsubscribed"
		fail=1
	fi
done
echo "ok   checked that no notification came for the acknowledgements after the end of the subscription"
# The drain raised one alarm on each of the five nodes, and nothing else
# in this run raises one, so the OSS took exactly five notifyNewAlarm,
# one about each node. The notificationId names one notification (TS
# 28.532 11.0.2): a notification sent twice comes with its id twice,
# whatever its type.
taken="$(docker compose exec -T oss wget -qO- http://127.0.0.1:8080/received | tr -d '\r')"
new_alarms="$("$python" -c '
import json, sys
for n in json.load(sys.stdin):
    if n["notification_type"] == "notifyNewAlarm":
        print(n["notification"]["href"])' <<<"$taken" | sort)"
want_new="$(for node in $peers; do href_of "$node"; done | sort)"
if [ "$new_alarms" = "$want_new" ]; then
	echo "ok   the OSS took five notifyNewAlarm, one about each node that reaches the HSS"
else
	echo "FAIL the OSS took $(grep -c . <<<"$new_alarms" || true) notifyNewAlarm, want one about each of the five nodes:"
	diff <(echo "$want_new") <(echo "$new_alarms") || true
	fail=1
fi
twice="$("$python" -c '
import collections, json, sys
ids = collections.Counter(n["notification"]["notificationId"] for n in json.load(sys.stdin))
for i, count in sorted(ids.items()):
    if count > 1:
        print("%s (%d times)" % (i, count))' <<<"$taken")"
if [ -z "$twice" ]; then
	echo "ok   no notificationId came twice among the $("$python" -c 'import json, sys; print(len(json.load(sys.stdin)))' <<<"$taken") notifications"
else
	echo "FAIL notifications came more than once: $twice"
	fail=1
fi
refused="$(docker compose exec -T oss wget -qO- http://127.0.0.1:8080/refused | tr -d '\r')"
if [ "$refused" = "[]" ]; then
	echo "ok   every notification the OAM sent had the names and the attributes of the OpenAPI files"
else
	echo "FAIL the OSS refused notifications: $refused"
	fail=1
fi

same_nodes

pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
echo "reading $pcap"
last=0
oam=172.28.0.65
oss=172.28.0.66
hss=172.28.0.10
scscf=172.28.0.30
# The OAM and the OSS both listen on 8080, one of the ports on which
# Wireshark decodes HTTP by itself, and the JSON bodies with it.
from_oss="ip.src == $oss && ip.dst == $oam && tcp.dstport == 8080"
to_oss="ip.src == $oam && ip.dst == $oss && tcp.srcport == 8080"
notification="http.request.method == \"POST\" && http.request.uri == \"/notifications\" && ip.src == $oam && ip.dst == $oss"
taken="http.response.code == 204 && ip.src == $oss && ip.dst == $oam"
provmns=/3GPPManagement/ProvMnS/v19/SubNetwork=ims
patch="http.request.method == \"PATCH\" && http.content_type == \"application/merge-patch+json\" && $from_oss"

# The answer to the last acknowledgement is the last frame that these
# checks read. The OSS sent one patch for each node that held a cleared
# alarm before the drain, one for the S-CSCF and one for each of the
# four others, and every one is answered 204, as is the DELETE of the
# subscription; the OAM answers nothing else to the OSS with 204.
patches=$(($(cut -d. -f1 <<<"$before" | sort -u | grep -c . || true) + 5))
await "the answers to the $patches patches and the DELETE of the OSS" "http.response.code == 204 && $to_oss" "$((patches + 1))"

echo "the subscription"
expect "PUT NtfSubscriptionControl=1 OSS -> OAM" "http.request.method == \"PUT\" && http.request.uri == \"$provmns/NtfSubscriptionControl=1\" && $from_oss && json.member_with_value == \"notificationRecipientAddress:http://oss:8080/notifications\""
expect "201 OAM -> OSS with the Location of the subscription" "http.response.code == 201 && $to_oss && http.location == \"$subscription_uri\""
expect "GET NtfSubscriptionControl=1 OSS -> OAM" "http.request.method == \"GET\" && http.request.uri == \"$provmns/NtfSubscriptionControl=1\" && $from_oss"
expect "200 OAM -> OSS with the subscription" "http.response.code == 200 && $to_oss && json.member_with_value == \"objectClass:NtfSubscriptionControl\""
echo "the MRFP that does not answer"
expect "POST notifyPotentialFaultyAlarmList OAM -> OSS" "$notification && json.member_with_value == \"notificationType:notifyPotentialFaultyAlarmList\" && json.member_with_value == \"href:$mrfp_href\""
expect "204 OSS -> OAM" "$taken"
expect "200 OAM -> OSS with the MRFP in unreliableAlarmScope" "http.response.code == 200 && $to_oss && json.member_with_value == \"unreliableAlarmScope:$mrfp_dn\""
expect "POST notifyAlarmListRebuilt OAM -> OSS" "$notification && json.member_with_value == \"notificationType:notifyAlarmListRebuilt\" && json.member_with_value == \"href:$mrfp_href\" && json.member_with_value == \"alarmListAlignmentRequirement:ALIGNMENT_REQUIRED\""
expect "204 OSS -> OAM" "$taken"
echo "the alarm of the S-CSCF"
expect "DPR HSS -> S-CSCF" "diameter.cmd.code == 282 && diameter.flags.request == 1 && ip.src == $hss && ip.dst == $scscf"
# The kernel of the HSS refuses the next attempt of the S-CSCF with a
# reset that answers its SYN (RFC 9293 3.10.7.1), and that is what
# raises the alarm.
expect "reset HSS -> S-CSCF that refuses the connection" "tcp.flags.reset == 1 && tcp.flags.ack == 1 && tcp.seq_raw == 0 && ip.src == $hss && tcp.srcport == 3868 && ip.dst == $scscf"
expect "POST notifyNewAlarm OAM -> OSS for the S-CSCF" "$notification && json.member_with_value == \"notificationType:notifyNewAlarm\" && json.member_with_value == \"href:$(href_of scscf)\" && json.member_with_value == \"probableCause:COMMUNICATIONS_SUBSYSTEM_FAILURE\""
expect "GET AlarmList=1 with a filter OSS -> OAM" "http.request.method == \"GET\" && http.request.uri contains \"$provmns/AlarmList=1?filter=\" && $from_oss"
expect "200 OAM -> OSS with the alarm records" "http.response.code == 200 && $to_oss && json.member_with_value == \"objectInstance:$(object_of scscf)\""
expect "PATCH AlarmList=1 with a JSON Merge Patch OSS -> OAM" "$patch && json.member_with_value == \"ackState:ACKNOWLEDGED\" && json.member_with_value == \"ackUserId:noc-operator\""
# The OAM answers once the node has changed the record, and the node's
# event goes to the OSS at the same time, so the two come in either
# order.
mark
expect "204 OAM -> OSS" "http.response.code == 204 && $to_oss"
back
expect "POST notifyAckStateChanged OAM -> OSS" "$notification && json.member_with_value == \"notificationType:notifyAckStateChanged\" && json.member_with_value == \"alarmId:$scscf_alarm\" && json.member_with_value == \"ackState:ACKNOWLEDGED\""
expect "CEA HSS -> S-CSCF after the unlock" "diameter.cmd.code == 257 && diameter.flags.request == 0 && ip.src == $hss && ip.dst == $scscf"
expect "POST notifyClearedAlarm OAM -> OSS for the S-CSCF" "$notification && json.member_with_value == \"notificationType:notifyClearedAlarm\" && json.member_with_value == \"alarmId:$scscf_alarm\" && json.member_with_value == \"perceivedSeverity:CLEARED\""
echo "the end of the subscription"
expect "DELETE NtfSubscriptionControl=1 OSS -> OAM" "http.request.method == \"DELETE\" && http.request.uri == \"$provmns/NtfSubscriptionControl=1\" && $from_oss"
expect "204 OAM -> OSS" "http.response.code == 204 && $to_oss"
none "notifications after the DELETE" "frame.number > $last && $notification"
none "answers of the OSS to a notification other than 204" "http.response && ip.src == $oss && ip.dst == $oam && http.response.code != 204"
# The OAM's own patches to the maintenance API of the nodes are JSON,
# not an MnS patch, so only those of the OSS count here.
none "patches of the OSS of a form other than the JSON Merge Patch" "http.request.method == \"PATCH\" && $from_oss && !(http.content_type == \"application/merge-patch+json\")"

# Every display filter that the guide shows its readers finds something
# in this capture.
guide_filters docs/guide/en/oss.md

# The ladder of the exchanges between the OSS and the OAM, with the
# Diameter messages of the HSS that raise and clear the alarms, for the
# guide and the CI artifact.
docker compose exec -T capture tshark -r "$pcap" -T fields -E separator=/t \
	-Y "(http && ip.addr == $oss) || (diameter.cmd.code in {257, 282} && ip.addr == $hss)" \
	-e frame.number -e frame.time_relative -e ip.src -e ip.dst -e _ws.col.Info | tr -d '\r' >e2e-out/fm-ladder.txt

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color oam oss | grep -E '"level":"(WARN|ERROR)"' | tail -n 40
fi
exit "$fail"
