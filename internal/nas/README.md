# NAS messages for SMS

[日本語](README.ja.md)

This package reads and writes the NAS messages that carry SMS between a UE and the core network: the CP layer of SMS (TS 24.011), UPLINK NAS TRANSPORT and DOWNLINK NAS TRANSPORT of EPS (TS 24.301), and UL NAS TRANSPORT and DL NAS TRANSPORT of 5GS (TS 24.501).

No node of this project uses this package. The tests compare each message with its octets:

```sh
go test ./internal/nas
go doc ./internal/nas
```

## What this package simplifies

The code has a `SIMPLIFIED:` comment at each of these places.

| Where | What is left out | Why |
| --- | --- | --- |
| `marshal5GMMNASTransport`, TS 24.501 8.2.10, 8.2.11 | Payload container types other than SMS, and the optional elements that they need, such as the PDU session ID. A message with another type is refused. | Those types carry the messages of other procedures, such as 5GS session management. This package covers SMS over NAS only. |
| `marshal5GMMNASTransport`, TS 24.501 4.4.4.1, 4.4.5 | Integrity protection and ciphering, with the message authentication code and the sequence number before the plain message. Only the plain message is written and read. | The keys come from 5G AKA and the security mode control procedure, which are not part of this package. |
| `parse5GMMNASTransport`, TS 24.501 8.2.10, 8.2.11, 7.6.1 | Reading the optional elements after the Payload container. They are ignored, also an unknown one coded as "comprehension required". | A payload of type SMS comes without them. |
| `marshalEMMNASTransport`, TS 24.301 4.4.2.3, 4.4.4.1, 4.4.5 | Integrity protection and ciphering, with the message authentication code and the sequence number before the plain message. Only the plain message is written and read. | The keys come from EPS AKA and the security mode control procedure, which are not part of this package. |
| `parseEMMNASTransport`, TS 24.301 7.6.1, TS 24.007 11.2.5 | Reading the octets after the NAS message container as information elements. They are ignored, also one coded as "comprehension required", which a receiver has to refuse. | Neither message has an optional element. |
| `ParseCP`, TS 24.007 11.2.4, 11.2.5 | Reading the octets after the last element of a CP message as information elements. They are ignored, so a malformed one goes unnoticed. | TS 24.011 defines no optional element in any CP message. |
