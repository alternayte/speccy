import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { profileKeys } from "./profile-help";

// The help panel explains every key a profile may hold. A key that ships with nothing said
// about it is a key a maintainer meets in a schema error and nowhere else.
describe("the profile help", () => {
  it("explains every key the profile schema allows", () => {
    const schema = JSON.parse(
      readFileSync(new URL("../../../../schemas/profile.schema.json", import.meta.url), "utf8"),
    );
    const allowed = Object.keys(schema.properties).filter((k) => k !== "version");
    expect([...profileKeys].sort()).toEqual([...allowed].sort());
  });
});
