// How the outcomes of a bulk provisioning or a restore are shown while
// they stream: the counts by result, and the outcomes that the tally
// lists. Redrawn at most once a frame, however fast the lines arrive.

import { results } from "./api.ts";
import { el, fillTable } from "./dom.ts";
import { maxListed, type OutcomeTally } from "./view.ts";

export interface OutcomeView {
  root: HTMLElement;
  // Draws the tally on the next frame.
  update(t: OutcomeTally, status: string): void;
}

export function outcomeView(caption: string, empty: string): OutcomeView {
  const status = el("p", { className: "note" });
  status.setAttribute("role", "status");
  const counts = el("ul", { className: "counts" });
  const table = el(
    "table",
    {},
    el("caption", { className: "visually-hidden", textContent: caption }),
    el("thead", {}, el("tr", {}, ...["Line", "Private identity", "Result", "Reason"].map((h) => el("th", { textContent: h })))),
  );
  const more = el("p", { className: "note", hidden: true });
  const root = el("div", { hidden: true }, status, counts, el("div", { className: "scroll" }, table), more);
  let pending: { t: OutcomeTally; status: string } | null = null;
  const draw = () => {
    if (pending === null) return;
    const { t, status: text } = pending;
    pending = null;
    root.hidden = false;
    status.textContent = text;
    counts.replaceChildren(...results.map((r) => el("li", { className: t.counts[r] > 0 ? `result-${r}` : "", textContent: `${r} ${t.counts[r]}` })));
    fillTable(
      table,
      t.listed.map((o) => [o.line === 0 ? "" : String(o.line), o.private_identity, el("span", { className: `result-${o.result}`, textContent: o.result }), o.reason]),
      empty,
    );
    more.hidden = t.unlisted === 0;
    more.textContent = `${t.unlisted} more not listed: the screen lists the first ${maxListed}.`;
  };
  return {
    root,
    update(t, text) {
      const scheduled = pending !== null;
      pending = { t, status: text };
      if (!scheduled) requestAnimationFrame(draw);
    },
  };
}
