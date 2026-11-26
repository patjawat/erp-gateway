import { readFileSync } from "node:fs";
import { Blob as NodeBlob } from "node:buffer";
import { resolve } from "node:path";
import { JSDOM, VirtualConsole } from "jsdom";
import { afterEach, describe, expect, it, vi } from "vitest";

const assets = resolve(process.cwd(), "../backend/internal/admin/assets");
const html = readFileSync(resolve(assets, "index.html"), "utf8");
const scripts = ["app.js", "guided.js"]
  .map((name) => readFileSync(resolve(assets, name), "utf8"))
  .join("\n");
const csr = "-----BEGIN CERTIFICATE REQUEST-----\nVGVzdCBwdWJsaWMgQ1NS\n-----END CERTIFICATE REQUEST-----\n";
const chain = "-----BEGIN CERTIFICATE-----\nVGVzdCBjbGllbnQ=\n-----END CERTIFICATE-----\n-----BEGIN CERTIFICATE-----\nVGVzdCBDQQ==\n-----END CERTIFICATE-----\n";
const windows: JSDOM[] = [];

async function setup(validationError?: string) {
  const console = new VirtualConsole();
  const errors: Error[] = [];
  console.on("jsdomError", (error) => {
    if (!error.message.includes("Not implemented: navigation")) errors.push(error);
  });
  const dom = new JSDOM(html, {
    url: "http://10.10.8.127:8080/setup/",
    runScripts: "outside-only",
    virtualConsole: console,
  });
  windows.push(dom);
  const { window } = dom;
  let readinessFailure = validationError;
  const fetch = vi.fn(async (path: string, _options?: RequestInit) => {
    const error = path === "/setup/api/validate" ? readinessFailure : undefined;
    return {
      ok: !error,
      status: error ? 400 : 200,
      json: async () => error ? { error: { code: "INVALID_SETUP", message: error } } : {
        data: path === "/setup/api/status"
          ? { state: "setup", authenticated: true, csrf_token: "fixture-csrf", public_url: "http://10.10.8.127:8080", lan_http_enabled: true }
          : path === "/setup/api/csr" ? { csr_pem: csr, fingerprint: "a".repeat(64) }
          : path === "/setup/api/activate" ? { active: true, redirect: "/admin/" }
          : { valid: true, checks: [{ name: "mtls", status: "ready", message: "ตรวจ key, chain, clientAuth และวันหมดอายุผ่าน" }] },
      },
    };
  });
  const createObjectURL = vi.fn((_blob: NodeBlob) => "blob:fixture-public-csr");
  Object.defineProperty(window, "fetch", { value: fetch });
  Object.defineProperty(window, "AbortSignal", { value: AbortSignal });
  Object.defineProperty(window, "Blob", { value: NodeBlob });
  Object.defineProperty(window.URL, "createObjectURL", { value: createObjectURL });
  Object.defineProperty(window.URL, "revokeObjectURL", { value: vi.fn() });
  const downloads: { filename: string; url: string }[] = [];
  vi.spyOn(window.HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
    downloads.push({ filename: this.download, url: this.href });
  });
  window.eval(`(() => {\n${scripts}\n})();`);
  await vi.waitFor(() => expect(window.document.getElementById("setup-workspace")!.hidden).toBe(false));
  const byID = <T extends HTMLElement>(id: string) => window.document.getElementById(id) as T;
  byID<HTMLInputElement>("setup-password").value = "a-test-only-password-12345";
  byID<HTMLButtonElement>("setup-next").click();
  byID<HTMLButtonElement>("setup-next").click();
  expect((window.document.querySelector('[data-setup-panel="2"]') as HTMLElement).hidden).toBe(false);
  return { window, byID, fetch, createObjectURL, downloads, errors, allowReadiness: () => { readinessFailure = undefined; } };
}

async function importChain(fixture: Awaited<ReturnType<typeof setup>>) {
  const { window, byID } = fixture;
  const fileInput = window.document.querySelector<HTMLInputElement>('[data-pem-target="setup-cert"]')!;
  const file = new window.File([chain], "gateway-client-chain.pem", { type: "application/x-pem-file" });
  Object.defineProperty(file, "text", { value: async () => chain });
  Object.defineProperty(fileInput, "files", { configurable: true, value: [file] });
  fileInput.dispatchEvent(new window.Event("change", { bubbles: true }));
  await vi.waitFor(() => expect(byID<HTMLTextAreaElement>("setup-cert").value).toBe(chain));
}

afterEach(() => {
  for (const dom of windows.splice(0)) dom.window.close();
  vi.restoreAllMocks();
});

describe("Setup certificate import using a Gateway-generated key", () => {
  it("downloads only the displayed CSR and validates/activates an uploaded chain with a blank private key", async () => {
    const fixture = await setup();
    const { byID, window, fetch, createObjectURL, downloads, errors } = fixture;
    byID<HTMLButtonElement>("setup-csr-create").click();
    await vi.waitFor(() => expect(byID<HTMLTextAreaElement>("setup-csr-pem").value).toBe(csr));
    expect(byID<HTMLTextAreaElement>("setup-key").value).toBe("");
    byID<HTMLButtonElement>("setup-csr-download").click();
    expect(downloads).toEqual([{ filename: "gateway-client.csr.pem", url: "blob:fixture-public-csr" }]);
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(await createObjectURL.mock.calls[0][0].text()).toBe(csr);
    expect(await createObjectURL.mock.calls[0][0].text()).not.toContain("PRIVATE KEY");

    await importChain(fixture);
    byID<HTMLButtonElement>("setup-next").click();
    expect((window.document.querySelector('[data-setup-panel="3"]') as HTMLElement).hidden).toBe(false);
    byID<HTMLButtonElement>("setup-validate").click();
    await vi.waitFor(() => expect(byID<HTMLButtonElement>("setup-activate").disabled).toBe(false));
    const validation = fetch.mock.calls.find(([path]) => path === "/setup/api/validate")!;
    const payload = JSON.parse(validation[1]!.body as string);
    expect(payload.client_certificate_pem).toBe(chain);
    expect(payload.client_private_key_pem).toBe("");
    expect((validation[1]!.headers as Record<string, string>)["X-CSRF-Token"]).toBe("fixture-csrf");

    byID<HTMLButtonElement>("setup-activate").click();
    await vi.waitFor(() => expect(fetch.mock.calls.some(([path]) => path === "/setup/api/activate")).toBe(true));
    const activation = fetch.mock.calls.find(([path]) => path === "/setup/api/activate")!;
    expect(JSON.parse(activation[1]!.body as string)).toEqual(payload);
    await vi.waitFor(() => expect(byID<HTMLTextAreaElement>("setup-cert").value).toBe(""));
    expect(errors).toEqual([]);
  });

  it("shows a readiness failure, blocks activation, and permits a corrected import to be checked again", async () => {
    const message = "ใบรับรองไม่ตรงกับ private key ของ Gateway กรุณาใช้ CSR จาก Gateway เครื่องนี้";
    const fixture = await setup(message);
    const { window, byID, fetch, allowReadiness, errors } = fixture;
    await importChain(fixture);
    byID<HTMLButtonElement>("setup-next").click();
    byID<HTMLButtonElement>("setup-validate").click();
    await vi.waitFor(() => expect(byID("setup-error").textContent).toBe(message));
    expect(byID("setup-error").hidden).toBe(false);
    expect(byID<HTMLButtonElement>("setup-activate").disabled).toBe(true);
    byID("setup-form").dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true }));
    expect(fetch.mock.calls.some(([path]) => path === "/setup/api/activate")).toBe(false);

    allowReadiness();
    await importChain(fixture);
    byID<HTMLButtonElement>("setup-validate").click();
    await vi.waitFor(() => expect(byID<HTMLButtonElement>("setup-activate").disabled).toBe(false));
    expect(byID("setup-error").hidden).toBe(true);
    expect(byID<HTMLTextAreaElement>("setup-key").value).toBe("");
    expect(errors).toEqual([]);
  });
});
