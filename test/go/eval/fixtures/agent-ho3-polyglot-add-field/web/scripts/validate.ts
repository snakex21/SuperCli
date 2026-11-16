// Reads JSON lines on stdin, validates each with parseOrderEvent and prints
// the parsed event as canonical JSON (keys sorted). Used by the contract check.
import { parseOrderEvent } from "../src/orderEvent.ts";

let buf = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (c) => (buf += c));
process.stdin.on("end", () => {
  for (const line of buf.split("\n")) {
    if (!line.trim()) continue;
    const e = parseOrderEvent(JSON.parse(line)) as unknown as Record<string, unknown>;
    const sorted = Object.fromEntries(Object.keys(e).sort().map((k) => [k, e[k]]));
    process.stdout.write(JSON.stringify(sorted) + "\n");
  }
});
