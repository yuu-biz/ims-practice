// The OAM screen. It operates every node of the network through
// the OAM API of internal/oam, which asks the nodes when it is asked:
// the screen keeps nothing that the OAM does not give it.

import "./style.css";
import { fetchInventory, type Endpoint } from "./api.ts";
import { byId, describe, showNote } from "./dom.ts";
import { mountAlarmsPanel } from "./alarms-panel.ts";
import { mountBackupsPanel } from "./backups-panel.ts";
import { mountConfigPanel } from "./config-panel.ts";
import { mountNodesPanel } from "./nodes-panel.ts";
import { mountSubscribersPanel } from "./subscribers-panel.ts";

const operatorInput = byId<HTMLInputElement>("operator");
// Who asks, for the OAM's log and history. The OAM records it as given:
// it checks no identity, as the OAM API has no authentication.
const operator = () => operatorInput.value.trim();

async function start(): Promise<void> {
  let inventory: Endpoint[];
  try {
    inventory = await fetchInventory();
  } catch (error) {
    showNote(byId("inventory-note"), `Could not load the inventory of the OAM: ${describe(error)}`);
    return;
  }
  const nodes = mountNodesPanel(
    {
      roles: byId("roles"),
      nodes: byId("nodes"),
      note: byId("nodes-note"),
      read: byId("nodes-read"),
      refresh: byId("nodes-refresh"),
      auto: byId("nodes-auto"),
      operations: byId("operations"),
    },
    inventory,
    operator,
  );
  const alarms = mountAlarmsPanel(byId("alarms-panel"), operator);
  mountConfigPanel(byId("config"), inventory, operator);
  mountSubscribersPanel(byId("subscribers"), operator);
  mountBackupsPanel(byId("backups"), operator);
  await Promise.all([nodes.refresh(), alarms.refresh()]);
}

void start();
