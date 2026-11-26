import {
  afterNextRender,
  AfterViewInit,
  Component,
  ElementRef,
  EventEmitter,
  HostListener,
  inject,
  Injector,
  Input,
  OnDestroy,
  Output,
  signal,
  ViewChild,
} from "@angular/core";
import { LucideX } from "@lucide/angular";
let modalID = 0;
@Component({
  selector: "gw-modal",
  standalone: true,
  imports: [LucideX],
  template: ` <dialog
    #dialog
    class="modal"
    [attr.aria-labelledby]="id + '-title'"
    [attr.aria-describedby]="description ? id + '-description' : null"
    (cancel)="onEscape($event)"
    (click)="backdrop($event)"
  >
    <header class="modal-header">
      <div>
        <h2 [id]="id + '-title'">{{ title }}</h2>
        @if (description) {
          <p [id]="id + '-description'" class="muted">{{ description }}</p>
        }
      </div>
      <button
        type="button"
        class="icon-button"
        aria-label="ปิดหน้าต่าง"
        [disabled]="busy"
        (click)="requestClose()"
      >
        <svg lucideX aria-hidden="true"></svg>
      </button>
    </header>
    <div class="modal-body">
      @if (confirmDiscard()) {
        <section class="notice notice-warning" role="alert">
          <strong>ทิ้งการแก้ไขที่ยังไม่ได้บันทึก?</strong>
          <p>ข้อมูลที่กรอกจะไม่ถูกเก็บไว้</p>
          <div class="row">
            <button type="button" class="button" (click)="returnToEditing()">
              กลับไปแก้ไข</button
            ><button
              type="button"
              class="button button-danger"
              (click)="finish()"
            >
              ทิ้งการแก้ไข
            </button>
          </div>
        </section>
      }
      <div [attr.inert]="confirmDiscard() ? '' : null"><ng-content /></div>
    </div>
  </dialog>`,
})
export class ModalComponent implements AfterViewInit, OnDestroy {
  @Input() title = "";
  @Input() description = "";
  @Input() busy = false;
  @Input() dirty = false;
  @Output() closed = new EventEmitter<void>();
  @ViewChild("dialog", { static: true }) dialog!: ElementRef<HTMLDialogElement>;
  readonly id = "gw-modal-" + ++modalID;
  readonly confirmDiscard = signal(false);
  private trigger = document.activeElement as HTMLElement | null;
  private injector = inject(Injector);
  private editingFocus: HTMLElement | null = null;
  ngAfterViewInit(): void {
    this.dialog.nativeElement.showModal();
    afterNextRender(
      () =>
        this.dialog.nativeElement
          .querySelector<HTMLElement>(
            "input:not([readonly]),textarea,select,button:not(.icon-button)",
          )
          ?.focus(),
      { injector: this.injector },
    );
  }
  @HostListener("focusin", ["$event"]) rememberEditingFocus(
    event: FocusEvent,
  ): void {
    const el = event.target;
    if (el instanceof HTMLElement && el.matches("input,textarea,select"))
      this.editingFocus = el;
  }
  returnToEditing(): void {
    this.confirmDiscard.set(false);
    afterNextRender(
      () => {
        if (this.editingFocus?.isConnected) this.editingFocus.focus();
        else
          this.dialog.nativeElement
            .querySelector<HTMLElement>("input:not([readonly]),textarea,select")
            ?.focus();
      },
      { injector: this.injector },
    );
  }
  onEscape(event: Event): void {
    event.preventDefault();
    if (this.confirmDiscard()) this.returnToEditing();
    else this.requestClose();
  }
  requestClose(): void {
    if (this.busy) return;
    if (this.dirty) {
      this.confirmDiscard.set(true);
      afterNextRender(
        () =>
          this.dialog.nativeElement
            .querySelector<HTMLElement>(".notice button")
            ?.focus(),
        { injector: this.injector },
      );
    } else this.finish();
  }
  finish(): void {
    if (!this.busy) {
      this.dialog.nativeElement.close();
      this.closed.emit();
    }
  }
  backdrop(event: MouseEvent): void {
    if (event.target !== this.dialog.nativeElement) return;
    const r = this.dialog.nativeElement.getBoundingClientRect();
    if (
      event.clientX < r.left ||
      event.clientX > r.right ||
      event.clientY < r.top ||
      event.clientY > r.bottom
    )
      this.requestClose();
  }
  @HostListener("document:keydown", ["$event"]) onTab(
    event: KeyboardEvent,
  ): void {
    if (event.key !== "Tab" || !this.dialog.nativeElement.open) return;
    const elements = [
      ...this.dialog.nativeElement.querySelectorAll<HTMLElement>(
        "button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),a[href],summary",
      ),
    ].filter((x) => x.getClientRects().length && !x.closest("[inert]"));
    const first = elements[0],
      last = elements.at(-1);
    if (!first) return;
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last?.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
  ngOnDestroy(): void {
    if (this.dialog.nativeElement.open) this.dialog.nativeElement.close();
    queueMicrotask(() => {
      if (this.trigger?.isConnected && this.trigger.getClientRects().length)
        this.trigger.focus();
      else document.querySelector<HTMLElement>("[data-page-heading]")?.focus();
    });
  }
}
