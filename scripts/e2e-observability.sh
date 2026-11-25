#!/usr/bin/env bash
# End-to-end check: the nodes serve their metrics on their maintenance
# listeners (deploy/compose/pcscf.yaml, deploy/compose/icscf.yaml,
# deploy/compose/scscf.yaml and the other nodes), and Prometheus and
# Grafana beside the Compose project read them (deploy/observability).
# After a registration, Prometheus has scraped all three CSCFs and holds
# their counts of successful registrations, with the set-up time that
# the P-CSCF measures, and Grafana returns the count of the S-CSCF
# through the Prometheus data source that the dashboard uses. A call
# adds the terminating session establishment that the I-CSCF counts,
# the successful establishments that the three CSCFs count on the 180
# (Ringing) of the callee, and the success ratio that the dashboard
# divides out of the two. The Cx requests of the registration and of
# the call, the answers of the HSS to them and the state of each
# Diameter connection are there as well. The MMTel AS counts the Sh
# requests of each call, and the IP-SM-GW those of each registration.
# The HSS holds the subscribers it was given and counts the identities
# of a registration while it lasts.
#
# The same listeners serve the maintenance API, through which an
# operator takes a node out of service and back. Three scripts check
# that on the same deployment: scripts/e2e-maintenance-sip.sh for the nodes of registrations and
# calls, scripts/e2e-maintenance-sms.sh for those of short messages, and
# scripts/e2e-maintenance-hss.sh for the HSS.
set -euo pipefail
cd "$(dirname "$0")/.."
. scripts/e2e-lib.sh
. scripts/e2e-observability-lib.sh

start_network
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
# The callee waits in the background for one call.
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
# The HSS counts the same four exchanges from its own side: each request
# on its receipt and each answer as it hands it back (TS 32.409 4.2.12.1
# and 4.2.12.2). They are the UAR and the LIR of the I-CSCF and the MAR
# and the SAR of the S-CSCF above, under the other value of the
# direction label. The result label is left open here too.
for series in 'ims_diameter_requests_total{role="hss",direction="received",command="UAR"}' \
	'ims_diameter_answers_total{role="hss",direction="sent",command="UAR"}' \
	'ims_diameter_requests_total{role="hss",direction="received",command="LIR"}' \
	'ims_diameter_answers_total{role="hss",direction="sent",command="LIR"}' \
	'ims_diameter_requests_total{role="hss",direction="received",command="MAR"}' \
	'ims_diameter_answers_total{role="hss",direction="sent",command="MAR"}' \
	'ims_diameter_requests_total{role="hss",direction="received",command="SAR"}' \
	'ims_diameter_answers_total{role="hss",direction="sent",command="SAR"}'; do
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
# The nodes of the media relay and the MRFP serve the same listener for
# their maintenance API, through which an operator takes them out of
# service, and neither counts a measurement of its own: their targets
# are up with an empty page to scrape.
for media in agw mrfp; do
	if value="$(until_positive prometheus_values "up{job=\"ims\",node=\"$media\"}")"; then
		echo "ok   Prometheus scrapes the $media node: up = $value"
	else
		echo "FAIL the $media target of Prometheus is not up: $value"
		curl -fsS "$prometheus/api/v1/targets?state=active" || true
		echo
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
same_nodes

finish
