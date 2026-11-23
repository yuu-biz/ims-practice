#!/usr/bin/env bash
# End-to-end check: the three CSCFs serve their metrics and their
# maintenance API on their maintenance listeners
# (deploy/compose/pcscf.yaml, deploy/compose/icscf.yaml,
# deploy/compose/scscf.yaml). After a registration, Prometheus has
# scraped all three and holds their counts of successful registrations,
# with the set-up time that the P-CSCF measures, and Grafana returns the
# count of the S-CSCF through the Prometheus data source that the
# dashboard uses (deploy/observability). A call adds the terminating
# session establishment that the I-CSCF counts. A shut down through the
# maintenance API then makes the S-CSCF answer a new REGISTER with 503
# and Retry-After, which the capture shows hop by hop, and an unlock
# lets the UE register again. A shut down of the P-CSCF makes the
# P-CSCF answer the REGISTER itself with 503 and Retry-After, forwarding
# nothing, and a shut down of the I-CSCF makes the I-CSCF answer 503,
# after which the P-CSCF, with no other I-CSCF left, answers the UE 504.
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
attempts='ims_session_attempts_total{role="icscf",direction="terminating"}'
if value="$(until_positive prometheus_values "$attempts")"; then
	echo "ok   Prometheus has $attempts = $value"
else
	echo "FAIL Prometheus has no $attempts above 0: $value"
	# Shown only to explain the failure, if the I-CSCF answers at all,
	# hence "|| true".
	docker compose exec -T icscf wget -qO- http://127.0.0.1:8085/metrics || true
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
same_nodes
await "the 200 OK to the registration after the unlock of the I-CSCF" "$registered" $((registrations + 1))

echo "reading $pcap"
last=0
ue=172.28.0.100
pcscf=172.28.0.40
icscf=172.28.0.20
scscf=172.28.0.30
register='sip.CSeq.method == "REGISTER"'

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

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color pcscf icscf scscf prometheus grafana | tail -n 80
fi
exit "$fail"
