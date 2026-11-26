import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { JSDOM, VirtualConsole } from "jsdom";
import { afterEach, describe, expect, it, vi } from "vitest";

const assets = resolve(process.cwd(), "../backend/internal/admin/assets");
const html = readFileSync(resolve(assets, "index.html"), "utf8");
const scripts = ["app.js", "guided.js"].map(name => readFileSync(resolve(assets, name), "utf8")).join("\n");
const windows: JSDOM[] = [];

async function setup(initialState = "setup") {
  const console = new VirtualConsole();
  const errors: Error[] = [];
  console.on("jsdomError", error => { if (!error.message.includes("Not implemented: navigation")) errors.push(error); });
  const dom = new JSDOM(html, { url: "https://new.example.org/setup/", runScripts: "outside-only", virtualConsole: console, pretendToBeVisual: true });
  windows.push(dom);
  const { window } = dom;
  Object.defineProperty(window.HTMLDialogElement.prototype, "showModal", { value() { this.open = true; } });
  Object.defineProperty(window.HTMLDialogElement.prototype, "close", { value() { this.open = false; } });
  let migrationState = "receiving";
  let driveReady = false;
  const fetch = vi.fn(async (path: string, _options?: RequestInit) => {
    const data = path === "/setup/api/status" ? { state: initialState, authenticated: true, csrf_token: "setup-only", public_url: "https://new.example.org", setup_enabled: true }
      : path === "/setup/api/backups/migration/receive" ? { code: "one-time-pairing", expires_at: "2026-11-26T12:30:00Z", installation_id: "new-installation" }
      : path === "/setup/api/backups" ? { migration: { state: migrationState, session_id: "migration-1" }, drive: { recovery_ready: driveReady } }
      : path.startsWith("/setup/api/backups/drive/snapshots?") ? { items: [{ id: "remote-1", created_at: "2026-11-26T12:00:00Z" }], next_cursor: "remote-next" }
      : path === "/setup/api/backups/drive/recover" ? { authorization_url: "https://accounts.google.com/o/oauth2/v2/auth?state=fixture" }
      : path === "/setup/api/backups/jobs/stage-1" ? { id: "stage-1", state: "completed", restore_id: "restore-1" }
      : path === "/setup/api/backups/restore/restore-1" ? { id: "restore-1", ready: true, certificate_expired: true, public_url: "https://new.example.org", created_at: "2026-11-26T12:00:00Z", summary: "2 หน่วยงาน" }
      : { job: { id: "stage-1", state: "running" } };
    return { ok: true, status: 200, json: async () => ({ data }) };
  });
  Object.defineProperty(window, "fetch", { value: fetch });
  Object.defineProperty(window, "AbortSignal", { value: AbortSignal });
  window.eval(`(() => {\n${scripts}\n})();`);
  const byID = <T extends HTMLElement>(id: string) => window.document.getElementById(id) as T;
  await vi.waitFor(() => expect(byID("setup-restore-entry").hidden).toBe(false));
  return { window, byID, fetch, errors, setMigration: (state: string) => { migrationState = state; }, setDriveReady: () => { driveReady = true; } };
}
afterEach(() => { windows.splice(0).forEach(dom => dom.window.close()); vi.restoreAllMocks(); });

describe("fresh Setup complete restore and migration", () => {
  it("creates a receiver code with authenticated Setup CSRF and renders its Thai expiry", async () => {
    const { byID, fetch, errors } = await setup();
    byID<HTMLButtonElement>("setup-open-receiver").click();
    byID<HTMLButtonElement>("setup-receiver-create").click();
    await vi.waitFor(() => expect(byID("setup-receiver-result").hidden).toBe(false));
    expect(byID<HTMLInputElement>("setup-receiver-code").value).toBe("one-time-pairing");
    expect(byID("setup-receiver-expiry").textContent).toContain("2569");
    const call = fetch.mock.calls.find(([path]) => path === "/setup/api/backups/migration/receive")!;
    expect((call[1]?.headers as Record<string, string>)["X-CSRF-Token"]).toBe("setup-only");
    expect(errors).toEqual([]);
  });

  it("does not activate a received base and requires final data plus explicit source-stop confirmation", async () => {
    const { byID, fetch, setMigration, errors } = await setup();
    setMigration("prepared"); byID<HTMLButtonElement>("setup-open-receiver").click();
    await vi.waitFor(() => expect(byID("setup-transfer-status").textContent).toContain("ข้อมูลรอบแรกพร้อม"));
    expect(byID("setup-import-inspection").hidden).toBe(true);
    expect(byID<HTMLButtonElement>("setup-import-activate").disabled).toBe(true);
    setMigration("final_ready"); byID<HTMLButtonElement>("setup-transfer-refresh").click();
    await vi.waitFor(() => expect(byID("setup-import-inspection").hidden).toBe(false));
    expect(byID<HTMLButtonElement>("setup-import-activate").disabled).toBe(true);
    const stopped = byID<HTMLInputElement>("setup-import-source-stopped");
    stopped.checked = true; stopped.dispatchEvent(new Event("change"));
    expect(byID<HTMLButtonElement>("setup-import-activate").disabled).toBe(false);
    expect(fetch.mock.calls.some(([path]) => path === "/setup/api/backups/migration/activate")).toBe(false);
    expect(errors).toEqual([]);
  });

  it("uploads an encrypted file with multipart CSRF, inspects it and preserves expired-certificate repair", async () => {
    const { window, byID, fetch, errors } = await setup();
    byID<HTMLButtonElement>("setup-open-import").click();
    const file = new window.File(["encrypted fixture"], "gateway-full.tar");
    Object.defineProperty(byID("setup-import-file"), "files", { value: [file], configurable: true });
    byID<HTMLInputElement>("setup-import-secret").value = "fixture-recovery-key";
    byID("setup-import-form").dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => expect(fetch.mock.calls.some(([path]) => path === "/setup/api/backups/restore")).toBe(true));
    const request = fetch.mock.calls.find(([path]) => path === "/setup/api/backups/restore")![1]!;
    expect(request.body).toBeInstanceOf(window.FormData);
    expect((request.body as FormData).get("recovery_secret")).toBe("fixture-recovery-key");
    expect((request.headers as Record<string, string>)["X-CSRF-Token"]).toBe("setup-only");
    expect((request.headers as Record<string, string>)["Content-Type"]).toBeUndefined();
    await vi.waitFor(() => expect(byID<HTMLInputElement>("setup-import-secret").value).toBe(""));
    byID<HTMLButtonElement>("setup-transfer-refresh").click();
    await vi.waitFor(() => expect(byID("setup-import-inspection").hidden).toBe(false));
    expect(byID("setup-import-expired").hidden).toBe(false);
    expect(byID("setup-import-summary").textContent).toContain("2 หน่วยงาน");
    expect(byID<HTMLButtonElement>("setup-import-activate").disabled).toBe(true);
    expect(errors).toEqual([]);
  });

  it("bootstraps Drive from the offline kit and fetches only one bounded snapshot page", async () => {
    const { window, byID, fetch, setDriveReady, errors } = await setup();
    byID<HTMLButtonElement>("setup-open-drive-recovery").click();
    byID<HTMLTextAreaElement>("setup-drive-kit").value = '{"recovery_secret":"fixture-secret"}';
    byID<HTMLInputElement>("setup-drive-callback").value = "https://new.example.org/backup/oauth/callback";
    byID("setup-drive-recovery-form").dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => expect(byID("setup-drive-authorization").hidden).toBe(false));
    const request = fetch.mock.calls.find(([path]) => path === "/setup/api/backups/drive/recover")![1]!;
    expect(JSON.parse(request.body as string)).toEqual({ recovery_kit: '{"recovery_secret":"fixture-secret"}', redirect_url: "https://new.example.org/backup/oauth/callback" });
    expect(byID<HTMLTextAreaElement>("setup-drive-kit").value).toBe("");
    setDriveReady(); byID<HTMLButtonElement>("setup-transfer-refresh").click();
    await vi.waitFor(() => expect(byID("setup-drive-snapshots").hidden).toBe(false));
    const calls = fetch.mock.calls.filter(([path]) => path.startsWith("/setup/api/backups/drive/snapshots?"));
    expect(calls).toHaveLength(1);
    expect(calls[0][0]).toBe("/setup/api/backups/drive/snapshots?limit=25");
    expect(byID("setup-drive-snapshot-list").textContent).toContain("2569");
    expect(errors).toEqual([]);
  });

  it("repairs a recovered identity without asking for a new Admin password or private key", async () => {
    const { window, byID, fetch, errors } = await setup("recovery");
    expect(byID("setup-open-repair").hidden).toBe(false);
    byID<HTMLButtonElement>("setup-open-repair").click();
    byID<HTMLTextAreaElement>("setup-repair-cert").value = "-----BEGIN CERTIFICATE-----\nfixture\n-----END CERTIFICATE-----";
    byID("setup-repair-form").dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => expect(fetch.mock.calls.some(([path]) => path === "/setup/api/backups/restore/repair")).toBe(true));
    const request = fetch.mock.calls.find(([path]) => path === "/setup/api/backups/restore/repair")![1]!;
    const body = JSON.parse(request.body as string);
    expect(Object.keys(body).sort()).toEqual(["client_certificate_pem"]);
    await vi.waitFor(() => expect(byID("setup-transfer-status").textContent).toContain("บัญชีเดิม"));
    expect(errors).toEqual([]);
  });

  it("can repair only the Hospital server CA while preserving the original valid client certificate", async () => {
    const { window, byID, fetch, errors } = await setup("recovery");
    byID<HTMLButtonElement>("setup-open-repair").click();
    byID<HTMLTextAreaElement>("setup-repair-ca").value = "-----BEGIN CERTIFICATE-----\nfixture-ca\n-----END CERTIFICATE-----";
    byID("setup-repair-form").dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => expect(byID("setup-transfer-status").textContent).toContain("บัญชีเดิม"));
    const request = fetch.mock.calls.find(([path]) => path === "/setup/api/backups/restore/repair")![1]!;
    expect(JSON.parse(request.body as string)).toEqual({ client_certificate_pem: "", hospital_ca_pem: "-----BEGIN CERTIFICATE-----\nfixture-ca\n-----END CERTIFICATE-----" });
    expect(errors).toEqual([]);
  });
});
