import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { JSDOM } from "jsdom";
import { afterEach, describe, expect, it, vi } from "vitest";

const assets = resolve(process.cwd(), "../backend/internal/admin/assets");
const html = readFileSync(resolve(assets, "index.html"), "utf8");
const scripts = ["app.js", "guided.js"].map((name) => readFileSync(resolve(assets, name), "utf8")).join("\n");
const windows: JSDOM[] = [];

async function setup() {
  const dom = new JSDOM(html, { url: "http://10.10.8.127:8080/setup/", runScripts: "outside-only" });
  windows.push(dom);
  const { window } = dom;
  const fetch = vi.fn(async (path: string) => ({
    ok: true,
    json: async () => ({ data: path === "/setup/api/status" ? { state: "setup", authenticated: true, csrf_token: "fixture-csrf", public_url: "http://10.10.8.127:8080", lan_http_enabled: true } : { valid: true, checks: [] } }),
  }));
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(window, "fetch", { value: fetch });
  Object.defineProperty(window, "AbortSignal", { value: AbortSignal });
  Object.defineProperty(window.navigator, "clipboard", { configurable: true, value: { writeText } });
  window.eval(`(() => {\n${scripts}\n})();`);
  await vi.waitFor(() => expect(window.document.getElementById("setup-workspace")!.hidden).toBe(false));
  const input = window.document.getElementById("setup-ca-validity-days") as HTMLInputElement;
  const sign = window.document.getElementById("setup-ca-sign-command") as HTMLTextAreaElement;
  const copy = window.document.querySelector<HTMLButtonElement>('[data-ca-command-copy="setup-ca-sign-command"]')!;
  return { window, fetch, writeText, input, sign, copy };
}

afterEach(() => { for (const dom of windows.splice(0)) dom.window.close(); });

describe("first-run client certificate validity guide", () => {
  it("starts at 90 days with real shell continuation and unchanged init/chain commands", async () => {
    const { window, input, sign } = await setup();
    expect(input.value).toBe("90");
    expect(sign.value.slice(-7).split("").map((character) => character.charCodeAt(0))).toEqual([32, 92, 10, 32, 32, 57, 48]);
    const originalDocument = new window.DOMParser().parseFromString(html, "text/html");
    for (const id of ["setup-ca-init-command", "setup-ca-chain-command"]) {
      const original = originalDocument.getElementById(id) as HTMLTextAreaElement;
      expect((window.document.getElementById(id) as HTMLTextAreaElement).value).toBe(original.value);
    }
  });

  it.each([90, 180, 365])("updates and copies the %s-day preset", async (days) => {
    const { window, writeText, input, sign, copy } = await setup();
    const preset = window.document.querySelector<HTMLButtonElement>(`[data-ca-validity-preset="${days}"]`)!;
    preset.click();
    expect(input.value).toBe(String(days));
    expect(sign.value.endsWith("\n  " + days)).toBe(true);
    expect(preset.getAttribute("aria-pressed")).toBe("true");
    copy.click();
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(sign.value));
    await vi.waitFor(() => expect(window.document.querySelector(".ca-signing-guide .copy-status")!.textContent).toContain("คัดลอกคำสั่งแล้ว"));
  });

  it.each(["", "0", "366", "1.5", "1e2"])("clears invalid days and disables only signing copy: %s", async (value) => {
    const { window, input, sign, copy, writeText } = await setup();
    input.value = value;
    input.dispatchEvent(new window.Event("input", { bubbles: true }));
    const error = window.document.getElementById("setup-ca-validity-error")!;
    expect(error.hidden).toBe(false);
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(input.getAttribute("aria-describedby")).toContain(error.id);
    expect(sign.value).toBe("");
    expect(copy.disabled).toBe(true);
    copy.click();
    expect(writeText).not.toHaveBeenCalled();
    for (const id of ["setup-ca-init-command", "setup-ca-chain-command"]) {
      expect(window.document.querySelector<HTMLButtonElement>(`[data-ca-command-copy="${id}"]`)!.disabled).toBe(false);
    }
  });

  it("supports custom days and the copy fallback on HTTP LAN", async () => {
    const { window, writeText, input, sign, copy } = await setup();
    input.value = "42";
    input.dispatchEvent(new window.Event("input", { bubbles: true }));
    expect(sign.value.endsWith("\n  42")).toBe(true);
    writeText.mockRejectedValueOnce(new Error("clipboard unavailable"));
    copy.click();
    await vi.waitFor(() => expect(window.document.activeElement).toBe(sign));
    expect(sign.selectionStart).toBe(0);
    expect(sign.selectionEnd).toBe(sign.value.length);
    expect(window.document.querySelector(".ca-signing-guide .copy-status")!.textContent).toContain("⌘C");
  });

  it("keeps optional guide input out of Setup validation and activation state", async () => {
    const { window, input } = await setup();
    (window.document.getElementById("setup-password") as HTMLInputElement).value = "a-test-only-password-12345";
    (window.document.getElementById("setup-cert") as HTMLTextAreaElement).value = "fixture-certificate";
    window.document.querySelector<HTMLButtonElement>('[data-setup-step="2"]')!.click();
    input.value = "366";
    input.dispatchEvent(new window.Event("input", { bubbles: true }));
    window.document.getElementById("setup-next")!.click();
    expect((window.document.querySelector('[data-setup-panel="3"]') as HTMLElement).hidden).toBe(false);
    window.document.getElementById("setup-validate")!.click();
    const activate = window.document.getElementById("setup-activate") as HTMLButtonElement;
    await vi.waitFor(() => expect(activate.disabled).toBe(false));
    input.value = "";
    input.dispatchEvent(new window.Event("input", { bubbles: true }));
    expect(activate.disabled).toBe(false);
  });

  it("prevents Enter in the guide helper from implicitly activating Setup", async () => {
    const { window, input, fetch } = await setup();
    window.document.getElementById("setup-validate")!.click();
    const activate = window.document.getElementById("setup-activate") as HTMLButtonElement;
    await vi.waitFor(() => expect(activate.disabled).toBe(false));
    const submit = vi.fn();
    window.document.getElementById("setup-form")!.addEventListener("submit", submit);
    const enter = new window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
    input.dispatchEvent(enter);
    expect(enter.defaultPrevented).toBe(true);
    expect(submit).not.toHaveBeenCalled();
    expect(fetch.mock.calls.some(([path]) => path === "/setup/api/activate")).toBe(false);
  });
});
