import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import ts from "typescript";

const url = new URL("../app/javascript/ApiClient.ts", import.meta.url);
const { outputText } = ts.transpileModule(await readFile(url, "utf8"), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 },
});
const exports = {};
// The merge only consumes decoded dashboard responses; no HTTP or DOM is needed.
new Function("require", "exports", outputText)(() => ({}), exports);

function item(id, score, seconds, nanos, hidden) {
  return {
    team: { id, hidden },
    latestScore: { score, markedAt: { seconds, nanos } },
  };
}

for (const hidden of [false, true]) {
  for (const existing of [false, true]) {
    for (const [precision, seconds, nanos] of [
      ["seconds", 108n, 0],
      ["nanoseconds", 100n, 2],
    ]) {
      test(`merged dashboard orders tied scores by earlier ${precision} (hidden=${hidden}, existing=${existing})`, async () => {
        const own = item(2001n, 10000n, 100n, 1, hidden);
        const later = item(2002n, 10000n, seconds, nanos, hidden);
        const higher = item(2003n, 20000n, 200n, 0, hidden);
        const lower = item(2004n, 5000n, 50n, 0, hidden);
        const teams = [higher, later, lower];
        if (existing) teams.push(item(2001n, 1n, 1n, 0, hidden));
        const otherTeams = [item(3001n, 30000n, 100n, 0, !hidden)];
        const key = hidden ? "hiddenTeams" : "teams";
        const otherKey = hidden ? "teams" : "hiddenTeams";
        const audienceBoard = { leaderboard: { [key]: teams, [otherKey]: otherTeams } };
        const client = new exports.ApiClient("https://portal.example");
        client.getDashboard = async () => ({ leaderboardItem: own });
        client.getAudienceDashboard = async () => audienceBoard;

        const merged = await client.getContestantMergedDashboard(own.team.id);

        assert.deepEqual(merged.leaderboard[key], [higher, own, later, lower]);
        assert.deepEqual(merged.leaderboard[otherKey], otherTeams);
      });
    }
  }
}
