# Service based interface

[日本語](README.ja.md)

This package is the part of the service based interface of the 5GC that every API here shares: HTTP/2 (TS 29.500 5.1 to 5.4), JSON bodies, and the error answers with ProblemDetails (TS 29.571 5.2.4.1) and the application errors of TS 29.500 Table 5.2.7.2-1. Package `n5` (TS 29.514) and package `nhss` (TS 29.562) build on it. No node of this project runs it: no binary in `cmd` builds it in. The tests show it at work:

```sh
go test ./internal/sbi
go doc ./internal/sbi
```

## What this package simplifies

The code has a `SIMPLIFIED:` comment at each of these places.

| Where | What is left out | Why |
| --- | --- | --- |
| Package `sbi`, TS 29.500 5.1 | TLS, OAuth 2.0 access tokens, and the NRF. The SBI runs over cleartext HTTP/2 (h2c with prior knowledge, RFC 9113 3.3), and the caller is given the apiRoot of its peer. | Cleartext lets a capture show the messages, as everywhere in this project. TS 29.500 5.1 lets a network protected by other means go without TLS, and the security sections of the OpenAPI files of TS 29.514 and TS 29.562 allow a request without a token. |
| `NewHTTPClient`, TS 29.500 6.10.9 | Redirection with 307 and 308 to another instance of the service is not followed; it comes back as an error. | There is no NRF and no second instance to be sent to: the caller is given the one peer it talks to. |
| `ProblemDetails`, TS 29.571 5.2.4.1 | The attributes about access tokens, the NRF and API versions. | Nothing here uses the NRF or OAuth 2.0. |
