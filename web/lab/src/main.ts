// The IMS Lab. It shows what the Lab server derived from the
// HEP copies that the nodes send: the calls, the registrations and the
// HSS subscribers, and for a selected call or registration a ladder
// diagram of its SIP and Cx messages with each message's payload and spec
// references. Through the server it also drives the UE simulators, the
// procedures that the EPC stub starts on its own, and the faults of the
// fault agent.

import "./style.css";
import {
  addSubscriber,
  alertingTones,
  deleteSubscriber,
  fetchCallMessages,
  fetchCalls,
  fetchRegistrations,
  fetchSubscribers,
  HttpError,
  ladderCallIds,
  parseMessage,
  putAlertingTone,
  type Call,
  type Message,
  type Registration,
  type Subscriber,
} from "./api.ts";
import { byId, clock, el, fillTable } from "./dom.ts";
import { mountEPCPanel } from "./epc-panel.ts";
import { mountFaultPanel } from "./fault-panel.ts";
import { buildLadder, type Ladder } from "./ladder.ts";
import { arrowKey, renderDetail, renderLadder } from "./ladder-svg.ts";
import { ladderText } from "./ladder-text.ts";
import { mountUEPanel } from "./ue-cards.ts";

const view = {
  // The calls last loaded.
  calls: [] as Call[],
  // The Call-ID of the call or the registration whose ladder is drawn,
  // and the Call-IDs of all the dialogs of that call.
  selectedCallId: null as string | null,
  selectedLegs: [] as string[],
  callMessages: [] as Message[],
  // The ladder now drawn, which the Copy as text button writes out.
  ladder: { columns: [], arrows: [] } as Ladder,
  selectedArrow: null as number | null,
  // The HSS subscriptions last loaded, which give the UE panel the tel
  // URI to call.
  subscribers: [] as Subscriber[],
};

const callsTable = byId<HTMLTableElement>("calls");
const ladderBox = byId<HTMLDivElement>("ladder");
const detailBox = byId<HTMLElement>("detail");
const copyButton = byId<HTMLButtonElement>("ladder-copy");
const copyNote = byId<HTMLSpanElement>("ladder-copy-note");
const ladderTextBox = byId<HTMLPreElement>("ladder-text");
const registrationsTable = byId<HTMLTableElement>("registrations");
const subscribersTable = byId<HTMLTableElement>("subscribers");
const subscriberForm = byId<HTMLFormElement>("subscriber-form");

function showNote(id: string, text: string | null, isError = true): void {
  const note = byId<HTMLParagraphElement>(id);
  note.hidden = text === null;
  note.textContent = text ?? "";
  note.classList.toggle("error", isError);
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

async function refreshCalls(): Promise<void> {
  try {
    const calls = await fetchCalls();
    view.calls = calls;
    showNote("calls-note", null);
    fillTable(callsTable, calls.map(callRow), "No calls captured yet.");
    // A call through an application server gets its next dialog while
    // its ladder is open.
    const selected = view.selectedCallId;
    if (selected !== null && ladderCallIds(calls, selected).join(" ") !== view.selectedLegs.join(" ")) {
      void selectCall(selected);
    }
  } catch (error) {
    showNote("calls-note", `Could not load the calls: ${describe(error)}`);
  }
}

// A button that draws the ladder of callId.
function ladderButton(label: string, callId: string): HTMLButtonElement {
  const select = el("button", { type: "button", className: "link", textContent: label });
  select.dataset["callId"] = callId;
  select.setAttribute("aria-current", String(callId === view.selectedCallId));
  select.addEventListener("click", () => void selectCall(callId));
  return select;
}

function callRow(call: Call): (string | Node)[] {
  return [
    ladderButton(call.call_id, call.call_id),
    String(call.legs.length),
    call.from,
    call.to,
    call.state,
    call.status === null ? "" : String(call.status),
    clock(call.started),
    call.ended === null ? "" : clock(call.ended),
    String(call.messages),
  ];
}

// selectCall draws the ladder of the call that dialogCallId is one
// dialog of, such as the dialog that a UE card knows, or of the
// registration with that Call-ID.
async function selectCall(dialogCallId: string): Promise<void> {
  const legs = ladderCallIds(view.calls, dialogCallId);
  const callId = legs[0] ?? dialogCallId;
  if (view.selectedCallId !== callId) view.selectedArrow = null;
  copyNote.textContent = "";
  ladderTextBox.hidden = true;
  view.selectedCallId = callId;
  view.selectedLegs = legs;
  for (const button of document.querySelectorAll<HTMLButtonElement>("button[data-call-id]")) {
    button.setAttribute("aria-current", String(button.dataset["callId"] === callId));
  }
  byId("ladder-call").textContent = legs.length > 1 ? `${callId} and ${legs.length - 1} more dialogs` : callId;
  let messages: Message[];
  try {
    messages = await fetchCallMessages(legs);
  } catch (error) {
    if (view.selectedCallId !== callId) return;
    view.callMessages = [];
    view.ladder = { columns: [], arrows: [] };
    copyButton.disabled = true;
    showNote("ladder-note", `Could not load the messages of ${callId}: ${describe(error)}`);
    ladderBox.replaceChildren();
    detailBox.hidden = true;
    return;
  }
  // A later selection wins over a slow answer for an earlier one.
  if (view.selectedCallId !== callId) return;
  view.callMessages = messages;
  drawLadder();
}

// The ladder as the monospace text of the guides' ladder diagrams. The
// clipboard needs a secure context, which the Lab has on localhost but
// not over plain HTTP from another machine; there the page shows the
// text instead, for the reader to select and copy.
async function copyLadderAsText(): Promise<void> {
  const text = ladderText(view.ladder);
  try {
    await navigator.clipboard.writeText(text);
    ladderTextBox.hidden = true;
    copyNote.textContent = "The ladder is on the clipboard as text.";
  } catch {
    ladderTextBox.textContent = text;
    ladderTextBox.hidden = false;
    copyNote.textContent = "This browser gave the page no clipboard. Select the text below and copy it.";
  }
}

function drawLadder(): void {
  if (view.selectedCallId === null) return;
  const ladder = buildLadder(view.callMessages);
  view.ladder = ladder;
  copyButton.disabled = ladder.arrows.length === 0;
  // The ladder of a call that goes on grows; the text shown for copying
  // grows with it.
  if (!ladderTextBox.hidden) ladderTextBox.textContent = ladderText(ladder);
  const legend = el(
    "p",
    { className: "legend" },
    el("span", { className: "sip" }, "SIP"),
    el("span", { className: "diameter" }, "Diameter (Cx)"),
  );
  ladderBox.replaceChildren(
    legend,
    renderLadder(ladder, view.selectedArrow, (arrow) => {
      view.selectedArrow = arrowKey(arrow);
      detailBox.replaceChildren(...renderDetail(arrow, ladder));
      detailBox.hidden = false;
    }),
  );
  const selected = ladder.arrows.find((a) => arrowKey(a) === view.selectedArrow);
  detailBox.hidden = selected === undefined;
  if (selected) detailBox.replaceChildren(...renderDetail(selected, ladder));
  showNote(
    "ladder-note",
    `${ladder.arrows.length} messages. Select an arrow to see its payload and spec references.`,
    false,
  );
}

async function refreshRegistrations(): Promise<void> {
  try {
    const registrations = await fetchRegistrations();
    showNote("registrations-note", null);
    fillTable(registrationsTable, registrations.map(registrationRow), "No registrations captured yet.");
  } catch (error) {
    showNote("registrations-note", `Could not load the registrations: ${describe(error)}`);
  }
}

// The IMPU draws the ladder of the registration, when the server saw its
// REGISTER.
function registrationRow(r: Registration): (string | Node)[] {
  const impu = r.call_id === "" ? r.impu : ladderButton(r.impu, r.call_id);
  return [impu, r.impi, r.contact, clock(r.expires), r.state, r.scscf];
}

async function refreshSubscribers(): Promise<void> {
  try {
    const subscribers = await fetchSubscribers();
    setProvisioning(true);
    showNote("subscribers-note", null);
    view.subscribers = subscribers;
    renderUEPanel();
    fillTable(subscribersTable, subscribers.map(subscriberRow), "The HSS has no subscribers.");
  } catch (error) {
    const unavailable = error instanceof HttpError && error.status === 503;
    setProvisioning(!unavailable);
    fillTable(subscribersTable, [], "Not available.");
    showNote(
      "subscribers-note",
      unavailable
        ? "Subscriber provisioning is not configured. Set lab.hss_provisioning in the Lab config to list, add and delete subscribers."
        : `Could not load the subscribers: ${describe(error)}`,
    );
  }
}

function setProvisioning(available: boolean): void {
  subscriberForm.querySelector("fieldset")!.disabled = !available;
}

function subscriberRow(s: Subscriber): (string | Node)[] {
  const remove = el("button", { type: "button", className: "link danger", textContent: "Delete" });
  remove.addEventListener("click", () => void removeSubscriber(s.private_identity));
  return [s.private_identity, s.public_identities.join("\n"), toneSelect(s), remove];
}

// The customized alerting tone of a subscriber: what its callers hear
// while it rings. "off" keeps the provisioned tone but stops it, so the
// callers hear their own ringing tone again.
function toneSelect(s: Subscriber): HTMLSelectElement {
  const current = s.customized_alerting_tone;
  const select = el(
    "select",
    { name: "customized_alerting_tone" },
    el("option", { value: "", textContent: "off (ringing tone)" }),
    ...alertingTones.map((tone) => el("option", { value: tone, textContent: tone })),
  );
  select.value = current?.active ? current.tone : "";
  select.setAttribute("aria-label", `Customized alerting tone of ${s.private_identity}`);
  select.addEventListener("change", () => {
    const tone = select.value === "" ? { tone: current?.tone ?? alertingTones[0], active: false } : { tone: select.value, active: true };
    void (async () => {
      try {
        await putAlertingTone(s.private_identity, tone);
      } catch (error) {
        showNote("subscribers-note", `Could not set the alerting tone of ${s.private_identity}: ${describe(error)}`);
      }
      await refreshSubscribers();
    })();
  });
  return select;
}

async function removeSubscriber(privateIdentity: string): Promise<void> {
  if (!confirm(`Delete the subscriber ${privateIdentity} from the HSS?`)) return;
  try {
    await deleteSubscriber(privateIdentity);
  } catch (error) {
    showNote("subscribers-note", `Could not delete ${privateIdentity}: ${describe(error)}`);
    return;
  }
  await refreshSubscribers();
}

subscriberForm.addEventListener("submit", (event) => {
  event.preventDefault();
  const data = new FormData(subscriberForm);
  const field = (name: string) => String(data.get(name) ?? "").trim();
  const subscriber = {
    private_identity: field("private_identity"),
    public_identities: field("public_identities")
      .split(/\s*\n\s*/)
      .filter((line) => line !== ""),
    password: field("password"),
  };
  void (async () => {
    try {
      await addSubscriber(subscriber);
    } catch (error) {
      showNote("subscribers-note", `Could not add ${subscriber.private_identity}: ${describe(error)}`);
      return;
    }
    subscriberForm.reset();
    await refreshSubscribers();
  })();
});

// A Cx copy that arrived before the server could link it comes without a
// call_id; the next reload of the ladder has it.
function onMessage(m: Message): void {
  if (m.call_id === "" || !view.selectedLegs.includes(m.call_id)) return;
  if (view.callMessages.some((seen) => seen.id === m.id)) return;
  view.callMessages = [...view.callMessages, m];
  drawLadder();
}

// The server pushes every captured message and a "state" event whenever
// the calls or registrations change. EventSource reconnects by itself;
// after a gap the page reloads everything, since events sent during the
// gap are lost.
function listen(): void {
  const live = byId("live");
  const setLive = (state: "live" | "down", text: string) => {
    live.dataset["state"] = state;
    live.textContent = text;
  };
  let missedEvents = false;
  const events = new EventSource("/api/events");
  events.addEventListener("open", () => {
    setLive("live", "live");
    if (!missedEvents) return;
    missedEvents = false;
    void refreshCalls();
    void refreshRegistrations();
    if (view.selectedCallId !== null) void selectCall(view.selectedCallId);
  });
  events.addEventListener("error", () => {
    missedEvents = true;
    setLive("down", "disconnected, retrying");
  });
  events.addEventListener("state", () => {
    void refreshCalls();
    void refreshRegistrations();
  });
  events.addEventListener("message", (event) => {
    let m: Message;
    try {
      m = parseMessage(JSON.parse(event.data as string));
    } catch (error) {
      console.warn("ignoring a malformed message event", error);
      return;
    }
    onMessage(m);
  });
}

copyButton.addEventListener("click", () => void copyLadderAsText());

const renderUEPanel = mountUEPanel(byId("ues"), byId("ues-note"), {
  subscribers: () => view.subscribers,
  selectCall: (callId) => void selectCall(callId),
});
mountEPCPanel(byId("epc-ues"), byId("epc-note"));
mountFaultPanel(byId("fault-form"), byId("faults"), byId("faults-note"));
void refreshCalls();
void refreshRegistrations();
void refreshSubscribers();
listen();
