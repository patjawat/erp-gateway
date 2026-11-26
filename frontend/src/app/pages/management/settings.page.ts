import { Component, OnDestroy, OnInit, inject, signal } from "@angular/core";
import { FormsModule } from "@angular/forms";
import { GatewayApi } from "../../core/api.service";
import { ModalComponent } from "../../core/modal.component";
import {
  RuntimeSettings,
  SettingsResponse,
  errorMessage,
  lines,
} from "./management.models";

type SettingsKey = keyof RuntimeSettings;
type SettingsDraft = Record<SettingsKey, string>;

const fields: { key: SettingsKey; label: string }[] = [
  { key: "public_url", label: "ที่อยู่ Gateway" },
  { key: "approved_upstreams", label: "ปลายทาง API ที่อนุญาต" },
  { key: "private_upstream_cidrs", label: "เครือข่ายภายในของปลายทาง" },
  { key: "trusted_proxy_cidrs", label: "เครือข่าย Reverse proxy ที่เชื่อถือ" },
];

function privateIPLiteral(host: string): boolean {
  if (host.startsWith("[")) return /^\[f[cd][0-9a-f]{2}:/i.test(host);
  const parts = host.split(".");
  if (
    parts.length !== 4 ||
    parts.some((part) => !/^(0|[1-9]\d{0,2})$/.test(part) || Number(part) > 255)
  )
    return false;
  const [first, second] = parts.map(Number);
  return (
    first === 10 ||
    (first === 172 && second >= 16 && second <= 31) ||
    (first === 192 && second === 168)
  );
}

@Component({
  selector: "gw-settings-page",
  standalone: true,
  imports: [FormsModule, ModalComponent],
  templateUrl: "./settings.page.html",
  styles: [
    `
      .settings-values {
        margin: 0;
        display: grid;
        gap: 18px;
      }
      .settings-values dt {
        color: var(--muted);
        margin-bottom: 5px;
      }
      .settings-values dd {
        margin: 0;
        white-space: pre-wrap;
        overflow-wrap: anywhere;
      }
      .settings-review {
        display: grid;
        gap: 18px;
      }
      .settings-change {
        display: grid;
        gap: 8px;
        padding-bottom: 16px;
        border-bottom: 1px solid var(--border);
      }
      .settings-change:last-child {
        border-bottom: 0;
        padding-bottom: 0;
      }
      .settings-change h3 {
        font-size: 14px;
        margin: 0;
      }
      .settings-comparison {
        display: grid;
        grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
        gap: 12px;
      }
      .settings-comparison div {
        min-width: 0;
      }
      .settings-comparison p {
        white-space: pre-wrap;
        overflow-wrap: anywhere;
        margin: 5px 0 0;
      }
      @media (max-width: 520px) {
        .settings-comparison {
          grid-template-columns: minmax(0, 1fr);
        }
      }
    `,
  ],
})
export class SettingsPage implements OnInit, OnDestroy {
  private readonly api = inject(GatewayApi);
  readonly data = signal<SettingsResponse | null>(null);
  readonly loading = signal(false);
  readonly error = signal("");
  readonly message = signal("");
  readonly modal = signal<"settings" | "recovery" | null>(null);
  readonly busy = signal(false);
  readonly modalError = signal("");
  readonly reviewing = signal(false);
  readonly fields = fields;
  draft: SettingsDraft = this.emptyDraft();
  passphrase = "";
  confirmation = "";
  private initialDraft = "";
  private revision = 0;
  private destroyed = false;

  ngOnInit(): void {
    void this.load();
  }

  ngOnDestroy(): void {
    this.destroyed = true;
    this.revision++;
    this.passphrase = "";
    this.confirmation = "";
  }

  private emptyDraft(): SettingsDraft {
    return {
      public_url: "",
      approved_upstreams: "",
      private_upstream_cidrs: "",
      trusted_proxy_cidrs: "",
    };
  }

  async load(): Promise<void> {
    const revision = ++this.revision;
    this.loading.set(true);
    this.error.set("");
    try {
      const data = await this.api.get<SettingsResponse>("settings");
      if (!this.destroyed && revision === this.revision) this.data.set(data);
    } catch (error) {
      if (!this.destroyed && revision === this.revision)
        this.error.set(errorMessage(error));
    } finally {
      if (!this.destroyed && revision === this.revision)
        this.loading.set(false);
    }
  }

  displayValue(key: SettingsKey, settings = this.data()?.settings): string {
    const value = settings?.[key];
    return Array.isArray(value)
      ? value.join("\n") || "ไม่ได้กำหนด"
      : value || "ไม่ได้กำหนด";
  }

  lanHTTPEnabled(): boolean {
    return this.data()?.lan_http_enabled === true;
  }

  open(kind: "settings" | "recovery"): void {
    const data = this.data();
    if (!data?.editable || !data.settings) return;
    this.passphrase = "";
    this.confirmation = "";
    this.modalError.set("");
    this.reviewing.set(false);
    this.draft = {
      public_url: data.settings.public_url,
      approved_upstreams: (data.settings.approved_upstreams || []).join("\n"),
      private_upstream_cidrs: (data.settings.private_upstream_cidrs || []).join(
        "\n",
      ),
      trusted_proxy_cidrs: (data.settings.trusted_proxy_cidrs || []).join("\n"),
    };
    this.initialDraft = JSON.stringify(this.draft);
    this.modal.set(kind);
  }

  close(): void {
    if (this.busy()) return;
    this.passphrase = "";
    this.confirmation = "";
    this.draft = this.emptyDraft();
    this.initialDraft = "";
    this.reviewing.set(false);
    this.modalError.set("");
    this.modal.set(null);
  }

  isDirty(): boolean {
    return this.modal() === "recovery"
      ? !!(this.passphrase || this.confirmation)
      : this.initialDraft !== JSON.stringify(this.draft);
  }

  payload(): RuntimeSettings {
    return {
      public_url: this.draft.public_url.trim(),
      approved_upstreams: lines(this.draft.approved_upstreams),
      private_upstream_cidrs: lines(this.draft.private_upstream_cidrs),
      trusted_proxy_cidrs: lines(this.draft.trusted_proxy_cidrs),
    };
  }

  changes(): {
    key: SettingsKey;
    label: string;
    before: string;
    after: string;
  }[] {
    const saved = this.data()?.settings;
    if (!saved) return [];
    const draft = this.payload();
    return fields
      .filter(
        (field) =>
          JSON.stringify(
            saved[field.key] ?? (field.key === "public_url" ? "" : []),
          ) !== JSON.stringify(draft[field.key]),
      )
      .map((field) => ({
        ...field,
        before: this.displayValue(field.key, saved),
        after: this.displayValue(field.key, draft),
      }));
  }

  review(): void {
    this.modalError.set("");
    try {
      const value = this.draft.public_url.trim();
      const url = new URL(value);
      const authority = value.match(
        /^https?:\/\/(\[[^\]]+\]|[^:/?#]+)(?::\d+)?\/?$/i,
      )?.[1];
      const allowedHTTP =
        this.lanHTTPEnabled() &&
        url.protocol === "http:" &&
        !!authority &&
        privateIPLiteral(authority);
      if (
        (url.protocol !== "https:" && !allowedHTTP) ||
        !authority ||
        !url.hostname ||
        url.username ||
        url.password ||
        url.pathname !== "/" ||
        url.search ||
        url.hash ||
        url.port === "0" ||
        /[\\\r\n]/.test(value)
      )
        throw new Error();
    } catch {
      this.modalError.set(
        this.lanHTTPEnabled()
          ? "ระบุ HTTPS หรือ HTTP ด้วย IP ภายใน เช่น http://10.10.8.127:8080 โดยไม่มี path ข้อมูลเข้าสู่ระบบ query หรือ fragment"
          : "ระบุที่อยู่ Gateway แบบ HTTPS โดยไม่มี path ข้อมูลเข้าสู่ระบบ query หรือ fragment",
      );
      return;
    }
    if (!this.changes().length) {
      this.modalError.set("ยังไม่มีค่าที่เปลี่ยนจากการตั้งค่าปัจจุบัน");
      return;
    }
    this.reviewing.set(true);
  }

  editAgain(): void {
    if (this.busy()) return;
    this.modalError.set("");
    this.reviewing.set(false);
  }

  async apply(): Promise<void> {
    if (this.busy() || !this.reviewing() || !this.data()?.editable) return;
    this.modalError.set("");
    this.busy.set(true);
    try {
      const result = await this.api.put<{
        applied: boolean;
        relogin_required: boolean;
      }>("settings", this.payload());
      if (this.destroyed || !this.api.authenticated()) return;
      if (!result.applied)
        throw new Error(
          "ยังใช้การตั้งค่าใหม่ไม่ได้ กรุณาโหลดข้อมูลแล้วลองอีกครั้ง",
        );
      this.draft = this.emptyDraft();
      this.initialDraft = "";
      this.modal.set(null);
      this.reviewing.set(false);
      this.message.set("บันทึกและใช้การตั้งค่าใหม่แล้ว");
      if (result.relogin_required)
        this.api.expire("บันทึกการตั้งค่าแล้ว กรุณาเข้าสู่ระบบอีกครั้ง");
      else await this.load();
    } catch (error) {
      if (!this.destroyed) this.modalError.set(errorMessage(error));
    } finally {
      if (!this.destroyed) this.busy.set(false);
    }
  }

  async exportRecovery(): Promise<void> {
    if (this.busy() || !this.data()?.editable) return;
    this.modalError.set("");
    const size = new TextEncoder().encode(this.passphrase).length;
    if ([...this.passphrase].length < 16 || size > 4096) {
      this.modalError.set(
        "ตั้งรหัสสำรองอย่างน้อย 16 ตัวอักษร และขนาดไม่เกิน 4096 ไบต์",
      );
      return;
    }
    if (this.confirmation !== this.passphrase) {
      this.modalError.set("รหัสสำรองที่ยืนยันไม่ตรงกัน");
      return;
    }
    this.busy.set(true);
    try {
      await this.api.download("recovery/export", {
        passphrase: this.passphrase,
      });
      if (this.destroyed || !this.api.authenticated()) return;
      this.passphrase = "";
      this.confirmation = "";
      this.modal.set(null);
      this.message.set(
        "ส่งไฟล์กู้คืนแบบเข้ารหัสให้เบราว์เซอร์แล้ว เก็บรหัสสำรองแยกจากไฟล์นี้",
      );
    } catch (error) {
      if (!this.destroyed) this.modalError.set(errorMessage(error));
    } finally {
      if (!this.destroyed) this.busy.set(false);
    }
  }
}
