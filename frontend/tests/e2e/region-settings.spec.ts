import { expect, test } from "@playwright/test";

test("リージョン不一致でもプロフィールと地域を更新できる", async ({ page }) => {
  let me = {
    id: "u1",
    username: "alice",
    displayName: "Alice",
    bio: "",
    region: "europe",
    accountType: "regular",
    status: "active",
    profileImageUrl: null,
  };
  await page.route("**://*/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/auth/login") {
      return route.fulfill({ json: { token: "token", user: me } });
    }
    if (path === "/api/me" && request.method() === "PATCH") {
      me = { ...me, ...request.postDataJSON() };
      return route.fulfill({ json: me });
    }
    if (me.region !== "asia") {
      return route.fulfill({ status: 403, json: { error: "region_mismatch" } });
    }
    if (path === "/api/me") return route.fulfill({ json: me });
    return route.fulfill({ contentType: "text/event-stream", body: "" });
  });
  await page.goto("/login");
  await page.getByLabel("ユーザー名").fill("alice");
  await page.getByLabel("SecretKey").fill("secret");
  await page.getByRole("button", { name: "ログイン" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "登録した地域と接続先が異なります",
  );
  await page.goto("/settings/profile");
  await expect(page.getByLabel("表示名")).toHaveValue("Alice");
  await page.getByLabel("表示名").fill("Updated Alice");
  await page.getByRole("button", { name: "保存", exact: true }).click();
  await expect.poll(() => me.displayName).toBe("Updated Alice");
  await page.goto("/settings/region");
  await page.getByRole("combobox", { name: "地域", exact: true }).selectOption(
    "asia",
  );
  await page.getByRole("button", { name: "地域を変更" }).click();
  await expect(page.getByText("現在の地域: アジア")).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
});
