import {
  Component,
  DestroyRef,
  ElementRef,
  OnDestroy,
  OnInit,
  ViewChild,
  inject,
  signal,
} from "@angular/core";
import { takeUntilDestroyed } from "@angular/core/rxjs-interop";
import { FormsModule } from "@angular/forms";
import { ActivatedRoute } from "@angular/router";
import { distinctUntilChanged, map } from "rxjs";
import { GatewayApi } from "../../core/api.service";
import { ModalComponent } from "../../core/modal.component";
import { LoadMoreDirective } from "../../core/load-more.directive";
import { PagedList } from "../../core/paged-list";
import { Hospital, HospitalUsage } from "../../core/models";
import { count, thaiDate, thaiTime } from "../../core/format";
import { HospitalAttachmentsComponent } from "./hospital-attachments.component";
import {
  HospitalDraft,
  HospitalFieldErrors,
  changesHospitalConfiguration,
  draftForHospital,
  emptyHospitalDraft,
  hospitalRequestBody,
  hospitalStatusFilter,
  renewalRequestBody,
  validateHospitalDraft,
  validateRenewalDate,
} from "./hospital-form";

type EditorMode = "add" | "edit" | "renew";
type HospitalAction = "test" | "approve" | "enable";

@Component({
  selector: "gw-hospitals-page",
  standalone: true,
  imports: [
    FormsModule,
    ModalComponent,
    LoadMoreDirective,
    HospitalAttachmentsComponent,
  ],
  templateUrl: "./hospitals.page.html",
  styles: [
    `
      :host {
        display: block;
      }
      .hospital-identity strong {
        display: block;
      }
      .hospital-identity small {
        display: block;
        margin-top: 4px;
      }
      .hospital-row-actions {
        display: flex;
        gap: 6px;
        flex-wrap: wrap;
        justify-content: flex-end;
        min-width: 210px;
      }
      .hospital-dates {
        display: block;
        margin-top: 7px;
        font-size: 12px;
      }
      .hospital-toolbar {
        margin: 22px 0 16px;
      }
      .hospital-toolbar .field {
        flex: 1;
        min-width: 180px;
      }
      .hospital-toolbar .field:last-child {
        flex: 0 1 210px;
      }
      .hospital-usage strong,
      .hospital-usage small {
        display: block;
      }
      .hospital-usage small {
        margin-top: 4px;
      }
      .hospital-status {
        display: flex;
        gap: 5px;
        flex-direction: column;
        align-items: flex-start;
      }
      .hospital-expiry-note {
        color: var(--warning);
      }
      .hospital-policy {
        margin-top: 18px;
      }
      .hospital-count {
        margin: 14px 0;
      }
      .hospital-confirm {
        padding: 16px;
        border: 1px solid var(--border);
        border-radius: 10px;
        background: var(--soft);
        margin-top: 18px;
      }
      .hospital-confirm label {
        display: flex;
        gap: 10px;
        align-items: flex-start;
      }
      .hospital-confirm input {
        width: 18px;
        height: 18px;
        margin-top: 3px;
        flex-shrink: 0;
      }
      .hospital-confirm p {
        margin: 0 0 12px;
      }
      .hospital-renew-context {
        padding-bottom: 16px;
        margin-bottom: 18px;
        border-bottom: 1px solid var(--border);
      }
      @media (max-width: 640px) {
        .hospital-toolbar {
          align-items: stretch;
        }
        .hospital-toolbar .field,
        .hospital-toolbar .field:last-child {
          flex-basis: 100%;
        }
        .hospital-row-actions {
          justify-content: flex-start;
        }
        .hospital-toolbar button {
          align-self: flex-start;
        }
      }
    `,
  ],
})
export class HospitalsPage implements OnInit, OnDestroy {
  private readonly api = inject(GatewayApi);
  private readonly route = inject(ActivatedRoute);
  private readonly destroyRef = inject(DestroyRef);
  readonly list = new PagedList<Hospital>(this.api, "hospitals", "hospitals");
  readonly editor = signal<EditorMode | null>(null);
  readonly saving = signal(false);
  readonly formError = signal("");
  readonly errors = signal<HospitalFieldErrors>({});
  readonly notice = signal("");
  readonly noticeError = signal(false);
  readonly busyId = signal<string | null>(null);
  readonly suspending = signal<Hospital | null>(null);
  readonly suspendError = signal("");
  readonly suspendBusy = signal(false);
  readonly attachmentHospital = signal<Hospital | null>(null);
  readonly thaiDate = thaiDate;
  readonly thaiTime = thaiTime;
  readonly count = count;
  search = "";
  status = "all";
  draft: HospitalDraft = emptyHospitalDraft();
  editing: Hospital | null = null;
  confirmedConfiguration = false;
  suspendReason = "";
  private originalDraft = "";
  private filterTimer?: ReturnType<typeof setTimeout>;
  private destroyed = false;
  @ViewChild("hospitalForm", { read: ElementRef })
  private hospitalForm?: ElementRef<HTMLFormElement>;

  ngOnInit(): void {
    this.route.queryParamMap
      .pipe(
        map((params) => hospitalStatusFilter(params.get("status"))),
        distinctUntilChanged(),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((status) => {
        this.status = status;
        this.reload();
      });
  }

  ngOnDestroy(): void {
    this.destroyed = true;
    clearTimeout(this.filterTimer);
    this.list.destroy();
    this.draft.gateway_key = "";
  }

  reload(): void {
    clearTimeout(this.filterTimer);
    this.list.reset({ q: this.search.trim(), status: this.status });
  }

  searchChanged(): void {
    clearTimeout(this.filterTimer);
    this.filterTimer = setTimeout(() => this.reload(), 300);
  }

  openEditor(mode: EditorMode, hospital?: Hospital): void {
    this.editing = hospital || null;
    this.draft = hospital ? draftForHospital(hospital) : emptyHospitalDraft();
    this.originalDraft = JSON.stringify(this.draft);
    this.confirmedConfiguration = false;
    this.errors.set({});
    this.formError.set("");
    this.editor.set(mode);
  }

  editorTitle(): string {
    return this.editor() === "add"
      ? "เพิ่มหน่วยงาน"
      : this.editor() === "renew"
        ? "ต่ออายุการใช้งาน"
        : "แก้ไขหน่วยงาน";
  }

  isDirty(): boolean {
    return JSON.stringify(this.draft) !== this.originalDraft;
  }

  configurationChanged(): boolean {
    return (
      this.editor() === "edit" &&
      !!this.editing &&
      changesHospitalConfiguration(this.draft, this.editing)
    );
  }

  closeEditor(): void {
    if (this.saving()) return;
    this.draft.gateway_key = "";
    this.editor.set(null);
    this.editing = null;
    this.formError.set("");
  }

  async saveHospital(): Promise<void> {
    if (this.saving() || !this.editor()) return;
    this.formError.set("");
    const mode = this.editor();
    const errors =
      mode === "renew" && this.editing
        ? validateRenewalDate(this.editing, this.draft.expiry_date)
        : validateHospitalDraft(this.draft, mode === "add");
    this.errors.set(errors);
    if (Object.keys(errors).length) {
      this.formError.set("ตรวจข้อมูลที่ระบุก่อนบันทึก");
      setTimeout(() => {
        if (!this.destroyed)
          this.hospitalForm?.nativeElement
            .querySelector<HTMLInputElement>('[aria-invalid="true"]')
            ?.focus();
      });
      return;
    }
    if (this.configurationChanged() && !this.confirmedConfiguration) {
      this.formError.set(
        "ยืนยันการตั้งสถานะรออนุมัติใหม่ก่อนบันทึกข้อมูลที่เปลี่ยน",
      );
      return;
    }
    this.saving.set(true);
    try {
      const body =
        mode === "renew" && this.editing
          ? renewalRequestBody(this.editing, this.draft.expiry_date)
          : hospitalRequestBody(this.draft);
      if (mode === "add") await this.api.post<Hospital>("hospitals", body);
      else
        await this.api.put<Hospital>(
          `hospitals/${encodeURIComponent(this.editing!.id)}`,
          body,
        );
      if (this.destroyed) return;
      this.noticeError.set(false);
      this.notice.set(
        mode === "renew"
          ? "บันทึกวันหมดอายุแล้ว สถานะเปิดใช้งานยังอยู่ภายใต้การดูแลของคุณ"
          : mode === "add"
            ? "เพิ่มหน่วยงานแล้ว ทดสอบการเชื่อมต่อก่อนอนุมัติและเปิดใช้งาน"
            : this.configurationChanged()
              ? "บันทึกแล้ว กรุณาทดสอบและอนุมัติหน่วยงานใหม่อีกครั้ง"
              : "บันทึกข้อมูลหน่วยงานแล้ว",
      );
      this.draft.gateway_key = "";
      this.editor.set(null);
      this.editing = null;
      this.reload();
    } catch (error) {
      if (!this.destroyed) this.formError.set(this.message(error));
    } finally {
      if (!this.destroyed) this.saving.set(false);
    }
  }

  async action(hospital: Hospital, action: HospitalAction): Promise<void> {
    if (this.busyId()) return;
    this.busyId.set(hospital.id);
    this.notice.set("");
    try {
      const updated = await this.api.post<Hospital>(
        `hospitals/${encodeURIComponent(hospital.id)}/${action}`,
        {},
      );
      if (this.destroyed) return;
      const offline =
        action === "test" && updated.connection_status !== "online";
      this.noticeError.set(offline);
      this.notice.set(
        offline
          ? `ยังติดต่อ ${hospital.name} ไม่ได้ ตรวจปลายทางและใบรับรองแล้วลองใหม่`
          : action === "test"
            ? `ทดสอบการเชื่อมต่อ ${hospital.name} สำเร็จ`
            : action === "approve"
              ? `อนุมัติและเปิดใช้งาน ${hospital.name} แล้ว`
              : `เปิดใช้งาน ${hospital.name} แล้ว`,
      );
      this.reload();
    } catch (error) {
      if (!this.destroyed) {
        this.noticeError.set(true);
        this.notice.set(this.message(error));
      }
    } finally {
      if (!this.destroyed) this.busyId.set(null);
    }
  }

  openSuspend(hospital: Hospital): void {
    this.suspendReason = "";
    this.suspendError.set("");
    this.suspending.set(hospital);
  }

  closeSuspend(): void {
    if (this.suspendBusy()) return;
    this.suspending.set(null);
    this.suspendReason = "";
  }

  async suspend(): Promise<void> {
    const hospital = this.suspending();
    if (!hospital || this.suspendBusy()) return;
    const reason = this.suspendReason.trim();
    if (!reason || new TextEncoder().encode(reason).length > 1000) {
      this.suspendError.set("ระบุเหตุผลการระงับไม่เกิน 1,000 ไบต์");
      return;
    }
    this.suspendBusy.set(true);
    this.suspendError.set("");
    try {
      await this.api.post(
        `hospitals/${encodeURIComponent(hospital.id)}/suspend`,
        { reason },
      );
      if (this.destroyed) return;
      this.noticeError.set(false);
      this.notice.set(
        `ระงับ ${hospital.name} แล้ว เมื่อเปิดใช้งานอีกครั้ง ผู้ใช้ต้องเข้าสู่ระบบใหม่`,
      );
      this.suspending.set(null);
      this.suspendReason = "";
      this.reload();
    } catch (error) {
      if (!this.destroyed) this.suspendError.set(this.message(error));
    } finally {
      if (!this.destroyed) this.suspendBusy.set(false);
    }
  }

  usageReady(usage: HospitalUsage | null | undefined): boolean {
    return (
      !!usage &&
      ["ready", "partial"].includes(usage.status) &&
      [usage.adoption_30d, usage.active_30d, usage.personnel].every(
        (value) => typeof value === "number" && Number.isFinite(value),
      )
    );
  }

  usageLabel(usage: HospitalUsage | null | undefined): string {
    return (
      (
        {
          partial: "ข้อมูลบางส่วน",
          stale: "ข้อมูลล่าช้า",
          unsupported: "API ยังไม่รองรับ",
          not_reported: "ยังไม่มีรายงาน",
          unavailable: "ยังรับข้อมูลไม่ได้",
        } as Record<string, string>
      )[usage?.status || ""] || "ยังไม่มีข้อมูล"
    );
  }

  expiryReminder(value: string): string {
    if (!value) return "";
    const today = new Intl.DateTimeFormat("en-CA", {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      timeZone: "Asia/Bangkok",
    }).format(new Date());
    const days = Math.round(
      (Date.parse(`${value}T12:00:00Z`) - Date.parse(`${today}T12:00:00Z`)) /
        86_400_000,
    );
    return days < 0
      ? "เลยวันที่กำหนด · รอผู้ดูแลพิจารณา"
      : days <= 30
        ? `เหลือ ${days} วัน`
        : "";
  }

  private message(error: unknown): string {
    return error instanceof Error
      ? error.message
      : "ดำเนินการไม่สำเร็จ กรุณาลองอีกครั้ง";
  }
}
