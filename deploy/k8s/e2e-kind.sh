#!/usr/bin/env bash
# End-to-end check of the IMS on kind. It
# makes a kind cluster from kind-cluster.yaml, loads the images, starts
# the capture and the IMS with one of the two DNS overlays, and then
# does what the end-to-end scripts of registration, calls and short
# messages in scripts/ do with Docker Compose: a UE registers, one with a wrong password does
# not, UE 1 calls UE 2, busy, cancelled and unregistered calls fail,
# and UE 1 sends UE 2 a short message with a status report. tshark in
# the capture Pod then checks the key messages in order, as the e2e
# scripts do. Only the addresses differ: they are those of the Pods.
#
#   deploy/k8s/e2e-kind.sh [--keep] cluster-dns|external-dns
#
# --keep leaves the cluster and the images, and prints how to remove
# them. Without it, the script removes what it made, and only that: the
# cluster, the images it built or pulled, the Docker network kind made
# for it, and its work directory. It refuses to start when a cluster of
# its name exists already. It uses a kubeconfig of its own, in the
# output directory, so that the person's kubectl stays as it was.
#
# Environment:
#   KIND_E2E_CLUSTER       cluster name (default ims-practice-e2e), so
#                          that the cluster of README.md is left alone
#   KIND_E2E_EXTRA_CONFIG  a file of top-level keys to add to
#                          kind-cluster.yaml, such as the patches for a
#                          host with cgroup v1 in README.md
#   IMS_IMAGE              an image of the project's Dockerfile built
#                          already; the script then builds none
#   CAPTURE_IMAGE          the same for images/capture
#   KIND_E2E_OUT           where the pcap and, on failure, the logs go
#                          (default e2e-out/kind-<overlay>)
#   KIND, KUBECTL          the commands (default kind and kubectl)
set -euo pipefail
cd "$(dirname "$0")/../.."
# For first_line. The library's other helpers look at Docker Compose
# and are not used here.
. scripts/e2e-lib.sh
export MSYS_NO_PATHCONV=1

usage() {
	echo "usage: deploy/k8s/e2e-kind.sh [--keep] cluster-dns|external-dns" >&2
	exit 2
}
keep=0
overlay=""
for arg in "$@"; do
	case "$arg" in
	--keep) keep=1 ;;
	cluster-dns | external-dns) overlay="$arg" ;;
	*) usage ;;
	esac
done
[ -n "$overlay" ] || usage

KIND="${KIND:-kind}"
KUBECTL="${KUBECTL:-kubectl}"
cluster="${KIND_E2E_CLUSTER:-ims-practice-e2e}"
out="${KIND_E2E_OUT:-e2e-out/kind-$overlay}"
for tool in docker "$KIND" "$KUBECTL"; do
	command -v "$tool" >/dev/null || {
		echo "FAIL $tool is not installed (README.md, Before you start)" >&2
		exit 2
	}
done
for image in ${IMS_IMAGE:+"$IMS_IMAGE"} ${CAPTURE_IMAGE:+"$CAPTURE_IMAGE"}; do
	docker image inspect "$image" >/dev/null 2>&1 || {
		echo "FAIL there is no image $image" >&2
		exit 2
	}
done
# A here-string rather than a pipe: grep -q stops reading at the first
# match, and kind could then die of SIGPIPE, a failure under pipefail.
if grep -qx "$cluster" <<<"$("$KIND" get clusters 2>/dev/null)"; then
	echo "FAIL a kind cluster named $cluster exists already; delete it, or set KIND_E2E_CLUSTER" >&2
	exit 2
fi

mkdir -p "$out"
work="$(mktemp -d)"
kubeconfig="$out/kubeconfig"
node="$cluster-control-plane"
# What this run made, so that the cleanup removes that and nothing else.
made_cluster=0
made_images=()
images_before="$(docker images -q --no-trunc | sort -u)"
# kind makes this Docker network for its first cluster and leaves it
# when the cluster goes.
network="${KIND_EXPERIMENTAL_DOCKER_NETWORK:-kind}"
made_network=0
docker network inspect "$network" >/dev/null 2>&1 || made_network=1

k() { "$KUBECTL" --kubeconfig "$kubeconfig" "$@"; }
kims() { k -n ims "$@"; }

# collect copies the pcap out of the node, and on failure the state and
# the logs of every Pod, before the cluster goes.
capture_pod=""
pcap=""
collect() {
	local status="$1" pod
	if [ -n "$capture_pod" ] && [ -n "$pcap" ]; then
		kims cp -c capture "$capture_pod:$pcap" "$out/kind-$overlay.pcap" >/dev/null 2>&1 &&
			echo "pcap: $out/kind-$overlay.pcap"
	fi
	[ "$status" -ne 0 ] || return 0
	mkdir -p "$out/logs"
	k get pods -A -o wide >"$out/pods.txt" 2>&1
	k get events -A --sort-by=.lastTimestamp >"$out/events.txt" 2>&1
	for pod in $(kims get pods -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do
		kims logs "$pod" --all-containers --prefix >"$out/logs/$pod.log" 2>&1
	done
	if [ "$overlay" = cluster-dns ]; then
		k -n kube-system logs deployment/coredns >"$out/logs/coredns.log" 2>&1
	fi
	cp "$work"/*.log "$out/logs/" 2>/dev/null
	echo "logs: $out/logs"
	cat "$out"/logs/*.log 2>/dev/null | grep -E '"level":"(WARN|ERROR)"' | tail -n 40
}

cleanup() {
	local status=$?
	set +e
	[ "$made_cluster" -eq 0 ] || collect "$status"
	if [ "$keep" -eq 1 ]; then
		echo "kept the cluster; use it with: KUBECONFIG=$kubeconfig kubectl -n ims get pods"
		echo "remove it with: $KIND delete cluster --name $cluster --kubeconfig $kubeconfig"
		[ "${#made_images[@]}" -eq 0 ] || echo "and the images with: docker image rm ${made_images[*]}"
		[ "$made_network" -eq 0 ] || echo "and the network with: docker network rm $network"
	else
		if [ "$made_cluster" -eq 1 ]; then
			"$KIND" delete cluster --name "$cluster" --kubeconfig "$kubeconfig"
			rm -f "$kubeconfig"
			# Another cluster may have come up on it meanwhile; then it stays.
			[ "$made_network" -eq 0 ] || docker network rm "$network" >/dev/null 2>&1
		fi
		[ "${#made_images[@]}" -eq 0 ] || docker image rm "${made_images[@]}" >/dev/null
	fi
	rm -rf "$work"
	exit "$status"
}
trap cleanup EXIT

# made_by_us is true when an image was not on the host before this run.
made_by_us() {
	local id
	id="$(docker image inspect -f '{{.Id}}' "$1" 2>/dev/null)" || return 1
	! grep -qx "$id" <<<"$images_before"
}

# Images. The default build uses tags of its own, so that the images
# ims-practice/ims:dev and ims-practice/capture:dev of Docker Compose
# stay as they are; inside the node they get the names the manifests
# use.
if [ -z "${IMS_IMAGE:-}" ] || [ -z "${CAPTURE_IMAGE:-}" ]; then
	if docker buildx inspect ims-practice >/dev/null 2>&1; then
		export BUILDX_BUILDER=ims-practice
	fi
fi
if [ -z "${IMS_IMAGE:-}" ]; then
	IMS_IMAGE=ims-practice/ims:kind-e2e
	echo "== building $IMS_IMAGE"
	docker buildx build --load -t "$IMS_IMAGE" .
	made_images+=("$IMS_IMAGE")
fi
if [ -z "${CAPTURE_IMAGE:-}" ]; then
	CAPTURE_IMAGE=ims-practice/capture:kind-e2e
	echo "== building $CAPTURE_IMAGE"
	docker buildx build --load -t "$CAPTURE_IMAGE" images/capture
	made_images+=("$CAPTURE_IMAGE")
fi
dns_image=coredns/coredns:1.12.1
if [ "$overlay" = external-dns ] && ! docker image inspect "$dns_image" >/dev/null 2>&1; then
	docker pull "$dns_image"
	made_images+=("$dns_image")
fi

echo "== creating the kind cluster $cluster"
cat deploy/k8s/kind-cluster.yaml ${KIND_E2E_EXTRA_CONFIG:+"$KIND_E2E_EXTRA_CONFIG"} >"$work/kind.yaml"
made_cluster=1
"$KIND" create cluster --name "$cluster" --config "$work/kind.yaml" --kubeconfig "$kubeconfig" --wait 180s
node_image="$(docker inspect -f '{{.Image}}' "$node")"
if made_by_us "$node_image"; then
	made_images+=("$node_image")
fi

# containerd_ref is the full name containerd gives an image that Docker
# calls by a short one.
containerd_ref() {
	local name="$1" first
	[[ "${name##*/}" == *:* ]] || name="$name:latest"
	first="${name%%/*}"
	if [[ "$name" != */* ]]; then
		name="docker.io/library/$name"
	elif [[ "$first" != *.* && "$first" != *:* && "$first" != localhost ]]; then
		name="docker.io/$name"
	fi
	echo "$name"
}
load_as() {
	local image="$1" name="$2"
	"$KIND" load docker-image "$image" --name "$cluster"
	if [ "$(containerd_ref "$image")" != "$(containerd_ref "$name")" ]; then
		docker exec "$node" ctr -n k8s.io images tag --force "$(containerd_ref "$image")" "$(containerd_ref "$name")" >/dev/null
	fi
}
echo "== loading the images"
load_as "$IMS_IMAGE" ims-practice/ims:dev
load_as "$CAPTURE_IMAGE" ims-practice/capture:dev
if [ "$overlay" = external-dns ]; then
	# kind load docker-image fails on an image of several platforms of
	# which Docker holds one (README.md, When it does not work).
	arch="$(docker version -f '{{.Server.Arch}}')"
	docker save --platform "linux/$arch" -o "$work/dns.tar" "$dns_image" 2>/dev/null ||
		docker save -o "$work/dns.tar" "$dns_image"
	"$KIND" load image-archive "$work/dns.tar" --name "$cluster"
fi

echo "== starting the capture and the IMS ($overlay)"
k apply -k deploy/k8s/capture
kims rollout status daemonset/capture --timeout=120s
k apply -k "deploy/k8s/overlays/$overlay"
if [ "$overlay" = cluster-dns ]; then
	k -n kube-system patch deployment coredns --patch-file deploy/k8s/overlays/cluster-dns/coredns-patch.yaml
	k -n kube-system rollout status deployment/coredns --timeout=180s
	dns="$(k -n kube-system get service kube-dns -o jsonpath='{.spec.clusterIP}')"
else
	# ims_dns_cluster_ip of environments/kind.
	dns="$(kims get service dns -o jsonpath='{.spec.clusterIP}')"
fi
for workload in $(kims get statefulset,deployment -o name); do
	# ue-a and ue-b have no replicas until someone scales them up.
	case "$workload" in */ue-a | */ue-b) continue ;; esac
	kims rollout status "$workload" --timeout=180s
done

# The CSCFs and the IP-SM-GW serve requests only once their Diameter
# connections to the HSS are open.
wait_for_open() {
	local pod="$1" i
	# Up to two minutes: a node that tried before the HSS listened
	# tries again after Tc, 30 s (RFC 6733 2.1).
	for i in $(seq 1 120); do
		if grep -q '"to":"I-Open (OKAY)"' <<<"$(kims logs "$pod" -c ims 2>/dev/null)"; then
			return 0
		fi
		sleep 1
	done
	echo "FAIL $pod never opened its Diameter connection" >&2
	return 1
}
wait_for_open icscf-0
wait_for_open scscf-0
wait_for_open ipsmgw-0

pod_ip() { kims get pod "$@" -o jsonpath='{.items[0].status.podIP}'; }
ue1="$(pod_ip -l app.kubernetes.io/name=ue)"
ue2ip="$(pod_ip -l app.kubernetes.io/name=ue2)"
pcscf="$(pod_ip -l app.kubernetes.io/name=pcscf)"
icscf="$(pod_ip -l app.kubernetes.io/name=icscf)"
scscf="$(pod_ip -l app.kubernetes.io/name=scscf)"
hss="$(pod_ip -l app.kubernetes.io/name=hss)"
agw="$(pod_ip -l app.kubernetes.io/name=agw)"
ipsmgw="$(pod_ip -l app.kubernetes.io/name=ipsmgw)"
echo "addresses: ue $ue1, ue2 $ue2ip, pcscf $pcscf, icscf $icscf, scscf $scscf, hss $hss, agw $agw, ipsmgw $ipsmgw, dns $dns"

# The UE simulators wait in their Pods; each command runs inside them.
# Their -pcscf default is the address of Docker Compose.
P=(--pcscf pcscf.ims.mnc001.mcc001.3gppnetwork.org:5060)
U2=(--impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org
	--impu sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org --password test-password-2)
ue() { timeout 90 "$KUBECTL" --kubeconfig "$kubeconfig" -n ims exec deploy/ue -- ue "$1" "${P[@]}" "${@:2}"; }
ue2() { timeout 90 "$KUBECTL" --kubeconfig "$kubeconfig" -n ims exec deploy/ue2 -- ue "$1" "${P[@]}" "${U2[@]}" "${@:2}"; }
# ue2_bg runs ue2 in the background, its output in $work/$1.log, and
# leaves the PID of kubectl in $ue2_pid.
ue2_bg() {
	local log="$work/$1.log"
	shift
	ue2 "$@" >"$log" 2>&1 &
	ue2_pid=$!
}
wait_log() {
	local log="$work/$1.log" pattern="$2" i
	for i in $(seq 1 30); do
		grep -q "$pattern" "$log" 2>/dev/null && return 0
		sleep 1
	done
	return 1
}
# stop_ue2 ends what ue2 runs; the UE deregisters on SIGTERM.
stop_ue2() {
	kims exec deploy/ue2 -- pkill -TERM -x ue >/dev/null 2>&1 || true
	wait "$ue2_pid" 2>/dev/null || true
}

fail=0
ok() { echo "ok   $1"; }
bad() {
	echo "FAIL $1"
	fail=1
}

echo "== registration"
if ue register --deregister-after 2s >/dev/null; then ok "UE registered and deregistered"; else bad "UE registration"; fi
if ue register --password wrong-password >/dev/null 2>&1; then bad "UE with a wrong password registered"; else ok "UE with a wrong password was refused"; fi

echo "== calls"
kims exec deploy/ue -- rm -f /tmp/ue1-sent.raw
kims exec deploy/ue2 -- rm -f /tmp/ue2-received.raw
ue2_bg answer answer --ring 1s --tone 5s --received-out /tmp/ue2-received.raw
wait_log answer '"msg":"waiting for a call"' || true
if ue call --tone 3s --sent-out /tmp/ue1-sent.raw tel:+1-202-555-0102 >/dev/null; then ok "UE 1 called UE 2 by number"; else bad "the call"; fi
if wait "$ue2_pid"; then ok "UE 2 answered and the call ended"; else
	bad "UE 2"
	tail -n 20 "$work/answer.log"
fi

ue2_bg busy answer --reject 486
wait_log busy '"msg":"waiting for a call"' || true
if grep -q '"call failed: 486 ' <<<"$(ue call sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org 2>/dev/null || true)"; then
	ok "busy callee answered 486"
else bad "busy callee"; fi
wait "$ue2_pid" || true

ue2_bg ring answer --ring 30s
wait_log ring '"msg":"waiting for a call"' || true
if grep -q '"call failed: 487 ' <<<"$(ue call --give-up-after 2s tel:+12025550102 2>/dev/null || true)"; then
	ok "unanswered call cancelled with 487"
else bad "unanswered call"; fi
stop_ue2

if grep -q '"call failed: 480 ' <<<"$(ue call tel:+12025550103 2>/dev/null || true)"; then
	ok "unregistered callee answered 480"
else bad "unregistered callee"; fi

echo "== a short message"
gsm_text='Hello UE-B, this is UE-A.'
ue2_bg sms register
if wait_log sms '"msg":"registered"'; then ok "UE 2 registered"; else bad "UE 2 did not register"; fi
if grep -q '"msg":"status report".*"status":0,' <<<"$(ue sms --status-report +12025550102 "$gsm_text" 2>&1 || true)"; then
	ok "UE 1 sent a short message to UE 2, and got a status report that it was delivered"
else bad "UE 1 to UE 2"; fi
if grep -q "\"msg\":\"sms received\",.*\"from\":\"+12025550101\",\"text\":\"$gsm_text\"" "$work/sms.log"; then
	ok "UE 2 received the text from UE 1"
else bad "UE 2 did not receive the text"; fi
stop_ue2
sleep 1

echo "== the capture"
capture_pod="$(kims get pod -l app.kubernetes.io/name=capture -o jsonpath='{.items[0].metadata.name}')"
pcap="$(kims exec "$capture_pod" -- sh -c 'ls -t /captures/ims-*.pcap | head -n 1' | tr -d '\r')"
# tshark reads a copy in /tmp of the capture Pod, owned by root, the user
# kubectl exec runs as. On a host whose AppArmor has a profile for
# /usr/bin/tshark (AppArmor 5.0 has one, and Ubuntu 26.04 loads it), that
# profile confines the tshark of the Pod too: the Pods of kind run with
# no AppArmor profile of their own, and the host's profiles attach by the
# path of the program. It lets tshark read files it owns under /tmp, but
# nothing under /captures.
copy=/tmp/ims-e2e.pcap
kims exec "$capture_pod" -- cp "$pcap" "$copy"
echo "reading $pcap, from a copy in $copy"

# tshark_identity prints who a tshark in the capture Pod runs as: its
# user and its AppArmor profile, read while it waits on an empty pipe.
tshark_identity() {
	kims exec "$capture_pod" -- sh -c '
		sleep 2 | tshark -r - >/dev/null 2>&1 &
		pid=$!
		sleep 1
		uid="$(awk "/^Uid:/ { print \$2 }" /proc/$pid/status 2>/dev/null)"
		label="$(cat /proc/$pid/attr/apparmor/current 2>/dev/null || cat /proc/$pid/attr/current 2>/dev/null)"
		echo "tshark in the capture Pod: uid ${uid:-unknown}, AppArmor profile: ${label:-none}"
		wait' | tr -d '\r'
}
tshark_identity

# tshark_fields prints the fields of the frames that pass the filter, one
# frame per line, the fields separated by "|".
tshark_fields() {
	local filter="$1" output args=()
	shift
	for f in "${@:-frame.number}"; do args+=(-e "$f"); done
	if ! output="$(kims exec "$capture_pod" -- tshark -r "$copy" -o rtp.heuristic_rtp:TRUE -Y "$filter" -T fields -E separator='|' "${args[@]}" 2>&1)"; then
		echo "tshark failed on filter: $filter" >&2
		echo "$output" >&2
		kims exec "$capture_pod" -- ls -ln "$pcap" "$copy" >&2
		tshark_identity >&2
		exit 2
	fi
	printf '%s\n' "$output" | tr -d '\r'
}
# expect finds the first frame after the previous match that passes the
# filter, so the checks assert the order of each flow. It leaves the
# frame in $last.
last=0
expect() {
	local label="$1" filter="$2" frame
	frame="$(tshark_fields "frame.number > $last && ($filter)" | first_line)"
	if [ -n "$frame" ]; then
		echo "ok   #$frame $label"
		last="$frame"
	else
		bad "$label (after frame $last)"
	fi
}
field() { tshark_fields "frame.number == $last" "$1"; }
# answer is the filter for the Diameter answer to the request that
# expect matched last.
answer() {
	local code sid
	code="$(field diameter.cmd.code | cut -d, -f1)"
	sid="$(field diameter.Session-Id | cut -d, -f1)"
	echo "diameter.flags.request == 0 && diameter.cmd.code == $code && diameter.Session-Id == \"$sid\""
}
none() {
	local label="$1" filter="$2" n
	n="$(tshark_fields "$filter" | grep -c . || true)"
	if [ "$n" -eq 0 ]; then ok "$label: none"; else bad "$label: $n, want none"; fi
}

register='sip.Method == "REGISTER"'
invite='sip.Method == "INVITE"'
message='sip.Method == "MESSAGE"'
sms='sip.Content-Type == "application/vnd.3gpp.sms"'
uar='diameter.cmd.code == 300'
sar='diameter.cmd.code == 301'
lir='diameter.cmd.code == 302'
mar='diameter.cmd.code == 303'
req='diameter.flags.request == 1'
ans='diameter.flags.request == 0'

first_register=0
echo "registration"
expect "REGISTER UE -> P-CSCF" "$register && ip.src == $ue1 && ip.dst == $pcscf"
first_register="$last"
expect "NAPTR query for the home domain" "dns.qry.type == 35 && dns.flags.response == 0 && ip.dst == $dns"
expect "REGISTER P-CSCF -> I-CSCF with Path" "$register && ip.src == $pcscf && ip.dst == $icscf && sip.Path"
expect "UAR I-CSCF -> HSS" "$uar && $req && ip.src == $icscf && ip.dst == $hss && diameter.Public-Identity"
expect "UAA first registration" "$uar && $ans && ip.src == $hss && diameter.Experimental-Result-Code == 2001"
expect "REGISTER I-CSCF -> S-CSCF" "$register && ip.src == $icscf && ip.dst == $scscf"
expect "MAR S-CSCF -> HSS" "$mar && $req && ip.src == $scscf && ip.dst == $hss"
expect "MAA with SIP Digest and H(A1)" "$mar && $ans && ip.src == $hss && diameter.Result-Code == 2001 && diameter.Digest-HA1"
expect "401 P-CSCF -> UE" "sip.Status-Code == 401 && ip.src == $pcscf && ip.dst == $ue1"
expect "REGISTER UE -> P-CSCF with a response" "$register && ip.src == $ue1 && sip.Authorization contains \"response=\""
expect "UAA subsequent registration with Server-Name" "$uar && $ans && ip.src == $hss && diameter.Experimental-Result-Code == 2002 && diameter.Server-Name"
expect "SAR REGISTRATION" "$sar && $req && ip.src == $scscf && diameter.Server-Assignment-Type == 1"
expect "SAA with User-Data" "$sar && $ans && ip.src == $hss && diameter.Result-Code == 2001 && diameter.Cx-User-Data"
expect "200 OK P-CSCF -> UE with P-Associated-URI" "sip.Status-Code == 200 && sip.CSeq.method == \"REGISTER\" && ip.src == $pcscf && ip.dst == $ue1 && sip.P-Associated-URI"
expect "REGISTER Expires 0" "$register && ip.src == $ue1 && sip.Contact contains \"expires=0\""
expect "SAR USER_DEREGISTRATION" "$sar && $req && diameter.Server-Assignment-Type == 5"
expect "SAR AUTHENTICATION_FAILURE" "$sar && $req && diameter.Server-Assignment-Type == 9"
expect "403 to the UE with the wrong password" "sip.Status-Code == 403 && ip.dst == $ue1"

echo "call setup and release"
expect "INVITE UE 1 -> P-CSCF with SDP" "$invite && ip.src == $ue1 && sip.Request-Line contains \"tel:+1-202-555-0102\" && sdp"
expect "INVITE P-CSCF -> S-CSCF with the relay in the SDP" "$invite && ip.src == $pcscf && ip.dst == $scscf && sdp.connection_info.address == $agw && sip.P-Asserted-Identity"
expect "ENUM query for the number" "dns.qry.type == 35 && dns.qry.name == \"2.0.1.0.5.5.5.2.0.2.1.e164.arpa\""
expect "INVITE S-CSCF -> I-CSCF to the SIP URI from ENUM" "$invite && ip.src == $scscf && ip.dst == $icscf && sip.r-uri.user == \"001010000000002\""
expect "LIR for the callee" "$lir && $req && diameter.Public-Identity == \"sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org\""
expect "LIA with the callee's S-CSCF" "$lir && $ans && diameter.Result-Code == 2001 && diameter.Server-Name"
expect "INVITE I-CSCF -> S-CSCF" "$invite && ip.src == $icscf && ip.dst == $scscf"
expect "INVITE S-CSCF -> P-CSCF along the Path" "$invite && ip.src == $scscf && ip.dst == $pcscf && sip.P-Called-Party-ID"
expect "INVITE P-CSCF -> UE 2" "$invite && ip.src == $pcscf && ip.dst == $ue2ip && sdp.connection_info.address == $agw"
expect "183 reliable from UE 2" "sip.Status-Code == 183 && ip.src == $ue2ip && sip.RSeq"
expect "PRACK" "sip.Method == \"PRACK\" && ip.dst == $ue2ip && sip.RAck"
expect "200 OK to the INVITE from UE 2" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.src == $ue2ip"
answered="$last"
# The MMTel AS is a B2BUA: it acknowledges UE 2's 200 on its own dialog
# with UE 2 at once (RFC 3261 13.2.2.4), while its 200 to UE 1 still has
# the other legs of the call to cross. The two may come in either order,
# as in the end-to-end script of calls.
expect "ACK reaches UE 2" "sip.Method == \"ACK\" && ip.dst == $ue2ip"
last="$answered"
expect "200 OK to UE 1" "sip.Status-Code == 200 && sip.CSeq.method == \"INVITE\" && ip.dst == $ue1"
expect "RTP UE 1 -> relay" "rtp && ip.src == $ue1 && ip.dst == $agw"
expect "RTP relay -> UE 2" "rtp && ip.src == $agw && ip.dst == $ue2ip"
expect "BYE from UE 1" "sip.Method == \"BYE\" && ip.src == $ue1"
first_bye="$last"
# The AS answers UE 1's BYE on UE 1's dialog and sends a BYE of its own
# on the next one, so either may come first.
expect "200 OK to the BYE reaches UE 1" "sip.Status-Code == 200 && sip.CSeq.method == \"BYE\" && ip.dst == $ue1"
last="$first_bye"
expect "BYE reaches UE 2" "sip.Method == \"BYE\" && ip.dst == $ue2ip"
expect "486 to UE 1" "sip.Status-Code == 486 && ip.dst == $ue1"
expect "CANCEL from UE 1" "sip.Method == \"CANCEL\" && ip.src == $ue1"
expect "487 to UE 1" "sip.Status-Code == 487 && ip.dst == $ue1"
expect "480 to UE 1" "sip.Status-Code == 480 && ip.dst == $ue1"

# The callee's RTP payload equals what the caller encoded, and what the
# relay sent on the wire during the first call.
sent="$(kims exec deploy/ue -- od -An -v -tx1 /tmp/ue1-sent.raw | tr -d ' \r\n')"
received="$(kims exec deploy/ue2 -- od -An -v -tx1 /tmp/ue2-received.raw | tr -d ' \r\n')"
on_wire="$(tshark_fields "frame.number < $first_bye && rtp && !icmp && ip.src == $agw && ip.dst == $ue2ip && rtp.p_type == 0" rtp.payload | tr -d ':\n')"
if [ -n "$sent" ] && [ "$sent" = "$received" ] && [ "$sent" = "$on_wire" ]; then
	ok "UE 2 received the ${#sent} hex digits of PCMU that UE 1 sent, and so did the wire"
else
	bad "payloads differ: sent ${#sent}, received ${#received}, on the wire ${#on_wire} hex digits"
fi

echo "a short message from UE 1 to UE 2 with a status report"
expect "third-party REGISTER S-CSCF -> IP-SM-GW for UE 2" "$register && ip.src == $scscf && ip.dst == $ipsmgw && sip.To contains \"001010000000002\" && sip.Expires == 3600"
expect "MESSAGE UE 1 -> P-CSCF to the SC: RP-DATA with SMS-SUBMIT" "$message && $sms && ip.src == $ue1 && ip.dst == $pcscf && sip.r-uri == \"tel:+12025550100\" && gsm_sms.tp-da == \"12025550102\" && gsm_sms.sms_text == \"$gsm_text\""
mo="$(field sip.Call-ID)"
expect "MESSAGE P-CSCF -> S-CSCF" "$message && sip.Call-ID == \"$mo\" && ip.src == $pcscf && ip.dst == $scscf"
expect "MESSAGE S-CSCF -> IP-SM-GW by the originating iFC" "$message && sip.Call-ID == \"$mo\" && ip.src == $scscf && ip.dst == $ipsmgw"
mo_at_gw="$last"
expect "202 Accepted reaches UE 1" "sip.Status-Code == 202 && sip.Call-ID == \"$mo\" && ip.dst == $ue1"
last="$mo_at_gw"
expect "UDR IP-SM-GW -> HSS for MSISDN 12025550102" "diameter.cmd.code == 306 && $req && ip.src == $ipsmgw && ip.dst == $hss && e164.msisdn == \"12025550102\""
expect "UDA 2001" "$(answer) && diameter.Result-Code == 2001"
expect "MT MESSAGE IP-SM-GW -> I-CSCF: SMS-DELIVER" "$message && $sms && ip.src == $ipsmgw && ip.dst == $icscf && sip.r-uri == \"tel:+12025550102\" && gsm_sms.tp-mti == 0 && gsm_sms.sms_text == \"$gsm_text\""
mt="$(field sip.Call-ID)"
expect "LIR for UE 2's tel URI" "$lir && $req && ip.src == $icscf && diameter.Public-Identity == \"tel:+12025550102\""
expect "MT MESSAGE I-CSCF -> S-CSCF" "$message && sip.Call-ID == \"$mt\" && ip.src == $icscf && ip.dst == $scscf"
expect "MT MESSAGE S-CSCF -> P-CSCF" "$message && sip.Call-ID == \"$mt\" && ip.src == $scscf && ip.dst == $pcscf && sip.r-uri.host == \"$ue2ip\""
expect "MT MESSAGE reaches UE 2" "$message && sip.Call-ID == \"$mt\" && ip.dst == $ue2ip && gsm_sms.sms_text == \"$gsm_text\""
expect "delivery report UE 2 -> P-CSCF in reply to the MT MESSAGE" "$message && $sms && ip.src == $ue2ip && sip.In-Reply-To == \"$mt\""
expect "status report IP-SM-GW -> I-CSCF: received by the SME" "$message && $sms && ip.src == $ipsmgw && sip.r-uri == \"tel:+12025550101\" && gsm_sms.tp-mti == 2 && gsm_sms.dis_field.st_error == 0"
sr="$(field sip.Call-ID)"
expect "status report reaches UE 1" "$message && sip.Call-ID == \"$sr\" && ip.dst == $ue1"

# From the first REGISTER on: the init containers' lookups before it
# are not part of the flows.
none "malformed SIP, SDP, DNS, Diameter, RP or TP packets" "frame.number >= $first_register && (sip || sdp || dns || diameter || gsm_a.rp || gsm_sms) && (_ws.malformed || _ws.expert.severity >= warning)"

exit "$fail"
