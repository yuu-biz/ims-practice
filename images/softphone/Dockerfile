# Independent SIP user agents to test the IMS against: pjsua, a
# softphone without a screen, and SIPp, a SIP traffic generator. They
# were written by others, so a misunderstanding shared by this
# project's UE simulator and its CSCFs does not slip through.
FROM alpine:3.24
RUN apk add --no-cache pjsua sipp
# SIPp's scenarios, such as the IMS AKA registration.
COPY sipp/ /etc/sipp/
ENTRYPOINT ["pjsua"]
