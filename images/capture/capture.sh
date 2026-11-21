#!/bin/sh
# Writes every packet on the compose bridge to one pcap per start.
set -eu
iface="${CAPTURE_INTERFACE:-ims0}"
dir="${CAPTURE_DIR:-/captures}"

i=0
until ip link show "$iface" >/dev/null 2>&1; do
	i=$((i + 1))
	if [ "$i" -ge 30 ]; then
		echo "capture: interface $iface not found" >&2
		exit 1
	fi
	sleep 1
done

file="$dir/ims-$(date -u +%Y%m%dT%H%M%SZ).pcap"
echo "capture: writing $file"
exec tcpdump -i "$iface" -U -n -w "$file"
