import { expect, test } from "@playwright/test";

test.skip(
  process.env.ISUREAL_COMPOSE_E2E !== "1",
  "composeで起動した実環境専用",
);

test("compose実環境でカメラ撮影とGPS位置を含む写真を投稿して配信できる", async ({ page, request }) => {
  const suffix = `${Date.now()}${Math.floor(Math.random() * 1000)}`;
  const username = `compose${suffix}`;

  const registration = await request.post("/api/auth/register", {
    data: { username, displayName: `Compose ${suffix}`, region: "asia" },
  });
  expect(registration.status()).toBe(201);
  const { secretKey } = await registration.json() as { secretKey: string };
  const login = await request.post("/api/auth/login", {
    data: { username, secretKey },
  });
  expect(login.status()).toBe(200);
  const { token } = await login.json() as { token: string };
  const authorization = { Authorization: `Bearer ${token}` };

  let currentWindow = await request.get("/api/posting-window/current", {
    headers: authorization,
  });
  const currentWindowBody = await currentWindow.json() as { window: unknown };
  if (currentWindowBody.window === null) {
    const adminLogin = await request.post("/api/admin/auth/login", {
      data: { username: "admin", password: "password1234" },
    });
    expect(adminLogin.status()).toBe(200);
    const { token: adminToken } = await adminLogin.json() as { token: string };
    const schedule = await request.post("/api/admin/posting-window", {
      headers: { Authorization: `Bearer ${adminToken}` },
      data: {
        region: "asia",
        localDate: new Date().toISOString().slice(0, 10),
      },
    });
    expect(schedule.status()).toBe(201);
    await expect.poll(async () => {
      currentWindow = await request.get("/api/posting-window/current", {
        headers: authorization,
      });
      return currentWindow.status();
    }, { timeout: 10_000 }).toBe(200);
  } else {
    expect(currentWindow.status()).toBe(200);
  }

  await page.goto("/");
  await page.evaluate(async (sessionToken) => {
    const opened = indexedDB.open("isureal-session", 1);
    await new Promise<void>((resolve, reject) => {
      opened.onupgradeneeded = () =>
        opened.result.createObjectStore("credentials");
      opened.onsuccess = () => {
        const transaction = opened.result.transaction(
          "credentials",
          "readwrite",
        );
        transaction.objectStore("credentials").put(sessionToken, "user-token");
        transaction.oncomplete = () => resolve();
        transaction.onerror = () => reject(transaction.error);
      };
      opened.onerror = () => reject(opened.error);
    });
  }, token);

  await page.goto("/posts/new");
  const camera = page.getByLabel("投稿用カメラ");
  await expect(camera).toBeVisible();
  await camera.evaluate(async (element) => {
    const video = element as HTMLVideoElement;
    if (video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA) return;
    await new Promise<void>((resolve) =>
      video.addEventListener("loadeddata", () => resolve(), { once: true })
    );
  });

  await page.getByRole("button", { name: "撮影する" }).click();
  await expect(page.getByAltText("投稿画像のプレビュー")).toBeVisible();
  const caption = `compose camera upload ${suffix}`;
  await page.getByLabel("キャプション").fill(caption);
  await page.getByRole("radio", { name: "公開", exact: true }).check();
  await page.getByRole("button", { name: /位置情報:/ }).click();
  await page.getByRole("radio", { name: "GPSで正確な位置を共有" }).check();
  await expect(page.getByText("GPS位置を取得しました")).toBeVisible();
  await expect(page.locator("input[type=number]")).toHaveCount(0);

  await page.getByRole("button", { name: "設定を適用" }).click();
  const postResponsePromise = page.waitForResponse((response) =>
    response.url().endsWith("/api/posts") &&
    response.request().method() === "POST"
  );
  await page.getByRole("button", { name: "投稿する" }).click();
  const postResponse = await postResponsePromise;
  const post = await postResponse.json() as {
    imageUrl: string;
    location: { kind: string; latitude: number; longitude: number };
  };
  expect(postResponse.status(), JSON.stringify(post)).toBe(201);
  expect(post.location.kind).toBe("exact");
  expect(post.location.latitude).toBeCloseTo(35.681236, 6);
  expect(post.location.longitude).toBeCloseTo(139.767125, 6);

  await expect(page).toHaveURL("/posts/complete");
  await page.getByRole("link", { name: "自分の投稿を確認" }).click();
  await expect(page).toHaveURL(/\/posts\/me\/current$/);
  await expect(page.getByText(caption)).toBeVisible();
  const imageResponse = await request.get(post.imageUrl, {
    headers: authorization,
  });
  expect(imageResponse.status()).toBe(200);
  expect(imageResponse.headers()["content-type"]).toBe("image/jpeg");
  expect((await imageResponse.body()).byteLength).toBeGreaterThan(0);
  const displayedImage = page.getByAltText("自分の投稿画像");
  await expect(displayedImage).toBeVisible();
  await expect.poll(() =>
    displayedImage.evaluate((image: HTMLImageElement) => image.naturalWidth)
  ).toBeGreaterThan(0);
});
