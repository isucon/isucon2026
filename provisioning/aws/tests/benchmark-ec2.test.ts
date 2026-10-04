import { expect, test } from "vitest";
import { benchmarkUserData } from "../lib/benchmark-ec2";
const image =
  `124502390568.dkr.ecr.ap-northeast-1.amazonaws.com/isucon2026-supervisor-portal-dev@sha256:${
    "a".repeat(64)
  }`;
test("boots a CLI runner with digest pinning and no portal secret", () => {
  const script = benchmarkUserData(image, "ap-northeast-1");
  expect(script).toContain("--entrypoint /usr/local/bin/isureal-bench");
  expect(script).toContain('"$@"');
  expect(script).toContain("get-login-password");
  expect(script).not.toContain("ISUXPORTAL_BENCH_TOKEN");
  expect(script).toContain("--network host");
});
test("rejects mutable images and shell injection", () => {
  expect(() => benchmarkUserData("repo:latest", "ap-northeast-1")).toThrow();
  expect(() => benchmarkUserData(image, "$(id)")).toThrow();
});
