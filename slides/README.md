# Study-session slides

[日本語](README.ja.md)

The decks for study sessions on this repository. Each deck is a text file in a small Markdown-like format. A generator of this repository, [cmd/slides](cmd/slides/main.go), lays out every slide and writes two files of it: a pptx to hand out and to edit in PowerPoint, and an HTML page to present from. Both draw the same boxes at the same places, so a slide looks the same in each.

The decks do not repeat the guides. Each slide says what to look at in the capture and links to the step of the guide in [docs/guide/](../docs/guide/), where the full ladder diagram and the explanation are.

| Deck | Japanese | English | Guide |
| --- | --- | --- | --- |
| 0. Go for reading this repository | [decks/ja/00-go.md](decks/ja/00-go.md) | [decks/en/00-go.md](decks/en/00-go.md) | [A Tour of Go](https://go.dev/tour/) |
| 1. Package layout | [decks/ja/01-packages.md](decks/ja/01-packages.md) | [decks/en/01-packages.md](decks/en/01-packages.md) | [README](../README.md) |
| 2. Registration | [decks/ja/02-registration.md](decks/ja/02-registration.md) | [decks/en/02-registration.md](decks/en/02-registration.md) | [registration](../docs/guide/en/registration.md) |
| 3. Voice call | [decks/ja/03-call.md](decks/ja/03-call.md) | [decks/en/03-call.md](decks/en/03-call.md) | [call](../docs/guide/en/call.md) |
| 4. SMS over IP | [decks/ja/04-sms.md](decks/ja/04-sms.md) | [decks/en/04-sms.md](decks/en/04-sms.md) | [sms](../docs/guide/en/sms.md) |
| 5. Supplementary services (MMTel AS) | [decks/ja/05-mmtel.md](decks/ja/05-mmtel.md) | [decks/en/05-mmtel.md](decks/en/05-mmtel.md) | [mmtel](../docs/guide/en/mmtel.md) |
| 6. VoLTE (access network, IMS AKA, Rx, preconditions) | [decks/ja/06-volte.md](decks/ja/06-volte.md) | [decks/en/06-volte.md](decks/en/06-volte.md) | [volte](../docs/guide/en/volte.md) |

Decks 1 to 6 go from the map of the code through registration, the call and SMS to the supplementary services and the VoLTE call. The numbers are the recommended order: the VoLTE deck comes after the supplementary services, because the VoLTE guide builds on the MMTel AS, and every VoLTE call goes through it. Deck 0 comes before them: it explains the Go that a reader of this repository meets, with excerpts of the code, for those who have not read Go before.

## Running a session

Each deck is one session, held in the order of the numbers. The decks carry no speaker notes: what a presenter says about a slide is on the slide, and the answers to the exercises and to "Looking back" are on the slide after them.

- Keep the guide of the session open next to the slides. The slides give the order of reading; the guide has the explanations and the full ladder diagrams.
- Ask everyone to have the repository open. Best is when everyone runs the scenario; if that is hard, show the presenter's screen, or hand out the capture of the scenario from the artifacts of the CI.
- The clause numbers are those of the editions that the guides name. Tell anyone who has another edition open that the numbers may differ.

| Deck | Time | How the time goes |
| --- | --- | --- |
| 0. Go | 60 min | 25 for "Why Go" (3), running the tests and the shape of the code (10), then 35 for what you meet on every page (20) and a spec-shaped package (10). The exercises take 15 more, in pairs. For those who program in another language but have not read Go; those who read Go start with deck 1. |
| 1. Package layout | 45 min | No signaling: the map of what is where. 3 for the top of the repository, 5 for "internal/ in layers", 10 for following one REGISTER through the code in pairs. |
| 2. Registration | 60 min | 35 for the way to the 200 OK, then 5 for a break or questions at "The UE is registered", and the rest for the subscriptions, the deregistration and the failures. The capture of the CI artifact `registration-capture` can stand in for running it. |
| 3. Call | 60 min | 30 until the INVITE reaches UE 2 (about the slide "6 and 7"), 20 from the answer to the hang-up, the rest for the failures and "Looking back". Whoever dislikes two terminals can drive the UEs from the dashboard. |
| 4. SMS | 75 min | May be held as two sessions; then the first ends after "13 to 15. The status report", at about 45 minutes, and the second starts with "Read the body". Reading the body by hand is a 10-minute exercise. The table of the six MESSAGEs, printed, keeps everyone on track in the capture. The CI artifact `sms-capture` holds the capture and the list of its frames. Registration and the call come first: Path, Service-Route and the LIR are not explained again. |
| 5. Supplementary services | 80 min | The first half is how the AS works and where its settings are; the second half reads the capture service by service. May be held as two sessions, split before "Open the capture". The call and SMS come first: INVITE, PRACK, ACK and how to read an iFC are not explained again. The CI artifact `mmtel-capture` holds the capture and the list of its frames. |
| 6. VoLTE | 90 min | The first half is the access network and IMS AKA, the second half Rx and the call. May be held as two sessions, split before "Policy control over Rx, and the VoLTE call"; end the first half at the AAR right after the registration (#786). Hold it after the session on the MMTel AS: every VoLTE call goes through the MMTel AS, and registration, the call and the three dialogs of the AS are not explained again. The CI artifact `volte-capture` holds the capture, the AMR recordings, the list of frames and the logs of the UEs. |

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
| Links to the guides | Hyperlinks. The package keeps their addresses as URIs, so a Japanese anchor is percent-encoded (`%E7%99%BB...`). |

There are no pictures, and there are no speaker notes.

Known limits:

- The pptx names its fonts: Calibri for Latin text, Yu Gothic for Japanese, Consolas for code. A machine without one of them substitutes another, whose letters may be wider or narrower.
- The generator measures text with the widths of Arial, which are wider than those of Calibri and of the Latin letters of most Japanese fonts. A line therefore fits wherever the fonts are of the usual widths, but the rows of a table may be a little taller than their text needs.
- A slide whose text was made smaller to fit (see "Check") keeps that smaller size in the pptx. Text typed into a box in PowerPoint does not move the boxes below it.

## The HTML

One file per deck, which opens from disk and loads nothing from the network. One slide shows at a time, scaled to the window.

| Key | What it does |
| --- | --- |
| Right, Down, Page Down, Space, Enter | Next slide |
| Left, Up, Page Up, Backspace | Previous slide |
| Home, End | First, last slide |
| `f` | Full screen |

The address keeps the number of the slide (`#12`), so a link or a reload opens the same slide. Printing gives one slide per page of 10 by 5.625 inches, the size of the pptx slides; "Save as PDF" in the print dialog makes a PDF.

With `?check` at the end of the address (`02-registration.html?check`), the page shows every slide and lists at its bottom each box whose text the browser drew larger than the box. It confirms the measure of the build with the fonts of a real browser.

[check-html.js](check-html.js) does this for every deck in a headless Chromium. Run it here after `build`:

```sh
node check-html.js   # reads dist/
```

It prints, for each deck, the number of boxes that overflow and the fonts that drew the text, and fails when a box overflows, or when more than 1% of the glyphs of a deck come from fonts that the deck does not name: that is what a machine without the fonts of the decks shows, and its measure would say nothing. A symbol that none of the fonts has, such as "⊕", comes from another font on any machine and stays under that share. It needs Node with Playwright and its Chromium, and the fonts: on Ubuntu, the packages `fonts-crosextra-carlito` (Carlito, with the widths of Calibri), `fonts-noto-cjk` (Noto Sans CJK JP) and `fonts-dejavu-core` (DejaVu Sans Mono).

## Check

`go run ./cmd/slides check` reports:

- a mistake in a source, with its line: an unknown block, a table row with too many cells, an HTML comment;
- a slide whose content does not fit, even at 80% of the text size. Nothing is cut off or shrunk further in silence: split the slide or shorten it;
- a link to a file that does not exist, and a link to a heading of a guide that no longer exists. The decks link to the steps of the guides by heading, so a renamed heading breaks them;
- a deck that is missing in one language, or whose slides differ between the languages in number or in layout;
- a command of a deck that the repository does not take. The check reads the `sh` blocks and the inline code: for `docker compose`, the services and the profiles of `compose.yaml` (or of the files that `-f` names); for `ue`, also inside `docker compose run`, the subcommands and the flags that `ue register -h` prints; for `curl` to the dashboard, the method and the path against the routes that `internal/dashboard` registers, read from its source; and every path under `scripts/`. A `curl` to another address fails until the address and its package are added to `httpHosts` in [internal/facts/routes.go](internal/facts/routes.go).

For each deck it names the slides whose text was made smaller to fit, and by how much. To read the flags of `ue`, the check runs `go run ./cmd/ue` at the root of the repository, so the first check after a change of the UE simulator takes longer, while Go builds it.

`go test ./...` at the root of the repository runs the same checks on the decks (the test `TestDecks` in `cmd/slides`), so the CI fails on them too. The tests of `internal/pptx` build every deck as well and check each part of the package: that every part has a content type and every relationship a part, and that each element the writer makes has its children in the order, and the children and attributes, that the schemas of ECMA-376 give its type. `python3 scripts/check_links.py` at the root checks the links of every Markdown file of the repository, the decks included.

## Write a deck

A deck is a text file that reads like Markdown in the repository. The format is small on purpose: a few block types, and a layout line where the default does not fit.

````markdown
---
title: A UE registers with the IMS
footer: ims-practice study session 2. Registration
lang: en
---

# A UE registers with the IMS

ims-practice study session 2 (registration with SIP Digest)

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

### Add a deck

1. Write the deck in both languages in the same commit: `decks/ja/NN-name.md` and `decks/en/NN-name.md`, with the same slides and the same layouts in the same order. Copy the front matter of an existing deck and change `title` and `footer`.
2. Link to the guides with paths relative to the deck, such as `../../../docs/guide/en/call.md#3-the-s-cscf-turns-the-number-into-a-sip-uri`. Do not copy a ladder diagram of a guide in full; a smaller one is fine when the slide says where the full one is.
3. Cite only the clause numbers that the guide already gives for the step, with the edition the guide names. Telephone numbers are the fictional ones of the guides (+1 202 555 01xx).
4. A code excerpt is a few lines copied exactly from the repository, with a link to its file right above the block. Check it again when the code changes.
5. Run `go run ./cmd/slides build` and `python3 scripts/check_links.py`, and open the HTML and the pptx to look at the slides.

### Change the look, or add a layout

The look is in [internal/layout/theme.go](internal/layout/theme.go): the colours, the fonts, the sizes of the text and the frame of the content slides. Both outputs take it from there, and so does the theme of the pptx.

A new layout takes four places:

1. its name in `Layouts` in [internal/deck/deck.go](internal/deck/deck.go);
2. its boxes in `Slide` in [internal/layout/layout.go](internal/layout/layout.go), with a test in `layout_test.go`;
3. the slide layout of the pptx that it uses, in `layoutOf` in [internal/pptx/slide.go](internal/pptx/slide.go) (the title, section and other slides each have one);
4. a row in the table of layouts above, in both languages.

The HTML needs no change: it draws whatever boxes the layout places.
