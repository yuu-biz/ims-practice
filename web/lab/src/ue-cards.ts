// The UE simulator panel: a card per UE that the Lab controls, kept
// live by each UE's event stream through the Lab.

import {
  answerModes,
  diversionServices,
  fetchUEServices,
  fetchUESimulators,
  parseUEStatus,
  postUEAction,
  postUECall,
  postUEDTMF,
  postUESMS,
  putAnswerPolicy,
  putUEService,
  ueEventsUrl,
  type AnswerMode,
  type SupplementaryService,
  type SupplementaryServices,
  type Subscriber,
  type UEAction,
  type UESMSHistory,
  type UESimulator,
  type UEStatus,
} from "./api.ts";
import { clock, el } from "./dom.ts";
import {
  describeCall,
  describeConference,
  describePolicy,
  describeSentSMS,
  describeService,
  describeSession,
  enabledControls,
  hangUpEffect,
  serviceLabel,
  suggestedNumber,
  suggestedTarget,
} from "./ue-panel.ts";

// How often the panel asks again for a UE whose stream is not open, such
// as one that is not running yet.
const retryMs = 5000;

interface Card {
  ue: UESimulator;
  busy: boolean;
  // Open while the UE answers. A stream that fails is closed and opened
  // again by the next retry, since EventSource gives up for good on the
  // 502 that the Lab answers for a UE that is down.
  events: EventSource | null;
  // The user typed a target or a number; the suggestion no longer
  // replaces it.
  targetEdited: boolean;
  smsToEdited: boolean;
  divertTargetEdited: boolean;
  root: HTMLElement;
  impu: HTMLElement;
  registration: HTMLElement;
  call: HTMLElement;
  // The other call on the line, and the conference.
  otherCall: HTMLElement;
  conference: HTMLElement;
  session: HTMLElement;
  showLadder: HTMLButtonElement;
  note: HTMLParagraphElement;
  buttons: Record<UEAction, HTMLButtonElement>;
  target: HTMLInputElement;
  dial: HTMLButtonElement;
  dtmfDigits: HTMLInputElement;
  dtmfSend: HTMLButtonElement;
  policy: HTMLSelectElement;
  policyText: HTMLElement;
  smsTo: HTMLInputElement;
  smsText: HTMLInputElement;
  smsStatusReport: HTMLInputElement;
  smsSend: HTMLButtonElement;
  smsSent: HTMLUListElement;
  smsReceived: HTMLUListElement;
  // The supplementary services, read over Ut when the section opens.
  // The UE keeps no copy, so neither does the card beyond the last
  // answer.
  services: HTMLDetailsElement;
  settings: SupplementaryServices | null;
  serviceList: HTMLUListElement;
  servicesEtag: HTMLElement;
  divertService: HTMLSelectElement;
  divertTarget: HTMLInputElement;
  divertOn: HTMLButtonElement;
  divertOff: HTMLButtonElement;
  servicesReload: HTMLButtonElement;
}

export interface UEPanelOptions {
  // The HSS subscriptions that the page has loaded, for the tel URI of
  // the other UE.
  subscribers: () => readonly Subscriber[];
  // Draws the ladder of a Call-ID.
  selectCall: (callId: string) => void;
}

export function mountUEPanel(container: HTMLElement, note: HTMLParagraphElement, options: UEPanelOptions): () => void {
  const cards = new Map<string, Card>();
  let listed = false;

  const render = () => {
    const all = [...cards.values()].map((c) => c.ue);
    for (const card of cards.values()) renderCard(card, all, options.subscribers(), options.selectCall);
  };

  const setStatus = (card: Card, status: UEStatus) => {
    card.ue = { name: card.ue.name, reachable: true, status };
    render();
  };

  // The status in the answer to a request is older than the events
  // that the request itself causes, which may come first: the UE answers
  // a POST /answer before its 200 goes out. With the stream open, the
  // events alone keep the card.
  const answered = (card: Card, status: UEStatus) => {
    if (card.events === null) setStatus(card, status);
  };

  const run = async (card: Card, what: string, request: () => Promise<void>) => {
    card.busy = true;
    card.note.hidden = true;
    render();
    try {
      await request();
    } catch (error) {
      card.note.hidden = false;
      card.note.textContent = `Could not ${what}: ${error instanceof Error ? error.message : String(error)}`;
    } finally {
      card.busy = false;
      render();
    }
  };

  const open = (card: Card) => {
    if (card.events !== null || !card.ue.reachable) return;
    const events = new EventSource(ueEventsUrl(card.ue.name));
    card.events = events;
    events.addEventListener("message", (event) => {
      try {
        setStatus(card, parseUEStatus(JSON.parse(event.data as string)));
      } catch (error) {
        console.warn("ignoring a malformed UE status", error);
      }
    });
    events.addEventListener("error", () => {
      events.close();
      card.events = null;
      card.ue = { name: card.ue.name, reachable: false, error: "the event stream stopped" };
      render();
    });
  };

  const refresh = async () => {
    let list: UESimulator[];
    try {
      list = await fetchUESimulators();
    } catch (error) {
      note.hidden = false;
      note.textContent = `Could not load the UE simulators: ${error instanceof Error ? error.message : String(error)}`;
      return;
    }
    listed = true;
    note.hidden = list.length > 0;
    note.textContent = "No UE simulators are configured. List them under lab.ues in the Lab config.";
    for (const ue of list) {
      let card = cards.get(ue.name);
      if (card === undefined) {
        card = newCard(ue, { run, answered, selectCall: options.selectCall });
        cards.set(ue.name, card);
        container.append(card.root);
      }
      // A UE with an open stream is already up to date.
      if (card.events === null) card.ue = ue;
      open(card);
    }
    render();
  };

  void refresh();
  setInterval(() => {
    if (!listed || [...cards.values()].some((c) => c.events === null)) void refresh();
  }, retryMs);
  return render;
}

interface CardActions {
  run: (card: Card, what: string, request: () => Promise<void>) => Promise<void>;
  answered: (card: Card, status: UEStatus) => void;
  selectCall: (callId: string) => void;
}

function newCard(ue: UESimulator, actions: CardActions): Card {
  const button = (label: string) => el("button", { type: "button", textContent: label });
  const buttons: Record<UEAction, HTMLButtonElement> = {
    register: button("Register"),
    deregister: button("Deregister"),
    answer: button("Answer"),
    hangup: button("Hang up"),
    hold: button("Hold"),
    resume: button("Resume"),
    swap: button("Swap"),
    merge: button("Merge"),
  };
  const target = el("input", { name: "target", required: true, placeholder: "tel: or sip: URI", spellcheck: false });
  const dial = el("button", { type: "submit", textContent: "Call" });
  const form = el("form", { className: "ue-call" }, target, dial);
  // RFC 4733 3.2 names the events 0-9, *, # and A-D; the UE refuses any
  // other character with 400.
  const dtmfDigits = el("input", { name: "dtmf_digits", required: true, placeholder: "12#", pattern: "[0-9*#A-Da-d]+", spellcheck: false });
  const dtmfSend = el("button", { type: "submit", textContent: "Send DTMF" });
  const dtmfForm = el(
    "form",
    { className: "ue-dtmf" },
    el("label", {}, el("span", { className: "visually-hidden" }, "DTMF digits"), dtmfDigits),
    dtmfSend,
  );
  const smsTo = el("input", { name: "sms_to", required: true, placeholder: "+12025550102", spellcheck: false });
  const smsText = el("input", { name: "sms_text", required: true, placeholder: "text", maxLength: 160 });
  const smsStatusReport = el("input", { type: "checkbox", name: "sms_status_report" });
  const smsSend = el("button", { type: "submit", textContent: "Send SMS" });
  const smsForm = el(
    "form",
    { className: "ue-sms" },
    el("label", {}, el("span", { className: "visually-hidden" }, "SMS to"), smsTo),
    el("label", {}, el("span", { className: "visually-hidden" }, "SMS text"), smsText),
    el("label", {}, smsStatusReport, " status report"),
    smsSend,
  );
  const policy = el(
    "select",
    { name: "answer_policy" },
    ...answerModes.map((mode) => el("option", { value: mode, textContent: mode })),
  );
  const divertService = el(
    "select",
    { name: "divert_service" },
    ...diversionServices.map((service) => el("option", { value: service, textContent: serviceLabel(service) })),
  );
  const divertTarget = el("input", { name: "divert_target", placeholder: "tel: or sip: URI", spellcheck: false });
  const divertOn = el("button", { type: "button", textContent: "On" });
  const divertOff = el("button", { type: "button", textContent: "Off" });
  const servicesReload = el("button", { type: "button", className: "link", textContent: "reload" });
  const card: Card = {
    ue,
    busy: false,
    events: null,
    targetEdited: false,
    smsToEdited: false,
    root: el("article", { className: "ue-card" }),
    impu: el("span", { className: "mono" }),
    registration: el("span"),
    call: el("span"),
    otherCall: el("span"),
    conference: el("span"),
    session: el("span"),
    showLadder: el("button", { type: "button", className: "link", textContent: "ladder" }),
    note: el("p", { className: "note error", hidden: true }),
    buttons,
    target,
    dial,
    dtmfDigits,
    dtmfSend,
    policy,
    policyText: el("span", { className: "muted" }),
    smsTo,
    smsText,
    smsStatusReport,
    smsSend,
    smsSent: el("ul", { className: "ue-sms-list" }),
    smsReceived: el("ul", { className: "ue-sms-list" }),
    divertTargetEdited: false,
    services: el("details", { className: "ue-services" }),
    settings: null,
    serviceList: el("ul", { className: "ue-service-list" }),
    servicesEtag: el("span", { className: "muted mono" }),
    divertService,
    divertTarget,
    divertOn,
    divertOff,
    servicesReload,
  };
  const name = ue.name;
  const act = (action: UEAction, what: string) =>
    void actions.run(card, what, async () => actions.answered(card, await postUEAction(name, action)));
  buttons.register.addEventListener("click", () => act("register", "register"));
  buttons.deregister.addEventListener("click", () => act("deregister", "deregister"));
  buttons.answer.addEventListener("click", () => act("answer", "answer"));
  buttons.hangup.addEventListener("click", () => act("hangup", "hang up"));
  buttons.hold.addEventListener("click", () => act("hold", "put the call on hold"));
  buttons.resume.addEventListener("click", () => act("resume", "resume the call"));
  buttons.swap.addEventListener("click", () => act("swap", "swap the calls"));
  buttons.merge.addEventListener("click", () => act("merge", "merge the calls into a conference"));
  dtmfForm.addEventListener("submit", (event) => {
    event.preventDefault();
    const digits = dtmfDigits.value.trim();
    void actions.run(card, `send the DTMF digits ${digits}`, async () => {
      actions.answered(card, await postUEDTMF(name, digits));
      dtmfDigits.value = "";
    });
  });
  target.addEventListener("input", () => (card.targetEdited = target.value !== ""));
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const to = target.value.trim();
    void actions.run(card, `call ${to}`, async () => actions.answered(card, await postUECall(name, to)));
  });
  smsTo.addEventListener("input", () => (card.smsToEdited = smsTo.value !== ""));
  smsForm.addEventListener("submit", (event) => {
    event.preventDefault();
    const sms = { to: smsTo.value.trim(), text: smsText.value, status_report: smsStatusReport.checked };
    // The UE's events bring the message into the list, and later its
    // submit report and status report.
    void actions.run(card, `send a short message to ${sms.to}`, async () => {
      await postUESMS(name, sms);
      smsText.value = "";
    });
  });
  policy.addEventListener("change", () => {
    const mode = policy.value as AnswerMode;
    void actions.run(card, "set the answer policy", async () => {
      const answerPolicy = await putAnswerPolicy(name, mode);
      if (card.ue.reachable) actions.answered(card, { ...card.ue.status, answer_policy: answerPolicy });
    });
  });
  // Each of these makes the UE read or change its simservs document over
  // Ut; the XCAP requests are in the capture, not in the ladders.
  const loadServices = () =>
    void actions.run(card, "read the supplementary services", async () => {
      card.settings = await fetchUEServices(name);
    });
  const divert = (active: boolean) => {
    const service = divertService.value as SupplementaryService;
    const to = active ? divertTarget.value.trim() : "";
    void actions.run(card, `switch ${serviceLabel(service)} ${active ? "on" : "off"}`, async () => {
      card.settings = await putUEService(name, service, active, to);
    });
  };
  card.services.addEventListener("toggle", () => {
    if (card.services.open && card.settings === null) loadServices();
  });
  servicesReload.addEventListener("click", loadServices);
  divertTarget.addEventListener("input", () => (card.divertTargetEdited = divertTarget.value !== ""));
  divertOn.addEventListener("click", () => divert(true));
  divertOff.addEventListener("click", () => divert(false));
  card.showLadder.addEventListener("click", () => {
    if (card.ue.reachable && card.ue.status.call?.call_id) actions.selectCall(card.ue.status.call.call_id);
  });

  const row = (label: string, ...value: (Node | string)[]) => el("div", {}, el("dt", {}, label), el("dd", {}, ...value));
  card.root.append(
    el("h3", {}, name, " ", card.impu),
    el(
      "dl",
      {},
      row("Registration", card.registration),
      row("Call", card.call, " ", card.showLadder),
      row("Other call", card.otherCall),
      row("Conference", card.conference),
      row("Session", card.session),
      row("Answers", el("label", {}, el("span", { className: "visually-hidden" }, "Answer policy"), policy), " ", card.policyText),
    ),
    el("div", { className: "ue-buttons" }, buttons.register, buttons.deregister, buttons.answer, buttons.hangup, buttons.hold, buttons.resume, buttons.swap, buttons.merge),
    form,
    dtmfForm,
    smsForm,
    el("dl", {}, row("SMS sent", card.smsSent), row("SMS received", card.smsReceived)),
    card.services,
    card.note,
  );
  card.services.append(
    el("summary", {}, "Supplementary services (Ut/XCAP)"),
    el(
      "div",
      { className: "ue-divert" },
      el("label", {}, el("span", { className: "visually-hidden" }, "Call forwarding service"), divertService),
      el("label", {}, el("span", { className: "visually-hidden" }, "Forward to"), divertTarget),
      divertOn,
      divertOff,
    ),
    card.serviceList,
    el("p", { className: "muted" }, "simservs.xml ETag ", card.servicesEtag, " ", servicesReload),
  );
  return card;
}

// The settings of the last answer, one line per service, or nothing yet.
function renderServices(card: Card, target: string): void {
  const usable = card.ue.reachable && !card.busy;
  for (const control of [card.divertService, card.divertTarget, card.divertOn, card.divertOff, card.servicesReload]) control.disabled = !usable;
  if (!card.divertTargetEdited) card.divertTarget.value = target;
  const { settings } = card;
  card.servicesEtag.textContent = settings?.etag ?? "";
  card.serviceList.replaceChildren(
    ...(settings === null
      ? [el("li", { className: "muted" }, "not read yet")]
      : [
          ...settings.services.map((s) => el("li", { className: s.active ? "" : "muted" }, describeService(s))),
          el("li", { className: "muted" }, `OIR by default: ${settings.oir_default_behaviour ?? "not provisioned"}`),
        ]),
  );
}

function renderCard(
  card: Card,
  all: readonly UESimulator[],
  subscribers: readonly Subscriber[],
  selectCall: (callId: string) => void,
): void {
  const { ue } = card;
  const controls = enabledControls(ue, card.busy);
  for (const action of ["register", "deregister", "answer", "hangup", "hold", "resume", "swap", "merge"] as const) {
    card.buttons[action].disabled = !controls[action];
  }
  card.dial.disabled = !controls.call;
  card.target.disabled = !controls.call;
  card.dtmfDigits.disabled = !controls.dtmf;
  card.dtmfSend.disabled = !controls.dtmf;
  card.policy.disabled = !controls.answerPolicy;
  for (const input of [card.smsTo, card.smsText, card.smsStatusReport, card.smsSend]) input.disabled = !controls.sms;
  card.root.dataset["reachable"] = String(ue.reachable);
  renderServices(card, ue.reachable ? suggestedTarget(ue, all, subscribers) : "");
  if (!ue.reachable) {
    card.impu.textContent = "";
    card.registration.textContent = `Not reachable (${ue.error}). Start the UE simulators with: docker compose --profile phones up -d`;
    card.call.textContent = "";
    card.otherCall.textContent = "";
    card.conference.replaceChildren();
    card.session.replaceChildren();
    card.showLadder.hidden = true;
    card.policyText.textContent = "";
    card.smsSent.replaceChildren();
    card.smsReceived.replaceChildren();
    return;
  }
  const { status } = ue;
  card.impu.textContent = status.impu;
  card.registration.textContent = status.registered ? "registered" : "not registered";
  card.buttons.register.textContent = status.registered ? "Refresh registration" : "Register";
  card.call.textContent = describeCall(status.call);
  const other = status.calls.find((c) => c.call_id !== status.call?.call_id) ?? null;
  card.otherCall.textContent = other === null ? "none" : describeCall(other);
  card.conference.replaceChildren(
    ...(status.conference === null
      ? ["none"]
      : describeConference(status.conference).flatMap((line, i) => (i === 0 ? [line] : [el("br"), line]))),
  );
  const session = status.call?.session ?? null;
  card.session.replaceChildren(
    ...(session === null ? ["none"] : describeSession(session).flatMap((line, i) => (i === 0 ? [line] : [el("br"), line]))),
  );
  card.showLadder.hidden = !status.call?.call_id;
  card.buttons.hangup.title = status.call === null ? "" : `Hang up ${hangUpEffect(status.call)}`;
  card.policy.value = status.answer_policy.mode;
  card.policyText.textContent = describePolicy(status.answer_policy);
  const target = suggestedTarget(ue, all, subscribers);
  if (!card.targetEdited) card.target.value = target;
  if (!card.smsToEdited) card.smsTo.value = suggestedNumber(target);
  renderSMS(card, status.sms, selectCall);
}

// Newest first. Each entry links to the ladder of its MESSAGE; the
// submit, delivery and status reports are MESSAGEs of their own, with
// Call-IDs of their own (TS 24.341 5.3.1.2, 5.3.2.4).
function renderSMS(card: Card, sms: UESMSHistory, selectCall: (callId: string) => void): void {
  const ladder = (callId: string) => {
    const link = el("button", { type: "button", className: "link", textContent: "ladder" });
    link.addEventListener("click", () => selectCall(callId));
    return link;
  };
  const none = (text: string) => el("li", { className: "muted" }, text);
  const sent = sms.sent.toReversed().map((m) =>
    el(
      "li",
      {},
      `${clock(m.sent)} to ${m.to}: `,
      el("q", {}, m.text),
      " ",
      ladder(m.call_id),
      el("br"),
      el("span", { className: "muted" }, describeSentSMS(m)),
    ),
  );
  const received = sms.received.toReversed().map((m) =>
    el("li", {}, `${clock(m.received)} from ${m.from}: `, el("q", {}, m.text), " ", ladder(m.call_id)),
  );
  card.smsSent.replaceChildren(...(sent.length > 0 ? sent : [none("none yet")]));
  card.smsReceived.replaceChildren(...(received.length > 0 ? received : [none("none yet")]));
}
