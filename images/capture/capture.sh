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
# The kernel keeps the packets that tcpdump has not written out yet in a
# buffer of its own, and drops the ones that no longer fit. A dropped
# packet leaves a hole that nothing in the file points at: the capture
# simply does not hold that message, and whoever reads the file sees the
# flow go on without it. -B asks for 32 MiB rather than the 2 MiB that
# the default gives, so that a burst on the bridge does not fill the
# buffer while tcpdump waits for the processor. -U writes each packet out
# as it comes, so that a reader of the file sees the flow as it happens.
exec tcpdump -i "$iface" -U -n -B 32768 -w "$file"
