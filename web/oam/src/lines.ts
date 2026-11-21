// Splits text that arrives in chunks into lines, holding only the line
// that is not complete yet. Both the answers that stream (one JSON
// object per line) and the bulk provisioning files are read this way, so
// that neither is ever held whole.

export interface LineSplitter {
  // The lines that chunk completes, without their "\n".
  push(chunk: string): string[];
  // The last line, when the text does not end with "\n".
  end(): string[];
}

export function lineSplitter(): LineSplitter {
  let pending = "";
  return {
    push(chunk) {
      const parts = (pending + chunk).split("\n");
      pending = parts.pop() ?? "";
      return parts;
    },
    end() {
      const last = pending;
      pending = "";
      return last === "" ? [] : [last];
    },
  };
}
