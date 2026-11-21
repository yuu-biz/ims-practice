# The tools that put a fault into the network namespace of a node: nft
# for packet loss and partitions, tc for delay (netem), and the ip of
# BusyBox to find the interface that holds an address. The IMS image
# needs neither the tools nor NET_ADMIN; a throw-away container of this
# image joins the node's namespace and has both (deploy/chaos/README.md).
FROM alpine:3.24
RUN apk add --no-cache nftables iproute2-tc
CMD ["nft", "list", "ruleset", "netdev"]
