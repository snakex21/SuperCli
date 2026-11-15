import { bucket } from "./hash.js";

// isEnabled reports whether flag `name` is on for userId.
export function isEnabled(flags, name, userId) {
  const flag = flags[name];
  if (!flag) {
    throw new Error(`unknown flag: ${name}`);
  }
  if (!flag.enabled) {
    return false;
  }
  return bucket(userId) < flag.rollout;
}
