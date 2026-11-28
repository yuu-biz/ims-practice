# How to read this book

This book explains 3GPP IMS (the IP Multimedia Subsystem) with the help of this repository, a learning implementation of it. IMS is how a network offers voice calls, short messages and services such as call forwarding over IP, for mobile and fixed phones alike. The book follows which node takes care of what, and what each signal settles, with figures and short excerpts of code.

Reading it needs nothing to run. There is no question to answer and no code to write. Each question the text asks is answered where it is asked or in the sections that follow. The code and the tests are what the author checked the explanations against.

## Who the book is for

The book is for people who are new to IMS. Knowing the basics of protocols, such as IP addresses and ports, requests and responses, is enough to start. Knowing SIP or Diameter is not needed: each header and AVP is explained where it does its work. The abbreviations are in the [glossary](../../glossary.md).

Reading Go is not needed either. The conclusions of every chapter are written so that the figures and the text carry them. For those who want to read the code, the appendix [Go for reading the code](go-reading.md) explains the Go that the excerpts use.

## The three levels

The chapters come in three levels. The [contents](README.md) list the chapters by level, and each chapter names first the chapters it builds on.

| Level | The question it answers | What it gives the reader |
| --- | --- | --- |
| Introduction | What is this exchange for, and who takes care of it | The nodes of IMS and the job of each. What registration and a call settle. Where the signalling and the media go |
| Intermediate | When a condition changes, which decision, state and message change | Which condition of the specifications is decided by which function, which state it changes and which response follows. The tests that check it |
| Advanced | Where, within the specifications, does the design choose | What the standard fixes and what an implementation or an operator chooses. The options and what happens with each. The choice of this implementation and its limits |

Reading the introduction in order gives the whole map. After that, any intermediate chapter of interest can come next. An advanced chapter needs only the intermediate chapters it names.

## How a chapter is built

Every chapter answers its question in its first few sentences. Then it follows the grounds for the answer: the condition in the specifications, the code that decides it, the state that changes and the result. Two sections end each chapter.

- **What this explanation rests on.** The names of the code and the tests that the explanation was checked against, and what each test checks. A test is said to show only what it checks.
- **Running it yourself.** For those who want to see the same flow in a capture of their own, the sections of the guides ([docs/guide/en/](../../guide/en/README.md)) to go to. The chapter is complete without them.

The text keeps four things apart: what the specifications require, what this implementation chose, what this implementation simplified, and examples made up to explain. What is simplified is also in the tables of "What this project simplifies" in the guides, and in the `SIMPLIFIED:` comments of the code.

## Figures and slides

The figures of the book are scenes of the slides of this repository ([slides/](../../../slides/README.md)), drawn at one step. In the HTML of the slides the same scene goes forward one step at a time. A chapter and its slides take the same question: the chapter gives the reasons and the exceptions, the slides the places and the changes.

A figure reads like this.

- A map puts the nodes on a grid and shows where a signal goes and who decides. A ladder diagram draws a vertical line for each node and shows who talks to whom, in what order.
- The kind of line is the protocol. SIP is a solid line, Diameter a dashed one, DNS a dotted one, and HTTP and the media have colours of their own. The label of a line names the protocol too.
- A box inside a node is a decision or a piece of work of that node.
- The table under a figure gives the headers, AVPs and values of the message that the step looks at.
- The last line under a figure says what it is drawn from.

| The line under the figure | What it means |
| --- | --- |
| A schematic of what this implementation does, drawn from the guide | The behaviour of this implementation: the order and the values checked in the guide and the code |
| A schematic of the procedure of the specification | The procedure that the specification sets, not limited to this implementation |
| An example made up to explain the idea | An example made for the explanation. Its values are shortened and are not those of this setup |

Every figure is a schematic, not a capture.

## Excerpts of code

A code block right under a paragraph that is only a link to a file is a copy of consecutive lines of that file, as they are. The generator of the slides checks that the lines are still in the file. A rewrite made for the explanation says "pseudocode" and is never shown in this form. The text around each excerpt explains the input, the condition it checks, what it changes and what happens next.

## The setup of the examples

Unless a chapter says otherwise, the examples come from the `deploy/compose` setup. One node for each role sits on one Docker network (172.28.0.0/24). The signalling is not encrypted, and it uses the standard ports, such as 5060 for SIP and 3868 for Diameter.

- **The UE.** A softphone without a SIM card reaches the P-CSCF over the Docker network, registers with SIP Digest and calls with G.711. This leaves out the procedures of an access network and shows the IMS alone.
- **VoLTE.** A UE that attaches to an EPC like a mobile phone, registers with IMS AKA using the keys of its USIM and waits for a voice bearer appears only in the chapters that say so. The EPC is a stub.
- **Values.** The telephone numbers are the fictitious +1 202 555 01xx, the identities and keys of the subscribers are test values, and the domain is `ims.mnc001.mcc001.3gppnetwork.org`.

## The versions of the specifications

The clause numbers are those of the versions that the guides follow. The guides on registration and calls follow Release 18 (TS 24.229 V18.11.0, TS 29.228 V18.0.0, TS 29.229 V18.1.0). The guides on SMS, the supplementary services, the MRF, VoLTE and interconnection follow later versions, such as TS 24.229 V20.1.0. Each guide states at its start the versions it follows, and the clause numbers of a chapter are those of the versions of the guides it points to. A chapter that names versions names those of its guides, usually near its start. Where the text of a version was at hand, the clauses that the chapter cites were checked in it. In another version a clause may have another number.
