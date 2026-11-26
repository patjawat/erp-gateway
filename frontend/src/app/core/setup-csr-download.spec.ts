import { Blob as NodeBlob } from "node:buffer";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { JSDOM } from "jsdom";
import { afterEach, describe, expect, it, vi } from "vitest";

const assets = resolve(process.cwd(), "../backend/internal/admin/assets");
const html = readFileSync(resolve(assets, "index.html"), "utf8");
const scripts = ["app.js", "guided.js"]
  .map((name) => readFileSync(resolve(assets, name), "utf8"))
  .join("\n");

// A public PEM envelope fixture only; no certificate request or key is created.
const publicCSR =
  "-----BEGIN CERTIFICATE REQUEST-----\nVEVTVA==\n-----END CERTIFICATE REQUEST-----\n";
const openWindows: JSDOM[] = [];

async function setup(csr = publicCSR) {
  const dom = new JSDOM(html, {
    url: "http://localhost/setup/",
    runScripts: "outside-only",
  });
  openWindows.push(dom);
  const window = dom.window;
  const fetch = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({
      data: { state: "setup", authenticated: false, existing_data: false },
    }),
  });
  const createURL = vi.fn((_blob: NodeBlob) => "blob:setup-csr-fixture");
  const revokeURL = vi.fn();
  const writeText = vi.fn().mockResolvedValue(undefined);
  const downloads: { filename: string; href: string; connected: boolean }[] = [];
  Object.defineProperty(window, "fetch", { value: fetch });
  Object.defineProperty(window, "Blob", { value: NodeBlob });
  Object.defineProperty(window, "AbortSignal", { value: AbortSignal });
  Object.defineProperty(window.URL, "createObjectURL", { value: createURL });
  Object.defineProperty(window.URL, "revokeObjectURL", { value: revokeURL });
  Object.defineProperty(window.navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  window.HTMLAnchorElement.prototype.click = function () {
    downloads.push({
      filename: this.download,
      href: this.href,
      connected: this.isConnected,
    });
  };
  window.eval(`(() => {\n${scripts}\n})();`);
  await vi.waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
  const textarea = window.document.getElementById(
    "setup-csr-pem",
  ) as HTMLTextAreaElement;
  textarea.value = csr;
  window.document.getElementById("setup-csr-result")!.hidden = false;
  return { dom, window, fetch, createURL, revokeURL, writeText, downloads, textarea };
}

afterEach(() => {
  for (const dom of openWindows.splice(0)) dom.window.close();
});

describe("first-run setup public CSR actions", () => {
  it("provides copy and download buttons next to the existing readonly CSR", async () => {
    const { window, textarea } = await setup();
    const result = window.document.getElementById("setup-csr-result")!;
    expect(textarea.readOnly).toBe(true);
    for (const id of ["setup-csr-copy", "setup-csr-download"]) {
      const button = window.document.getElementById(id) as HTMLButtonElement;
      expect(button).not.toBeNull();
      expect(button.type).toBe("button");
      expect(result.contains(button)).toBe(true);
    }
  });

  it("downloads the displayed public CSR bytes without another HTTP request", async () => {
    const { window, createURL, revokeURL, downloads, fetch } = await setup();
    window.document.getElementById("setup-csr-download")!.click();
    expect(createURL).toHaveBeenCalledOnce();
    const blob = createURL.mock.calls[0][0] as NodeBlob;
    expect(blob.type).toBe("application/pkcs10");
    expect(await blob.text()).toBe(publicCSR);
    expect(downloads).toEqual([
      {
        filename: "gateway-client.csr.pem",
        href: "blob:setup-csr-fixture",
        connected: true,
      },
    ]);
    expect(window.document.querySelector("a[download]")).toBeNull();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(fetch.mock.calls[0][0]).toBe("/setup/api/status");
    expect(fetch.mock.calls[0][1].method).toBe("GET");
    await vi.waitFor(
      () => expect(revokeURL).toHaveBeenCalledWith("blob:setup-csr-fixture"),
      { timeout: 2500 },
    );
  });

  it.each([
    "",
    "   \n",
    "-----BEGIN PRIVATE KEY-----\nVEVTVA==\n-----END PRIVATE KEY-----\n",
    "-----BEGIN CERTIFICATE-----\nVEVTVA==\n-----END CERTIFICATE-----\n",
    "-----BEGIN CERTIFICATE REQUEST-----\nnot base64!!\n-----END CERTIFICATE REQUEST-----\n",
    "-----BEGIN CERTIFICATE REQUEST-----\n" +
      "A".repeat(64 * 1024) +
      "\n-----END CERTIFICATE REQUEST-----\n",
    publicCSR + "-----BEGIN PRIVATE KEY-----\nVEVTVA==\n-----END PRIVATE KEY-----\n",
  ])("refuses an empty, private-key or malformed payload %#", async (value) => {
    const { window, createURL, downloads, fetch } = await setup(value);
    window.document.getElementById("setup-csr-download")!.click();
    expect(createURL).not.toHaveBeenCalled();
    expect(downloads).toEqual([]);
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("copies the existing CSR without creating a new request", async () => {
    const { window, writeText, fetch } = await setup();
    window.document.getElementById("setup-csr-copy")!.click();
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(publicCSR));
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("selects the CSR for manual copy if clipboard access fails", async () => {
    const { window, writeText, textarea, fetch } = await setup();
    writeText.mockRejectedValueOnce(new Error("clipboard unavailable"));
    window.document.getElementById("setup-csr-copy")!.click();
    await vi.waitFor(() => expect(window.document.activeElement).toBe(textarea));
    expect(textarea.selectionStart).toBe(0);
    expect(textarea.selectionEnd).toBe(publicCSR.length);
    expect(fetch).toHaveBeenCalledTimes(1);
  });
});
