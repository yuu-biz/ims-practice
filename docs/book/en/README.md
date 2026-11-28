# Reading IMS through an implementation

[日本語](../ja/README.md)

This book takes the repository, a learning implementation of 3GPP IMS, and explains with figures and short excerpts of code what IMS decides and how. It is written to be read: there is nothing to run and no question to answer. For those who want to run it themselves, the end of each chapter and Running it yourself point to the guides.

There are three ways to read it. Each can be read from the front, but any chapter can be read on its own: each names the chapters it builds on at its start.

## Introduction (the whole picture)

What the nodes of IMS take care of, and what registration and a call settle, on the smallest map and with typical signals.

4. [Three things that registration settles](registration.md)
5. [From a number to the other phone](call.md)
6. [What application servers add](services.md)

## Intermediate (the conditions of the specifications and the code)

Which decision, state and message change when a condition changes, followed through the clauses of the specifications, the code that decides, and the tests that check it.

- Registration
  - [Why a UE sends REGISTER twice](registration-two-registers.md)
  - [Which request follows Path and which follows Service-Route](registration-routes.md)
  - [Why a late deregistration does not remove a newer registration](registration-stale-deregistration.md)
  - [Refresh, deregistration, subscription and removal by the network](registration-lifecycle.md)
- [Transactions, dialogs and media negotiation in a call](call-signaling.md)
- [iFC and the supplementary services of the MMTel AS](mmtel.md)
- [Announcements, ringback tones and conferences of the MRF](mrf.md)

## Advanced (design decisions within the specifications)

What the standards settle is told apart from what an implementation or an operator chooses; the ways to choose are compared, and the choice of this implementation and its limits are explained. Each chapter names the intermediate chapters it builds on.

- [Registration order and expiry](design-registration-order.md)
- [What several nodes and failures hand over](design-shared-state.md)
- [When a media renegotiation fails](design-media-rollback.md)
- [Taking a node out for maintenance, and losing it](design-maintenance.md)

## The slides and the guides

The slides in [slides/](../../../slides/README.md) show the same questions with figures and steps, and each chapter names its scenes. The guides in [docs/guide/en/](../../guide/en/README.md) have the steps to run the setups and the lists of headers to look at in a capture.
