FROM alpine:3.24
RUN apk add --no-cache tcpdump tshark
COPY capture.sh /usr/local/bin/capture.sh
ENTRYPOINT ["/bin/sh", "/usr/local/bin/capture.sh"]
