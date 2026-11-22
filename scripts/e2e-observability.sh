#!/usr/bin/env bash
# End-to-end check: the S-CSCF serves its metrics and its maintenance
# API on its maintenance listener (deploy/compose/scscf.yaml). After a
# registration, Prometheus has scraped the S-CSCF and holds a count of
# successful registrations, and Grafana returns that count through the
# Prometheus data source that the dashboard uses
# (deploy/observability). A shut down through the maintenance API then
# makes the S-CSCF answer a new REGISTER with 503 and Retry-After, which
# the capture shows hop by hop, and an unlock lets the UE register
# again.
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
# Windows has a python3 stub that only opens the Store.
python=python3
"$python" -c 1 2>/dev/null || python=python

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
# returns from the HTTP API of Prometheus, one per line.
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
# nobody has changed here.
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

# administrative_state asks the maintenance API of the S-CSCF for the
# administrative state $1, as the registration guide does, and prints
# the answer. The API is not published on the host, so the request goes
# from inside the container of the S-CSCF, with the nc of the image.
administrative_state() {
	docker compose exec -T scscf sh -c '
body="{\"administrative_state\":\"$1\",\"operator\":\"e2e\"}"
printf "PUT /maintenance/v1/functions/scscf/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
	nc -w 5 127.0.0.1 8085' sh "$1" | tr -d '\r'
}

# node_state prints the state of the node of the S-CSCF, from its
# maintenance API.
node_state() {
	docker compose exec -T scscf wget -qO- http://127.0.0.1:8085/maintenance/v1/state | tr -d '\r'
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
	curl -fsS "$prometheus/api/v1/targets?state=active" || true
	echo
	fail=1
fi
successes='ims_registration_successes_total{role="scscf"}'
if value="$(until_positive prometheus_values "$successes")"; then
	echo "ok   Prometheus has $successes = $value"
else
	echo "FAIL Prometheus has no $successes above 0: $value"
	docker compose exec -T scscf wget -qO- http://127.0.0.1:8085/metrics || true
	fail=1
fi
if value="$(until_positive grafana_values "$successes")"; then
	echo "ok   Grafana returns $successes = $value from its Prometheus data source"
else
	echo "FAIL Grafana returns no $successes above 0: $value"
	fail=1
fi

echo "drain"
# No transaction is in progress, so the shut down makes the S-CSCF
# LOCKED at once; SHUTTINGDOWN refuses a new REGISTER all the same.
answer="$(administrative_state SHUTTINGDOWN || true)"
state="$(node_state || true)"
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
answer="$(administrative_state UNLOCKED || true)"
state="$(node_state || true)"
if grep -q '"administrative_state":"UNLOCKED"' <<<"$state"; then
	echo "ok   the maintenance API unlocked the S-CSCF"
else
	echo "FAIL the unlock of the S-CSCF: $answer"
	echo "     state of the node: $state"
	fail=1
fi
if docker compose run --no-deps --rm ue register -deregister-after 2s; then
	echo "ok   UE registered after the unlock"
else
	echo "FAIL UE registration after the unlock"
	fail=1
fi
same_nodes
sleep 1

pcap="$(docker compose exec -T capture sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
echo "reading $pcap"
last=0
ue=172.28.0.100
pcscf=172.28.0.40
icscf=172.28.0.20
scscf=172.28.0.30
register='sip.CSeq.method == "REGISTER"'

echo "the REGISTER while the S-CSCF is shut down"
expect "503 S-CSCF -> I-CSCF with Retry-After 30" "$register && sip.Status-Code == 503 && ip.src == $scscf && ip.dst == $icscf && sip.Retry-After == 30"
expect "503 I-CSCF -> P-CSCF" "$register && sip.Status-Code == 503 && ip.src == $icscf && ip.dst == $pcscf"
expect "504 P-CSCF -> UE" "$register && sip.Status-Code == 504 && ip.src == $pcscf && ip.dst == $ue"
echo "the REGISTER after the unlock"
expect "401 S-CSCF -> I-CSCF" "$register && sip.Status-Code == 401 && ip.src == $scscf"
expect "200 S-CSCF -> I-CSCF" "$register && sip.Status-Code == 200 && ip.src == $scscf"

if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color scscf prometheus grafana | tail -n 80
fi
exit "$fail"
