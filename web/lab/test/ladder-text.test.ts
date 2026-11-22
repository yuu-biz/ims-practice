import { describe, expect, it } from "vitest";
import { ladderText } from "../src/ladder-text.ts";
import { buildLadder, type Arrow, type Column, type Ladder } from "../src/ladder.ts";
import { allMessages, callCallId } from "./fixtures.ts";

// A ladder written out by hand. ladderText reads the columns and the
// arrows, not the captured copies behind them.
const column = (host: string, node: string): Column => ({ host, node });
const arrow = (from: string, to: string, summary: string): Arrow => ({
  from,
  to,
  protocol: "sip",
  summary,
  messages: [],
});
const ladderOf = (columns: Column[], arrows: Arrow[]): Ladder => ({ columns, arrows });
// The text ends with a newline, so that it pastes into a text block as
// its own lines.
const text = (...lines: string[]) => `${lines.join("\n")}\n`;

describe("ladder text", () => {
  it("names the columns over their lifelines and draws each arrow between them", () => {
    const ladder = ladderOf(
      [column("1", "ue"), column("2", "pcscf"), column("3", "hss")],
      [
        arrow("1", "2", "REGISTER"),
        arrow("2", "3", "UAR"),
        arrow("3", "2", "UAA 2001"),
        arrow("2", "1", "200 OK"),
      ],
    );
    expect(ladderText(ladder)).toBe(
      text(
        "UE       P-CSCF       HSS",
        " |REGISTER-->          |",
        " |          |UAR------->",
        " |          <UAA 2001--|",
        " <200 OK----|          |",
      ),
    );
  });

  it("widens the spaces a long label spans, and draws the arrow over the columns between", () => {
    const ladder = ladderOf(
      [column("1", "ue"), column("2", "pcscf"), column("3", "scscf")],
      [arrow("1", "3", "INVITE tel:+1-202-555-0102"), arrow("2", "3", "UAR")],
    );
    expect(ladderText(ladder)).toBe(
      text(
        "UE          P-CSCF       S-CSCF",
        " |INVITE tel:+1-202-555-0102>",
        " |             |UAR--------->",
      ),
    );
  });

  it("turns an arrow back to its own column for a node that sends to itself", () => {
    const ladder = ladderOf(
      [column("1", "ue"), column("2", "scscf"), column("3", "hss")],
      [arrow("1", "2", "INVITE"), arrow("2", "2", "INVITE tel:+12025550102"), arrow("2", "3", "LIR")],
    );
    expect(ladderText(ladder)).toBe(
      text(
        "UE       S-CSCF                        HSS",
        " |INVITE---->                           |",
        " |          <-+ INVITE tel:+12025550102 |",
        " |          |LIR------------------------>",
      ),
    );
  });

  it("lets a self arrow of the last column run past the end of the row", () => {
    const ladder = ladderOf(
      [column("1", "ue"), column("2", "scscf")],
      [arrow("1", "2", "INVITE tel:+12025550102"), arrow("2", "2", "INVITE tel:+12025550102")],
    );
    expect(ladderText(ladder)).toBe(
      text(
        "UE                    S-CSCF",
        " |INVITE tel:+12025550102>",
        " |                       <-+ INVITE tel:+12025550102",
      ),
    );
  });

  it("numbers apart the columns that hold the same kind of node", () => {
    const ladder = buildLadder(allMessages.filter((m) => m.call_id === callCallId));
    const lines = ladderText(ladder).split("\n");
    expect(lines.slice(0, 6)).toEqual([
      "UE 1                         P-CSCF     I-CSCF                        S-CSCF       HSS       UE 2",
      "  |INVITE tel:+1-202-555-0102--->          |                             |          |          |",
      "  <100 Trying (INVITE)----------|          |                             |          |          |",
      "  |                             |INVITE tel:+1-202-555-0102-------------->          |          |",
      "  |                             <100 Trying (INVITE)---------------------|          |          |",
      "  |                             |          <INVITE tel:+1-202-555-0102---|          |          |",
    ]);
    expect(lines).toHaveLength(ladder.arrows.length + 2);
  });
});
