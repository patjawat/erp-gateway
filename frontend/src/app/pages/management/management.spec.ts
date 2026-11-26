import "@angular/compiler";
import { Injector, runInInjectionContext, signal } from "@angular/core";
import { describe, expect, it, vi } from "vitest";
import { GatewayApi } from "../../core/api.service";
import { SettingsPage } from "./settings.page";
import { CertificatesPage } from "./certificates.page";
import { SettingsResponse, auditDateBounds } from "./management.models";

const saved: SettingsResponse = {
  configured: true,
  editable: true,
  message: "",
  settings: {
    public_url: "https://gateway.example.org",
    approved_upstreams: ["12345=https://hospital.example.org"],
    private_upstream_cidrs: ["10.0.0.0/8"],
    trusted_proxy_cidrs: ["172.20.0.0/24"],
  },
};

function setup() {
  const api = {
    authenticated: signal(true),
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    download: vi.fn(),
    expire: vi.fn(),
  };
  const injector = Injector.create({
    providers: [{ provide: GatewayApi, useValue: api }],
  });
  return {
    api,
    settings: runInInjectionContext(injector, () => new SettingsPage()),
    certificates: runInInjectionContext(injector, () => new CertificatesPage()),
  };
}

describe("management workflows", () => {
  it("maps a Thai calendar day to an inclusive day with an exclusive next-day bound", () => {
    const bounds = auditDateBounds("2026-12-31", "2026-12-31");
    expect(new Date(bounds["after"] * 1000).toISOString()).toBe(
      "2026-12-30T17:00:00.000Z",
    );
    expect(new Date(bounds["before"] * 1000).toISOString()).toBe(
      "2026-12-31T17:00:00.000Z",
    );
    expect(auditDateBounds("", "")).toEqual({});
  });

  it("requires review and expires the local session only after settings were applied", async () => {
    const { api, settings } = setup();
    settings.data.set(saved);
    settings.open("settings");
    settings.draft.public_url = "https://next.example.org";
    await settings.apply();
    expect(api.put).not.toHaveBeenCalled();
    settings.review();
    api.put.mockResolvedValue({ applied: true, relogin_required: true });
    await settings.apply();
    expect(api.put).toHaveBeenCalledWith(
      "settings",
      expect.objectContaining({
        public_url: "https://next.example.org",
        approved_upstreams: saved.settings!.approved_upstreams,
      }),
    );
    expect(api.expire).toHaveBeenCalledOnce();
    expect(settings.modal()).toBeNull();
  });

  it("keeps the reviewed settings draft after a rejected write", async () => {
    const { api, settings } = setup();
    settings.data.set(saved);
    settings.open("settings");
    settings.draft.public_url = "https://next.example.org";
    settings.review();
    api.put.mockRejectedValue(new Error("ต้องคงเครือข่ายเดิม"));
    await settings.apply();
    expect(settings.draft.public_url).toBe("https://next.example.org");
    expect(settings.reviewing()).toBe(true);
    expect(settings.modalError()).toBe("ต้องคงเครือข่ายเดิม");
    expect(api.expire).not.toHaveBeenCalled();
  });

  it.each([undefined, false])(
    "keeps HTTP disabled when the LAN capability is %s",
    (lan_http_enabled) => {
      const { settings } = setup();
      settings.data.set({ ...saved, lan_http_enabled });
      settings.open("settings");
      settings.draft.public_url = "http://10.10.8.127:8080";
      settings.review();
      expect(settings.reviewing()).toBe(false);
      expect(settings.modalError()).toContain("HTTPS");
    },
  );

  it.each([
    "http://10.10.8.127:8080",
    "http://172.16.0.10:8080/",
    "http://192.168.1.10",
    "http://[fd00::127]:8080",
    "http://[fc00::1]",
  ])("reviews private HTTP only with the server LAN capability: %s", (url) => {
    const { settings } = setup();
    settings.data.set({ ...saved, lan_http_enabled: true });
    settings.open("settings");
    settings.draft.public_url = url;
    settings.review();
    expect(settings.modalError()).toBe("");
    expect(settings.reviewing()).toBe(true);
  });

  it.each(["localhost", "127.0.0.1", "127.10.0.2", "[::1]"])(
    "reviews HTTP %s only with the separate local capability",
    (host) => {
      const { settings } = setup();
      settings.data.set({ ...saved, local_http_enabled: true });
      settings.open("settings");
      settings.draft.public_url = `http://${host}:8080`;
      settings.review();
      expect(settings.modalError()).toBe("");
      expect(settings.reviewing()).toBe(true);
    },
  );

  it.each([
    { lan_http_enabled: true, url: "http://localhost:8080" },
    { lan_http_enabled: true, url: "http://127.0.0.1:8080" },
    { local_http_enabled: true, url: "http://10.10.8.127:8080" },
    { local_http_enabled: true, url: "http://2130706433:8080" },
    { local_http_enabled: true, url: "http://0x7f000001:8080" },
  ])("does not broaden one HTTP capability into another: $url", ({ url, ...capability }) => {
    const { settings } = setup();
    settings.data.set({ ...saved, ...capability });
    settings.open("settings");
    settings.draft.public_url = url;
    settings.review();
    expect(settings.reviewing()).toBe(false);
    expect(settings.modalError()).not.toBe("");
  });

  it.each([
    "http://8.8.8.8:8080",
    "http://172.15.0.10",
    "http://172.32.0.10",
    "http://localhost:8080",
    "http://gateway.example.org",
    "http://[2001:db8::1]",
    "http://0x0a0a087f:8080",
    "http://168429695:8080",
    "http://10.10.8.127:8080/setup/",
    "http://user:password@10.10.8.127:8080",
    "http://10.10.8.127:8080?next=admin",
    "http://10.10.8.127:8080#admin",
    "http://10.10.8.127:0",
    "https://gateway.example.org/setup/",
    "https:/gateway.example.org",
    "https://gateway.example.org/./",
  ])("rejects unsupported Gateway URLs even in LAN mode: %s", (url) => {
    const { settings } = setup();
    settings.data.set({ ...saved, lan_http_enabled: true });
    settings.open("settings");
    settings.draft.public_url = url;
    settings.review();
    expect(settings.reviewing()).toBe(false);
    expect(settings.modalError()).not.toBe("");
  });

  it("treats Go nil settings slices as empty lists without inventing a change", () => {
    const { settings } = setup();
    settings.data.set({
      ...saved,
      settings: {
        ...saved.settings!,
        trusted_proxy_cidrs: null as unknown as string[],
        private_upstream_cidrs: null as unknown as string[],
        approved_upstreams: null as unknown as string[],
      },
    });
    settings.open("settings");
    expect(settings.draft.approved_upstreams).toBe("");
    expect(settings.changes()).toEqual([]);
  });

  it("does not export recovery with mismatched passphrases and clears them on close", async () => {
    const { api, settings } = setup();
    settings.data.set(saved);
    settings.open("recovery");
    settings.passphrase = "example-recovery-passphrase";
    settings.confirmation = "mismatch";
    await settings.exportRecovery();
    expect(api.download).not.toHaveBeenCalled();
    expect(settings.modalError()).toContain("ไม่ตรงกัน");
    settings.close();
    expect(settings.passphrase).toBe("");
    expect(settings.confirmation).toBe("");
  });

  it("keeps the certificate chain after validation fails and submits no private key or CA rotation", async () => {
    const { api, certificates } = setup();
    certificates.data.set({
      gateway: null,
      issuer: { status: "not_configured", message: "" },
      capabilities: { csr: true, import: true, renew: false },
      pending_request: {
        csr_pem: "CSR",
        fingerprint: "fingerprint",
        created_at: 1,
      },
    });
    certificates.open("import");
    certificates.certificatePem = "signed-chain";
    certificates.importConfirmed = true;
    api.post.mockRejectedValue(new Error("ใบรับรองไม่ตรงกับ CSR"));
    await certificates.importCertificate();
    expect(api.post).toHaveBeenCalledWith("certificates/import", {
      certificate_pem: "signed-chain",
    });
    expect(certificates.certificatePem).toBe("signed-chain");
    expect(certificates.modal()).toBe("import");
    expect(certificates.modalError()).toContain("ไม่ตรงกับ CSR");
  });
});
