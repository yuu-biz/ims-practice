// Writes a Ladder as monospace text, in the form the ladder diagrams of
// the guides use: a row of column names over the lifelines, then one row
// per message, with its arrow drawn from the sender's lifeline to the
// receiver's. Pure, so that the tests need no browser.

import { columnLabel, type Arrow, type Ladder } from "./ladder.ts";

// The narrowest space between two lifelines, as in the guides: ten
// characters, which hold a short label such as "LIR" with dashes to the
// arrowhead. A space grows when a label or a column name needs more.
const narrowestGap = 10;

// What a node that sends to itself draws right of its own lifeline: the
// turn back to the same column, with the arrowhead on the lifeline.
const turnBack = "<-+ ";

// Where each column is, by host.
type Columns = Map<string, number>;

export function ladderText(ladder: Ladder): string {
  const names = numbered(ladder.columns.map(columnLabel));
  const columns: Columns = new Map(ladder.columns.map((column, i) => [column.host, i]));
  const lifelines = place(ladder, columns, names);
  const lines = [header(names, lifelines), ...ladder.arrows.map((arrow) => row(arrow, columns, lifelines))];
  return `${lines.map((line) => line.trimEnd()).join("\n")}\n`;
}

// Two columns can hold the same kind of node, as the UE of the caller
// and the UE of the callee do. The SVG ladder tells them apart by the
// host under each name; the text has only the names, so it numbers
// them from the left.
function numbered(names: string[]): string[] {
  const used = new Map<string, number>();
  return names.map((name) => {
    if (names.filter((other) => other === name).length === 1) return name;
    const nth = (used.get(name) ?? 0) + 1;
    used.set(name, nth);
    return `${name} ${nth}`;
  });
}

// The column of each lifeline. The first one sits far enough right for
// its own name, and every space after it is wide enough for the names
// beside it and for the labels of the arrows that cross it.
function place(ladder: Ladder, columns: Columns, names: string[]): number[] {
  const gaps = names.slice(0, -1).map((name, i) => Math.max(narrowestGap, rightOf(name) + 1 + leftOf(names[i + 1] ?? "")));
  // A label is drawn between the lifelines that its arrow spans.
  // Widening the shortest spans first leaves the longer ones less to
  // ask for.
  for (const { first, last, label } of claims(ladder, columns)) {
    const missing = label.length - between(gaps, first, last);
    if (missing <= 0) continue;
    const spanned = last - first;
    for (let i = 0; i < spanned; i++) {
      gaps[first + i] = (gaps[first + i] ?? 0) + Math.floor(missing / spanned) + (i < missing % spanned ? 1 : 0);
    }
  }
  const lifelines = [leftOf(names[0] ?? "")];
  gaps.forEach((gap, i) => lifelines.push((lifelines[i] ?? 0) + gap + 1));
  return lifelines;
}

// The room the arrows ask for, between the leftmost and the rightmost
// column each one reaches, shortest span first. A message that a node
// sends to itself is drawn right of its own lifeline, so it asks of the
// space to its right; at the last column it has none and runs past the
// end of the row instead.
function claims(ladder: Ladder, columns: Columns): { first: number; last: number; label: string }[] {
  const asked = ladder.arrows.map((arrow) => {
    const from = columns.get(arrow.from) ?? 0;
    const to = columns.get(arrow.to) ?? 0;
    return from === to
      ? { first: from, last: from + 1, label: `${turnBack}${arrow.summary}` }
      : { first: Math.min(from, to), last: Math.max(from, to), label: arrow.summary };
  });
  return asked.filter(({ last }) => last < columns.size).sort((a, b) => a.last - a.first - (b.last - b.first));
}

// The characters between two lifelines: the spaces they span, and the
// lifelines of the columns in between, which an arrow draws over.
function between(gaps: number[], first: number, last: number): number {
  let width = last - first - 1;
  for (let i = first; i < last; i++) width += gaps[i] ?? 0;
  return width;
}

// A name is centred on its lifeline, and reaches one character further
// left than right when its length is even.
const leftOf = (name: string) => Math.floor(name.length / 2);
const rightOf = (name: string) => name.length - 1 - leftOf(name);

function header(names: string[], lifelines: number[]): string {
  let line = "";
  names.forEach((name, i) => {
    line = line.padEnd((lifelines[i] ?? 0) - leftOf(name)) + name;
  });
  return line;
}

function row(arrow: Arrow, columns: Columns, lifelines: number[]): string {
  const cells = new Array<string>((lifelines[lifelines.length - 1] ?? 0) + 1).fill(" ");
  for (const at of lifelines) cells[at] = "|";

  const sender = lifelines[columns.get(arrow.from) ?? 0] ?? 0;
  const receiver = lifelines[columns.get(arrow.to) ?? 0] ?? 0;
  if (sender === receiver) {
    write(cells, sender, `${turnBack}${arrow.summary}`);
    return cells.join("");
  }
  // The label sits at the left end of the arrow and dashes fill the
  // rest, so that the arrowhead reaches the receiver's lifeline.
  const left = Math.min(sender, receiver);
  const right = Math.max(sender, receiver);
  cells[receiver] = sender < receiver ? ">" : "<";
  write(cells, left + 1, arrow.summary.padEnd(right - left - 1, "-"));
  return cells.join("");
}

function write(cells: string[], at: number, text: string): void {
  for (let i = 0; i < text.length; i++) cells[at + i] = text[i] as string;
}
