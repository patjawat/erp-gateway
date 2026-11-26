import "@angular/compiler";
import { ɵresolveComponentResources as resolveComponentResources } from "@angular/core";
import { TestBed } from "@angular/core/testing";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CertificateCaGuideComponent } from "./certificate-ca-guide.component";

const guideHTML = readFileSync(resolve(process.cwd(), "src/app/pages/management/certificate-ca-guide.component.html"), "utf8");
const clipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, "clipboard");

afterEach(() => {
  vi.restoreAllMocks();
  TestBed.resetTestingModule();
  if (clipboardDescriptor) Object.defineProperty(navigator, "clipboard", clipboardDescriptor);
  else Reflect.deleteProperty(navigator, "clipboard");
});

async function renderGuide() {
  await resolveComponentResources(async (url) => {
    if (!url.endsWith("certificate-ca-guide.component.html")) throw new Error("Unexpected component resource: " + url);
    return guideHTML;
  });
  await TestBed.configureTestingModule({ imports: [CertificateCaGuideComponent] }).compileComponents();
  const fixture = TestBed.createComponent(CertificateCaGuideComponent);
  fixture.detectChanges();
  await fixture.whenStable();
  return fixture;
}

describe("offline client certificate validity commands", () => {
  it("starts at 90 days without changing CA initialization or chain commands", () => {
    const guide = new CertificateCaGuideComponent();
    expect(guide.validityDays()).toBe("90");
    expect(guide.signingCommand()).toMatch(/gateway-client\.crt\.pem" \\\n  90$/);
    expect(guide.signingCommand().slice(-7).split("").map((character) => character.charCodeAt(0))).toEqual([32, 92, 10, 32, 32, 57, 48]);
    expect(guide.commands.init).toMatch(/central-gateway-client-ca"$/);
    expect(guide.commands.chain).toMatch(/gateway-client-chain\.pem"$/);
    expect(guide.commands.init).not.toContain("365");
  });

  it.each(["1", "30", "90", "180", "365"])("updates the signing command for %s days", (days) => {
    const guide = new CertificateCaGuideComponent();
    const init = guide.commands.init;
    const chain = guide.commands.chain;
    guide.setValidityDays(days);
    expect(guide.validityError()).toBe("");
    expect(guide.signingCommand()).toMatch(new RegExp("\\n  " + days + "$"));
    expect(guide.commands.init).toBe(init);
    expect(guide.commands.chain).toBe(chain);
  });

  it.each(["", " ", "0", "366", "-1", "1.5", "1e2", "abc", "90; touch /tmp/test"])("does not generate or copy a signing command from invalid days: %s", async (days) => {
    const guide = new CertificateCaGuideComponent();
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
    const command = document.createElement("textarea");
    command.value = guide.signingCommand();
    guide.setValidityDays("365");
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    await guide.copySigningCommand(command);
    expect(writeText).toHaveBeenCalledWith(guide.signingCommand());
    expect(command.value.endsWith("\n  365")).toBe(true);
  });

  it.each([90, 180, 365])("updates the displayed command immediately from the %s-day preset", async (days) => {
    const fixture = await renderGuide();
    const element: HTMLElement = fixture.nativeElement;
    const preset = [...element.querySelectorAll<HTMLButtonElement>('[role="group"] button')].find((button) => button.textContent?.trim() === `${days} วัน`)!;
    preset.click();
    fixture.detectChanges();
    expect((element.querySelector('input[type="number"]') as HTMLInputElement).value).toBe(String(days));
    expect(preset.getAttribute("aria-pressed")).toBe("true");
    expect((element.querySelector('[aria-label="คำสั่งลงนาม CSR บน Mac"]') as HTMLTextAreaElement).value.endsWith("\n  " + days)).toBe(true);
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
    expect(copies.map((button) => button.disabled)).toEqual([false, true, false]);
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
