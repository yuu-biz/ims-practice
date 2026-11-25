# Helpers for the end-to-end scripts that run on the network of
# compose.yaml with Prometheus and Grafana beside it
# (deploy/observability). Those scripts source this file after
# e2e-lib.sh, whose $python it uses. It is not a scenario of its own.
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

# wait_for_open waits for the first $2 connections of the service $1, 1
# by default, and gives the whole run up when they never come.
wait_for_open() {
	if wait_for_opens "$1" "${2:-1}"; then
		return 0
	fi
	echo "FAIL $1 did not open ${2:-1} Diameter connections within two minutes" >&2
	docker compose logs --no-color "$1" | tail -n 30 >&2
	exit 1
}

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

# start_network starts the network and waits until its scenes can begin.
# Each script that sources this file starts its first scene at once, so
# every Diameter connection that a scene counts or ends must be open by
# then: a node that tried before its peer listened tries again only
# after Tc, 30 s. The I-CSCF, the S-CSCF and the MMTel AS each hold one
# connection to the HSS, Cx or Sh, and the SC one over S6c; the
# IP-SM-GW holds two, Sh to the HSS and SGd to the SC.
start_network() {
	docker compose up -d --build
	wait_for_open icscf
	wait_for_open scscf
	wait_for_open ipsmgw 2
	wait_for_open mmtel
	wait_for_open sc
	wait_for_http Prometheus "$prometheus/-/ready"
	wait_for_http Grafana "$grafana/api/health"
	# ended keeps the log of each UE that ran in the background here.
	mkdir -p e2e-out
}

# ue2 is the identity of UE 2, which answers the calls of UE 1. Flags go
# before the URI of "ue call", as the flag package stops at the first
# argument.
ue2=(--impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org
	--impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org --password test-password-2)

# finish shows the last lines of the logs of the nodes when a check
# failed, and ends the script with the result of its checks.
finish() {
	if [ "$fail" -ne 0 ]; then
		docker compose logs --no-color pcscf icscf scscf hss mmtel mrfc ipsmgw sc agw mrfp prometheus grafana | tail -n 80
	fi
	exit "$fail"
}
