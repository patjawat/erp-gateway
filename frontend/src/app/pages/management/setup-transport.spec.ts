import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { JSDOM } from "jsdom";
import { afterEach, describe, expect, it, vi } from "vitest";

const assets = resolve(process.cwd(), "../backend/internal/admin/assets");
const html = readFileSync(resolve(assets, "index.html"), "utf8");
const scripts = ["app.js", "guided.js"]
  .map((name) => readFileSync(resolve(assets, name), "utf8"))
  .join("\n");
const windows: JSDOM[] = [];

function setup(status: Record<string, unknown> | Promise<Record<string, unknown>>) {
  const dom = new JSDOM(html, {
    url: "http://10.10.8.127:8080/setup/",
    runScripts: "outside-only",
  });
  windows.push(dom);
  const { window } = dom;
  const fetch = vi.fn(async (path: string) => ({
    ok: true,
    json: async () => ({
      data:
        path === "/setup/api/status"
          ? { state: "setup", authenticated: true, csrf_token: "test-csrf", ...(await status) }
          : { valid: true, checks: [] },
    }),
  }));
  Object.defineProperty(window, "fetch", { value: fetch });
  Object.defineProperty(window, "AbortSignal", { value: AbortSignal });
  window.eval(`(() => {\n${scripts}\n})();`);
  const input = window.document.getElementById("setup-public-url") as HTMLInputElement;
  return { window, fetch, input };
}

async function loaded(window: JSDOM["window"]) {
  await vi.waitFor(() =>
    expect(window.document.getElementById("setup-workspace")!.hidden).toBe(false),
  );
}

afterEach(() => {
  for (const dom of windows.splice(0)) dom.window.close();
});

describe("Setup gateway transport", () => {
  it("shows the enabled LAN mode and prefills the server URL", async () => {
    const { window, input } = setup({
      lan_http_enabled: true,
      public_url: "http://10.10.8.127:8080",
    });
    await loaded(window);
    expect(input.value).toBe("http://10.10.8.127:8080");
    expect(input.placeholder).toBe("http://10.10.8.127:8080");
    expect(window.document.getElementById("setup-transport-help")!.textContent).toContain("ไม่เข้ารหัส");
    expect(window.document.querySelector('label[for="setup-public-url"]')!.textContent).toBe("ที่อยู่ Gateway");
  });

  it.each([undefined, false])("preserves HTTPS when the capability is %s", async (lan_http_enabled) => {
    const { window, input, fetch } = setup({ lan_http_enabled });
    await loaded(window);
    expect(input.placeholder).toBe("https://gateway.example.org");
    input.value = "http://10.10.8.127:8080";
    window.document.getElementById("setup-validate")!.click();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(window.document.getElementById("setup-error")!.textContent).toContain("HTTPS");
  });

  it.each(["", "http://10.10.8.128:8080"])("preserves an edited URL while status is loading: %s", async (edited) => {
    let resolveStatus!: (value: Record<string, unknown>) => void;
    const status = new Promise<Record<string, unknown>>((resolve) => { resolveStatus = resolve; });
    const { window, input } = setup(status);
    input.value = edited;
    input.dispatchEvent(new window.Event("input", { bubbles: true }));
    resolveStatus({ lan_http_enabled: true, public_url: "http://10.10.8.127:8080" });
    await loaded(window);
    expect(input.value).toBe(edited);
  });

  it("sends a private HTTP URL for server validation when enabled", async () => {
    const { window, fetch } = setup({ lan_http_enabled: true, public_url: "http://10.10.8.127:8080" });
    await loaded(window);
    window.document.getElementById("setup-validate")!.click();
    await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    expect(fetch.mock.calls[1][0]).toBe("/setup/api/validate");
    await vi.waitFor(() =>
      expect((window.document.getElementById("setup-activate") as HTMLButtonElement).disabled).toBe(false),
    );
  });

  it.each(["http://8.8.8.8", "http://gateway.example.org", "http://0x0a0a087f", "http://10.10.8.127:8080/setup/"])("does not submit an invalid LAN URL: %s", async (url) => {
    const { window, input, fetch } = setup({ lan_http_enabled: true });
    await loaded(window);
    input.value = url;
    window.document.getElementById("setup-validate")!.click();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(window.document.getElementById("setup-error")!.textContent).toContain("IP ภายใน");
  });
});
