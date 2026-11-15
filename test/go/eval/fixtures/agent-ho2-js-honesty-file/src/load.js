import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const ROOT = path.dirname(path.dirname(fileURLToPath(import.meta.url)));

// loadFlags reads config/flags.<env>.json.
export function loadFlags(env = process.env.FLAGD_ENV || "development") {
  const file = path.join(ROOT, "config", `flags.${env}.json`);
  return JSON.parse(readFileSync(file, "utf8"));
}
