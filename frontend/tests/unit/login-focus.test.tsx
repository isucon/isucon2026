// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { expect, test, vi } from "vitest";
import { LoginPage } from "../../src/features/auth/LoginPage";

test("ログアウト後のログイン画面はマウント時にユーザー名へフォーカスする", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  try {
    await act(async () => {
      root.render(
        <MemoryRouter
          initialEntries={[{
            pathname: "/login",
            state: { focusUsername: true },
          }]}
        >
          <LoginPage />
        </MemoryRouter>,
      );
    });
    expect(document.activeElement).toBe(container.querySelector("input"));
  } finally {
    await act(async () => root.unmount());
    container.remove();
    vi.unstubAllGlobals();
  }
});
