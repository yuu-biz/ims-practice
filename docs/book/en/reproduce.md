# Running it yourself

This book is written to be read without running anything. This appendix is for those who want to see the signals of the book in a capture of their own. The steps themselves are in the guides; here, for each chapter, is the guide and the section to open.

## What you need

Docker with Docker Compose, and Wireshark to open the captures. Go on the host is not needed. The first build takes a few minutes. "The first commands" in the index of the guides, [docs/guide/en/README.md](../../guide/en/README.md), start the setup and run one registration.

To see the captures without running anything, the CI of this repository keeps a capture of each scenario among its artifacts: `registration-capture`, `sms-capture`, `mmtel-capture`, `mrf-capture`, `volte-capture` (with the AMR recordings and the logs of the UEs) and `interconnect-capture` (both networks and the link between them).

## The guide for each chapter

| Chapter | The guide and section to open |
| --- | --- |
| [What IMS does](overview.md) | "What the Compose project starts" in the [index of the guides](../../guide/en/README.md) |
| [Watching the signalling](observing.md) | [Capture signaling and open it in Wireshark](../../guide/en/capture.md), [Watch the IMS in the Lab](../../guide/en/lab.md) |
| The chapters on registration (from [Three things that registration settles](registration.md) to [Refresh, deregistration, subscription and removal by the network](registration-lifecycle.md)) | "Run a registration" and "Open the capture" in [the registration guide](../../guide/en/registration.md) |
| [From a number to the other phone](call.md), [Transactions, dialogs and media negotiation in a call](call-signaling.md) | "Make a call" and "Open the capture" in [the call guide](../../guide/en/call.md) |
| [iFC and the supplementary services of the MMTel AS](mmtel.md) | "Run it" and "Open the capture" in [the MMTel guide](../../guide/en/mmtel.md) |
| [Delivering SMS and reporting on it](sms.md) | "Send a short message" and "Open the capture" in [the SMS guide](../../guide/en/sms.md) |
| [Announcements, ringback tones and conferences of the MRF](mrf.md) | "Run it" and "Open the capture" in [the MRF guide](../../guide/en/mrf.md) |
| [Registration and calls over VoLTE](volte.md) | "Run it" and "Open the capture" in [the VoLTE guide](../../guide/en/volte.md) |
| [Interconnection between networks](interconnect.md) | "Run it" in [the interconnection guide](../../guide/en/interconnect.md) |
| The advanced chapters | "Run it yourself" at the end of each. For several instances, "The state of the users in a store outside the nodes" in the [index of the guides](../../guide/en/README.md) |

## Run the tests

The tests that "What this account rests on" names in each chapter are Go tests. With Go on the host, run `GOTOOLCHAIN=auto go test -timeout 500s ./internal/<package>/ -run <name of the test>`; without it, use the Go in a container as the index of the guides shows. Each test runs IMS, or a part of it, in one process, and needs no network to be set up.

## Clean up

Each guide ends with the steps that stop what it started. The way to stop everything at once is in "Clean up" in the [index of the guides](../../guide/en/README.md). A command with `-v` removes the stored data (the volumes) other than the captures.
