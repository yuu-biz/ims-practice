// What the access network panel offers for each UE that is attached to
// the EPC stub, and how a bearer reads.

import type { EPSBearer } from "./api.ts";

// The QoS classes of the bearers of a VoLTE UE (TS 23.203 Table
// 6.1.7-A): the default bearer of the IMS APN carries the signalling
// (IR.92 4.4.1) and the dedicated bearer carries the voice (4.4.2).
const qciNames = new Map([
  [1, "voice"],
  [5, "IMS signalling"],
]);
const imsSignallingQCI = 5;

// The network deactivates a dedicated bearer on its own (TS 24.301
// 6.4.4.2). The default bearer goes with the PDN connection, so the stub
// refuses it and asks for a detach instead.
export function canRelease(bearer: EPSBearer): boolean {
  return bearer.eps_qos.qci !== imsSignallingQCI;
}

export function describeBearer(bearer: EPSBearer): string {
  const qci = bearer.eps_qos.qci;
  const name = qciNames.get(qci);
  return `EBI ${bearer.eps_bearer_identity}, QCI ${qci}${name === undefined ? "" : ` (${name})`}, ${bearer.state}`;
}
