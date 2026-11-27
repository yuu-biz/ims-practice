# Study-session slides

[日本語](README.ja.md)

The decks for study sessions on this repository. Each deck is a text file in a small Markdown-like format. A generator of this repository, [cmd/slides](cmd/slides/main.go), lays out every slide and writes two files of it: a pptx to hand out and to edit in PowerPoint, and an HTML page to present from and to study with.

A slide is either a page of text, tables and code, or a scene: a diagram of the network or of the code that the learner steps through. A scene follows a signal one hop at a time, moves in on the node that handles it, shows the values it received and what it decided, and draws the next signal that follows from it. The HTML steps through a scene; the pptx keeps the states of it that a still page can explain, with the text of the steps written out beside the diagram.

The decks do not repeat the guides. Each slide says what to look at in the capture and links to the step of the guide in [docs/guide/](../docs/guide/), where the full ladder diagram and the explanation are.

| Deck | Japanese | English | Guide |
| --- | --- | --- | --- |
| 0. Go for reading this repository | [decks/ja/00-go.md](decks/ja/00-go.md) | [decks/en/00-go.md](decks/en/00-go.md) | [A Tour of Go](https://go.dev/tour/) |
| 1. Package layout | [decks/ja/01-packages.md](decks/ja/01-packages.md) | [decks/en/01-packages.md](decks/en/01-packages.md) | [README](../README.md) |
| 2. The map of the network | [decks/ja/02-network.md](decks/ja/02-network.md) | [decks/en/02-network.md](decks/en/02-network.md) | [Guides](../docs/guide/en/README.md) |
| 3. Captures and the Lab | [decks/ja/03-captures.md](decks/ja/03-captures.md) | [decks/en/03-captures.md](decks/en/03-captures.md) | [capture](../docs/guide/en/capture.md), [lab](../docs/guide/en/lab.md) |
| 4. Registration | [decks/ja/04-registration.md](decks/ja/04-registration.md) | [decks/en/04-registration.md](decks/en/04-registration.md) | [registration](../docs/guide/en/registration.md) |
| 5. Voice call | [decks/ja/05-call.md](decks/ja/05-call.md) | [decks/en/05-call.md](decks/en/05-call.md) | [call](../docs/guide/en/call.md) |
| 6. SMS over IP | [decks/ja/06-sms.md](decks/ja/06-sms.md) | [decks/en/06-sms.md](decks/en/06-sms.md) | [sms](../docs/guide/en/sms.md) |
| 7. Supplementary services (MMTel AS) | [decks/ja/07-mmtel.md](decks/ja/07-mmtel.md) | [decks/en/07-mmtel.md](decks/en/07-mmtel.md) | [mmtel](../docs/guide/en/mmtel.md) |
| 8. The MRF (announcements, tones, conferences) | [decks/ja/08-mrf.md](decks/ja/08-mrf.md) | [decks/en/08-mrf.md](decks/en/08-mrf.md) | [mrf](../docs/guide/en/mrf.md) |
| 9. VoLTE (access network, IMS AKA, Rx, preconditions) | [decks/ja/09-volte.md](decks/ja/09-volte.md) | [decks/en/09-volte.md](decks/en/09-volte.md) | [volte](../docs/guide/en/volte.md) |
| 10. Two networks joined at the border (the IBCF and the TrGW) | [decks/ja/10-interconnect.md](decks/ja/10-interconnect.md) | [decks/en/10-interconnect.md](decks/en/10-interconnect.md) | [interconnect](../docs/guide/en/interconnect.md) |

Decks 1 to 10 go from the map of the code, the map of the network and the two ways of reading the signaling through registration, the call and SMS to the supplementary services, the media that the network itself plays and mixes, the VoLTE call, and a call that leaves for another operator's network. The numbers are the recommended order: the map of the network and the deck on captures and the Lab come before registration, because every deck from there on reads a part of that map in a capture, and most of them open the Lab as well; the MRF deck comes after the supplementary services, because the MMTel AS is what drives the MRFC over Mr'; the VoLTE deck comes after them, because the VoLTE guide builds on the MMTel AS, and every VoLTE call goes through it. The deck on two networks comes last, because it repeats the flows of the decks before it across a border: the call that still visits the MMTel AS and the short messages of the SMS deck, read in the captures of both networks and of the link between them, and the conference of the MRF deck, whose URI has to reach a participant of the other network unchanged. Deck 0 comes before them all: it explains the Go that a reader of this repository meets, with excerpts of the code, for those who have not read Go before.

## Running a session

Each deck is one session, held in the order of the numbers. The decks carry no speaker notes: what a presenter says about a slide is on the slide, and the answers to the exercises and to "Looking back" are on the slide after them.

- Keep the guide of the session open next to the slides. The slides give the order of reading; the guide has the explanations and the full ladder diagrams.
- Ask everyone to have the repository open. Best is when everyone runs the scenario; if that is hard, show the presenter's screen, or hand out the capture of the scenario from the artifacts of the CI.
- The clause numbers are those of the editions that the guides name. Tell anyone who has another edition open that the numbers may differ.

| Deck | Time | How the time goes |
| --- | --- | --- |
| 0. Go | 60 min | 25 for "Why Go" (3), running the tests and the shape of the code (10), then 35 for what you meet on every page (20) and a spec-shaped package (10). The exercises take 15 more, in pairs. For those who program in another language but have not read Go; those who read Go start with deck 1. |
| 1. Package layout | 45 min | No signaling: the map of what is where. 3 for the top of the repository, 5 for "internal/ in layers", 10 for following one REGISTER through the code in pairs. |
| 2. The map of the network | 45 min | No signaling either: the nodes, the interfaces between them, and the protocol on each. 10 for the nodes, 15 for the reference points, 10 for which session opens which part of the map. It comes before registration, because every session from there on reads a part of the map in a capture. |
| 3. Captures and the Lab | 45 min | No new signaling either: the two ways of looking at it. 20 for the capture, the filter and the ladder of Wireshark, 15 for the Lab and how it builds a ladder of its own, 10 for "Looking back" and for trying the filters on a capture that is already there. Every session from here on opens a capture, and most of them the Lab. |
| 4. Registration | 60 min | 35 for the way to the 200 OK, then 5 for a break or questions at "The UE is registered", and the rest for the subscriptions, the deregistration and the failures. The capture of the CI artifact `registration-capture` can stand in for running it. |
| 5. Call | 60 min | 30 until the INVITE reaches UE 2 (about the slide "6 and 7"), 20 from the answer to the hang-up, the rest for the failures and "Looking back". Whoever dislikes two terminals can drive the UEs from the Lab. |
| 6. SMS | 90 min | May be held as two sessions; then the first ends after "13 to 15. The status report", at about 45 minutes, and the second starts with the four slides on holding and alerting, from "A1 to A4", which take about 20 minutes. Reading the body by hand is a 10-minute exercise. The table of the six MESSAGEs, printed, keeps everyone on track in the capture. The CI artifact `sms-capture` holds the capture and the list of its frames. Registration and the call come first: Path, Service-Route and the LIR are not explained again. |
| 7. Supplementary services | 80 min | The first half is how the AS works and where its settings are; the second half reads the capture service by service. May be held as two sessions, split before "Open the capture". The call and SMS come first: INVITE, PRACK, ACK and how to read an iFC are not explained again. The CI artifact `mmtel-capture` holds the capture and the list of its frames. |
| 8. The MRF | 60 min | 20 for what an MRF is, the MRFC and the MRFP, and how the AS asks for a service; 20 for the announcement and the tone in the capture; 20 for the conference. The session on the MMTel AS comes first: every request for media starts at the AS, and the calls are the calls of that session. The CI artifact `mrf-capture` holds the capture, the list of frames and the logs of the UEs. |
| 9. VoLTE | 90 min | The first half is the access network and IMS AKA, the second half Rx and the call. May be held as two sessions, split before "Policy control over Rx, and the VoLTE call"; end the first half at the AAR right after the registration (#786). Hold it after the session on the MMTel AS: every VoLTE call goes through the MMTel AS, and registration, the call and the three dialogs of the AS are not explained again. The CI artifact `volte-capture` holds the capture, the AMR recordings, the list of frames and the logs of the UEs. |
| 10. Two networks | 75 min | 20 for the two networks, the IBCF and the TrGW, and the four reference points of the border; 25 for the call and the three captures side by side; 15 for the agreements and the calls that fail on them; the rest for the short messages and the conference across the border. Hold it after the sessions on the call, SMS and the supplementary services: this session runs their flows across a border and explains them again nowhere. The CI artifact `interconnect-capture` holds the captures of both networks and of the link. |

## Build

You need Go, the version in [go.mod](../go.mod); with `GOTOOLCHAIN=auto` an older Go fetches it. Nothing else: no Node, no browser, no fonts, no LibreOffice. Run these in this directory.

```sh
GOTOOLCHAIN=auto go run ./cmd/slides check   # check every deck
GOTOOLCHAIN=auto go run ./cmd/slides build   # check, then write dist/
```

Without Go on the host, run the same in the container that builds the rest of the repository, from the root of the repository:

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD:/src" -v ims-practice-gomod:/go/pkg/mod \
  -w /src/slides golang:1.27.4-alpine go run ./cmd/slides build
```

`build` writes `dist/ja/` and `dist/en/`, one `.pptx` and one `.html` per deck. It writes nothing when a check fails. `dist/` is not tracked by Git. The output depends only on the sources, so building twice gives the same files.

The links to the guides are relative, so they work in the files that are opened from `dist/` inside a checkout. For files that are copied elsewhere, such as a CI artifact, give the address of the repository tree and the links become absolute:

```sh
go run ./cmd/slides -repo-url https://github.com/<owner>/<repository>/blob/<commit> build
```

## What is in the pptx

The pptx is an ordinary PowerPoint file. Everything on a slide is a native object that can be edited:

| On the slide | In the pptx |
| --- | --- |
| Title, subtitle, footer, slide number | Placeholders of the slide layouts "Title Slide", "Section Header" and "Title Only". The titles show in PowerPoint's outline. |
| Paragraphs, lists, block quotes | Text boxes. Bullets are bullets of the paragraph, numbers are automatic numbering. |
| Tables | Tables (`a:tbl`), with a header row. |
| Code blocks | Text boxes in a monospace font, on a grey fill. |
| Ladder diagrams | One group of shapes: a box and a line for each participant, an arrow (a connector with an arrowhead) and a text box for each message, a box for each note. Ungroup it to edit a part. |
| The diagram of a scene | One group of shapes named after what they show: a rounded box with its text for each node (`node icscf`), a connector with an arrowhead for each message (`event uar1`) and a text box for its label (`label uar1`), a box for each processing step in a node. The names show in PowerPoint's selection pane. |
| Links to the guides | Hyperlinks. The package keeps their addresses as URIs, so a Japanese anchor is percent-encoded (`%E7%99%BB...`). |

There are no pictures, and there are no speaker notes.

A scene gives a page for each of its checkpoints, the states that its source marks to keep, and the last step of the scene is always one. The page draws the diagram as the checkpoint leaves it, numbers each message with the step that is about it, and writes out, beside or below the diagram, the title and the text of every step since the checkpoint before, with the fields of their messages in a table. A page that the learner reads without stepping through the scene so says what the HTML shows one step at a time. A map too large to share a page with that text gets a page of its own, and the text follows on the next page. The longer explanation of the scene follows on a page of its own with the tag "Details". The arrows are not glued to the boxes: moving a node in PowerPoint leaves its arrows where they are.

Known limits:

- The pptx names its fonts: Calibri for Latin text, Yu Gothic for Japanese, Consolas for code. A machine without one of them substitutes another, whose letters may be wider or narrower.
- The generator measures text with the widths of Arial, which are wider than those of Calibri and of the Latin letters of most Japanese fonts. A line therefore fits wherever the fonts are of the usual widths, but the rows of a table may be a little taller than their text needs.
- A slide whose text was made smaller to fit (see "Check") keeps that smaller size in the pptx. Text typed into a box in PowerPoint does not move the boxes below it.

## The HTML

One file per deck, which opens from disk and loads nothing from the network. One slide shows at a time, scaled to the window. A page of text is drawn from the same boxes as the pptx. A scene is a stage: its question as the title, the diagram, the text of the current step with the fields of its message, and a button that opens the longer explanation of the scene.

| Key | What it does |
| --- | --- |
| Right, Down, Page Down, Space, Enter | Next step; after the last step of a slide, the next slide |
| Left, Up, Page Up, Backspace | Back one step; from the first step of a slide, the last step of the slide before |
| Home, End | First slide, last step of the last slide |
| `d` | Open or close the longer explanation of a scene ("Read more"); Escape closes it |
| `m` | Less motion: every change shows at once |
| `r` | The reading view; Escape goes back |
| `f` | Full screen |

The bar under the stage has the same buttons, a list of the slides, and a button for each step of the current scene. Space and Enter on a button press the button and do not step.

The address names the slide and the step: `#select/uaa` for the step `uaa` of the scene `select`, `#12` for slide 12 when it is a page of text. A link or a reload opens the same state, and stepping to it, stepping back to it or opening it from its address draw the same picture: each step is drawn from its complete state, not from the step before it. An address that names no slide opens the first. The link to the deck in the other language keeps the address, so it opens the same step.

Each step that adds a message shows a dot running along it from its sender to its receiver, and the view glides when a step moves in on a node. "Less motion", or the setting of the system that asks for less motion, leaves both out; nothing is lost. On a narrow screen, such as a phone, the slides are not scaled down: the text flows, the diagram takes the width of the screen, and the text of the step follows below it.

The reading view shows every slide one after the other: a page of text as flowing text, and a scene at each of its checkpoints, as the pptx does, followed by its longer explanation. Printing prints the reading view; "Save as PDF" in the print dialog makes a PDF of it.

With `?check` at the end of the address (`04-registration.html?check`), the page shows every slide, draws every step of every scene, and lists at its bottom each box whose text the browser drew larger than the box, each scene panel whose text does not fit, each node whose name is wider than its box, and each label of a message that covers a node or another label. It confirms the measure of the build with the fonts of a real browser.

[check-html.js](check-html.js) does this for every deck in a headless Chromium. Run it here after `build`:

```sh
node check-html.js   # reads dist/
```

It prints, for each deck, the number of problems and the fonts that drew the text, and fails when there is a problem, or when more than 1% of the glyphs of a deck come from fonts that the deck does not name: that is what a machine without the fonts of the decks shows, and its measure would say nothing. A symbol that none of the fonts has, such as "⊕", comes from another font on any machine and stays under that share. It needs Node with Playwright and its Chromium, and the fonts: on Ubuntu, the packages `fonts-crosextra-carlito` (`Carlito`, with the widths of `Calibri`), `fonts-noto-cjk` (`Noto Sans CJK JP`) and `fonts-dejavu-core` (`DejaVu Sans Mono`).

### Sizes

The units differ between the outputs, and the same number is not the same length in each.

| Where | Unit | Size |
| --- | --- | --- |
| A page of the layout, and the pptx | point; 1 point is 12700 EMU | 960 by 540 points, 13⅓ by 7½ inches |
| The stage of the HTML | CSS pixel | 1280 by 720, scaled to the window. A page of text is drawn at 4/3 of its points, so that it fills the stage. |
| A diagram | its own unit | A grid of 300 by 160 per node on a map, a column of 168 and a row of 42 on a ladder. Each output scales the diagram to its room. |
| The reading view and the printout | CSS pixel | The width of the window, or of the paper |

## Check

`go run ./cmd/slides check` reports:

- a mistake in a source, with its line: an unknown block, a table row with too many cells, an HTML comment;
- a slide whose content does not fit, even at 80% of the text size. Nothing is cut off or shrunk further in silence: split the slide or shorten it;
- a link to a file that does not exist, and a link to a heading of a guide that no longer exists. The decks link to the steps of the guides by heading, so a renamed heading breaks them;
- a code excerpt that is no longer in its file. An excerpt is a code block right below a paragraph that is only a link to a file, and the code blocks in the same language that follow it, such as a second `go` block; a block in another language, such as the `sh` that runs the code, ends them. Each excerpt has to be lines of the file, one after another, as they are there, tabs and all, so a change of the code that the deck did not follow fails the check;
- a deck that is missing in one language, or whose slides differ between the languages in number or in layout, or whose model or scenes differ: the ids, the nodes and their places, the senders and receivers of the events, the names of the signals, the number of fields, and the complete state of each step;
- a mistake in a scene, with its line: an unknown key, an id that is taken, an event that a step shows twice or hides without showing, a node that the scene does not draw, and a checkpoint whose diagram and text do not fit on a page;
- a command of a deck that the repository does not take. The check reads the `sh` blocks and the inline code: for `docker compose`, the services and the profiles of `compose.yaml` (or of the files that `-f` names); for `ue`, also inside `docker compose run`, the subcommands that `ue register -h` lists and, for each subcommand, the flags that `ue SUBCOMMAND -h` prints; for `curl` to the Lab, the method and the path against the routes that `internal/lab` registers, read from its source; and every path under `scripts/`. A `curl` to another address fails until the address and its package are added to `httpHosts` in [internal/facts/routes.go](internal/facts/routes.go).
- a node of a deck that the Compose files no longer run at that address. The check reads every table of a deck and, in each cell, every address of a Compose network and every subnet; a cell may write a list of addresses with the last number only, so `172.28.0.100, 101, 103` is three of them. Each has to be the address of a service, or the subnet of a network, of `compose.yaml` or of `deploy/interconnect/compose.yaml`, which adds the second network and the link between the two. Where the first two columns of a table are the nodes and their addresses, a service that the node column names in code (`` `mrfp` ``) also has to be the service of the addresses of its row. Four numbers outside the range of every Compose network are the clause of a specification, not an address. Only the tables are read: a diagram or a sentence may name an address on purpose that no node has, such as the host of no peer network whose requests a border drops.

A node whose name or role is wider than its box on a map or its head on a ladder, a label of a message that still covers a node or another label after the generator has looked for a free place, and a label wider than its ladder, which the view would cut, fail the check of the deck as well. So does a heading of a Japanese deck, of a slide or of a step, with a full stop inside it, such as 「CAT。着信側が選ぶ呼び出し音」: a heading names what the slide shows in one phrase, with a detail in parentheses.

The text of a scene goes through the same checks as the text of a slide: the lead, the title and the text of each step, and the names and values of the fields of the events it draws. A link in a step, or an address or a command in a field, fails as it would in a paragraph or a table.

For each deck it names the slides whose text was made smaller to fit, and by how much. To read the flags of `ue`, the check builds `./cmd/ue` at the root of the repository and runs it, so a check takes longer while Go builds it.

`go test ./...` at the root of the repository runs the same checks on the decks (the test `TestDecks` in `cmd/slides`), so the CI fails on them too. The tests of `internal/pptx` build every deck as well and check each part of the package: that every part has a content type and every relationship a part, and that each element the writer makes has its children in the order, and the children and attributes, that the schemas of ECMA-376 give its type. They then hand every part to `xmllint` to validate against those schemas themselves: [internal/pptx/testdata/ecma376](internal/pptx/testdata/ecma376/README.md) holds the Transitional schemas that Ecma International publishes with ECMA-376, the ones for the namespaces the writer makes, and names where each comes from. The schemas catch what a reader of the file would also refuse and the check of the order does not see, such as an attribute whose value is outside the set its type allows. A machine without `xmllint` (the package `libxml2-utils`) skips that test and runs the rest; with `PPTX_XMLLINT=xmllint` in the environment, a missing `xmllint` is a failure instead, which is how the CI runs it. `python3 scripts/check_links.py` at the root checks the links of every Markdown file of the repository, the decks included.

## Write a deck

A deck is a text file that reads like Markdown in the repository. The format is small on purpose: a few block types, and a layout line where the default does not fit.

````markdown
---
title: A UE registers with the IMS
footer: ims-practice study session 4. Registration
lang: en
---

# A UE registers with the IMS

ims-practice study session 4 (registration with SIP Digest)

---

## The shape of the whole

```ladder
UE, P-CSCF, I-CSCF, S-CSCF, HSS
UE -> P-CSCF -> I-CSCF: REGISTER // 1 - 2
I-CSCF <-> HSS: UAR, UAA // 3
UE <-- P-CSCF <-- I-CSCF <-- S-CSCF: 401 // 6
```

- The first REGISTER says who. The second proves it.

---

[layout: exercise]

## Looking back

1. Why are there two REGISTERs?
````

- **Front matter.** The file starts with `title` (the name of the deck), `footer` (the text at the foot of each slide) and `lang` (`en` or `ja`, the same as the directory), between two `---` lines. No other key is allowed.
- **Slides.** A line `---` ends a slide. Each slide starts with its heading: `#` for the first slide, the title slide, and for a slide that opens a part of the session; `##` for every other slide. A title slide and a section slide hold only paragraphs, which become the subtitle.
- **Layout.** A line `[layout: NAME]` before the heading picks a layout other than `content`. A `two-columns` slide has one line `[column]` between its two columns.
- **No comments.** An HTML comment `<!-- ... -->` is an error: the decks have no speaker notes. A question for the room goes on its slide with its answer, the answers to the exercises on an `answers` slide after them, and the timing in "Running a session" above.

### Blocks

| Block | How to write it | On the slide |
| --- | --- | --- |
| Paragraph | Lines of text. A line break in the source is a line break on the slide. | Text |
| List | `- ` for bullets, `1. ` for numbers. A nested item is indented by two spaces, one level deep; an indented line without a marker continues the item above it. | Bullets or automatic numbers |
| Table | `\| a \| b \|` rows, with a delimiter row `\| --- \| ---: \|` after the header. `\|` in a cell is `\\|`. | A table across the whole width |
| Code | A fenced block, ` ``` ` with a word such as `sh` or `go`. Tabs become four spaces. | Monospace text, never wrapped; a wide block gets a smaller font |
| Ladder diagram | A fenced block ` ```ladder `, see below. | Shapes |
| Quote | Lines that start with `> `. The decks use them to point to the guide. | Small grey text with a bar on the left |

Inline: `**bold**`, `` `code` ``, `[text](https://example.org/)` and `<https://example.org/>`. A backslash before a mark makes it plain text.

### Ladder diagrams

The first line names the participants, from left to right, separated by commas. Every other line is one row.

| Row | Drawn as |
| --- | --- |
| `A -> B: label` | A request from A to B, a solid arrow with the label above it |
| `A --> B: label` | A response, a dashed arrow |
| `B <- A: label`, `B <-- A: label` | The same, written from right to left |
| `A -> B -> C: label` | One message that passes B: an arrow for each hop. The participants of one row are in order, left to right or right to left |
| `A <-> B: label` | A request and its answer, such as a Diameter pair: one arrow with a head at each end |
| `note A, C: text` | A box across the participants from A to C (`note A: text` for one) |
| `... // remark` | The remark goes to a column on the right, such as the numbers of the steps of the guide |

A label is plain text. A row with a label wider than the diagram, or a participant whose name is wider than its column, fails the check.

### Layouts

| Layout | Chosen by | What it is |
| --- | --- | --- |
| `title` | `#` on the first slide | The title of the session, a short rule and the subtitle, beside a band of the accent colour. |
| `section` | `#` on any later slide | Opens a part of the session, white on the accent colour. |
| `content` | `##`, the default | The title with a rule under it, and the blocks from top to bottom. Tables, code and ladder diagrams take the whole width. |
| `two-columns` | `[layout: two-columns]` | The same, with the blocks in two columns of equal width. |
| `exercise` | `[layout: exercise]` | A content slide in ochre, with the tag "Exercise", for the exercises and "Looking back". |
| `answers` | `[layout: answers]` | A content slide in green, with the tag "Answers", right after its exercise. |
| `summary` | `[layout: summary]` | The blocks on a tinted panel, for "What you will know after this session". |

Every slide has the footer of the deck at the bottom left, and every slide but the first its number at the bottom right. A slide whose content is too tall gets a smaller text, down to 80%; below that the check fails.

### Scenes

A deck with scenes has `format: visual` in its front matter, and between the front matter and the title slide a block `ims-model`: the nodes and every event that a scene of the deck draws. A slide is a scene when its body starts with a block `ims-scene`. The blocks are YAML; every key is known, and a mistyped key fails the check with its line.

````markdown
---
title: A UE registers with the IMS
footer: ims-practice study session 4. Registration
lang: en
format: visual
---

```ims-model
nodes:
  - {id: icscf, label: I-CSCF, role: entry of the home network, at: [2, 0]}
  - {id: hss, label: HSS, role: subscriber data, at: [3, 0]}
  - {id: scscf, label: S-CSCF, role: authenticates and registers, at: [3, 1]}
events:
  - id: uar1
    msg: UAR
    from: icscf
    to: hss
    proto: Cx
    note: (1st)
    fields:
      - User-Authorization-Type: REGISTRATION (0)
  - id: uaa1
    msg: UAA
    from: hss
    to: icscf
    proto: Cx
    note: (1st)
    fields:
      - Experimental-Result: DIAMETER_FIRST_REGISTRATION (2001)
  - id: icscf-select
    do: chooses the S-CSCF
    at: icscf
```

---

# A UE registers with the IMS

---

## The I-CSCF asks the HSS with a UAR (3, 4)

```ims-scene
id: select
lead: The first UAA returns capabilities, not the name of an S-CSCF.
steps:
  - id: uar
    title: It asks the HSS (UAR)
    show: [uar1]
    text: Cx command 300.
  - id: uaa
    title: The HSS returns the capabilities needed (UAA)
    show: [uaa1]
    checkpoint: true
    text: No S-CSCF serves the user yet.
  - id: choose
    title: The I-CSCF chooses the S-CSCF
    show: [icscf-select]
    focus: [icscf]
    zoom: [icscf, hss, scscf]
    text: From its configured list.
```

| Look at | What to see |
| --- | --- |
| Result of the UAA | DIAMETER_FIRST_REGISTRATION (2001), inside Experimental-Result. |
````

The text below the scene block is the longer explanation of the scene: the blocks of any slide, which the HTML shows behind "Read more" and the pptx on a page of its own. It keeps what the learner needs beyond the steps: the tables of what to look at in the capture, the filters, the commands, the links to the guide and the clauses.

**The model.**

| Key | What it is |
| --- | --- |
| `nodes` | Each node has an `id`, a `label` (its name, the same in every language), a `role` (a few words, in the language of the deck, said under the name on a map and on the head of a lifeline) and `at: [column, row]`, its place on the grid of the map. A node keeps its place in every scene of the deck. |
| `groups` | A frame around nodes that belong together, such as two networks: `{id, label, nodes: [...]}`. |
| `events` | Each event has an `id` and is one of three kinds. `msg: NAME` with `from`, `to` and `proto` is one message over one hop: a request and its answer are two events, and so are the hops of one REGISTER. `do: TEXT` with `at` is what one node does with what it received. `rel: TEXT` with `from` and `to` is a relation that is not a signal, such as an import between packages; it is drawn dashed, with an arrowhead. A relation with `proto` is a reference point, such as `rel: Cx` with `proto: Diameter`: the interface between two nodes, drawn in the line of its protocol and without an arrowhead. |
| `note` | A short qualifier under the name of the event, such as `(2nd)`. |
| `fields` | The headers, AVPs or values that a step can show, one `- name: value` each. A value is a line of inline Markdown; put it in quotes when it starts with a backtick. |

The name of a message is the name of the signal (`REGISTER`, `UAA`, `200 OK`) and is the same in both languages. The protocol of a message picks its line: SIP solid, Diameter (`Cx`, `Sh`, `Rx` and the others) dashed, DNS dotted, HTTP and media in colours of their own. The label of each line also names the protocol, so that neither the colour nor the dash is the only sign.

**The scene.**

| Key | What it is |
| --- | --- |
| `id` | The name of the scene in the address, `#select/uaa`. It is unique in the deck and the same in both languages. |
| `view` | `map` (the default): the nodes on their grid, each message between its two nodes. `ladder`: a lifeline for each node and a row for each event, in the order in which the steps show them. |
| `nodes` | The nodes that the scene draws. A map draws every node of the model by default, a ladder those that its events touch. |
| `lead` | One line under the title: the answer to the question of the scene. |
| `basis` | What the diagram is drawn from: `repository` (the default; this implementation, as the guides describe it), `standard` (a procedure of a specification) or `illustration` (an example made up to explain an idea). The diagram says it in a line under it, so that no drawing passes for a capture. |
| `steps` | The steps, in order. |

**A step.**

| Key | What it is |
| --- | --- |
| `id`, `title`, `text` | The name of the step in the address, its title (a name of what happens, not a number), and a line or two that say what the diagram now shows. |
| `show`, `hide`, `only` | The events that the step draws: those of the step before, minus `hide`, plus `show`. `only` starts over from its list. |
| `active` | The event that the step is about; it is drawn strongest. By default it is the last message or process that the step adds. `active: ''` makes none. |
| `focus` | The nodes drawn strongest; by default the ends of the active event. The other nodes that no drawn event touches are drawn faint. |
| `zoom` | The nodes that the HTML closes in on, with the events between them. Without it the view shows the whole diagram. |
| `detail` | The events whose fields the step shows; by default the active event, if it has fields. Two events side by side, such as the first and the second UAA, compare them. |
| `checkpoint` | `true` keeps the state as a page of the pptx and of the reading view. The last step is always a checkpoint. |

The parser turns each step into its complete state, so a step means the same whether the learner steps to it or opens it from its address. A scene in one language has the same ids, nodes, events and states as in the other; only the words differ.

How to choose and split the scenes:

- Give each scene one question that the learner can answer from the diagram, and put the answer in `lead`. Let each step add one thing to understand: a hop, a decision, a value that changes.
- A map shows where a signal goes and who decides; a ladder shows who talks to whom, and in which order. A relation shows what depends on what, such as packages; it is not a signal, and a scene does not mix it with messages.
- A step about what a node decides is a `do` event at that node, with its inputs and its result as fields.
- Where many events gather on a map, start a step with `only` to clear what the learner no longer needs.
- Mark a checkpoint where a still page can explain what happened since the one before. A checkpoint whose text and fields do not fit on a page fails the check: mark an earlier step as a checkpoint.
- Keep everything of the old slide that the steps do not say in the longer explanation: nothing that a learner needs is left out, and nothing goes into speaker notes.

### Add a deck

1. Write the deck in both languages in the same commit: `decks/ja/NN-name.md` and `decks/en/NN-name.md`, with the same slides and the same layouts in the same order, and the same model and scenes. Copy the front matter of an existing deck and change `title` and `footer`.
2. Link to the guides with paths relative to the deck, such as `../../../docs/guide/en/call.md#3-the-s-cscf-turns-the-number-into-a-sip-uri`. Do not copy a ladder diagram of a guide in full; a smaller one is fine when the slide says where the full one is.
3. Cite only the clause numbers that the guide already gives for the step, with the edition the guide names. Telephone numbers are the fictional ones of the guides (+1 202 555 01xx).
4. A code excerpt is a few lines copied exactly from the repository, with a link to its file right above the block. The check fails when the file no longer has those lines in that order, so a change of the code shows which slide to change with it.
5. Run `go run ./cmd/slides build`, `node check-html.js` and `python3 scripts/check_links.py`. Open the HTML and step through every scene, forward and back, on a wide and on a narrow window; open the pptx and read each page of a checkpoint as a learner who has not seen the steps would.

### Change the look, or add a layout

The look is in [internal/layout/theme.go](internal/layout/theme.go): the colours, the fonts, the sizes of the text and the frame of the content slides. Both outputs take it from there, and so does the theme of the pptx.

A new layout takes four places:

1. its name in `Layouts` in [internal/deck/deck.go](internal/deck/deck.go);
2. its boxes in `Slide` in [internal/layout/layout.go](internal/layout/layout.go), with a test in `layout_test.go`;
3. the slide layout of the pptx that it uses, in `layoutOf` in [internal/pptx/slide.go](internal/pptx/slide.go) (the title, section and other slides each have one);
4. a row in the table of layouts above, in both languages.

The HTML needs no change: it draws whatever boxes the layout places.

The diagrams of the scenes are placed in [internal/layout/diagram.go](internal/layout/diagram.go), drawn on the pages of the pptx in [internal/layout/scene.go](internal/layout/scene.go), and drawn on the stage in [internal/html/svg.go](internal/html/svg.go), with the look of the stage in [internal/html/style.go](internal/html/style.go) and its steps in [internal/html/script.go](internal/html/script.go).
