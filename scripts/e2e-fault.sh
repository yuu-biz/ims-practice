#!/usr/bin/env bash
# End-to-end check: the fault agent runs beside the IMS and breaks a
# node when its API is asked to. It freezes a node and lets it go again,
# makes the node's process die and watches the container runtime bring
# it back, and has the container replaced by a new one with the same
# name and address.
#
# The network faults (loss, delay, partition) are not here: they need
# the fault-tools image and a kernel with the egress hook of nftables
# and with netem, which deploy/chaos/README.md explains. The node faults
# need only the Engine.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
export MSYS_NO_PATHCONV=1
# The agent starts only with its own override file, and chaos.yaml is
# the one a learner has on top as well.
export COMPOSE_FILE=compose.yaml:deploy/chaos/chaos.yaml:deploy/chaos/agent.yaml
export COMPOSE_PATH_SEPARATOR=:
if docker buildx inspect ims-practice >/dev/null 2>&1; then
	export BUILDX_BUILDER=ims-practice
fi

# The node that the faults hit. It carries no registration and no call
# in this scenario, so breaking it disturbs nothing else.
target=mmtel
api=http://127.0.0.1:8070/fault/v1/faults
# The same API through the Lab, as the page reaches it.
lab=http://localhost:8080/api/fault/faults

fail=0
# Built first and started apart. Every node's service builds the same
# image name, and "up --build" labels each container with the digest of
# its own service's build, while a later "up" compares them with the one
# image the name points to last. The check below, that Compose leaves
# the replaced container alone, would then fail for every node, not
# only for the one the fault replaced.
docker compose build
docker compose up -d

for i in $(seq 1 60); do
	curl -fsS "$api" >/dev/null 2>&1 && break
	sleep 1
done
faults="$(curl -fsS "$api")" || { echo "FAIL the fault agent does not answer $api"; exit 1; }
if [ "$faults" = "[]" ]; then
	echo "ok   the fault agent answers its API and holds no fault"
else
	echo "FAIL the agent answers $faults, want no fault"
	fail=1
fi
docker compose ps fault-agent

container="$(docker compose ps -q "$target" | first_line)"
[ -n "$container" ] || { echo "FAIL no container of $target"; exit 1; }
address="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' "$container")"

# inject URL BODY prints the id of the fault the agent took, and stops
# the script when it took none. URL is the agent's API, or the Lab's,
# which passes the request on.
inject() {
	local body
	# The messages go to the error output, because the caller reads the
	# id from this function.
	body="$(curl -sS -X POST -H 'Content-Type: application/json' -d "$2" "$1")" || { echo "FAIL $1 did not answer $2" >&2; exit 1; }
	case $body in
	*'"state":"active"'* | *'"state":"done"'*) ;;
	*)
		echo "FAIL $2: $body" >&2
		exit 1
		;;
	esac
	sed -n 's/.*"id":"\([0-9a-f]*\)".*/\1/p' <<<"$body"
}

# state_is WANT SECONDS waits for the container of the target to be in
# that state.
state_is() {
	local want="$1" seconds="$2" i got
	for i in $(seq 1 "$seconds"); do
		# A container that is gone has no state, hence "|| true"; the
		# FAIL line below names it "gone".
		got="$(docker inspect -f '{{.State.Status}}' "$container" 2>/dev/null || true)"
		if [ "$got" = "$want" ]; then
			echo "ok   $target is $want"
			return 0
		fi
		sleep 1
	done
	echo "FAIL $target is ${got:-gone}, want $want"
	fail=1
}

echo "== freeze the node and let it go again"
id="$(inject "$api" "{\"target\":\"$target\",\"kind\":\"pause\",\"duration\":\"2m\"}")"
state_is paused 20
curl -fsS -X DELETE "$api/$id" >/dev/null
state_is running 20

# The browser reaches the agent through the Lab alone: the agent is on
# the network chaos, which agent.yaml gives the Lab an address on, and
# the IMS nodes do not join it.
echo "== freeze the node through the Lab"
for i in $(seq 1 60); do
	curl -fsS "$lab" >/dev/null 2>&1 && break
	sleep 1
done
if [ "$(curl -fsS "$lab")" = "$(curl -fsS "$api")" ]; then
	echo "ok   the Lab lists the faults of the agent"
else
	echo "FAIL the Lab lists $(curl -fsS "$lab"), the agent $(curl -fsS "$api")"
	fail=1
fi
id="$(inject "$lab" "{\"target\":\"$target\",\"kind\":\"pause\",\"duration\":\"2m\"}")"
state_is paused 20
curl -fsS -X DELETE "$lab/$id" >/dev/null
state_is running 20
if grep -q '"state":"cleared"' <<<"$(curl -fsS "$api/$id")"; then
	echo "ok   the agent holds the fault $id of the Lab as cleared"
else
	echo "FAIL the agent holds $id as $(curl -fsS "$api/$id")"
	fail=1
fi

# crashes_and_comes_back makes the node's process die and waits for the
# restart policy of its container to start it again. On the container
# that a recreate made, it also shows that the new container carries
# that policy: without it the node would die and stay down.
crashes_and_comes_back() {
	local restarts now i
	restarts="$(docker inspect -f '{{.RestartCount}}' "$container")"
	inject "$api" "{\"target\":\"$target\",\"kind\":\"crash\"}" >/dev/null
	for i in $(seq 1 60); do
		now="$(docker inspect -f '{{.RestartCount}}' "$container" 2>/dev/null || echo "$restarts")"
		[ "$now" -gt "$restarts" ] && break
		sleep 1
	done
	if [ "${now:-$restarts}" -gt "$restarts" ]; then
		echo "ok   the runtime started $target again ($restarts to $now)"
	else
		echo "FAIL $target was never restarted (still $restarts)"
		fail=1
	fi
	state_is running 30
}

echo "== make the node's process die"
crashes_and_comes_back

echo "== replace the container"
inject "$api" "{\"target\":\"$target\",\"kind\":\"recreate\"}" >/dev/null
new=
for i in $(seq 1 60); do
	new="$(docker compose ps -q "$target" | first_line)"
	[ -n "$new" ] && [ "$new" != "$container" ] && break
	sleep 1
done
if [ -n "$new" ] && [ "$new" != "$container" ]; then
	echo "ok   $target runs in a new container"
else
	echo "FAIL $target is still the container it was"
	fail=1
fi
container="$new"
state_is running 30
if [ "$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' "$container")" = "$address" ]; then
	echo "ok   the new container has the address of the old one ($address)"
else
	echo "FAIL the new container is at $(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' "$container"), want $address"
	fail=1
fi
# The name the other nodes reach it by on the Compose network. A
# container that is gone has none, hence "|| true", which the check
# reports.
aliases="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{range .Aliases}}{{.}} {{end}}{{end}}' "$container" 2>/dev/null || true)"
case " $aliases " in
*" $target "*) echo "ok   the new container answers to the name $target" ;;
*)
	echo "FAIL the new container has the network names ${aliases:-none}, want $target among them"
	fail=1
	;;
esac
policy="$(docker inspect -f '{{.HostConfig.RestartPolicy.Name}}' "$container")"
if [ "$policy" = "unless-stopped" ]; then
	echo "ok   the new container keeps the restart policy ($policy)"
else
	echo "FAIL the new container has the restart policy ${policy:-none}, want unless-stopped"
	fail=1
fi
# Compose compares the labels of a container with the service it reads
# from the file, so a container that lost them, or whose configuration
# differs, is replaced here.
docker compose up -d >/dev/null
after="$(docker compose ps -q "$target" | first_line)"
if [ "$after" = "$container" ]; then
	echo "ok   Compose takes the new container for the service $target and leaves it alone"
else
	echo "FAIL Compose replaced the new container of $target"
	fail=1
	container="$after"
fi
echo "== make the process of the new container die"
crashes_and_comes_back

echo "== the faults the agent took"
curl -fsS "$api"
if [ "$fail" -ne 0 ]; then
	docker compose logs --no-color fault-agent "$target" | tail -n 60
fi
exit "$fail"
