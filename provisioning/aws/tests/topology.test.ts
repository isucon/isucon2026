import { describe, expect, test } from "vitest";
import { gzipSync } from "node:zlib";
import {
  publicIpIsolationRules,
  requireIpv4HostCidr,
  routerMediatedIngressCidrs,
  serverNetworks,
} from "../lib/topology";
import { routerUserData, serviceUserData } from "../lib/user-data";

describe("AWS topology", () => {
  test("accepts only canonical IPv4 host CIDRs for public ingress", () => {
    expect(requireIpv4HostCidr("clientCidr", "203.0.113.47/32")).toBe(
      "203.0.113.47/32",
    );
    for (const value of [
      "0.0.0.0/0",
      "203.0.113.0/24",
      "203.0.113.047/32",
      "256.180.142.47/32",
      "::/0",
    ]) {
      expect(() => requireIpv4HostCidr("clientCidr", value)).toThrow(
        "clientCidr must be one canonical IPv4 address with a /32 prefix",
      );
    }
  });

  test("uses distinct VPCs, subnets, and fixed addresses", () => {
    expect(new Set(serverNetworks.map((item) => item.vpcCidr)).size).toBe(4);
    expect(new Set(serverNetworks.map((item) => item.serviceIp)).size).toBe(4);
    expect(new Set(serverNetworks.map((item) => item.routerIp)).size).toBe(4);
    for (const network of serverNetworks) {
      expect(network.serviceSubnetCidr).not.toBe(network.routerSubnetCidr);
      expect(network.serviceIp).not.toBe(network.routerIp);
    }
  });

  test("service user data shares db1 and keeps initialization role-specific", () => {
    for (const current of serverNetworks) {
      const rendered = Buffer.from(
        serviceUserData(
          "r2026.08.06.3",
          current,
          "example.invalid",
          "database-password-123",
          "cursor-key-012345678901234567890123456789",
        ),
      ).toString();
      expect(rendered).not.toContain("ip route replace");
      const files = [...rendered.matchAll(/    content: ([A-Za-z0-9+/=]+)/g)]
        .map((match) => Buffer.from(match[1], "base64").toString());
      expect(files.some((content) => content.includes(
        "@192.168.63.6:5432/isureal?sslmode=disable",
      ))).toBe(true);
      const initialize = files.find((content) => content.includes("pg_createcluster"))!;
      expect(initialize).toContain(`if [ "${current.id}" = db1 ]; then`);
      const [database, application] = initialize.split("\nelse\n");
      expect(database).toContain("/opt/isucon/bin/seed --if-empty");
      expect(application).not.toContain("/opt/isucon/bin/seed");
      expect(database).not.toContain("enable --now isureal-api");
      expect(application).toContain("disable --now postgresql.service");
      for (const app of serverNetworks.filter((item) => item.id !== "db1")) {
        expect(database).toContain(`host isureal isureal ${app.serviceIp}/32 scram-sha-256`);
      }
      expect(rendered).toContain(
        Buffer.from("ISUREAL_SCHEMA_PATH=/opt/isucon/share/schema.sql").toString(
          "base64",
        ),
      );
    }
  });

  test("allows every protocol only from Router-mediated internal sources", () => {
    for (const destination of serverNetworks) {
      const ingressCidrs = routerMediatedIngressCidrs(destination);
      expect(ingressCidrs).toHaveLength(4);
      expect(ingressCidrs).toContain(`${destination.routerIp}/32`);
      expect(ingressCidrs).not.toContain(destination.vpcCidr);
      for (const source of serverNetworks) {
        if (source.id !== destination.id) {
          expect(ingressCidrs).toContain(source.vpcCidr);
        }
      }
    }
  });

  test("denies peer Global IPs before allowing other public traffic", () => {
    expect(publicIpIsolationRules(["198.51.100.10", "198.51.100.20"])).toEqual([
      {
        protocol: "-1",
        ruleNo: 100,
        action: "deny",
        cidrBlock: "198.51.100.10/32",
        fromPort: 0,
        toPort: 0,
      },
      {
        protocol: "-1",
        ruleNo: 110,
        action: "deny",
        cidrBlock: "198.51.100.20/32",
        fromPort: 0,
        toPort: 0,
      },
      {
        protocol: "-1",
        ruleNo: 1000,
        action: "allow",
        cidrBlock: "0.0.0.0/0",
        fromPort: 0,
        toPort: 0,
      },
    ]);
  });

  test("router user data preserves source IP network identities", () => {
    const rendered = routerUserData(
      "r2026.08.06.3",
      "https://portal.example.invalid",
      "example.invalid",
      "192.168.60.69",
      "02:00:00:00:00:01",
      ["02:00:00:00:00:11", "02:00:00:00:00:12", "02:00:00:00:00:13", "02:00:00:00:00:14"],
      "certificate",
      "private-key",
    );
    for (const network of serverNetworks) {
      expect(rendered).toContain(
        Buffer.from(network.vpcCidr).toString("base64").slice(0, 8),
      );
    }
    expect(rendered).not.toContain("MASQUERADE");
    expect(rendered).not.toContain("SNAT");
    expect(gzipSync(Buffer.from(rendered)).byteLength).toBeLessThan(16 * 1024);
  });
});
