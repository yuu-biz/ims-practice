// Small DOM helpers. Text from the network is set as text nodes only,
// never parsed as HTML, because SIP payloads come straight off the wire.

export type Child = Node | string;

export function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  props: Partial<HTMLElementTagNameMap[K]> = {},
  ...children: Child[]
): HTMLElementTagNameMap[K] {
  const element = Object.assign(document.createElement(tag), props);
  element.append(...children);
  return element;
}

export function byId<T extends HTMLElement>(id: string): T {
  const element = document.getElementById(id);
  if (!element) throw new Error(`#${id} is missing from index.html`);
  return element as T;
}

export function fillTable(table: HTMLTableElement, rows: Child[][], empty: string): void {
  const body = table.tBodies[0] ?? table.createTBody();
  const columns = table.tHead?.rows[0]?.cells.length ?? 1;
  body.replaceChildren(
    ...(rows.length === 0
      ? [el("tr", {}, el("td", { colSpan: columns, className: "empty" }, empty))]
      : rows.map((cells) => el("tr", {}, ...cells.map((cell) => el("td", {}, cell))))),
  );
}

// "2026-11-19T09:01:00.123456789Z" -> "09:01:00.123". Times are UTC.
export function clock(time: string): string {
  const match = /T(\d{2}:\d{2}:\d{2})(?:\.(\d+))?/.exec(time);
  if (!match) return time;
  return `${match[1]}.${(match[2] ?? "").slice(0, 3).padEnd(3, "0")}`;
}
