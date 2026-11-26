import "@angular/compiler";
import { ɵresolveComponentResources as resolveComponentResources } from "@angular/core";
import { TestBed } from "@angular/core/testing";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CertificateCaGuideComponent } from "./certificate-ca-guide.component";

const guideHTML = readFileSync(resolve(process.cwd(), "src/app/pages/management/certificate-ca-guide.component.html"), "utf8");
const csr = "-----BEGIN CERTIFICATE REQUEST-----\nVGVzdCBwdWJsaWMgQ1NS\n-----END CERTIFICATE REQUEST-----\n";
const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, "clipboard");

afterEach(() => {
  vi.restoreAllMocks();
  TestBed.resetTestingModule();
  if (clipboardDescriptor) Object.defineProperty(navigator, "clipboard", clipboardDescriptor);
  else Reflect.deleteProperty(navigator, "clipboard");
});

async function renderGuide(withCSR = true) {
  await resolveComponentResources(async (url) => {
    if (!url.endsWith("certificate-ca-guide.component.html")) throw new Error("Unexpected component resource: " + url);
    return guideHTML;
  });
  await TestBed.configureTestingModule({ imports: [CertificateCaGuideComponent] }).compileComponents();
  const fixture = TestBed.createComponent(CertificateCaGuideComponent);
  if (withCSR) fixture.componentRef.setInput("csrPem", csr);
  fixture.detectChanges();
  await fixture.whenStable();
  return fixture;
}

describe("offline client certificate validity commands", () => {
  it("does not offer signing until the current CSR is available", async () => {
    const fixture = await renderGuide(false);
    const element: HTMLElement = fixture.nativeElement;
    expect(fixture.componentInstance.signingCommand()).toBe("");
    const copy = [...element.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent?.includes("คัดลอกคำสั่งลงนาม"))!;
    expect(copy.disabled).toBe(true);
    expect(element.textContent).toContain("สร้างหรือเปิดคำขอ CSR");
    fixture.componentRef.setInput("csrPem", csr);
    fixture.detectChanges();
    expect(copy.disabled).toBe(false);
    expect(fixture.componentInstance.signingCommand()).toContain(csr.trim());
  });

  it.each([
    "",
    "-----BEGIN PRIVATE KEY-----\nVGVzdA==\n-----END PRIVATE KEY-----\n",
    "-----BEGIN CERTIFICATE REQUEST-----\n$(touch /tmp/not-run)\n-----END CERTIFICATE REQUEST-----\n",
    "-----BEGIN CERTIFICATE REQUEST-----\n`touch /tmp/not-run`\n-----END CERTIFICATE REQUEST-----\n",
    "-----BEGIN CERTIFICATE REQUEST-----\nGATEWAY_CLIENT_CSR_PEM\n-----END CERTIFICATE REQUEST-----\n",
    "-----BEGIN CERTIFICATE REQUEST-----\n" + "A".repeat(64 * 1024) + "\n-----END CERTIFICATE REQUEST-----\n",
  ])("rejects unavailable, non-CSR, shell-active or oversized input", async (value) => {
    const guide = new CertificateCaGuideComponent();
    guide.csrPem = value;
    const writeText = vi.fn();
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    expect(guide.signingCommand()).toBe("");
    await guide.copySigningCommand(document.createElement("textarea"));
    expect(writeText).not.toHaveBeenCalled();
  });

  it("refreshes an already rendered command when the pending CSR changes", async () => {
    const fixture = await renderGuide();
    const nextCSR = csr.replace("VGVzdCBwdWJsaWMgQ1NS", "QW5vdGhlciBwdWJsaWMgQ1NS");
    fixture.componentRef.setInput("csrPem", nextCSR);
    fixture.detectChanges();
    const command = (fixture.nativeElement as HTMLElement).querySelector<HTMLTextAreaElement>('[aria-label="คำสั่งลงนาม CSR บน Mac"]')!.value;
    expect(command).toContain(nextCSR.trim());
    expect(command).not.toContain(csr.trim());
  });

  it("starts at 90 days with the current CSR and a complete command in a new directory", () => {
    const guide = new CertificateCaGuideComponent();
    guide.csrPem = csr;
    expect(guide.validityDays()).toBe("90");
    expect(guide.signingCommand()).toContain(csr.trim());
    expect(guide.signingCommand()).toContain('GATEWAY_CERT_WORK=$(mktemp -d "$HOME/Downloads/gateway-client.XXXXXX")');
    expect(guide.signingCommand()).toContain("\n    90\n");
    expect(guide.signingCommand()).toContain('<<\'GATEWAY_CLIENT_CSR_PEM\'');
    expect(guide.signingCommand()).not.toContain('"$HOME/Downloads/gateway-client.csr.pem"');
    expect(guide.commands.init).toMatch(/central-gateway-client-ca"$/);
    expect(guide.commands.init).not.toContain("365");
  });

  it.each(["1", "30", "90", "180", "365"])("updates the signing command for %s days", (days) => {
    const guide = new CertificateCaGuideComponent();
    guide.csrPem = csr;
    const init = guide.commands.init;
    guide.setValidityDays(days);
    expect(guide.validityError()).toBe("");
    expect(guide.signingCommand()).toContain("\n    " + days + "\n");
    expect(guide.commands.init).toBe(init);
  });

  it.each(["", " ", "0", "366", "-1", "1.5", "1e2", "abc", "90; touch /tmp/test"])("does not generate or copy a signing command from invalid days: %s", async (days) => {
    const guide = new CertificateCaGuideComponent();
    guide.csrPem = csr;
    const writeText = vi.fn();
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    guide.setValidityDays(days);
    expect(guide.validityError()).toContain("1–365");
    expect(guide.signingCommand()).toBe("");
    await guide.copySigningCommand(document.createElement("textarea"));
    expect(writeText).not.toHaveBeenCalled();
  });

  it("copies the selected validity and preserves the HTTP LAN fallback", async () => {
    const guide = new CertificateCaGuideComponent();
    guide.csrPem = csr;
    guide.setValidityDays("180");
    const command = document.createElement("textarea");
    command.value = guide.signingCommand();
    document.body.append(command);
    const writeText = vi.fn().mockRejectedValue(new Error("clipboard unavailable"));
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    try {
      await guide.copySigningCommand(command);
      expect(writeText).toHaveBeenCalledWith(command.value);
      expect(document.activeElement).toBe(command);
      expect(command.selectionStart).toBe(0);
      expect(command.selectionEnd).toBe(command.value.length);
      expect(guide.feedback()).toContain("⌘C");
    } finally {
      command.remove();
    }
  });

  it("copies the current validated command even before the textarea has rendered again", async () => {
    const guide = new CertificateCaGuideComponent();
    guide.csrPem = csr;
    const command = document.createElement("textarea");
    command.value = guide.signingCommand();
    guide.setValidityDays("365");
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    await guide.copySigningCommand(command);
    expect(writeText).toHaveBeenCalledWith(guide.signingCommand());
    expect(command.value).toContain("\n    365\n");
  });

  it.each([90, 180, 365])("updates the displayed command immediately from the %s-day preset", async (days) => {
    const fixture = await renderGuide();
    const element: HTMLElement = fixture.nativeElement;
    const preset = [...element.querySelectorAll<HTMLButtonElement>('[role="group"] button')].find((button) => button.textContent?.trim() === `${days} วัน`)!;
    preset.click();
    fixture.detectChanges();
    expect((element.querySelector('input[type="number"]') as HTMLInputElement).value).toBe(String(days));
    expect(preset.getAttribute("aria-pressed")).toBe("true");
    expect((element.querySelector('[aria-label="คำสั่งลงนาม CSR บน Mac"]') as HTMLTextAreaElement).value).toContain("\n    " + days + "\n");
  });

  it("associates an inline error with the input and disables only the signing copy action", async () => {
    const fixture = await renderGuide();
    const element: HTMLElement = fixture.nativeElement;
    const input = element.querySelector<HTMLInputElement>('input[type="number"]')!;
    input.value = "366";
    input.dispatchEvent(new Event("input", { bubbles: true }));
    fixture.detectChanges();
    const error = element.querySelector<HTMLElement>('[role="alert"]')!;
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(input.getAttribute("aria-describedby")).toContain(error.id);
    expect(error.hidden).toBe(false);
    expect(error.textContent).toContain("1–365");
    const copies = [...element.querySelectorAll<HTMLButtonElement>("button")].filter((button) => button.textContent?.includes("คัดลอกคำสั่ง"));
    expect(copies.map((button) => button.disabled)).toEqual([false, true]);
    expect((element.querySelector('[aria-label="คำสั่งลงนาม CSR บน Mac"]') as HTMLTextAreaElement).value).toBe("");
  });

  it("prevents Enter in the advisory control from submitting its containing form", async () => {
    const fixture = await renderGuide();
    const input = (fixture.nativeElement as HTMLElement).querySelector<HTMLInputElement>('input[type="number"]')!;
    const enter = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
    input.dispatchEvent(enter);
    expect(enter.defaultPrevented).toBe(true);
  });
});
