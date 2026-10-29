import type { Timestamp } from "@bufbuild/protobuf/wkt";
import type { DashboardResponse } from "../../../proto/isuxportal/services/admin/dashboard_pb";

export function waitingMilliseconds(at: Timestamp | undefined, now: number): number {
  return at ? Math.max(0, now - (Number(at.seconds) * 1000 + at.nanos / 1_000_000)) : 0;
}

export function attentionLevel(data: DashboardResponse, now: number): "normal" | "warning" | "danger" {
  if (
    (data.unansweredClarificationCount > 0n &&
      waitingMilliseconds(data.earliestUnansweredClarificationAt, now) >= 300_000) ||
    (data.pendingBenchmarkJobCount > 0n && waitingMilliseconds(data.earliestPendingBenchmarkJobAt, now) >= 60_000)
  )
    return "danger";
  return data.unansweredClarificationCount > 0n ? "warning" : "normal";
}
