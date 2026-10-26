import { expect, test } from "@playwright/test";

test("ストリーム通知を受信し、再接続して投稿画面を開ける", async ({ page }) => {
  const notification = {
    id: "n1",
    postingWindowId: "w1",
    region: "asia",
    createdAt: "2026-07-19T12:00:00Z",
  };
  let connections = 0;
  await page.route("**://*/api/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/api/auth/login") {
      return route.fulfill({
        json: { token: "session-token", user: { region: "asia" } },
      });
    }
    if (path === "/api/notifications") {
      expect(route.request().headers().authorization).toBe(
        "Bearer session-token",
      );
      connections++;
      return route.fulfill({
        contentType: "text/event-stream",
        body: connections === 1
          ? `data: ${JSON.stringify(notification)}\n\n`
          : "",
      });
    }
    return route.fulfill({ status: 204 });
  });
  await page.goto("/login");
  await page.getByLabel("ユーザー名").fill("alice");
  await page.getByLabel("SecretKey").fill("secret");
  await page.getByRole("button", { name: "ログイン" }).click();
  await expect(page.getByRole("status")).toContainText(
    "投稿時間が始まりました",
  );
  await expect.poll(() => connections).toBeGreaterThanOrEqual(2);
  await page.getByRole("link", { name: "通知", exact: true }).click();
  await expect(page.getByText(/asia の投稿時間/)).toBeVisible();
  await expect(page.getByText("未読")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Push/ })).toHaveCount(0);
  await page.getByRole("main").getByRole("link", {
    name: "投稿する",
    exact: true,
  }).click();
  await expect(page).toHaveURL("/posts/new");
});
