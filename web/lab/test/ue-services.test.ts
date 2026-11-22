import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchUEServices, HttpError, putUEOIRDefault, putUEService } from "../src/api.ts";
import { describeService } from "../src/ue-panel.ts";

afterEach(() => {
  vi.unstubAllGlobals();
});

// What GET /xcap of "ue serve" answers: Go leaves out an empty target.
const settings = {
  etag: '"3"',
  services: [
    { name: "cfu", provisioned: true, active: true, target: "tel:+12025550102" },
    { name: "cfb", provisioned: true, active: false },
    { name: "cfnl", provisioned: false, active: false },
    { name: "baoc", provisioned: true, active: true },
  ],
  oir_default_behaviour: "presentation-not-restricted",
  document: "<simservs/>",
};

describe("supplementary services of a UE", () => {
  it("reads the settings through the Lab", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify(settings)));
    vi.stubGlobal("fetch", fetch);
    const got = await fetchUEServices("phone1");
    expect(fetch).toHaveBeenCalledWith("/api/ues/phone1/xcap", undefined);
    expect(got.etag).toBe('"3"');
    expect(got.services[1]).toEqual({ name: "cfb", provisioned: true, active: false, target: "" });
    expect(got.oir_default_behaviour).toBe("presentation-not-restricted");
  });

  it("sends one service with its target, and none when switching off", async () => {
    const fetch = vi.fn(async (_path: string, _init?: RequestInit) => new Response(JSON.stringify(settings)));
    vi.stubGlobal("fetch", fetch);
    await putUEService("phone1", "cfu", true, "tel:+12025550102");
    await putUEService("phone1", "cfu", false, "");
    await putUEOIRDefault("phone1", "presentation-restricted");
    expect(fetch.mock.calls.map(([path, init]) => [path, init?.method, init?.body])).toEqual([
      ["/api/ues/phone1/xcap/services/cfu", "PUT", JSON.stringify({ active: true, target: "tel:+12025550102" })],
      ["/api/ues/phone1/xcap/services/cfu", "PUT", JSON.stringify({ active: false })],
      ["/api/ues/phone1/xcap/oir", "PUT", JSON.stringify({ default_behaviour: "presentation-restricted" })],
    ]);
  });

  it("reports why the UE or the XCAP server refused", async () => {
    vi.stubGlobal("fetch", async () => new Response(JSON.stringify({ error: "invalid setting: cfu needs a target" }), { status: 400 }));
    await expect(putUEService("phone1", "cfu", true, "")).rejects.toEqual(new HttpError(400, "invalid setting: cfu needs a target"));
  });

  it("rejects a service the UE does not have", async () => {
    vi.stubGlobal("fetch", async () => new Response(JSON.stringify({ ...settings, services: [{ name: "cfx", provisioned: true, active: true }] })));
    await expect(fetchUEServices("phone1")).rejects.toThrow(/name/);
  });

  it("describes each service in one line", () => {
    expect(describeService({ name: "cfu", provisioned: true, active: true, target: "tel:+12025550102" })).toBe(
      "CFU (unconditional): on to tel:+12025550102",
    );
    expect(describeService({ name: "cfb", provisioned: true, active: false, target: "" })).toBe("CFB (busy): off");
    expect(describeService({ name: "cfnl", provisioned: false, active: false, target: "" })).toBe("CFNL (not logged in): not provisioned");
  });
});
