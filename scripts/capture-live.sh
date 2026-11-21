#!/usr/bin/env bash
# Streams the compose bridge into Wireshark on the host.
# Set WIRESHARK to the Wireshark binary if it is not on PATH.
set -euo pipefail
cd "$(dirname "$0")/.."
wireshark="${WIRESHARK:-wireshark}"
docker compose exec -T capture tcpdump -i ims0 -U -n -w - | "$wireshark" -k -i -
