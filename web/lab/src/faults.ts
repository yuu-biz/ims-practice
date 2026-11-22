// What the fault panel offers for each kind of fault, and how a fault
// reads. The rules follow what the fault agent takes: a field that
// belongs to another kind is refused with 400, a crash and a recreate
// take no duration because they happen once, and only an active fault
// can be cleared (the agent answers 409 while it applies or clears one,
// and reads an ended one back).

import type { Fault, FaultKind, FaultRequest } from "./api.ts";

// The fields of a request that this kind takes, besides the target.
export interface FaultFields {
  // The Compose network whose interface of the target the fault sits on.
  network: boolean;
  // The delay and the jitter of netem.
  delay: boolean;
  lossPercent: boolean;
  // What the target can no longer reach nor hear.
  peers: boolean;
  duration: boolean;
}

export function faultFields(kind: FaultKind): FaultFields {
  const happensOnce = kind === "crash" || kind === "recreate";
  return {
    network: kind === "delay" || kind === "loss" || kind === "partition",
    delay: kind === "delay",
    lossPercent: kind === "loss",
    peers: kind === "partition",
    duration: !happensOnce,
  };
}

// What the form holds, each field as the input has it. The agent reads
// the durations and the percentage, so they go to it as they were
// typed.
export interface FaultForm {
  target: string;
  kind: FaultKind;
  network: string;
  delay: string;
  jitter: string;
  lossPercent: string;
  // One peer per line, as in the textarea.
  peers: string;
  duration: string;
}

// faultRequest builds the body of the injection: the target, the kind,
// and of the rest only the fields that the kind takes and that are
// filled in. An empty duration leaves the agent its default of five
// minutes.
export function faultRequest(form: FaultForm): FaultRequest {
  const fields = faultFields(form.kind);
  const request: FaultRequest = { target: form.target.trim(), kind: form.kind };
  const peers = form.peers
    .split("\n")
    .map((peer) => peer.trim())
    .filter((peer) => peer !== "");
  if (fields.network && form.network.trim() !== "") request.network = form.network.trim();
  if (fields.delay && form.delay.trim() !== "") request.delay = form.delay.trim();
  if (fields.delay && form.jitter.trim() !== "") request.jitter = form.jitter.trim();
  if (fields.lossPercent && form.lossPercent.trim() !== "") request.loss_percent = Number(form.lossPercent);
  if (fields.peers && peers.length > 0) request.peers = peers;
  if (fields.duration && form.duration.trim() !== "") request.duration = form.duration.trim();
  return request;
}

// describeFault is the fault in one line: what it does to the target,
// where it sits and how long it lasts.
export function describeFault(fault: Fault): string {
  const parts: string[] = [fault.kind];
  switch (fault.kind) {
    case "delay":
      parts.push(fault.jitter === "" ? fault.delay : `${fault.delay}, jitter ${fault.jitter}`);
      break;
    case "loss":
      parts.push(`${fault.loss_percent ?? 0}% of the packets`);
      break;
    case "partition":
      parts.push(fault.peers.length === 0 ? "from everything" : `from ${fault.peers.join(", ")}`);
      break;
  }
  if (fault.network !== "") parts.push(`on the network ${fault.network}`);
  if (fault.duration !== "") parts.push(`for ${fault.duration}`);
  return parts.join(" ");
}

export function canClear(fault: Fault): boolean {
  return fault.state === "active";
}
