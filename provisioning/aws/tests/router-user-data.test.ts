import { expect, test } from "vitest";
import { team } from "../lib/topology";
import { routerUserData } from "../lib/user-data";

test("keeps cloud-init from regenerating the repaired router network", () => {
  const data = routerUserData(team(6), "06:ff:f7:f4:e5:2b", "cert", "key");
  expect(data).toContain(
    "path: /etc/cloud/cloud.cfg.d/99-isucon-router-network.cfg",
  );
  expect(data).toContain(
    Buffer.from("network: {config: disabled}\n").toString("base64"),
  );
});
