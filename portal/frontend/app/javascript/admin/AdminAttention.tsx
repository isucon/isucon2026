import React from "react";
import { Link } from "react-router-dom";
import type { DashboardResponse } from "../../../proto/isuxportal/services/admin/dashboard_pb";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import type { AdminApiClient } from "./AdminApiClient";
import { AdminAttentionSound } from "./AdminAttentionSound";
import { attentionLevel, waitingMilliseconds } from "./attentionLevel";

export const AdminAttention: React.FC<{ client: AdminApiClient }> = ({ client }) => {
  const [data, setData] = React.useState<DashboardResponse | null>(null);
  const [lastUpdated, setLastUpdated] = React.useState<number | null>(null);
  const [failed, setFailed] = React.useState(false);
  const [now, setNow] = React.useState(Date.now());

  React.useEffect(() => {
    let disposed = false;
    let requesting = false;
    const refresh = async () => {
      if (requesting) return;
      requesting = true;
      try {
        const response = await client.getDashboard();
        if (!disposed) {
          setData(response);
          setLastUpdated(Date.now());
          setFailed(false);
        }
      } catch {
        if (!disposed) setFailed(true);
      } finally {
        requesting = false;
      }
    };
    void refresh();
    const poll = window.setInterval(refresh, 10_000);
    const clock = window.setInterval(() => setNow(Date.now()), 1000);
    window.addEventListener("focus", refresh);
    return () => {
      disposed = true;
      window.clearInterval(poll);
      window.clearInterval(clock);
      window.removeEventListener("focus", refresh);
    };
  }, [client]);

  const level = data ? attentionLevel(data, now) : "normal";
  React.useEffect(() => {
    if (level === "normal") return;
    const className = `isux-admin-attention-${level}`;
    document.body.classList.add(className);
    return () => document.body.classList.remove(className);
  }, [level]);

  const waiting = (at: Timestamp | undefined) => {
    if (!at) return "—";
    const seconds = Math.floor(waitingMilliseconds(at, now) / 1000);
    return `${Math.floor(seconds / 60)}分${seconds % 60}秒`;
  };
  const stale = failed || (lastUpdated !== null && now - lastUpdated >= 30_000);

  return (
    <aside className="isux-admin-attention-summary" aria-label="運営の未対応状況">
      {data ? (
        <>
          <Link to="/admin/clarifications?unanswered_only=1">
            未回答Clar {data.unansweredClarificationCount.toString()}件／最長
            {waiting(data.earliestUnansweredClarificationAt)}
          </Link>
          <Link to="/admin/benchmark_jobs?status=0">
            pending {data.pendingBenchmarkJobCount.toString()}件／最長{waiting(data.earliestPendingBenchmarkJobAt)}
          </Link>
        </>
      ) : (
        <span>未対応状況を取得中…</span>
      )}
      <AdminAttentionSound level={level} />
      {stale && (
        <span role="status">
          更新停止・最終取得: {lastUpdated === null ? "未取得" : new Date(lastUpdated).toLocaleTimeString("ja-JP")}
        </span>
      )}
    </aside>
  );
};
