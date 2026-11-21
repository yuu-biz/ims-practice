// The target of an operation: one node, or every node of a role, chosen
// from the inventory. The node operations and the configuration change
// use it.

import type { Endpoint, Scope } from "./api.ts";
import { el, fillSelect } from "./dom.ts";

export interface ScopePicker {
  root: HTMLFieldSetElement;
  scope(): Scope;
  // Called when the choice changes.
  onChange(f: () => void): void;
}

export function scopePicker(name: string, inventory: readonly Endpoint[]): ScopePicker {
  const roles = [...new Set(inventory.flatMap((e) => e.roles))];
  const byNode = el("input", { type: "radio", name: `${name}-kind`, value: "node", checked: true });
  const byRole = el("input", { type: "radio", name: `${name}-kind`, value: "role" });
  const node = el("select", { name: `${name}-node` });
  const role = el("select", { name: `${name}-role` });
  fillSelect(
    node,
    inventory.map((e) => e.name),
  );
  fillSelect(role, roles);
  node.setAttribute("aria-label", "Node");
  role.setAttribute("aria-label", "Role");
  const listeners: (() => void)[] = [];
  const sync = () => {
    node.disabled = !byNode.checked;
    role.disabled = !byRole.checked;
    for (const f of listeners) f();
  };
  for (const control of [byNode, byRole, node, role]) control.addEventListener("change", sync);
  sync();
  const root = el(
    "fieldset",
    { className: "controls" },
    el("legend", { textContent: "Target" }),
    el("label", { className: "check" }, byNode, "One node", node),
    el("label", { className: "check" }, byRole, "Every node of a role", role),
  );
  return {
    root,
    scope: () => (byNode.checked ? { kind: "node", node: node.value } : { kind: "role", role: role.value }),
    onChange: (f) => listeners.push(f),
  };
}
