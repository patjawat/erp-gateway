import { Component, signal, ViewChild } from "@angular/core";
import { TestBed } from "@angular/core/testing";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ModalComponent } from "./modal.component";
@Component({
  standalone: true,
  imports: [ModalComponent],
  template:
    '<gw-modal title="แก้ไข" [dirty]="dirty()" [busy]="busy()" (closed)="closed()"><input id="test-draft"><button type="button">บันทึก</button><details><summary>วิธีลงนาม CA</summary><p>คู่มือ</p></details></gw-modal>',
})
class Host {
  @ViewChild(ModalComponent) modal!: ModalComponent;
  dirty = signal(true);
  busy = signal(false);
  closed = vi.fn();
}
describe("modal draft protection", () => {
  beforeEach(() => {
    Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
      configurable: true,
      value: function (this: HTMLDialogElement) {
        this.open = true;
      },
    });
    Object.defineProperty(HTMLDialogElement.prototype, "close", {
      configurable: true,
      value: function (this: HTMLDialogElement) {
        this.open = false;
      },
    });
  });
  afterEach(() => TestBed.resetTestingModule());
  async function create() {
    const fixture = TestBed.createComponent(Host);
    fixture.detectChanges();
    await fixture.whenStable();
    return fixture;
  }
  it("asks before discarding a draft and returns focus after rendering", async () => {
    const fixture = await create();
    const host = fixture.componentInstance;
    const input = fixture.nativeElement.querySelector(
      "input",
    ) as HTMLInputElement;
    input.focus();
    host.modal.requestClose();
    fixture.detectChanges();
    await fixture.whenStable();
    expect(host.modal.confirmDiscard()).toBe(true);
    expect(host.closed).not.toHaveBeenCalled();
    expect(document.activeElement?.textContent?.trim()).toBe("กลับไปแก้ไข");
    host.modal.returnToEditing();
    fixture.detectChanges();
    await fixture.whenStable();
    expect(host.modal.confirmDiscard()).toBe(false);
    expect(document.activeElement).toBe(input);
    fixture.destroy();
  });
  it("keeps a busy write open when close is requested", async () => {
    const fixture = await create();
    fixture.componentInstance.busy.set(true);
    fixture.detectChanges();
    fixture.componentInstance.modal.requestClose();
    expect(fixture.componentInstance.closed).not.toHaveBeenCalled();
    expect(fixture.componentInstance.modal.confirmDiscard()).toBe(false);
    fixture.destroy();
  });
  it("closes an unchanged form without a discard prompt", async () => {
    const fixture = await create();
    fixture.componentInstance.dirty.set(false);
    fixture.detectChanges();
    fixture.componentInstance.modal.requestClose();
    expect(fixture.componentInstance.closed).toHaveBeenCalledOnce();
    expect(fixture.componentInstance.modal.confirmDiscard()).toBe(false);
    fixture.destroy();
  });
  it("lets Tab reach collapsed help before wrapping focus inside the dialog", async () => {
    const fixture = await create();
    const root = fixture.nativeElement as HTMLElement;
    for (const element of root.querySelectorAll("button,input,summary")) {
      vi.spyOn(element, "getClientRects").mockReturnValue([
        { width: 100, height: 44 },
      ] as unknown as DOMRectList);
    }
    const save = root.querySelector<HTMLButtonElement>(
      "button:not(.icon-button)",
    )!;
    save.focus();
    const next = new KeyboardEvent("keydown", { key: "Tab", cancelable: true });
    fixture.componentInstance.modal.onTab(next);
    expect(next.defaultPrevented).toBe(false);

    root.querySelector<HTMLElement>("summary")!.focus();
    const wrap = new KeyboardEvent("keydown", { key: "Tab", cancelable: true });
    fixture.componentInstance.modal.onTab(wrap);
    expect(wrap.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(root.querySelector(".icon-button"));
    fixture.destroy();
  });
});
