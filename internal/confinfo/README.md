# Conference information

[日本語](README.ja.md)

This package builds and reads the conference information document of RFC 4575, `application/conference-info+xml`. It is the body of every NOTIFY of the conference event package: the conference focus sends it to tell the participants who is in the conference and in what state (TS 24.147 5.3.3.2, TS 24.605 4.5.2.2.2). The MMTel AS builds it as the focus of a three-party conference, and the UE reads it.

```sh
go test ./internal/confinfo
go doc ./internal/confinfo
```

## What this package simplifies

The code has a `SIMPLIFIED:` comment at each of these places.

| Where | What is left out | Why |
| --- | --- | --- |
| `Conference.FullState`, RFC 4575 3.2 | Partial documents. The default policy is a full document in the NOTIFY that answers a SUBSCRIBE and only the changes after that. This package builds full documents only, so the focus sends the whole state every time. | A subscriber must accept a full document at any time (RFC 4575 4.6), and a conference of IR.92 has few users, so the documents stay small. |
| `Document`, RFC 4575 5.3, 5.4, 5.6.2 to 5.6.5 and 5.9 | The elements host-info, sidebars-by-ref, sidebars-by-val, free-text, keywords, associated-aors, roles, languages and cascaded-focus, and the elements and attributes of other namespaces. Parse skips them, so a parsed document that is written again loses them. | The conference AS of this project does not produce them, and GSMA IR.92 2.3.3 does not ask for them. |
