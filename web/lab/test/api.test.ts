import { afterEach, describe, expect, it, vi } from "vitest";
import {
  type Call,
  fetchCallMessages,
  fetchCalls,
  fetchRegistrations,
  fetchSubscribers,
  HttpError,
  ladderCallIds,
  parseMessage,
  patchSubscriber,
  putAlertingTone,
} from "../src/api.ts";

function serve(status: number, body: string): void {
  vi.stubGlobal("fetch", async () => new Response(body, { status }));
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("API responses", () => {
  it("reads the alerting tone of a subscriber, and none when the HSS omits it", async () => {
    serve(
      200,
      JSON.stringify([
        {
          private_identity: "a@example.test",
          public_identities: ["sip:a@example.test"],
          effective_service_profile: "default",
          customized_alerting_tone: { tone: "cat2", active: true },
        },
        { private_identity: "b@example.test", public_identities: ["sip:b@example.test"], effective_service_profile: "default" },
      ]),
    );
    const [a, b] = await fetchSubscribers();
    expect(a?.customized_alerting_tone).toEqual({ tone: "cat2", active: true });
    expect(b?.customized_alerting_tone).toBeNull();
  });

  it("reads the mark, the service profile and the barred identities of a subscriber", async () => {
    serve(
      200,
      JSON.stringify([
        {
          private_identity: "a@example.test",
          public_identities: ["sip:a@example.test", "tel:+12025550101"],
          barred_identities: ["sip:a@example.test"],
          test_user: true,
          service_profile: "trial",
          effective_service_profile: "trial",
        },
        { private_identity: "b@example.test", public_identities: ["sip:b@example.test"], effective_service_profile: "default" },
      ]),
    );
    const [a, b] = await fetchSubscribers();
    expect(a).toMatchObject({ barred_identities: ["sip:a@example.test"], test_user: true, service_profile: "trial", effective_service_profile: "trial" });
    // The HSS leaves out a mark that is not set, a profile that the
    // subscription does not name and an empty list of barred
    // identities, and names the profile the settings give it instead.
    expect(b).toMatchObject({ barred_identities: [], test_user: false, service_profile: "", effective_service_profile: "default" });
  });

  it("patches the service profile of a subscriber", async () => {
    const fetch = vi.fn(async () => new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    await patchSubscriber("a@example.test", { service_profile: "no-services" });
    expect(fetch).toHaveBeenCalledWith("/api/subscribers/a%40example.test", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: '{"service_profile":"no-services"}',
    });
  });

  it("puts the alerting tone of a subscriber to the HSS", async () => {
    const fetch = vi.fn(async () => new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    await putAlertingTone("a@example.test", { tone: "cat1", active: false });
    expect(fetch).toHaveBeenCalledWith("/api/subscribers/a%40example.test/customized-alerting-tone", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: '{"tone":"cat1","active":false}',
    });
  });

  it("reads a null list, as Go encodes an empty slice, as no entries", async () => {
    serve(200, "null");
    expect(await fetchCalls()).toEqual([]);
  });

  it("keeps a registration whose optional fields are null", async () => {
    serve(200, JSON.stringify([{ impu: "sip:a@example.test", impi: null, contact: null, expires: null, state: "unregistered", scscf: null, call_id: "" }]));
    expect(await fetchRegistrations()).toEqual([
      { impu: "sip:a@example.test", impi: "", contact: "", expires: "", state: "unregistered", scscf: "", call_id: "" },
    ]);
  });

  it("reports the status of a refused request", async () => {
    serve(503, "provisioning not configured");
    await expect(fetchCalls()).rejects.toEqual(new HttpError(503, "provisioning not configured"));
    await expect(fetchCalls()).rejects.toHaveProperty("status", 503);
  });

  it("accepts a message without node, call_id or refs", () => {
    const m = parseMessage({
      id: 1,
      time: "2026-11-19T09:00:00.000000001Z",
      protocol: "diameter",
      transport: "tcp",
      src: "172.28.0.30:40200",
      dst: "172.28.0.10:3868",
      direction: "send",
      summary: "DWR",
      correlation_id: "",
      payload: "{}",
      refs: null,
    });
    expect([m.node, m.call_id, m.refs]).toEqual(["", "", []]);
  });

  it("reads the decoded short message of a MESSAGE, and null for the rest", () => {
    const m = { id: 1, time: "2026-11-20T03:10:29Z", protocol: "sip", transport: "udp", src: "", dst: "", direction: "send", summary: "", payload: "" };
    expect(parseMessage(m).sms).toBeNull();
    const sms = {
      hex: "02 00 41 02 00 00",
      fields: [
        { name: "RP-MTI", value: "2, RP-ACK (MS to network)" },
        { name: "TP-MTI", value: "SMS-DELIVER-REPORT" },
      ],
    };
    expect(parseMessage({ ...m, sms }).sms).toEqual({ ...sms, text: "", error: "" });
    expect(() => parseMessage({ ...m, sms: { ...sms, fields: [{ name: "RP-MR", value: 0 }] } })).toThrow(/value/);
  });

  it("rejects a registration without call_id", async () => {
    serve(200, JSON.stringify([{ impu: "sip:a@example.test", state: "registered" }]));
    await expect(fetchRegistrations()).rejects.toThrow(/call_id/);
  });

  it("rejects a capture time that is not in UTC", () => {
    const m = { id: 1, protocol: "sip", transport: "udp", src: "", dst: "", direction: "send", summary: "", payload: "" };
    expect(() => parseMessage({ ...m, time: "2026-11-19T18:00:00.001+09:00" })).toThrow(/time/);
    expect(() => parseMessage({ ...m, time: "2026-11-19T09:00:00.0000000001Z" })).toThrow(/time/);
    expect(parseMessage({ ...m, time: "2026-11-19T09:00:00.123456789Z" }).time).toBe("2026-11-19T09:00:00.123456789Z");
  });

  it("rejects a message with an unknown direction", () => {
    expect(() =>
      parseMessage({ id: 1, time: "2026-11-19T09:00:00Z", protocol: "sip", transport: "udp", src: "", dst: "", direction: "both", summary: "", payload: "" }),
    ).toThrow(/direction/);
  });
});

describe("calls through an application server", () => {
  const call = (call_id: string, legs: string[]): Call => ({
    call_id,
    legs,
    from: "sip:a@example.test",
    to: "tel:+12025550102",
    state: "confirmed",
    status: 200,
    started: "2026-11-20T09:00:00Z",
    ended: null,
    messages: 3,
  });

  it("reads the dialogs of a call, and takes a call without legs as its own dialog", async () => {
    const base = { from: "f", to: "t", state: "confirmed", status: 200, started: "2026-11-20T09:00:00Z", ended: null, messages: 1 };
    serve(200, JSON.stringify([{ ...base, call_id: "a", legs: ["a", "b", "c"] }, { ...base, call_id: "x" }]));
    const calls = await fetchCalls();
    expect(calls.map((c) => c.legs)).toEqual([["a", "b", "c"], ["x"]]);
  });

  it("draws the whole call for any of its dialogs", () => {
    const calls = [call("a", ["a", "b", "c"]), call("x", ["x"])];
    expect(ladderCallIds(calls, "c")).toEqual(["a", "b", "c"]);
    expect(ladderCallIds(calls, "x")).toEqual(["x"]);
    // The Call-ID of a REGISTER is no call.
    expect(ladderCallIds(calls, "register-1")).toEqual(["register-1"]);
  });

  it("asks for the messages of every dialog", async () => {
    const asked: string[] = [];
    vi.stubGlobal("fetch", async (url: string) => {
      asked.push(url);
      return new Response("[]", { status: 200 });
    });
    await fetchCallMessages(["a@x", "b;1"]);
    expect(asked).toEqual(["/api/messages?call_id=a%40x&call_id=b%3B1"]);
  });
});
