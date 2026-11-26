import { afterEach, describe, expect, it, vi } from "vitest";
import { detachUE, fetchAttachedUEs, HttpError, releaseBearer } from "../src/api.ts";
import { canRelease, describeBearer } from "../src/epc.ts";

afterEach(() => {
  vi.unstubAllGlobals();
});

// What GET /ues of the EPC stub answers for a VoLTE UE in a call: the
// default bearer of the IMS APN and the dedicated bearer of the voice.
const volteA = {
  imsi: "001010000000010",
  pdn_address: "172.28.0.120",
  access_point_name: "ims",
  bearers: [
    { eps_bearer_identity: 5, state: "BEARER CONTEXT ACTIVE", eps_qos: { qci: 5 } },
    { eps_bearer_identity: 6, state: "BEARER CONTEXT ACTIVE", eps_qos: { qci: 1, guaranteed_bit_rate_for_uplink: 41 } },
  ],
  rx_sessions: ["pcscf.ims.mnc001.mcc001.3gppnetwork.org;1;1"],
};

describe("the UEs of the EPC stub", () => {
  it("reads a UE with its bearers", async () => {
    vi.stubGlobal("fetch", async () => new Response(JSON.stringify([volteA])));
    const [ue] = await fetchAttachedUEs();
    expect(ue?.imsi).toBe("001010000000010");
    expect(ue?.bearers).toEqual([
      { eps_bearer_identity: 5, state: "BEARER CONTEXT ACTIVE", eps_qos: { qci: 5 } },
      { eps_bearer_identity: 6, state: "BEARER CONTEXT ACTIVE", eps_qos: { qci: 1 } },
    ]);
    expect(ue?.rx_sessions).toEqual(["pcscf.ims.mnc001.mcc001.3gppnetwork.org;1;1"]);
  });

  it("rejects a bearer without its QoS class", async () => {
    const bearers = [{ eps_bearer_identity: 6, state: "BEARER CONTEXT ACTIVE", eps_qos: {} }];
    vi.stubGlobal("fetch", async () => new Response(JSON.stringify([{ ...volteA, bearers }])));
    await expect(fetchAttachedUEs()).rejects.toThrow(/qci/);
  });

  it("asks the stub for the procedures that the network initiates", async () => {
    const fetch = vi.fn(async (_path: string, _init?: RequestInit) => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetch);
    await releaseBearer("001010000000010", 6);
    await detachUE("001010000000010");
    // A POST goes as JSON, which a page of another site cannot have the
    // browser send without a preflight that the Lab does not answer.
    expect(fetch.mock.calls.map(([path, init]) => [path, init?.method, init?.headers])).toEqual([
      ["/api/epc/network-initiated/ues/001010000000010/bearers/6", "DELETE", undefined],
      ["/api/epc/network-initiated/ues/001010000000010/detach", "POST", { "Content-Type": "application/json" }],
    ]);
  });

  it("reports why the stub refused", async () => {
    vi.stubGlobal("fetch", async () => new Response(JSON.stringify({ error: "no such EPS bearer: 6" }), { status: 404 }));
    await expect(releaseBearer("001010000000010", 6)).rejects.toEqual(new HttpError(404, "no such EPS bearer: 6"));
  });
});

describe("the bearers of a UE", () => {
  it("offers to release the dedicated bearer, not the default one", () => {
    const [defaultBearer, voice] = volteA.bearers;
    // The default bearer goes with the PDN connection: the stub refuses
    // to deactivate it and asks for a detach instead.
    expect(canRelease(defaultBearer!)).toBe(false);
    expect(canRelease(voice!)).toBe(true);
  });

  it("names the bearer by its identity, its QoS class and its state", () => {
    expect(describeBearer(volteA.bearers[0]!)).toBe("EBI 5, QCI 5 (IMS signalling), BEARER CONTEXT ACTIVE");
    expect(describeBearer(volteA.bearers[1]!)).toBe("EBI 6, QCI 1 (voice), BEARER CONTEXT ACTIVE");
    expect(describeBearer({ eps_bearer_identity: 7, state: "BEARER CONTEXT ACTIVE PENDING", eps_qos: { qci: 9 } })).toBe(
      "EBI 7, QCI 9, BEARER CONTEXT ACTIVE PENDING",
    );
  });
});
