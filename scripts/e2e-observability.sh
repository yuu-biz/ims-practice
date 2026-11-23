#!/usr/bin/env bash
# End-to-end check: the three CSCFs serve their metrics and their
# maintenance API on their maintenance listeners
# (deploy/compose/pcscf.yaml, deploy/compose/icscf.yaml,
# deploy/compose/scscf.yaml). After a registration, Prometheus has
# scraped all three and holds their counts of successful registrations,
# with the set-up time that the P-CSCF measures, and Grafana returns the
# count of the S-CSCF through the Prometheus data source that the
# dashboard uses (deploy/observability). A call adds the terminating
# session establishment that the I-CSCF counts, the successful
# establishments that the three CSCFs count on the 180 (Ringing) of the
# callee, and the success ratio that the dashboard divides out of the
# two. The HSS holds the subscribers it was given and counts the
# identities of a registration while it lasts. The Cx requests of the
# registration and of the call, the answers of the HSS to them and the
# state of each Diameter connection are there as well, and the
# connection of the I-CSCF reads as down while the HSS is out of service
# and as up again after the unlock. The MMTel AS serves its metrics on
# the same listener and counts the Sh requests of each call. A shut
# down through the maintenance API then makes the S-CSCF answer a new
# REGISTER with 503 and Retry-After, which the capture shows hop by
# hop, and an unlock lets the UE register again. A shut down of the
# P-CSCF makes the P-CSCF answer the REGISTER itself with 503 and
# Retry-After, forwarding nothing, and a shut down of the I-CSCF makes
# the I-CSCF answer 503, after which the P-CSCF, with no other I-CSCF
# left, answers the UE 504.
# A shut down of the MMTel AS makes the AS answer 503 to the INVITE that
# the filter criteria send it, which the S-CSCF turns into the 500 that
# ends the call, since the criterion of the originating case says
# SESSION_TERMINATED; after the unlock the call goes through the AS on
# both legs again. A shut down of the MRFC makes the MRFC answer 503
# with Retry-After to the INVITE with which the AS asks it for an
# announcement, so a barred call of UE 1 is rejected without the early
# media of the MRFP; after the unlock the caller hears the announcement
# again. A shut down of the IP-SM-GW makes it answer 503 with
# Retry-After to the third-party REGISTER and to the MESSAGE of a short
# message, and the S-CSCF, whose criteria say SESSION_CONTINUED, leaves
# the registration standing and sends the MESSAGE on to the PSI of the
# SC, which this network cannot translate, so UE 1 sees 404; after the
# unlock the short message reaches the SC again. A shut down of the HSS
# instead sends every Diameter
# peer a DPR with Disconnect-Cause REBOOTING and stops the node
# listening on 3868, so the I-CSCF reports itself DISABLED and no UE
# can register; after the unlock the CSCFs open their connections again
# by themselves.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
export MSYS_NO_PATHCONV=1
# Prometheus and Grafana come from the second file; the IMS is that of
# compose.yaml.
export COMPOSE_FILE=compose.yaml:deploy/observability/compose.observability.yaml COMPOSE_PATH_SEPARATOR=:
# Use the project's own builder when it exists, so the build
# cache stays separate from other projects. CI has no such builder.
if docker buildx inspect ims-practice >/dev/null 2>&1; then
	export BUILDX_BUILDER=ims-practice
fi

prometheus=http://localhost:9090
grafana=http://localhost:3000

docker compose up -d --build

# The nodes that ask the HSS can only serve their own requests once
# their Diameter connections to it are open.
#
# diameter_opens prints how many times the service $1 has opened a
# Diameter connection, from the state transitions in its log. Only a
# move into I-Open counts: on a connection that is already open, the
# watchdog goes to SUSPECT and back to I-Open (OKAY) without any new
# connection (RFC 3539 3.4.1), and that must not be counted as one.
diameter_opens() {
	docker compose logs --no-color "$1" 2>/dev/null |
		grep '"to":"I-Open' | grep -vc '"from":"I-Open' || true
}

# wait_for_opens waits up to two minutes until the service $1 has opened
# a Diameter connection $2 times, which is how a connection that came
# back is told from the one before it. Two minutes: a node that was
# refused connects again after Tc, 30 s (RFC 6733 2.1).
wait_for_opens() {
	local i
	for i in $(seq 1 120); do
		if [ "$(diameter_opens "$1")" -ge "$2" ]; then
			return 0
		fi
		sleep 1
	done
	return 1
}

# wait_for_open waits for the first connection of the service $1, and
# gives the whole run up when it never comes.
wait_for_open() {
	if wait_for_opens "$1" 1; then
		return 0
	fi
	echo "FAIL $1 never opened its Diameter connection" >&2
	docker compose logs --no-color "$1" | tail -n 30 >&2
	exit 1
}
wait_for_open icscf
wait_for_open scscf
# The IP-SM-GW and the MMTel AS each hold an Sh connection to the HSS,
# which the drain of the HSS ends as well.
wait_for_open ipsmgw
wait_for_open mmtel

# wait_for_http waits up to a minute until url answers 2xx.
wait_for_http() {
	local name="$1" url="$2" i
	for i in $(seq 1 60); do
		curl -fsS "$url" >/dev/null 2>&1 && return 0
		sleep 1
	done
	echo "FAIL $name did not answer at $url within a minute" >&2
	exit 1
}
wait_for_http Prometheus "$prometheus/-/ready"
wait_for_http Grafana "$grafana/api/health"

# largest prints the largest number in a list of numbers, one per line,
# or nothing for an empty list.
largest() {
	"$python" -c '
import sys
values = [float(v) for v in sys.stdin.read().split()]
print(max(values) if values else "")'
}

# prometheus_values prints the value of each series that the query
# returns from the HTTP API of Prometheus, one per line. It prints
# nothing, hence "|| true", while Prometheus has no answer to read, so
# that until_positive asks again.
prometheus_values() {
	curl -fsS --get --data-urlencode "query=$1" "$prometheus/api/v1/query" 2>/dev/null |
		"$python" -c '
import json, sys
for series in json.load(sys.stdin)["data"]["result"]:
    print(series["value"][1])' 2>/dev/null || true
}

# grafana_values prints the numbers that Grafana returns for the query,
# one per line: Grafana asks the Prometheus data source that it loaded
# at start (uid prometheus), as a panel of the dashboard does. The
# dashboard can be read without signing in; should the anonymous viewer
# not be let to query, the script asks again as admin, whose password
# nobody has changed here. Like prometheus_values, it prints nothing,
# hence "|| true", while Grafana has no answer to read.
grafana_values() {
	local body answer
	body="$("$python" -c '
import json, sys
print(json.dumps({"queries": [{"refId": "A", "datasource": {"type": "prometheus", "uid": "prometheus"},
    "expr": sys.argv[1], "instant": True, "range": False, "intervalMs": 15000, "maxDataPoints": 100}],
    "from": "now-15m", "to": "now"}))' "$1")"
	answer="$(curl -fsS -H 'Content-Type: application/json' -d "$body" "$grafana/api/ds/query" 2>/dev/null ||
		curl -fsS -u admin:admin -H 'Content-Type: application/json' -d "$body" "$grafana/api/ds/query" 2>/dev/null || true)"
	"$python" -c '
import json, sys
for frame in json.loads(sys.argv[1])["results"]["A"].get("frames", []):
    for field, column in zip(frame["schema"]["fields"], frame["data"]["values"]):
        if field.get("type") == "number":
            for v in column:
                if v is not None:
                    print(v)' "$answer" 2>/dev/null || true
}

# until_positive waits up to 90 seconds, six scrapes of Prometheus,
# until source (prometheus_values or grafana_values) returns a value
# above 0 for the query. It prints the largest value it got.
until_positive() {
	local source="$1" query="$2" value i
	for i in $(seq 1 90); do
		value="$("$source" "$query" | largest)"
		if [ -n "$value" ] && "$python" -c 'import sys; sys.exit(0 if float(sys.argv[1]) > 0 else 1)' "$value"; then
			echo "$value"
			return 0
		fi
		sleep 1
	done
	echo "${value:-none}"
	return 1
}

# until_equal waits up to 90 seconds, six scrapes of Prometheus, until
# the query returns the value $3. It prints the largest value it got.
until_equal() {
	local source="$1" query="$2" wanted="$3" value i
	for i in $(seq 1 90); do
		value="$("$source" "$query" | largest)"
		if [ -n "$value" ] && "$python" -c 'import sys; sys.exit(0 if float(sys.argv[1]) == float(sys.argv[2]) else 1)' "$value" "$wanted"; then
			echo "$value"
			return 0
		fi
		sleep 1
	done
	echo "${value:-none}"
	return 1
}

# administrative_state asks the maintenance API of the node of the
# service $1, which runs the role of the same name, for the
# administrative state $2 of that role, as the registration guide does,
# and prints the answer. The API is not published on the host, so the
# request goes from inside the container of the node, with the nc of
# the image.
administrative_state() {
	docker compose exec -T "$1" sh -c '
body="{\"administrative_state\":\"$2\",\"operator\":\"e2e\"}"
printf "PUT /maintenance/v1/functions/$1/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
	nc -w 5 127.0.0.1 8085' sh "$1" "$2" | tr -d '\r'
}

# node_state prints the state of the node of the service $1, from its
# maintenance API.
node_state() {
	docker compose exec -T "$1" wget -qO- http://127.0.0.1:8085/maintenance/v1/state | tr -d '\r'
}

# until_state waits up to 30 seconds until the state of the node of the
# service $1 holds the text $2.
until_state() {
	local i
	for i in $(seq 1 30); do
		if grep -qF "$2" <<<"$(node_state "$1" || true)"; then
			return 0
		fi
		sleep 1
	done
	return 1
}

remember_nodes
fail=0

echo "metrics"
if docker compose run --no-deps --rm ue register -deregister-after 2s; then
	echo "ok   UE registered and deregistered"
else
	echo "FAIL UE registration"
	fail=1
fi
if value="$(until_positive prometheus_values 'up{job="ims",node="scscf"}')"; then
	echo "ok   Prometheus scrapes the S-CSCF: up = $value"
else
	echo "FAIL the scscf target of Prometheus is not up: $value"
	# Shown only to explain the failure, if Prometheus answers at all,
	# hence "|| true".
	curl -fsS "$prometheus/api/v1/targets?state=active" || true
	echo
	fail=1
fi
successes='ims_registration_successes_total{role="scscf"}'
if value="$(until_positive prometheus_values "$successes")"; then
	echo "ok   Prometheus has $successes = $value"
else
	echo "FAIL Prometheus has no $successes above 0: $value"
	# Shown only to explain the failure, if the S-CSCF answers at all,
	# hence "|| true".
	docker compose exec -T scscf wget -qO- http://127.0.0.1:8085/metrics || true
	fail=1
fi
if value="$(until_positive grafana_values "$successes")"; then
	echo "ok   Grafana returns $successes = $value from its Prometheus data source"
else
	echo "FAIL Grafana returns no $successes above 0: $value"
	fail=1
fi
if value="$(until_positive prometheus_values 'up{job="ims",node="pcscf"}')"; then
	echo "ok   Prometheus scrapes the P-CSCF: up = $value"
else
	echo "FAIL the pcscf target of Prometheus is not up: $value"
	curl -fsS "$prometheus/api/v1/targets?state=active" || true
	echo
	fail=1
fi
# The P-CSCF counts the same registration, under its own role, and the
# time that the initial registration took (TS 32.409 4.1.1.1.4).
for series in 'ims_registration_successes_total{role="pcscf"}' 'ims_registration_setup_seconds_count{role="pcscf"}'; do
	if value="$(until_positive prometheus_values "$series")"; then
		echo "ok   Prometheus has $series = $value"
	else
		echo "FAIL Prometheus has no $series above 0: $value"
		# Shown only to explain the failure, if the P-CSCF answers at
		# all, hence "|| true".
		docker compose exec -T pcscf wget -qO- http://127.0.0.1:8085/metrics || true
		fail=1
	fi
done
if value="$(until_positive prometheus_values 'up{job="ims",node="icscf"}')"; then
	echo "ok   Prometheus scrapes the I-CSCF: up = $value"
else
	echo "FAIL the icscf target of Prometheus is not up: $value"
	curl -fsS "$prometheus/api/v1/targets?state=active" || true
	echo
	fail=1
fi
# The I-CSCF counts the initial INVITE that enters the home network (TS
# 32.409 4.1.2.11), so a call is needed to give it anything to count.
# The callee waits in the background for one call; flags go before the
# URI of "ue call", as the flag package stops at the first argument.
ue2=(--impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org
	--impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org --password test-password-2)
mkdir -p e2e-out
docker compose run --no-deps -d --name ims-practice-ue2-metrics ue2 answer "${ue2[@]}" --ring 500ms --tone 3s >/dev/null
if wait_log ims-practice-ue2-metrics '"msg":"waiting for a call"'; then
	if docker compose run --no-deps --rm ue call --tone 1s tel:+1-202-555-0102; then
		echo "ok   UE 1 called UE 2 through the I-CSCF"
	else
		echo "FAIL the call"
		fail=1
	fi
else
	fail=1
fi
if [ "$(ended ims-practice-ue2-metrics)" = "0" ]; then
	echo "ok   UE 2 answered and the call ended"
else
	echo "FAIL UE 2 did not answer the call"
	fail=1
fi
# The I-CSCF counts the initial INVITE on receipt (TS 32.409 4.1.2.11),
# and each of the three CSCFs counts the response that tells the session
# got through (4.1.2.2, 4.1.2.10, 4.1.2.12). UE 2 rings for 500 ms
# before it answers, so that response is the 180 (Ringing) and not the
# 200 (OK). The P-CSCF counts the call on both its legs, as does the
# S-CSCF, which serves the caller and the callee.
for wanted in \
	'icscf ims_session_attempts_total{role="icscf",direction="terminating"}' \
	'icscf ims_session_successes_total{role="icscf",direction="terminating",response="180"}' \
	'scscf ims_session_successes_total{role="scscf",direction="originating",response="180"}' \
	'scscf ims_session_successes_total{role="scscf",direction="terminating",response="180"}' \
	'pcscf ims_session_successes_total{role="pcscf",direction="originating",response="180"}' \
	'pcscf ims_session_successes_total{role="pcscf",direction="terminating",response="180"}'; do
	node="${wanted%% *}"
	series="${wanted#* }"
	if value="$(until_positive prometheus_values "$series")"; then
		echo "ok   Prometheus has $series = $value"
	else
		echo "FAIL Prometheus has no $series above 0: $value"
		# Shown only to explain the failure, if the node answers at all,
		# hence "|| true".
		docker compose exec -T "$node" wget -qO- http://127.0.0.1:8085/metrics || true
		fail=1
	fi
done
# The success ratio panel of the dashboard divides the two by role and
# direction. Read here for the caller's S-CSCF alone, so that one number
# comes back, and through the data source that the panel reads, so that
# the division is the one Grafana computes. The panel divides the rise
# of the counters over the range it is shown for; this check divides the
# counters themselves, because the one call of this scene leaves each
# series at 1, where a rise needs a second call.
ratio='sum by (role, direction) (ims_session_successes_total{role="scscf",direction="originating"}) / sum by (role, direction) (ims_session_attempts_total{role="scscf",direction="originating"})'
if value="$(until_positive grafana_values "$ratio")"; then
	echo "ok   Grafana returns the session establishment success ratio of the S-CSCF = $value"
else
	echo "FAIL Grafana returns no session establishment success ratio above 0: $value"
	fail=1
fi
# The Diameter metrics come from the peer group that each role sends
# through: the I-CSCF asks the HSS whether the user may register (UAR)
# and where the callee is (LIR), and the S-CSCF asks for the
# authentication vector (MAR) and tells the HSS that it serves the user
# (SAR). The result of each answer is in the result label, which this
# check leaves open so that it reads whichever result the HSS gave.
# ims_diameter_peer_up is read from the state of the connection at the
# scrape.
for wanted in \
	'icscf ims_diameter_requests_total{role="icscf",command="UAR"}' \
	'icscf ims_diameter_answers_total{role="icscf",command="UAR"}' \
	'icscf ims_diameter_requests_total{role="icscf",command="LIR"}' \
	'icscf ims_diameter_answers_total{role="icscf",command="LIR"}' \
	'scscf ims_diameter_requests_total{role="scscf",command="MAR"}' \
	'scscf ims_diameter_answers_total{role="scscf",command="MAR"}' \
	'scscf ims_diameter_requests_total{role="scscf",command="SAR"}' \
	'scscf ims_diameter_answers_total{role="scscf",command="SAR"}' \
	'icscf ims_diameter_peer_up{node="icscf"}' \
	'scscf ims_diameter_peer_up{node="scscf"}'; do
	node="${wanted%% *}"
	series="${wanted#* }"
	if value="$(until_positive prometheus_values "$series")"; then
		echo "ok   Prometheus has $series = $value"
	else
		echo "FAIL Prometheus has no $series above 0: $value"
		# Shown only to explain the failure, if the node answers at all,
		# hence "|| true".
		docker compose exec -T "$node" wget -qO- http://127.0.0.1:8085/metrics || true
		fail=1
	fi
done
if value="$(until_positive prometheus_values 'up{job="ims",node="hss"}')"; then
	echo "ok   Prometheus scrapes the HSS: up = $value"
else
	echo "FAIL the hss target of Prometheus is not up: $value"
	curl -fsS "$prometheus/api/v1/targets?state=active" || true
	echo
	fail=1
fi
if value="$(until_positive prometheus_values 'up{job="ims",node="mmtel"}')"; then
	echo "ok   Prometheus scrapes the MMTel AS: up = $value"
else
	echo "FAIL the mmtel target of Prometheus is not up: $value"
	curl -fsS "$prometheus/api/v1/targets?state=active" || true
	echo
	fail=1
fi
# The MMTel AS reads the settings of the served user's services from the
# HSS over Sh for every initial INVITE (UDR), so the call above left it
# one request per leg and the answers that came back.
for series in 'ims_diameter_requests_total{role="mmtel",command="UDR"}' \
	'ims_diameter_answers_total{role="mmtel",command="UDR"}' \
	'ims_diameter_peer_up{node="mmtel"}'; do
	if value="$(until_positive prometheus_values "$series")"; then
		echo "ok   Prometheus has $series = $value"
	else
		echo "FAIL Prometheus has no $series above 0: $value"
		# Shown only to explain the failure, if the AS answers at all,
		# hence "|| true".
		docker compose exec -T mmtel wget -qO- http://127.0.0.1:8085/metrics || true
		fail=1
	fi
done
if value="$(until_positive prometheus_values 'up{job="ims",node="ipsmgw"}')"; then
	echo "ok   Prometheus scrapes the IP-SM-GW: up = $value"
else
	echo "FAIL the ipsmgw target of Prometheus is not up: $value"
	curl -fsS "$prometheus/api/v1/targets?state=active" || true
	echo
	fail=1
fi
# The IP-SM-GW writes SMSRegistrationInfo for a user whose registration
# state says that a contact takes short messages (PUR), so the
# registration above left it one request and the answer that came back.
for series in 'ims_diameter_requests_total{role="ipsmgw",command="PUR"}' \
	'ims_diameter_answers_total{role="ipsmgw",command="PUR"}' \
	'ims_diameter_peer_up{node="ipsmgw"}'; do
	if value="$(until_positive prometheus_values "$series")"; then
		echo "ok   Prometheus has $series = $value"
	else
		echo "FAIL Prometheus has no $series above 0: $value"
		# Shown only to explain the failure, if the IP-SM-GW answers at
		# all, hence "|| true".
		docker compose exec -T ipsmgw wget -qO- http://127.0.0.1:8085/metrics || true
		fail=1
	fi
done
# The HSS counts what it holds, whether anyone is registered or not (TS
# 32.409 4.2.1.1): the subscribers of deploy/compose/hss.yaml, each with
# a SIP URI and most of them with a tel URI as well.
for series in ims_hss_subscriptions 'ims_hss_public_identities{format="sip"}' 'ims_hss_public_identities{format="tel"}'; do
	if value="$(until_positive prometheus_values "$series")"; then
		echo "ok   Prometheus has $series = $value"
	else
		echo "FAIL Prometheus has no $series above 0: $value"
		# Shown only to explain the failure, if the HSS answers at all,
		# hence "|| true".
		docker compose exec -T hss wget -qO- http://127.0.0.1:8085/metrics || true
		fail=1
	fi
done
# The identities in the registered state (TS 32.409 4.2.1.2) follow one
# registration up and down. Every other UE of this run deregistered
# before it stopped, so the numbers are those of this one registration:
# the SIP URI and the tel URI of the subscriber, which are one implicit
# registration set, and the one private identity behind them. The UE
# stays registered for two scrapes of Prometheus.
registered_identities=ims_hss_registered_public_identities
docker compose run --no-deps -d --name ims-practice-ue-hss ue register -deregister-after 35s >/dev/null
for wanted in "$registered_identities=2" ims_hss_registered_private_identities=1; do
	series="${wanted%=*}"
	count="${wanted##*=}"
	if value="$(until_equal prometheus_values "$series" "$count")"; then
		echo "ok   Prometheus has $series = $value while the UE is registered"
	else
		echo "FAIL Prometheus has no $series = $count while the UE is registered: $value"
		docker compose exec -T hss wget -qO- http://127.0.0.1:8085/metrics || true
		fail=1
	fi
done
if [ "$(ended ims-practice-ue-hss)" = "0" ]; then
	echo "ok   the UE deregistered"
else
	echo "FAIL the UE did not register and deregister again"
	fail=1
fi
if value="$(until_equal prometheus_values "$registered_identities" 0)"; then
	echo "ok   $registered_identities is $value again after the deregistration"
else
	echo "FAIL $registered_identities did not come back to 0 after the deregistration: $value"
	fail=1
fi

echo "drain of the S-CSCF"
# No transaction is in progress, so the shut down makes the S-CSCF
# LOCKED at once; SHUTTINGDOWN refuses a new REGISTER all the same. A
# request that fails leaves its answer empty, hence "|| true", and the
# check reports it with the state of the node.
answer="$(administrative_state scscf SHUTTINGDOWN || true)"
state="$(node_state scscf || true)"
if grep -Eq '"administrative_state":"(SHUTTINGDOWN|LOCKED)"' <<<"$state"; then
	echo "ok   the maintenance API shut the S-CSCF down"
else
	echo "FAIL the shut down of the S-CSCF: $answer"
	echo "     state of the node: $state"
	fail=1
fi
if docker compose run --no-deps --rm ue register -deregister-after 2s 2>/dev/null; then
	echo "FAIL UE registered with a shut down S-CSCF"
	fail=1
else
	echo "ok   UE could not register while the S-CSCF is shut down"
fi
answer="$(administrative_state scscf UNLOCKED || true)"
state="$(node_state scscf || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the S-CSCF"
else
	echo "FAIL the unlock of the S-CSCF: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# The checks read the capture up to the 200 OK of this registration:
# the one after those that the S-CSCF has sent so far.
pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
registered='sip.CSeq.method == "REGISTER" && sip.Status-Code == 200 && ip.src == 172.28.0.30'
registrations="$(count "$registered")"
if docker compose run --no-deps --rm ue register -deregister-after 2s; then
	echo "ok   UE registered after the unlock of the S-CSCF"
else
	echo "FAIL UE registration after the unlock of the S-CSCF"
	fail=1
fi
await "the 200 OK to the registration after the unlock of the S-CSCF" "$registered" $((registrations + 1))

echo "drain of the P-CSCF"
# The UEs that registered above have deregistered, but the P-CSCF counts
# each as a user until it forgets its IP association, 64*T1 later, so
# the shut down may leave it SHUTTINGDOWN for a while. Either state
# refuses a new REGISTER.
answer="$(administrative_state pcscf SHUTTINGDOWN || true)"
state="$(node_state pcscf || true)"
if grep -Eq '"administrative_state":"(SHUTTINGDOWN|LOCKED)"' <<<"$state"; then
	echo "ok   the maintenance API shut the P-CSCF down"
else
	echo "FAIL the shut down of the P-CSCF: $answer"
	echo "     state of the node: $state"
	fail=1
fi
if docker compose run --no-deps --rm ue register -deregister-after 2s 2>/dev/null; then
	echo "FAIL UE registered with a shut down P-CSCF"
	fail=1
else
	echo "ok   UE could not register while the P-CSCF is shut down"
fi
answer="$(administrative_state pcscf UNLOCKED || true)"
state="$(node_state pcscf || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the P-CSCF"
else
	echo "FAIL the unlock of the P-CSCF: $answer"
	echo "     state of the node: $state"
	fail=1
fi
registrations="$(count "$registered")"
if docker compose run --no-deps --rm ue register -deregister-after 2s; then
	echo "ok   UE registered after the unlock of the P-CSCF"
else
	echo "FAIL UE registration after the unlock of the P-CSCF"
	fail=1
fi
await "the 200 OK to the registration after the unlock of the P-CSCF" "$registered" $((registrations + 1))

echo "drain of the I-CSCF"
# The I-CSCF serves no transaction between requests, so it is LOCKED at
# once.
answer="$(administrative_state icscf SHUTTINGDOWN || true)"
state="$(node_state icscf || true)"
if grep -Eq '"administrative_state":"(SHUTTINGDOWN|LOCKED)"' <<<"$state"; then
	echo "ok   the maintenance API shut the I-CSCF down"
else
	echo "FAIL the shut down of the I-CSCF: $answer"
	echo "     state of the node: $state"
	fail=1
fi
if docker compose run --no-deps --rm ue register -deregister-after 2s 2>/dev/null; then
	echo "FAIL UE registered with a shut down I-CSCF"
	fail=1
else
	echo "ok   UE could not register while the I-CSCF is shut down"
fi
answer="$(administrative_state icscf UNLOCKED || true)"
state="$(node_state icscf || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the I-CSCF"
else
	echo "FAIL the unlock of the I-CSCF: $answer"
	echo "     state of the node: $state"
	fail=1
fi
registrations="$(count "$registered")"
if docker compose run --no-deps --rm ue register -deregister-after 2s; then
	echo "ok   UE registered after the unlock of the I-CSCF"
else
	echo "FAIL UE registration after the unlock of the I-CSCF"
	fail=1
fi
await "the 200 OK to the registration after the unlock of the I-CSCF" "$registered" $((registrations + 1))

echo "drain of the MMTel AS"
# The AS answers the INVITE of each leg it serves, so a call through it
# again after the unlock adds 2xx of its own to the capture. Count what
# is there first.
asAnswers='sip.CSeq.method == "INVITE" && sip.Status-Code == 200 && ip.src == 172.28.0.75'
asAnswered="$(count "$asAnswers")"
# The AS is serving no call, so the shut down makes it LOCKED at once;
# SHUTTINGDOWN refuses a new INVITE just the same. The filter criterion
# of the originating case says SESSION_TERMINATED, so the S-CSCF ends
# the call rather than go on without the AS (TS 24.229 5.4.3.2).
answer="$(administrative_state mmtel SHUTTINGDOWN || true)"
state="$(node_state mmtel || true)"
if grep -Eq '"administrative_state":"(SHUTTINGDOWN|LOCKED)"' <<<"$state"; then
	echo "ok   the maintenance API shut the MMTel AS down"
else
	echo "FAIL the shut down of the MMTel AS: $answer"
	echo "     state of the node: $state"
	fail=1
fi
if docker compose run --no-deps --rm ue call --tone 1s tel:+1-202-555-0102 2>/dev/null; then
	echo "FAIL UE 1 called with a shut down MMTel AS"
	fail=1
else
	echo "ok   UE 1 could not call while the MMTel AS is shut down"
fi
# Ut goes out of service with the AS: the XCAP server answers 503
# instead of the settings (RFC 9110 15.6.4). "ue xcap" needs no
# registration, and ends by itself on the refusal.
if docker compose run --no-deps --rm ue xcap get >/dev/null 2>&1; then
	echo "FAIL Ut answered UE 1 while the MMTel AS is shut down"
	fail=1
else
	echo "ok   Ut refused UE 1 while the MMTel AS is shut down"
fi
answer="$(administrative_state mmtel UNLOCKED || true)"
state="$(node_state mmtel || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the MMTel AS"
else
	echo "FAIL the unlock of the MMTel AS: $answer"
	echo "     state of the node: $state"
	fail=1
fi
if docker compose run --no-deps --rm ue xcap get >/dev/null; then
	echo "ok   Ut served UE 1 after the unlock of the MMTel AS"
else
	echo "FAIL Ut after the unlock of the MMTel AS"
	fail=1
fi
# A call after the unlock goes through the AS on both legs again. UE 2
# waits in the background for it, as in the metrics scene above.
docker compose run --no-deps -d --name ims-practice-ue2-mmtel ue2 answer "${ue2[@]}" --ring 500ms --tone 1s >/dev/null
if wait_log ims-practice-ue2-mmtel '"msg":"waiting for a call"'; then
	if docker compose run --no-deps --rm ue call --tone 1s tel:+1-202-555-0102; then
		echo "ok   UE 1 called UE 2 after the unlock of the MMTel AS"
	else
		echo "FAIL the call after the unlock of the MMTel AS"
		fail=1
	fi
else
	fail=1
fi
if [ "$(ended ims-practice-ue2-mmtel)" = "0" ]; then
	echo "ok   UE 2 answered the call after the unlock of the MMTel AS"
else
	echo "FAIL UE 2 did not answer the call after the unlock of the MMTel AS"
	fail=1
fi

await "the 200 OK of the call after the unlock of the MMTel AS" "$asAnswers" $((asAnswered + 1))

echo "drain of the MRFC"
# The MRFC takes its work from the AS, so the drain shows in a call
# whose service needs the MRF: a barred call, before whose rejection the
# AS has the MRFC play an announcement to the caller (TS 24.628 4.2.4).
# The AS answers the INVITE of each leg it serves, and the MRFC answers
# the one of each dialog of its own, so count the 2xx of the MRFC first.
mrfcAnswers='sip.CSeq.method == "INVITE" && sip.Status-Code == 200 && ip.src == 172.28.0.85'
mrfcAnswered="$(count "$mrfcAnswers")"
# UE 1 bars its own outgoing calls over Ut, as the MRF guide has it.
# Every scene after this one, and the checks of the capture, need UE 1's
# calls to go through again, so the barring comes off on the way out of
# this scene whatever happens in between, and the trap goes once the
# scene has taken it off itself.
trap 'docker compose run --no-deps --rm ue xcap set baoc off >/dev/null 2>&1 || true' EXIT
# The drain only shows in a call that the AS rejects, so the two calls
# below say nothing about it unless the barring took. Check that here,
# before the MRFC is shut down. "ue xcap" prints every supplementary
# service of the document, each as "name", "provisioned" and "active"
# in that order, so the check reads the three lines of baoc itself: a
# bare '"active": true' would match any other service.
services="$(docker compose run --no-deps --rm ue xcap set baoc on 2>&1)"
if grep -A 2 '"name": "baoc"' <<<"$services" | grep -q '"active": true'; then
	echo "ok   UE 1 barred its outgoing calls over Ut"
else
	echo "FAIL UE 1 could not bar its outgoing calls, so the calls of this scene say nothing about the drain"
	fail=1
fi
# The MRFC holds no dialog of its own, so the shut down makes it LOCKED
# at once; SHUTTINGDOWN refuses a new INVITE just the same.
answer="$(administrative_state mrfc SHUTTINGDOWN || true)"
state="$(node_state mrfc || true)"
if grep -Eq '"administrative_state":"(SHUTTINGDOWN|LOCKED)"' <<<"$state"; then
	echo "ok   the maintenance API shut the MRFC down"
else
	echo "FAIL the shut down of the MRFC: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# The call is barred either way, so "ue call" exits 1, hence "|| true".
# What the drain changes is the announcement: the AS gets 503 from the
# MRFC and rejects the call without the early media of the MRFP.
out="$(docker compose run --no-deps --rm ue call --tone 1s tel:+1-202-555-0102 2>&1 || true)"
if grep -q '"call failed: 603 ' <<<"$out" && ! grep -q '"msg":"early media authorization"' <<<"$out"; then
	echo "ok   UE 1's barred call was rejected without an announcement while the MRFC is shut down"
else
	echo "FAIL the barred call while the MRFC is shut down: $(tail -n 2 <<<"$out")"
	fail=1
fi
answer="$(administrative_state mrfc UNLOCKED || true)"
state="$(node_state mrfc || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the MRFC"
else
	echo "FAIL the unlock of the MRFC: $answer"
	echo "     state of the node: $state"
	fail=1
fi
out="$(docker compose run --no-deps --rm ue call --tone 1s tel:+1-202-555-0102 2>&1 || true)"
if grep -q '"msg":"early media authorization"' <<<"$out" && grep -q '"call failed: 603 ' <<<"$out"; then
	echo "ok   UE 1 heard the announcement before the rejection after the unlock of the MRFC"
else
	echo "FAIL the barred call after the unlock of the MRFC: $(tail -n 2 <<<"$out")"
	fail=1
fi
services="$(docker compose run --no-deps --rm ue xcap set baoc off 2>&1)"
if grep -A 2 '"name": "baoc"' <<<"$services" | grep -q '"active": false'; then
	echo "ok   UE 1 let its outgoing calls through again"
else
	echo "FAIL UE 1 could not stop barring its outgoing calls"
	fail=1
fi
# The barring is off, so the trap that would take it off has nothing
# left to do.
trap - EXIT

await "the 200 OK of the MRFC after its unlock" "$mrfcAnswers" $((mrfcAnswered + 1))

echo "drain of the IP-SM-GW"
# The IP-SM-GW takes its work from the S-CSCF by the filter criteria of
# the served user, so the drain shows in a short message that UE 1
# sends. The IP-SM-GW answers each MESSAGE it accepts with 202, so count
# those first.
gwAccepts='sip.CSeq.method == "MESSAGE" && sip.Status-Code == 202 && ip.src == 172.28.0.70'
gwAccepted="$(count "$gwAccepts")"
# The scenes above left the IP-SM-GW nothing to finish: the last UE
# deregistered, and no short message has been sent, so no delivery
# waits for a delivery report either. Wait until it says so, so that
# the shut down below is the shut down of a function with no user,
# which X.731 7.1.3.3 makes LOCKED at once.
if until_state ipsmgw '"users":0'; then
	echo "ok   the IP-SM-GW has nothing in its hands before the shut down"
else
	echo "FAIL the IP-SM-GW still counts users: $(node_state ipsmgw || true)"
	fail=1
fi
answer="$(administrative_state ipsmgw SHUTTINGDOWN || true)"
state="$(node_state ipsmgw || true)"
if grep -q '"administrative_state":"LOCKED"' <<<"$state"; then
	echo "ok   the maintenance API shut the IP-SM-GW down, which locked it at once"
else
	echo "FAIL the shut down of the IP-SM-GW: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# "ue sms" registers, sends the short message and waits for the submit
# report. The MESSAGE gets 404 here, so the command exits 1, hence
# "|| true"; the registration itself goes through, since the criterion
# of the third-party REGISTER says SESSION_CONTINUED.
out="$(docker compose run --no-deps --rm ue sms +12025550102 'Is anyone on duty?' 2>&1 || true)"
if grep -q '"msg":"registered"' <<<"$out" &&
	grep -q '"msg":"sms not sent".*MESSAGE answered 404' <<<"$out"; then
	echo "ok   UE 1 registered but could not send a short message while the IP-SM-GW is shut down"
else
	echo "FAIL the short message while the IP-SM-GW is shut down: $(tail -n 2 <<<"$out")"
	fail=1
fi
answer="$(administrative_state ipsmgw UNLOCKED || true)"
state="$(node_state ipsmgw || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the IP-SM-GW"
else
	echo "FAIL the unlock of the IP-SM-GW: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# UE 2 is not running, so the SC cannot deliver the short message; what
# this checks is that the IP-SM-GW accepts it again and the submit
# report comes back.
out="$(docker compose run --no-deps --rm ue sms +12025550102 'Back on duty?' 2>&1 || true)"
if grep -q '"msg":"submit report"' <<<"$out"; then
	echo "ok   UE 1 sent a short message after the unlock of the IP-SM-GW"
else
	echo "FAIL the short message after the unlock of the IP-SM-GW: $(tail -n 2 <<<"$out")"
	fail=1
fi

await "the 202 of the IP-SM-GW after its unlock" "$gwAccepts" $((gwAccepted + 1))

echo "drain of the HSS"
# The HSS has no SIP to answer with: it ends each Diameter connection
# with a DPR whose Disconnect-Cause is REBOOTING and stops listening on
# 3868, so the CSCFs have no HSS until the unlock. Count what is there
# first: each node opens its connection again only after Tc, 30 s.
hss_address=172.28.0.10
dprs="$(count "diameter.cmd.code == 282 && diameter.flags.request == 1 && ip.src == $hss_address")"
icscf_opens="$(diameter_opens icscf)"
scscf_opens="$(diameter_opens scscf)"
ipsmgw_opens="$(diameter_opens ipsmgw)"
mmtel_opens="$(diameter_opens mmtel)"
answer="$(administrative_state hss SHUTTINGDOWN || true)"
state="$(node_state hss || true)"
if grep -Eq '"administrative_state":"(SHUTTINGDOWN|LOCKED)"' <<<"$state"; then
	echo "ok   the maintenance API shut the HSS down"
else
	echo "FAIL the shut down of the HSS: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# With no instance of the HSS left, the I-CSCF cannot work, which it
# says in its operational state (TS 28.622 5.3.3) without the operator
# having taken it out of service.
if until_state icscf '"operational_state":"DISABLED"'; then
	echo "ok   the I-CSCF reports itself DISABLED without the HSS"
else
	echo "FAIL the I-CSCF stayed ENABLED without the HSS: $(node_state icscf || true)"
	fail=1
fi
if docker compose run --no-deps --rm ue register -deregister-after 2s 2>/dev/null; then
	echo "FAIL UE registered with a shut down HSS"
	fail=1
else
	echo "ok   UE could not register while the HSS is shut down"
fi
# The gauge is read from the state of the connection at each scrape, so
# the closed connection shows without anything writing to it.
peer_up='ims_diameter_peer_up{node="icscf"}'
if value="$(until_equal prometheus_values "$peer_up" 0)"; then
	echo "ok   $peer_up is $value while the HSS is out of service"
else
	echo "FAIL $peer_up did not fall to 0 while the HSS is out of service: $value"
	fail=1
fi
answer="$(administrative_state hss UNLOCKED || true)"
state="$(node_state hss || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the HSS"
else
	echo "FAIL the unlock of the HSS: $answer"
	echo "     state of the node: $state"
	fail=1
fi
# REBOOTING lets the peer come back (RFC 6733 5.4.3), which it does at
# its next attempt after Tc.
if wait_for_opens icscf $((icscf_opens + 1)) && wait_for_opens scscf $((scscf_opens + 1)) &&
	wait_for_opens ipsmgw $((ipsmgw_opens + 1)) && wait_for_opens mmtel $((mmtel_opens + 1)); then
	echo "ok   every peer of the HSS opened its Diameter connection again"
else
	echo "FAIL a peer of the HSS did not open its Diameter connection again after the unlock"
	echo "     opens so far: icscf $(diameter_opens icscf), scscf $(diameter_opens scscf), ipsmgw $(diameter_opens ipsmgw), mmtel $(diameter_opens mmtel)"
	fail=1
fi
if value="$(until_positive prometheus_values "$peer_up")"; then
	echo "ok   $peer_up is $value again after the unlock of the HSS"
else
	echo "FAIL $peer_up did not come back to 1 after the unlock of the HSS: $value"
	fail=1
fi
registrations="$(count "$registered")"
if docker compose run --no-deps --rm ue register -deregister-after 2s; then
	echo "ok   UE registered after the unlock of the HSS"
else
	echo "FAIL UE registration after the unlock of the HSS"
	fail=1
fi
await "the 200 OK to the registration after the unlock of the HSS" "$registered" $((registrations + 1))
# One DPR per peer: the two Cx connections and the two Sh connections.
await "the DPRs of the drained HSS" "diameter.cmd.code == 282 && diameter.flags.request == 1 && ip.src == $hss_address" $((dprs + 4))
same_nodes

echo "reading $pcap"
last=0
ue=172.28.0.100
pcscf=172.28.0.40
icscf=172.28.0.20
scscf=172.28.0.30
mmtel=172.28.0.75
mrfc=172.28.0.85
mrfp=172.28.0.86
ipsmgw=172.28.0.70
agw=172.28.0.50
register='sip.CSeq.method == "REGISTER"'
invite='sip.CSeq.method == "INVITE"'

echo "the REGISTER while the S-CSCF is shut down"
expect "503 S-CSCF -> I-CSCF with Retry-After 30" "$register && sip.Status-Code == 503 && ip.src == $scscf && ip.dst == $icscf && sip.Retry-After == 30"
# A proxy does not pass a 503 on (RFC 3261 16.7 step 6): the I-CSCF has
# no other S-CSCF to try, so it answers 500 of its own.
expect "500 I-CSCF -> P-CSCF without Retry-After" "$register && sip.Status-Code == 500 && ip.src == $icscf && ip.dst == $pcscf && !sip.Retry-After"
expect "500 P-CSCF -> UE without Retry-After" "$register && sip.Status-Code == 500 && ip.src == $pcscf && ip.dst == $ue && !sip.Retry-After"
echo "the REGISTER after the unlock of the S-CSCF"
expect "401 S-CSCF -> I-CSCF" "$register && sip.Status-Code == 401 && ip.src == $scscf"
expect "200 S-CSCF -> I-CSCF" "$register && sip.Status-Code == 200 && ip.src == $scscf"
echo "the REGISTER while the P-CSCF is shut down"
# The P-CSCF answers itself, with a Retry-After longer than Timer F of
# the UE (TS 24.229 5.1.1.2.1), and sends the REGISTER nowhere.
before="$last"
expect "503 P-CSCF -> UE with Retry-After 300" "$register && sip.Status-Code == 503 && ip.src == $pcscf && ip.dst == $ue && sip.Retry-After == 300"
if [ "$last" != "$before" ]; then
	refused="sip.Call-ID == \"$(field sip.Call-ID)\""
	none "REGISTERs of that call that the P-CSCF sent on" "sip.Method == \"REGISTER\" && ip.src == $pcscf && $refused"
fi
echo "the REGISTER after the unlock of the P-CSCF"
expect "401 P-CSCF -> UE" "$register && sip.Status-Code == 401 && ip.src == $pcscf && ip.dst == $ue"
expect "200 P-CSCF -> UE" "$register && sip.Status-Code == 200 && ip.src == $pcscf && ip.dst == $ue"
echo "the REGISTER while the I-CSCF is shut down"
# The I-CSCF answers 503 without asking the HSS. The P-CSCF has no other
# I-CSCF to try, so it answers the UE 504 (TS 24.229 5.2.2.1 item 7).
before="$last"
expect "503 I-CSCF -> P-CSCF with Retry-After 30" "$register && sip.Status-Code == 503 && ip.src == $icscf && ip.dst == $pcscf && sip.Retry-After == 30"
if [ "$last" != "$before" ]; then
	refused="sip.Call-ID == \"$(field sip.Call-ID)\""
	none "REGISTERs of that call that the I-CSCF sent on" "sip.Method == \"REGISTER\" && ip.src == $icscf && $refused"
fi
expect "504 P-CSCF -> UE without Retry-After" "$register && sip.Status-Code == 504 && ip.src == $pcscf && ip.dst == $ue && !sip.Retry-After"
echo "the REGISTER after the unlock of the I-CSCF"
expect "401 I-CSCF -> P-CSCF" "$register && sip.Status-Code == 401 && ip.src == $icscf && ip.dst == $pcscf"
expect "200 I-CSCF -> P-CSCF" "$register && sip.Status-Code == 200 && ip.src == $icscf && ip.dst == $pcscf"

echo "the INVITE while the MMTel AS is shut down"
# The AS answers 503 and sends nothing on. The S-CSCF has no other
# instance of the AS to try, and the filter criterion of the
# originating case says SESSION_TERMINATED, so it ends the call with a
# 500 of its own: a proxy does not pass a 503 on (RFC 3261 16.7 step 6).
expect "INVITE S-CSCF -> MMTel AS" "sip.Method == \"INVITE\" && ip.src == $scscf && ip.dst == $mmtel"
refusedInvite="$last"
expect "503 MMTel AS -> S-CSCF with Retry-After 30" "$invite && sip.Status-Code == 503 && ip.src == $mmtel && ip.dst == $scscf && sip.Retry-After == 30"
# The AS is a B2BUA, so the INVITE it would have sent on has a Call-ID
# of its own: what shows that it sent none is that no INVITE left it
# between the one it was given and its 503.
none "INVITEs that the AS sent on while it was shut down" "sip.Method == \"INVITE\" && ip.src == $mmtel && frame.number > $refusedInvite && frame.number < $last"
expect "500 S-CSCF -> P-CSCF without Retry-After" "$invite && sip.Status-Code == 500 && ip.src == $scscf && ip.dst == $pcscf && !sip.Retry-After"
expect "500 P-CSCF -> UE without Retry-After" "$invite && sip.Status-Code == 500 && ip.src == $pcscf && ip.dst == $ue && !sip.Retry-After"
echo "the Ut request while the MMTel AS is shut down"
# Ut has the administrative state of the AS, so the XCAP server answers
# 503 with Retry-After, without the challenge of HTTP Digest that it
# would answer an unauthenticated GET with (RFC 9110 15.6.4).
expect "GET UE 1 -> XCAP server" "http.request.method == \"GET\" && ip.src == $ue && ip.dst == $mmtel"
expect "503 XCAP server -> UE 1 with Retry-After 30" "http.response.code == 503 && ip.src == $mmtel && ip.dst == $ue && http.response.line matches \"^retry-after: 30\""
echo "the Ut request after the unlock of the MMTel AS"
expect "401 XCAP server -> UE 1 with the challenge of HTTP Digest" "http.response.code == 401 && ip.src == $mmtel && ip.dst == $ue && http.www_authenticate"
expect "200 XCAP server -> UE 1 with the settings" "http.response.code == 200 && ip.src == $mmtel && ip.dst == $ue"
echo "the call after the unlock of the MMTel AS"
# The AS is the B2BUA of the call again: it answers the INVITE of the
# S-CSCF on one leg and sends a new INVITE back to the S-CSCF on the
# other (TS 24.229 5.7.5).
expect "INVITE S-CSCF -> MMTel AS" "sip.Method == \"INVITE\" && ip.src == $scscf && ip.dst == $mmtel"
expect "INVITE MMTel AS -> S-CSCF of the second leg" "sip.Method == \"INVITE\" && ip.src == $mmtel && ip.dst == $scscf"
expect "200 MMTel AS -> S-CSCF" "$invite && sip.Status-Code == 200 && ip.src == $mmtel && ip.dst == $scscf"

echo "the announcement of the barred call while the MRFC is shut down"
# The AS asks the MRFC for the announcement of RFC 4240 3. The MRFC
# answers 503 with Retry-After and sets up no media, so the AS rejects
# the barred call without the early media it would have authorized, and
# the 603 carries no Reason: the AS adds that only to a rejection the
# caller has heard an announcement before (TS 24.628 4.2.4).
expect "INVITE MMTel AS -> MRFC for the announcement notice" "sip.Method == \"INVITE\" && ip.src == $mmtel && ip.dst == $mrfc && sip.r-uri contains \"annc@\""
refusedAnnouncement="$last"
expect "503 MRFC -> MMTel AS with Retry-After 30" "$invite && sip.Status-Code == 503 && ip.src == $mrfc && ip.dst == $mmtel && sip.Retry-After == 30"
none "2xx of the MRFC while it was shut down" "frame.number > $refusedAnnouncement && frame.number <= $last && $invite && sip.Status-Code == 200 && ip.src == $mrfc"
expect "603 P-CSCF -> UE 1 for the barred call, without a Reason" "sip.Status-Code == 603 && ip.src == $pcscf && ip.dst == $ue && !sip.Reason"
none "responses that authorize early media for UE 1 while the MRFC was shut down" "frame.number > $refusedAnnouncement && frame.number < $last && sip.P-Early-Media && ip.dst == $ue"
echo "the announcement after the unlock of the MRFC"
expect "INVITE MMTel AS -> MRFC for the announcement notice" "sip.Method == \"INVITE\" && ip.src == $mmtel && ip.dst == $mrfc && sip.r-uri contains \"annc@\""
expect "200 MRFC -> MMTel AS with the SDP of the MRFP" "$invite && sip.Status-Code == 200 && ip.src == $mrfc && sdp.connection_info.address == \"$mrfp\""
expect "183 P-CSCF -> UE 1 with P-Early-Media: sendonly" "sip.Status-Code == 183 && ip.dst == $ue && sip.P-Early-Media == \"sendonly\""
expect "RTP of notice media relay -> UE 1" "rtp && ip.src == $agw && ip.dst == $ue"
expect "BYE MRFC -> MMTel AS once notice has played" "sip.Method == \"BYE\" && ip.src == $mrfc && ip.dst == $mmtel"
expect "603 P-CSCF -> UE 1 with Reason: SIP;cause=603" "sip.Status-Code == 603 && ip.src == $pcscf && ip.dst == $ue && sip.Reason contains \"cause=603\""

echo "the short message while the IP-SM-GW is shut down"
# The S-CSCF sends the third-party REGISTER of UE 1's registration and
# the MESSAGE of its short message to the IP-SM-GW, which answers 503
# with Retry-After to both. The criteria say SESSION_CONTINUED, so the
# registration stands and the MESSAGE goes on to its Request-URI, the
# PSI of the SC. No node of this network answers at that number and
# ENUM has no record for it, so the S-CSCF answers 404 (TS 24.229
# 5.4.3.2 step 10).
#
# The S-CSCF answers the UE before it registers the user anywhere else
# (TS 24.229 5.4.1.7), so the 200 of the registration comes before the
# third-party REGISTER that the IP-SM-GW refuses. The Service-Route
# tells that 200 from the one of the deregistration that ended the
# scene above, which carries none.
message='sip.CSeq.method == "MESSAGE"'
expect "200 S-CSCF -> I-CSCF for UE 1's registration, with Service-Route" "$register && sip.Status-Code == 200 && ip.src == $scscf && ip.dst == $icscf && sip.Service-Route && sip.To contains \"+12025550101\""
expect "third-party REGISTER S-CSCF -> IP-SM-GW" "sip.Method == \"REGISTER\" && ip.src == $scscf && ip.dst == $ipsmgw"
refusedRegister="$last"
expect "503 IP-SM-GW -> S-CSCF for the third-party REGISTER, with Retry-After 30" "$register && sip.Status-Code == 503 && ip.src == $ipsmgw && ip.dst == $scscf && sip.Retry-After == 30"
expect "MESSAGE S-CSCF -> IP-SM-GW with the short message" "sip.Method == \"MESSAGE\" && ip.src == $scscf && ip.dst == $ipsmgw && sip.Content-Type == \"application/vnd.3gpp.sms\""
refusedMessage="$last"
expect "503 IP-SM-GW -> S-CSCF with Retry-After 30" "$message && sip.Status-Code == 503 && ip.src == $ipsmgw && ip.dst == $scscf && sip.Retry-After == 30"
expect "404 P-CSCF -> UE 1 for the short message" "$message && sip.Status-Code == 404 && ip.src == $pcscf && ip.dst == $ue"
none "202 of the IP-SM-GW while it was shut down" "frame.number > $refusedMessage && frame.number <= $last && $message && sip.Status-Code == 202 && ip.src == $ipsmgw"
# The refused third-party REGISTER leaves no subscription behind: the
# IP-SM-GW subscribes to the registration state only for one it took.
none "SUBSCRIBEs of the IP-SM-GW while it was shut down" "frame.number > $refusedRegister && frame.number <= $last && sip.Method == \"SUBSCRIBE\" && ip.src == $ipsmgw"
echo "the short message after the unlock of the IP-SM-GW"
expect "MESSAGE S-CSCF -> IP-SM-GW with the short message" "sip.Method == \"MESSAGE\" && ip.src == $scscf && ip.dst == $ipsmgw && sip.Content-Type == \"application/vnd.3gpp.sms\""
expect "202 IP-SM-GW -> S-CSCF" "$message && sip.Status-Code == 202 && ip.src == $ipsmgw && ip.dst == $scscf"
expect "submit report IP-SM-GW -> I-CSCF, in reply to the short message" "sip.Method == \"MESSAGE\" && ip.src == $ipsmgw && ip.dst == $icscf && sip.In-Reply-To"

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color pcscf icscf scscf hss mmtel mrfc ipsmgw prometheus grafana | tail -n 80
fi
exit "$fail"
