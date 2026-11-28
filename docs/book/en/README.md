# Reading IMS through an implementation

[日本語](../ja/README.md)

This book takes the repository, a learning implementation of 3GPP IMS, and explains with figures and short excerpts of code what IMS decides and how. It is written to be read: there is nothing to run and no question to answer. For those who want to run it themselves, the end of each chapter and Running it yourself point to the guides.

There are three ways to read it. Each can be read from the front, but any chapter can be read on its own: each names the chapters it builds on at its start.

## Introduction (the whole picture)

What the nodes of IMS take care of, and what registration and a call settle, on the smallest map and with typical signals.

4. [Three things that registration settles](registration.md)

## Intermediate (the conditions of the specifications and the code)

Which decision, state and message change when a condition changes, followed through the clauses of the specifications, the code that decides, and the tests that check it.

- Registration
  - [Why a UE sends REGISTER twice](registration-two-registers.md)
  - [Which request follows Path and which follows Service-Route](registration-routes.md)
  - [Why a late deregistration does not remove a newer registration](registration-stale-deregistration.md)
  - [Refresh, deregistration, subscription and removal by the network](registration-lifecycle.md)

## The slides and the guides

The slides in [slides/](../../../slides/README.md) show the same questions with figures and steps, and each chapter names its scenes. The guides in [docs/guide/en/](../../guide/en/README.md) have the steps to run the setups and the lists of headers to look at in a capture.
