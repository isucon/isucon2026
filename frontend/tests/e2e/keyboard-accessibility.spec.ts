import { expect, type Locator, type Page, test } from "@playwright/test";

const me = {
  id: "10000000-0000-0000-0000-000000000001",
  username: "alice",
  displayName: "Alice",
  profileImageUrl: null,
  bio: "bio",
  region: "asia",
  accountType: "regular",
  status: "active",
};
const other = {
  ...me,
  id: "10000000-0000-0000-0000-000000000002",
  username: "bob",
  displayName: "Bob",
  accountType: "certified_public",
};
const post = {
  id: "20000000-0000-0000-0000-000000000001",
  author: other,
  postingWindowId: "30000000-0000-0000-0000-000000000001",
  imageUrl: "/image.jpg",
  caption: "today",
  visibility: "friends",
  location: { kind: "none" },
  postedAt: "2026-07-19T12:00:00Z",
  onTime: true,
  lateSeconds: 0,
  comments: [],
  realmojis: [],
};
const notification = {
  id: "40000000-0000-0000-0000-000000000001",
  postingWindowId: post.postingWindowId,
  region: "asia",
  createdAt: post.postedAt,
  read: false,
};

async function tabTo(page: Page, target: Locator): Promise<void> {
  for (let index = 0; index < 100; index++) {
    if (
      await target.evaluate((element) => element === document.activeElement)
        .catch(() => false)
    ) break;
    await page.keyboard.press("Tab");
  }
  await expect(target).toBeFocused();
  const outline = await target.evaluate((element) =>
    getComputedStyle(element).outlineWidth
  );
  expect(outline).not.toBe("0px");
}

async function seedSession(page: Page): Promise<void> {
  await page.goto("/");
  await page.evaluate(async () => {
    const request = indexedDB.open("isureal-session", 1);
    await new Promise<void>((resolve, reject) => {
      request.onupgradeneeded = () =>
        request.result.createObjectStore("credentials");
      request.onsuccess = () => {
        const transaction = request.result.transaction(
          "credentials",
          "readwrite",
        );
        transaction.objectStore("credentials").put(
          "keyboard-token",
          "user-token",
        );
        transaction.oncomplete = () => resolve();
        transaction.onerror = () => reject(transaction.error);
      };
      request.onerror = () => reject(request.error);
    });
  });
}

async function mockAPI(page: Page): Promise<void> {
  await page.route("**://*/api/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path === "/api/auth/register") {
      return route.fulfill({
        status: 201,
        json: {
          token: "keyboard-token",
          secretKey: "keyboard-secret",
          user: me,
        },
      });
    }
    if (path === "/api/auth/login") {
      return route.fulfill({ json: { token: "keyboard-token", user: me } });
    }
    if (path === "/api/me") return route.fulfill({ json: me });
    if (path === "/api/users") {
      return route.fulfill({ json: { items: [other], hasMore: false } });
    }
    if (path === `/api/users/${other.id}`) {
      return route.fulfill({ json: other });
    }
    if (path === "/api/posts" && request.method() === "POST") {
      return route.fulfill({ status: 201, json: post });
    }
    if (path === `/api/posts/${post.id}` || path === "/api/posts/me/current") {
      return route.fulfill({ json: post });
    }
    if (path === "/api/posting-window/current") {
      return route.fulfill({
        json: {
          window: { id: "w" },
          hasPosted: true,
          canPost: false,
          deletionCount: 0,
        },
      });
    }
    if (path.startsWith("/api/feeds/")) {
      return route.fulfill({ json: { items: [post], hasMore: false } });
    }
    if (path.endsWith("/comments") || path.endsWith("/realmojis")) {
      return route.fulfill({ json: { items: [], hasMore: false } });
    }
    if (path === "/api/memories") {
      return route.fulfill({ json: { items: [post], hasMore: false } });
    }
    if (path === "/api/notifications") {
      return route.fulfill({
        contentType: "text/event-stream",
        body: `data: ${JSON.stringify(notification)}\n\n`,
      });
    }
    if (path.endsWith(`/notifications/${notification.id}/open`)) {
      return route.fulfill({
        json: { destination: "create-post", path: "/posts/new" },
      });
    }
    return route.fulfill({ status: 204 });
  });
}

test("登録・ログイン・ログアウトをキーボードだけで完了しフォーカスを視認できる", async ({ page }) => {
  await mockAPI(page);
  await page.goto("/register");
  await tabTo(page, page.getByLabel("ユーザー名"));
  await page.keyboard.type("alice");
  await page.keyboard.press("Tab");
  await page.keyboard.type("Alice");
  await page.keyboard.press("Tab");
  await page.keyboard.press("End");
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "登録" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByLabel("保存した鍵")).toHaveText("keyboard-secret");
  await page.getByRole("button", { name: "はじめる" }).focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL("/");
  await tabTo(page, page.getByRole("link", { name: "プロフィールメニュー" }));
  await page.keyboard.press("Enter");
  await tabTo(page, page.getByRole("button", { name: "ログアウト" }));
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL("/login");
  await expect(page.getByLabel("ユーザー名")).toBeFocused();
  await page.keyboard.type("alice");
  await page.keyboard.press("Tab");
  await page.keyboard.press("Control+A");
  await page.keyboard.type("keyboard-secret");
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "ログイン" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL("/");
});

test("FR-038の投稿・フィード・関係・交流・プロフィール・Memories・通知をキーボード操作できる", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "mediaDevices", {
      value: { getUserMedia: async () => new MediaStream() },
    });
  });
  await mockAPI(page);
  await seedSession(page);
  await page.goto("/posts/new");
  await tabTo(page, page.getByRole("button", { name: "撮影する" }));
  await page.keyboard.press("Enter");
  await tabTo(page, page.getByRole("button", { name: "投稿する" }));
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL("/posts/complete");
  await tabTo(page, page.getByRole("link", { name: "友達の写真を見る" }));
  await page.keyboard.press("Enter");

  for (
    const feed of ["My friends", "Friends of friends", "公開", "フォロー中"]
  ) {
    await tabTo(page, page.getByRole("link", { name: feed, exact: true }));
    await page.keyboard.press("Enter");
    await expect(page.getByRole("heading", { name: `${feed}フィード` }))
      .toBeVisible();
  }

  await page.goto(`/users/${other.id}`);
  for (
    const name of [
      "友達申請",
      "フォロー",
      "友達解除",
      "フォロー解除",
      "ブロック",
      "ブロック解除",
    ]
  ) {
    await tabTo(page, page.getByRole("button", { name, exact: true }));
    await page.keyboard.press("Enter");
    if (
      ["友達解除", "フォロー解除", "ブロック", "ブロック解除"].includes(name)
    ) {
      await tabTo(
        page,
        page.getByRole("dialog").getByRole("button", { name, exact: true }),
      );
      await page.keyboard.press("Enter");
      await expect(page.getByRole("dialog")).not.toBeVisible();
    }
  }

  await page.goto(`/posts/${post.id}/comments`);
  await tabTo(page, page.getByLabel("コメント"));
  await page.keyboard.type("keyboard comment");
  await tabTo(page, page.getByRole("button", { name: "送信" }));
  await page.keyboard.press("Enter");
  await page.goto(`/posts/${post.id}/realmoji`);
  await tabTo(page, page.getByRole("button", { name: "カメラを開始" }));
  await page.keyboard.press("Enter");
  await tabTo(page, page.getByRole("button", { name: "撮影する" }));

  await page.goto("/settings/profile");
  await tabTo(page, page.getByLabel("表示名"));
  await page.keyboard.press("Control+A");
  await page.keyboard.type("Keyboard Alice");
  await tabTo(page, page.getByRole("button", { name: "保存" }));
  await page.keyboard.press("Enter");
  await page.goto("/settings/region");
  await tabTo(page, page.getByRole("button", { name: "地域を変更" }));

  await page.goto("/memories");
  await tabTo(page, page.getByLabel("月"));
  await tabTo(page, page.getByRole("link", { name: /過去投稿を見る/ }));
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "過去投稿・交流を表示" }))
    .toBeVisible();

  await page.goto("/notifications");
  await tabTo(
    page,
    page.getByRole("main").getByRole("link", { name: "投稿する", exact: true }),
  );
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL("/posts/new");
});
