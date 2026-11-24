# The Lab's web UI (web/lab), built into static files that
# the Go build embeds.
FROM node:26.13.1-alpine AS web
WORKDIR /web
COPY web/lab/package.json web/lab/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY web/lab/ ./
RUN npm run build

# The OAM screen (web/oam), built the same way. It is a frontend of its
# own, so it has its own stage and its own dependencies.
FROM node:26.13.1-alpine AS web-oam
WORKDIR /web
COPY web/oam/package.json web/oam/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY web/oam/ ./
RUN npm run build

FROM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Replace the committed placeholder pages with the built UIs before
# go:embed picks them up.
RUN rm -rf internal/lab/web internal/oam/web
COPY --from=web /web/dist/ internal/lab/web/
COPY --from=web-oam /web/dist/ internal/oam/web/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/ims ./cmd/ue ./cmd/oss

# Alpine rather than scratch, so that learners have a shell inside the
# container.
FROM alpine:3.24
# iproute2 for "ip xfrm", with which a P-CSCF or a UE that protects the
# signalling of IMS AKA sets up IPsec ESP, and with which a learner
# reads the result (TS 33.203 7.1). The ip of BusyBox has no xfrm.
RUN apk add --no-cache iproute2
COPY --from=build /out/ims /out/ue /out/oss /usr/local/bin/
# The announcements of the MRFP (deploy/announcements). compose.yaml
# mounts the same directory over them, so that a changed WAV file plays
# without a new build.
COPY deploy/announcements/*.wav /usr/share/ims/announcements/
# Diameter (RFC 6733 2.1) and SIP (RFC 3261 19.1.2) on their standard
# ports, so that Wireshark decodes them without settings.
EXPOSE 3868 5060/udp
# Exec form, so that ims is PID 1 and receives SIGTERM from docker stop
# and sends its DPR.
ENTRYPOINT ["ims", "-config", "/etc/ims/node.yaml"]
