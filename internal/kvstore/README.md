# The store outside the nodes

[日本語](README.ja.md)

Package `kvstore` is a store of keyed items outside the nodes, so that the state of a node does not die with its process and several instances of a role can share it: a node that is replaced after a failure goes on with the registrations and calls of the old one. `MemoryStore` keeps the items in the process, and `Valkey` keeps them in a Valkey server. Each package under it is the Store of one role, such as `scscfstore` for the S-CSCF, and `kvlock` is a lock per user that the instances of a role share.

```sh
go test ./internal/kvstore/...
go doc ./internal/kvstore
```

## What these packages simplify

The code has a `SIMPLIFIED:` comment at each of these places.

| Where | What is left out | Why |
| --- | --- | --- |
| `NewValkey`, on one address | TLS and `AUTH` (with the users and ACLs of Valkey), and the discovery of a replica that takes over (Sentinel, cluster). The server is reached on one address. | The store sits in the private network of the nodes, and like Diameter and SIP it is not encrypted by default, so that captures can be read. The store is one server and so a single point of failure; what this project shows is the redundancy of the IMS nodes, not that of the store. |
| `kvlock.Locker.Lock`, no fencing token | The writes under the lock carry no fencing token: a holder that stops for longer than the lease and then goes on may overwrite what the next holder wrote. | Fencing would make the version of the lock a condition of every write of the role's Store, which the Store of no role takes. The lease is longer than the longest work under the lock. |
| `mmtelstore`, TS 24.147 5.3.2.7 | When the instance that hosts the focus of a conference stops, nothing removes the participants or deallocates the conference URI, and the record stays in the store. | The dialogs of the conference were in the process of that instance, and no other instance continues a conference that another one hosted. The record of a call stays in the same way when no instance sees its BYE. |
