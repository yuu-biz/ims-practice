// The access network panel: the UEs that are attached to the EPC stub,
// their bearers, and the procedures that the network starts on its own.
// A learner watches what the IMS does when the access takes the voice
// bearer away or detaches the UE.

import { detachUE, fetchAttachedUEs, HttpError, releaseBearer, type AttachedUE } from "./api.ts";
import { el, fillTable } from "./dom.ts";
import { canRelease, describeBearer } from "./epc.ts";

// How often the panel asks again, so that the voice bearer of a call
// shows up while the call is being set up.
const refreshMs = 5000;

export function mountEPCPanel(table: HTMLTableElement, note: HTMLParagraphElement): void {
  // The panel stops asking when the stub is not there, so that the Lab
  // does not log a failed request every few seconds.
  let available = true;
  let busy = false;
  const reload = el("button", { type: "button", className: "link", textContent: "reload" });

  const show = (text: string | null, isError = true) => {
    note.hidden = text === null;
    note.textContent = "";
    if (text !== null) note.append(text, " ", reload);
    note.classList.toggle("error", isError);
  };

  const run = async (what: string, request: () => Promise<void>) => {
    if (busy) return;
    busy = true;
    try {
      await request();
      busy = false;
      await load();
    } catch (error) {
      busy = false;
      await load();
      show(`Could not ${what}: ${error instanceof Error ? error.message : String(error)}`);
    }
  };

  const bearerList = (ue: AttachedUE): HTMLUListElement =>
    el(
      "ul",
      { className: "bearer-list" },
      ...ue.bearers.map((bearer) => {
        const release = el("button", { type: "button", className: "link danger", textContent: "Release" });
        release.hidden = !canRelease(bearer);
        release.addEventListener("click", () => {
          void run(`release bearer ${bearer.eps_bearer_identity} of ${ue.imsi}`, async () => {
            await releaseBearer(ue.imsi, bearer.eps_bearer_identity);
          });
        });
        return el("li", {}, describeBearer(bearer), " ", release);
      }),
    );

  const row = (ue: AttachedUE): (string | Node)[] => {
    const detach = el("button", { type: "button", className: "link danger", textContent: "Detach" });
    detach.addEventListener("click", () => {
      // The UE answers with DETACH ACCEPT and stays off the network: the
      // stub asks for no re-attach, so the UE simulator has to be
      // started again.
      if (!confirm(`Detach ${ue.imsi}? Its PDN connection ends, and the UE does not attach again by itself.`)) return;
      void run(`detach ${ue.imsi}`, async () => {
        await detachUE(ue.imsi);
      });
    });
    return [ue.imsi, ue.pdn_address, ue.access_point_name, bearerList(ue), ue.rx_sessions.join("\n"), detach];
  };

  const load = async () => {
    let ues: AttachedUE[];
    try {
      ues = await fetchAttachedUEs();
    } catch (error) {
      available = false;
      fillTable(table, [], "Not available.");
      if (error instanceof HttpError && error.status === 503) {
        show("No EPC stub is configured. Set lab.epc in the Lab config to follow the UEs of the access network.");
      } else {
        show(`Could not load the UEs of the EPC stub: ${error instanceof Error ? error.message : String(error)}`);
      }
      return;
    }
    available = true;
    show(ues.length > 0 ? null : "Start the VoLTE UEs with: docker compose --profile volte up -d volte-a volte-b", false);
    fillTable(table, ues.map(row), "No UE is attached.");
  };

  reload.addEventListener("click", () => void load());
  void load();
  setInterval(() => {
    if (available && !busy) void load();
  }, refreshMs);
}
