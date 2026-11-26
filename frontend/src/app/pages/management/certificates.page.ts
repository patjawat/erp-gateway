import { Component, OnDestroy, OnInit, inject, signal } from "@angular/core";
import { FormsModule } from "@angular/forms";
import { GatewayApi } from "../../core/api.service";
import { ModalComponent } from "../../core/modal.component";
import { CertificateCaGuideComponent } from "./certificate-ca-guide.component";
import { thaiTime } from "../../core/format";
import {
  CertificateRequest,
  CertificatesResponse,
  errorMessage,
} from "./management.models";

@Component({
  selector: "gw-certificates-page",
  standalone: true,
  imports: [FormsModule, ModalComponent, CertificateCaGuideComponent],
  templateUrl: "./certificates.page.html",
  styles: [
    `
      .certificate-grid {
        display: grid;
        align-items: start;
        grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
        gap: 24px;
      }
      .certificate-details {
        display: grid;
        grid-template-columns: 150px minmax(0, 1fr);
        gap: 12px 20px;
        margin: 0;
      }
      .certificate-details dt {
        color: var(--muted);
      }
      .certificate-details dd {
        margin: 0;
        overflow-wrap: anywhere;
      }
      .certificate-pem {
        min-height: 210px;
        font-family: ui-monospace, monospace;
        font-size: 12px;
        line-height: 1.65;
      }
      @media (max-width: 760px) {
        .certificate-grid {
          grid-template-columns: minmax(0, 1fr);
        }
        .certificate-details {
          grid-template-columns: minmax(0, 1fr);
          gap: 4px;
        }
        .certificate-details dd {
          margin-bottom: 10px;
        }
      }
    `,
  ],
})
export class CertificatesPage implements OnInit, OnDestroy {
  private readonly api = inject(GatewayApi);
  readonly data = signal<CertificatesResponse | null>(null);
  readonly loading = signal(false);
  readonly error = signal("");
  readonly message = signal("");
  readonly modal = signal<"csr" | "import" | null>(null);
  readonly busy = signal(false);
  readonly modalError = signal("");
  readonly copied = signal(false);
  readonly formatTime = thaiTime;
  certificatePem = "";
  importConfirmed = false;
  private revision = 0;
  private fileRevision = 0;
  private destroyed = false;

  ngOnInit(): void {
    void this.load();
  }

  ngOnDestroy(): void {
    this.destroyed = true;
    this.revision++;
    this.fileRevision++;
    this.certificatePem = "";
  }

  async load(): Promise<void> {
    const revision = ++this.revision;
    this.loading.set(true);
    this.error.set("");
    try {
      const data = await this.api.get<CertificatesResponse>("certificates");
      if (!this.destroyed && revision === this.revision) this.data.set(data);
    } catch (error) {
      if (!this.destroyed && revision === this.revision)
        this.error.set(errorMessage(error));
    } finally {
      if (!this.destroyed && revision === this.revision)
        this.loading.set(false);
    }
  }

  open(kind: "csr" | "import"): void {
    const data = this.data();
    if (
      !data ||
      (kind === "csr"
        ? !data.capabilities.csr
        : !data.capabilities.import || !data.pending_request)
    )
      return;
    this.certificatePem = "";
    this.importConfirmed = false;
    this.modalError.set("");
    this.copied.set(false);
    this.modal.set(kind);
  }

  close(): void {
    if (this.busy()) return;
    this.fileRevision++;
    this.certificatePem = "";
    this.importConfirmed = false;
    this.modalError.set("");
    this.modal.set(null);
  }

  async createCSR(): Promise<void> {
    if (this.busy() || !this.data()?.capabilities.csr) return;
    this.busy.set(true);
    this.modalError.set("");
    try {
      const request = await this.api.post<CertificateRequest>(
        "certificates/csr",
        {},
      );
      if (this.destroyed || !this.api.authenticated()) return;
      this.data.update((data) =>
        data ? { ...data, pending_request: request } : data,
      );
      this.message.set(
        "สร้างคำขอใบรับรองแล้ว ส่งเฉพาะ CSR ให้ผู้ดูแล CA ลงนาม",
      );
    } catch (error) {
      if (!this.destroyed) this.modalError.set(errorMessage(error));
    } finally {
      if (!this.destroyed) this.busy.set(false);
    }
  }

  async copyCSR(): Promise<void> {
    const csr = this.data()?.pending_request?.csr_pem;
    if (!csr) return;
    this.modalError.set("");
    try {
      await navigator.clipboard.writeText(csr);
      if (!this.destroyed) this.copied.set(true);
    } catch {
      this.modalError.set(
        "คัดลอกอัตโนมัติไม่ได้ เลือกข้อความ CSR แล้วคัดลอก หรือดาวน์โหลดไฟล์",
      );
    }
  }

  downloadCSR(): void {
    const csr = this.data()?.pending_request?.csr_pem;
    if (!csr) return;
    const url = URL.createObjectURL(
      new Blob([csr], { type: "application/pkcs10" }),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = "gateway-client.csr.pem";
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }

  async readFile(event: Event): Promise<void> {
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0];
    input.value = "";
    if (!file || this.busy()) return;
    this.modalError.set("");
    if (file.size > 1024 * 1024) {
      this.modalError.set("ไฟล์ใบรับรองต้องมีขนาดไม่เกิน 1 MiB");
      return;
    }
    const revision = ++this.fileRevision;
    this.busy.set(true);
    try {
      const text = await file.text();
      if (
        !this.destroyed &&
        revision === this.fileRevision &&
        this.modal() === "import"
      ) {
        this.certificatePem = text;
        this.importConfirmed = false;
      }
    } catch {
      if (!this.destroyed && revision === this.fileRevision)
        this.modalError.set("อ่านไฟล์ไม่ได้ กรุณาเลือกใหม่หรือวางข้อความ PEM");
    } finally {
      if (!this.destroyed && revision === this.fileRevision)
        this.busy.set(false);
    }
  }

  async importCertificate(): Promise<void> {
    if (
      this.busy() ||
      !this.data()?.capabilities.import ||
      !this.data()?.pending_request
    )
      return;
    this.modalError.set("");
    if (!this.certificatePem.trim()) {
      this.modalError.set("วางใบรับรองและ chain ที่ลงนามแล้วก่อนนำเข้า");
      return;
    }
    if (!this.importConfirmed) {
      this.modalError.set("ยืนยันการเปิดใช้ใบรับรองก่อนนำเข้า");
      return;
    }
    this.busy.set(true);
    try {
      await this.api.post<{ activated: boolean }>("certificates/import", {
        certificate_pem: this.certificatePem,
      });
      if (this.destroyed || !this.api.authenticated()) return;
      this.certificatePem = "";
      this.importConfirmed = false;
      this.modal.set(null);
      this.message.set("นำเข้าและเปิดใช้ใบรับรองแล้ว");
      await this.load();
    } catch (error) {
      if (!this.destroyed) this.modalError.set(errorMessage(error));
    } finally {
      if (!this.destroyed) this.busy.set(false);
    }
  }

  statusLabel(status: string): string {
    return (
      (
        {
          valid: "พร้อมใช้งาน",
          expiring: "ใกล้หมดอายุ",
          expired: "หมดอายุแล้ว",
          not_yet_valid: "ยังไม่ถึงวันเริ่มใช้งาน",
        } as Record<string, string>
      )[status] || "ยังไม่ทราบสถานะ"
    );
  }
}
