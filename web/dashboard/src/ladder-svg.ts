// Draws a Ladder as SVG, and the details of one arrow for the side panel.

import type { Message, ShortMessage } from "./api.ts";
import { clock, el } from "./dom.ts";
import type { Arrow, Column, Ladder } from "./ladder.ts";

const svgNs = "http://www.w3.org/2000/svg";

const timeWidth = 80;
const columnWidth = 180;
const headerHeight = 44;
const rowHeight = 28;
// Average width of a label character at 12px, to cut labels that would
// run past the arrow.
const charWidth = 6.6;

const displayNames: Record<string, string> = {
  ue: "UE",
  pcscf: "P-CSCF",
  icscf: "I-CSCF",
  scscf: "S-CSCF",
  ipsmgw: "IP-SM-GW",
  mmtel: "MMTel AS",
  mrfc: "MRFC",
  // The epc node holds the EPC stub and the PCRF, and only the PCRF
  // sends HEP: its Rx messages.
  epc: "PCRF",
  hss: "HSS",
  agw: "IMS-AGW",
  mrfp: "MRFP",
};

export function columnLabel(column: Column): string {
  if (column.node === undefined) return "(no HEP)";
  return displayNames[column.node] ?? column.node;
}

// The key of an arrow that survives a redraw: the id of its first copy.
export const arrowKey = (arrow: Arrow): number => arrow.messages[0]?.id ?? -1;

export function renderLadder(ladder: Ladder, selectedKey: number | null, onSelect: (arrow: Arrow) => void): SVGSVGElement {
  const width = timeWidth + columnWidth * ladder.columns.length;
  const height = headerHeight + rowHeight * ladder.arrows.length + 12;
  const x = new Map(ladder.columns.map((c, i) => [c.host, timeWidth + columnWidth * (i + 0.5)]));
  const start = Date.parse(ladder.arrows[0]?.messages[0]?.time ?? "");

  const svg = svgElement("svg", { width, height, viewBox: `0 0 ${width} ${height}`, role: "img" });
  svg.append(svgElement("title", {}, "Ladder diagram"), markers());

  for (const column of ladder.columns) {
    const cx = x.get(column.host) ?? 0;
    svg.append(
      svgElement("text", { x: cx, y: 16, "text-anchor": "middle", class: "column-name" }, columnLabel(column)),
      svgElement("text", { x: cx, y: 32, "text-anchor": "middle", class: "column-host" }, column.host),
      svgElement("line", { x1: cx, y1: headerHeight - 6, x2: cx, y2: height - 4, class: "lifeline" }),
    );
  }

  const groups: SVGGElement[] = [];
  ladder.arrows.forEach((arrow, row) => {
    const top = headerHeight + rowHeight * row;
    const y = top + rowHeight - 8;
    const x1 = x.get(arrow.from) ?? 0;
    const x2 = x.get(arrow.to) ?? 0;
    const elapsed = (Date.parse(arrow.messages[0]?.time ?? "") - start) / 1000;

    const group = svgElement("g", {
      class: `arrow ${arrow.protocol}`,
      tabindex: 0,
      role: "button",
      "aria-pressed": String(arrowKey(arrow) === selectedKey),
      "aria-label": `${arrow.summary}, ${arrow.from} to ${arrow.to}`,
    });
    group.append(
      svgElement("rect", { x: 0, y: top, width, height: rowHeight, class: "band" }),
      svgElement("title", {}, `${arrow.summary}\n${arrow.messages[0]?.src} -> ${arrow.messages[0]?.dst}`),
      svgElement("text", { x: 4, y, class: "time" }, Number.isNaN(elapsed) ? "" : `+${elapsed.toFixed(3)}s`),
      ...wire(arrow, x1, x2, y),
    );
    const select = () => {
      for (const g of groups) g.setAttribute("aria-pressed", String(g === group));
      onSelect(arrow);
    };
    group.addEventListener("click", select);
    group.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        select();
      }
    });
    groups.push(group);
    svg.append(group);
  });
  return svg;
}

function wire(arrow: Arrow, x1: number, x2: number, y: number): SVGElement[] {
  const marker = `url(#head-${arrow.protocol})`;
  if (x1 === x2) {
    // A node sending to its own host: a small loop to the right.
    const d = `M ${x1} ${y - 8} h 36 v 10 h -32`;
    return [
      svgElement("path", { d, class: "hit" }),
      svgElement("path", { d, class: "wire", "marker-end": marker }),
      svgElement("text", { x: x1 + 42, y: y - 1, class: "label" }, arrow.summary),
    ];
  }
  // Stop short of the lifeline so that the arrowhead touches it.
  const end = x2 + (x2 > x1 ? -2 : 2);
  const span = Math.abs(x2 - x1) - 8;
  return [
    svgElement("line", { x1, y1: y, x2: end, y2: y, class: "hit" }),
    svgElement("line", { x1, y1: y, x2: end, y2: y, class: "wire", "marker-end": marker }),
    svgElement("text", { x: (x1 + x2) / 2, y: y - 5, "text-anchor": "middle", class: "label" }, fit(arrow.summary, span)),
  ];
}

function fit(label: string, width: number): string {
  const max = Math.max(4, Math.floor(width / charWidth));
  return label.length <= max ? label : `${label.slice(0, max - 1)}…`;
}

function markers(): SVGDefsElement {
  const defs = svgElement("defs", {});
  for (const protocol of ["sip", "diameter"]) {
    const marker = svgElement("marker", {
      id: `head-${protocol}`,
      class: protocol,
      viewBox: "0 0 10 10",
      refX: 10,
      refY: 5,
      markerWidth: 8,
      markerHeight: 8,
      orient: "auto",
    });
    marker.append(svgElement("path", { d: "M 0 0 L 10 5 L 0 10 z" }));
    defs.append(marker);
  }
  return defs;
}

function svgElement<K extends keyof SVGElementTagNameMap>(
  tag: K,
  attributes: Record<string, string | number>,
  text?: string,
): SVGElementTagNameMap[K] {
  const element = document.createElementNS(svgNs, tag);
  for (const [name, value] of Object.entries(attributes)) element.setAttribute(name, String(value));
  if (text !== undefined) element.textContent = text;
  return element;
}

// The side panel for one arrow: where it went, who reported it, its spec
// references and its payload.
export function renderDetail(arrow: Arrow, ladder: Ladder): Node[] {
  const first = arrow.messages[0] as Message;
  const label = (host: string) => {
    const column = ladder.columns.find((c) => c.host === host);
    return column ? `${columnLabel(column)} ` : "";
  };
  const refs = first.refs.map((ref) =>
    el("li", {}, `${ref.spec} ${ref.clause === "" ? "(clause unverified)" : ref.clause}${ref.title ? `, ${ref.title}` : ""}`),
  );
  return [
    el("h3", {}, arrow.summary),
    el(
      "dl",
      {},
      el("dt", {}, "Time (UTC)"),
      el("dd", {}, clock(first.time)),
      el("dt", {}, "Protocol"),
      el("dd", {}, `${first.protocol === "sip" ? "SIP" : "Diameter"} over ${first.transport.toUpperCase()}`),
      el("dt", {}, "From"),
      el("dd", {}, `${label(arrow.from)}${first.src}`),
      el("dt", {}, "To"),
      el("dd", {}, `${label(arrow.to)}${first.dst}`),
      el("dt", {}, "Reported by"),
      el("dd", {}, arrow.messages.map((m) => `${m.node} (${m.direction})`).join(", ")),
      ...(first.correlation_id && first.protocol === "diameter"
        ? [el("dt", {}, "Session-Id"), el("dd", {}, first.correlation_id)]
        : []),
    ),
    el("h4", {}, "Spec references"),
    refs.length > 0 ? el("ul", {}, ...refs) : el("p", { className: "note" }, "None given."),
    ...(first.sms === null ? [] : renderShortMessage(first.sms)),
    el("h4", {}, "Payload"),
    el("pre", {}, readablePayload(first)),
  ];
}

// The body of SMS over IP, which the payload shows only as raw octets:
// the RP message, the TPDU inside it, and the text (TS 24.341).
function renderShortMessage(sms: ShortMessage): Node[] {
  return [
    el("h4", {}, "Short message"),
    el(
      "dl",
      {},
      ...sms.fields.flatMap((f) => [el("dt", {}, f.name), el("dd", {}, f.value)]),
      ...(sms.text === "" ? [] : [el("dt", {}, "Text"), el("dd", {}, sms.text)]),
    ),
    ...(sms.error === "" ? [] : [el("p", { className: "note error" }, `Could not decode the rest: ${sms.error}`)]),
    el("h4", {}, "Body (hex)"),
    el("pre", {}, sms.hex),
  ];
}

// The server sends Diameter as one line of JSON; indent it for reading.
function readablePayload(m: Message): string {
  if (m.protocol !== "diameter") return m.payload;
  try {
    return JSON.stringify(JSON.parse(m.payload), null, 2);
  } catch {
    return m.payload;
  }
}
