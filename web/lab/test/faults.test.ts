import { afterEach, describe, expect, it, vi } from "vitest";
import { clearFault, type Fault, fetchFaults, HttpError, injectFault, parseFault } from "../src/api.ts";
import { canClear, describeFault, faultFields, faultRequest, type FaultForm } from "../src/faults.ts";

afterEach(() => {
  vi.unstubAllGlobals();
});

// A fault as the agent answers it: Go leaves out the fields that the
// kind does not use.
const pause = {
  id: "113e54e0",
  target: "pcscf",
  kind: "pause",
  duration: "5s",
  state: "active",
  created_at: "2026-11-20T23:57:48.449Z",
  expires_at: "2026-11-20T23:57:53.449Z",
};

const form: FaultForm = {
  target: "pcscf",
  kind: "pause",
  network: "",
  delay: "",
  jitter: "",
  lossPercent: "",
  peers: "",
  duration: "",
};

const fault = (fields: Partial<Fault>): Fault => ({ ...parseFault(pause), ...fields });

describe("the faults of the agent", () => {
  it("reads a fault with only the fields of its kind", async () => {
    vi.stubGlobal("fetch", async () => new Response(JSON.stringify([pause])));
    const [got] = await fetchFaults();
    expect(got).toEqual({
      id: "113e54e0",
      target: "pcscf",
      kind: "pause",
      state: "active",
      network: "",
      delay: "",
      jitter: "",
      loss_percent: null,
      peers: [],
      duration: "5s",
      error: "",
      created_at: "2026-11-20T23:57:48.449Z",
      expires_at: "2026-11-20T23:57:53.449Z",
      ended_at: "",
    });
  });

  it("rejects a kind and a state that the agent does not have", () => {
    expect(() => parseFault({ ...pause, kind: "unplug" })).toThrow(/kind/);
    expect(() => parseFault({ ...pause, state: "running" })).toThrow(/state/);
  });

  it("sends the injection and the clearing to the agent", async () => {
    const fetch = vi.fn(async (_path: string, _init?: RequestInit) => new Response(JSON.stringify(pause)));
    vi.stubGlobal("fetch", fetch);
    await injectFault({ target: "agw", kind: "loss", loss_percent: 10, duration: "2m" });
    await clearFault("113e54e0");
    expect(fetch.mock.calls.map(([path, init]) => [path, init?.method, init?.body])).toEqual([
      ["/api/fault/faults", "POST", '{"target":"agw","kind":"loss","loss_percent":10,"duration":"2m"}'],
      ["/api/fault/faults/113e54e0", "DELETE", undefined],
    ]);
  });

  it("reports why the agent refused a fault", async () => {
    vi.stubGlobal(
      "fetch",
      async () => new Response(JSON.stringify({ error: "conflict: fault 113e54e0 (pause) on pcscf is not over" }), { status: 409 }),
    );
    await expect(injectFault({ target: "pcscf", kind: "pause" })).rejects.toEqual(
      new HttpError(409, "conflict: fault 113e54e0 (pause) on pcscf is not over"),
    );
  });
});

describe("the fields of a fault", () => {
  it("offers a duration to the faults that last, and none to those that happen once", () => {
    expect(faultFields("pause").duration).toBe(true);
    expect(faultFields("crash").duration).toBe(false);
    expect(faultFields("recreate").duration).toBe(false);
  });

  it("offers each field to the kinds that take it", () => {
    expect(faultFields("delay")).toEqual({ network: true, delay: true, lossPercent: false, peers: false, duration: true });
    expect(faultFields("loss")).toEqual({ network: true, delay: false, lossPercent: true, peers: false, duration: true });
    expect(faultFields("partition")).toEqual({ network: true, delay: false, lossPercent: false, peers: true, duration: true });
    expect(faultFields("stop")).toEqual({ network: false, delay: false, lossPercent: false, peers: false, duration: true });
  });

  it("sends only the fields of the kind, so that the agent takes the request", () => {
    expect(faultRequest({ ...form, duration: " 20s " })).toEqual({ target: "pcscf", kind: "pause", duration: "20s" });
    // A value left over from another kind in the form is not sent.
    expect(faultRequest({ ...form, kind: "crash", duration: "20s", lossPercent: "10" })).toEqual({ target: "pcscf", kind: "crash" });
    expect(faultRequest({ ...form, kind: "delay", delay: "200ms", jitter: "20ms", network: "nni" })).toEqual({
      target: "pcscf",
      kind: "delay",
      network: "nni",
      delay: "200ms",
      jitter: "20ms",
    });
    expect(faultRequest({ ...form, kind: "loss", lossPercent: "10" })).toEqual({ target: "pcscf", kind: "loss", loss_percent: 10 });
  });

  it("takes one peer per line and drops the empty ones", () => {
    expect(faultRequest({ ...form, kind: "partition", peers: " icscf \n\n scscf-* \n" })).toEqual({
      target: "pcscf",
      kind: "partition",
      peers: ["icscf", "scscf-*"],
    });
    // No peers at all cuts the target off from everything, which the
    // agent takes as a request without the field.
    expect(faultRequest({ ...form, kind: "partition", peers: "\n" })).toEqual({ target: "pcscf", kind: "partition" });
  });
});

describe("how a fault reads", () => {
  it("says what the fault does and how long it lasts", () => {
    expect(describeFault(fault({}))).toBe("pause for 5s");
    expect(describeFault(fault({ kind: "crash", duration: "" }))).toBe("crash");
    expect(describeFault(fault({ kind: "delay", delay: "200ms", jitter: "20ms", duration: "5m0s" }))).toBe(
      "delay 200ms, jitter 20ms for 5m0s",
    );
    expect(describeFault(fault({ kind: "delay", delay: "200ms", duration: "5m0s" }))).toBe("delay 200ms for 5m0s");
    expect(describeFault(fault({ kind: "loss", loss_percent: 10, duration: "2m0s" }))).toBe("loss 10% of the packets for 2m0s");
    expect(describeFault(fault({ kind: "partition", peers: ["icscf", "scscf"], duration: "1m0s" }))).toBe(
      "partition from icscf, scscf for 1m0s",
    );
    expect(describeFault(fault({ kind: "partition", network: "nni", duration: "1m0s" }))).toBe(
      "partition from everything on the network nni for 1m0s",
    );
  });

  it("offers to clear only a fault that is in place", () => {
    expect(canClear(fault({}))).toBe(true);
    // The agent answers 409 while it applies or clears one, and reads an
    // ended one back without doing anything.
    for (const state of ["applying", "clearing", "done", "cleared", "expired", "failed"] as const) {
      expect(canClear(fault({ state }))).toBe(false);
    }
  });
});
