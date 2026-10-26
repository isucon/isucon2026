import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { exportPosts } from "../../api/account";
import { ProfileSettingsPage } from "./ProfileSettingsPage";

vi.mock("../../api/account", () => ({
  getMe: vi.fn(async () => ({
    id: "me",
    displayName: "Me",
    bio: "",
    region: "asia",
    profileImageUrl: null,
  })),
  cachedMe: vi.fn(),
  exportPosts: vi.fn(),
  deleteProfileImage: vi.fn(),
  putProfileImage: vi.fn(),
  requestAccountDeletion: vi.fn(),
  updateMe: vi.fn(),
}));
vi.mock(
  "../../app/auth",
  () => ({ clearToken: vi.fn(), getSecretKey: vi.fn(async () => null) }),
);

let container: HTMLDivElement;
let root: Root;
beforeEach(async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  await act(async () => {
    root.render(
      <MemoryRouter>
        <ProfileSettingsPage view="export" />
      </MemoryRouter>,
    );
  });
});
afterEach(async () => {
  await act(async () => root.unmount());
  container.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  vi.useRealTimers();
});
const exportButton = () =>
  Array.from(container.querySelectorAll("button")).find((button) =>
    button.textContent?.includes("全投稿")
  )!;

it("disables the button while preparing the ZIP and triggers a download", async () => {
  vi.useFakeTimers();
  const blob = new Blob(["ZIP"], { type: "application/zip" });
  let resolve!: (blob: Blob) => void;
  vi.mocked(exportPosts).mockReturnValue(
    new Promise<Blob>((done) => {
      resolve = done;
    }),
  );
  const createObjectURL = vi.fn(() => "blob:export");
  const revokeObjectURL = vi.fn();
  vi.stubGlobal("URL", { createObjectURL, revokeObjectURL });
  let filename = "";
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(
    function (this: HTMLAnchorElement) {
      filename = this.download;
    },
  );
  const button = exportButton();
  await act(async () => button.click());
  expect(button.disabled).toBe(true);
  expect(container.textContent).toContain("エクスポート中");
  await act(async () => resolve(blob));
  expect(button.disabled).toBe(false);
  expect(filename).toBe("isureal-posts.zip");
  expect(createObjectURL).toHaveBeenCalledWith(blob);
  expect(container.textContent).toContain("ダウンロードを開始しました");
  vi.runAllTimers();
  expect(revokeObjectURL).toHaveBeenCalledWith("blob:export");
});

it("shows failures and permits retry", async () => {
  vi.mocked(exportPosts).mockRejectedValue(new Error("network failed"));
  const button = exportButton();
  await act(async () => button.click());
  expect(container.querySelector('[role="alert"]')?.textContent).toContain(
    "エクスポートできませんでした",
  );
  expect(button.disabled).toBe(false);
});
