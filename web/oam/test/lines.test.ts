import { describe, expect, it } from "vitest";
import { lineSplitter } from "../src/lines.ts";

describe("lineSplitter", () => {
  it("holds a line until its end arrives", () => {
    const split = lineSplitter();
    expect(split.push('{"a"')).toEqual([]);
    expect(split.push(':1}\n{"b":2}\n{"c"')).toEqual(['{"a":1}', '{"b":2}']);
    expect(split.push(":3}")).toEqual([]);
    expect(split.end()).toEqual(['{"c":3}']);
  });

  it("gives blank lines, and nothing at the end of text that ends a line", () => {
    const split = lineSplitter();
    expect(split.push("a\n\nb\n")).toEqual(["a", "", "b"]);
    expect(split.end()).toEqual([]);
  });
});
