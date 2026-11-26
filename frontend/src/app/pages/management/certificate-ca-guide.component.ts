import { Component, Input, computed, signal } from "@angular/core";

let validityControlSequence = 0;

@Component({
  selector: "gw-certificate-ca-guide",
  standalone: true,
  templateUrl: "./certificate-ca-guide.component.html",
  styles: [
    `
      :host {
        display: block;
        min-width: 0;
      }
      details {
        border-top: 1px solid var(--border);
        padding-top: 12px;
      }
      summary {
        min-height: 44px;
        padding: 10px 0;
        cursor: pointer;
        color: var(--accent);
        font-weight: 600;
      }
      summary:focus-visible {
        outline: 3px solid var(--focus);
        outline-offset: 4px;
      }
      .ca-guide-body {
        padding-top: 12px;
        overflow-wrap: anywhere;
      }
      ol {
        list-style: decimal;
        padding-left: 24px;
        margin: 20px 0;
      }
      li + li {
        margin-top: 24px;
      }
      h3 {
        margin: 0 0 8px;
        font-size: 14px;
        font-weight: 600;
      }
      p {
        margin: 8px 0;
      }
      textarea {
        display: block;
        width: 100%;
        max-width: 100%;
        margin: 12px 0 8px;
        padding: 12px;
        border: 1px solid var(--border);
        border-radius: 8px;
        background: var(--bg);
        color: var(--text);
        font: 12px/1.7 ui-monospace, monospace;
        white-space: pre;
        overflow: auto;
        resize: vertical;
      }
      code {
        font-size: 12px;
        overflow-wrap: anywhere;
      }
      .copy-status:empty {
        display: none;
      }
    `,
  ],
})
export class CertificateCaGuideComponent {
  private readonly currentCSR = signal("");
  @Input() set csrPem(value: string) {
    this.currentCSR.set(value || "");
    this.feedback.set("");
  }
  readonly feedback = signal("");
  readonly validityDays = signal("90");
  readonly validityControlID = `gateway-client-validity-${++validityControlSequence}`;
  readonly presets = [90, 180, 365];
  readonly validatedDays = computed(() => {
    const value = this.validityDays().trim();
    if (!/^\d+$/.test(value)) return null;
    const days = Number(value);
    return Number.isInteger(days) && days >= 1 && days <= 365 ? days : null;
  });
  readonly validityError = computed(() =>
    this.validatedDays() === null ? "ระบุจำนวนเต็ม 1–365 วัน" : "",
  );
  // Keep the commands in sync with the first-run Setup guide and GATEWAY_CLIENT_CA.md.
  readonly commands = {
    init: `mkdir -p -m 700 "$HOME/.hospital-erp-pki"
bash "$HOME/dev/erp-platform/gateway/deploy/gateway/issue-gateway-client-cert.sh" init \\
  "$HOME/.hospital-erp-pki/central-gateway-client-ca"`,
  };
  readonly validCSR = computed(() => {
    const value = this.currentCSR();
    return value.length <= 64 * 1024 && /^-----BEGIN CERTIFICATE REQUEST-----\r?\n(?:[A-Za-z0-9+/=]+\r?\n)+-----END CERTIFICATE REQUEST-----\s*$/.test(value);
  });
  readonly signingCommand = computed(() => {
    const days = this.validatedDays();
    if (days === null || !this.validCSR()) return "";
    const csr = this.currentCSR().trim().replace(/\r\n/g, "\n");
    return `(\n  set -eu\n  umask 077\n  GATEWAY_CERT_WORK=$(mktemp -d "$HOME/Downloads/gateway-client.XXXXXX")\n  cat > "$GATEWAY_CERT_WORK/gateway-client.csr.pem" <<'GATEWAY_CLIENT_CSR_PEM'\n${csr}\nGATEWAY_CLIENT_CSR_PEM\n  bash "$HOME/dev/erp-platform/gateway/deploy/gateway/issue-gateway-client-cert.sh" sign \\\n    "$HOME/.hospital-erp-pki/central-gateway-client-ca" \\\n    "$GATEWAY_CERT_WORK/gateway-client.csr.pem" \\\n    "$GATEWAY_CERT_WORK/gateway-client.crt.pem" \\\n    ${days}\n  cat "$GATEWAY_CERT_WORK/gateway-client.crt.pem" \\\n    "$HOME/.hospital-erp-pki/central-gateway-client-ca/gateway-client-ca.crt.pem" \\\n    > "$GATEWAY_CERT_WORK/gateway-client-chain.pem"\n  printf '\\nไฟล์สำหรับนำเข้า Gateway: %s\\n' "$GATEWAY_CERT_WORK/gateway-client-chain.pem"\n)`;
  });

  setValidityDays(value: string): void {
    this.validityDays.set(value);
    this.feedback.set("");
  }

  onValidityInput(event: Event): void {
    this.setValidityDays((event.target as HTMLInputElement).value);
  }

  async copySigningCommand(command: HTMLTextAreaElement): Promise<void> {
    const generated = this.signingCommand();
    if (!generated) return;
    command.value = generated;
    await this.copy(command);
  }

  async copy(command: HTMLTextAreaElement): Promise<void> {
    this.feedback.set("");
    try {
      await navigator.clipboard.writeText(command.value);
      this.feedback.set("คัดลอกคำสั่งแล้ว นำไปวางใน Terminal บน Mac");
    } catch {
      command.focus();
      command.select();
      this.feedback.set("เลือกคำสั่งแล้ว กด ⌘C บน Mac หรือ Ctrl+C เพื่อคัดลอก");
    }
  }
}
