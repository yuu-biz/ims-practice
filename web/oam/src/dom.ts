// Small DOM helpers, as in the dashboard. Text from the network is set
// as text nodes only, never parsed as HTML.

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

export function showNote(note: HTMLElement, text: string | null, isError = true): void {
  note.hidden = text === null;
  note.textContent = text ?? "";
  note.classList.toggle("error", isError);
}

export function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

// A select whose options are values, each its own label.
export function fillSelect(select: HTMLSelectElement, values: readonly string[], selected = select.value): void {
  select.replaceChildren(...values.map((v) => el("option", { value: v, textContent: v, selected: v === selected })));
}

const confirmDialog = document.createElement("dialog");
confirmDialog.className = "confirm";
confirmDialog.setAttribute("aria-labelledby", "confirm-title");

// confirmAction asks the operator to confirm an action that changes the
// network, stating what it acts on. It resolves true only when the
// operator chose the action button. Escape and Cancel resolve false.
export function confirmAction(title: string, scope: readonly string[], action: string): Promise<boolean> {
  if (!confirmDialog.isConnected) document.body.append(confirmDialog);
  const cancel = el("button", { type: "button", textContent: "Cancel" });
  const ok = el("button", { type: "button", className: "danger", textContent: action });
  confirmDialog.replaceChildren(
    el("h2", { id: "confirm-title", textContent: title }),
    el("ul", {}, ...scope.map((line) => el("li", { textContent: line }))),
    el("div", { className: "buttons" }, cancel, ok),
  );
  return new Promise((resolve) => {
    const done = (answer: boolean) => {
      confirmDialog.removeEventListener("close", onClose);
      if (confirmDialog.open) confirmDialog.close();
      resolve(answer);
    };
    const onClose = () => done(false);
    confirmDialog.addEventListener("close", onClose);
    cancel.addEventListener("click", () => done(false));
    ok.addEventListener("click", () => done(true));
    confirmDialog.showModal();
    // The safe choice has the focus, so that Enter does not act.
    cancel.focus();
  });
}
