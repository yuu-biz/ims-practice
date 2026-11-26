# EVS payloads

[日本語](README.ja.md)

This package carries EVS speech frames in RTP and in files, in the formats of TS 26.445 Annex A: the Compact and the Header-Full RTP payload (A.2.1, A.2.2) and the storage format (A.2.6). Like package `amr`, it never encodes or decodes speech: frames come encoded from elsewhere, travel as they are, and are handed on. Package `sdp` takes from it the payload format that an EVS session negotiated and the size of each frame type.

```sh
go test ./internal/evs
go doc ./internal/evs
```

## What this package simplifies

The code has a `SIMPLIFIED:` comment at each of these places.

| Where | What is left out | Why |
| --- | --- | --- |
| Package `evs`, TS 26.445 A.2.5 | Sessions with more than one mono channel (dual-mono). Every packet carries the frames of one channel. | IR.92 and NG.114 do not ask for them, and TS 26.114 5.2.1.4 makes dual-mono optional. |
| `WriteStorage`, TS 26.445 A.2.6.1 | Files of more than one channel. The channel count is always 1. | The payloads that this package reads carry one channel, as the row above says. |
