import {
  Component,
  ElementRef,
  EventEmitter,
  Input,
  OnDestroy,
  OnInit,
  Output,
  ViewChild,
  inject,
  signal,
} from "@angular/core";
import { FormsModule } from "@angular/forms";
import { GatewayApi } from "../../core/api.service";
import { ModalComponent } from "../../core/modal.component";
import { LoadMoreDirective } from "../../core/load-more.directive";
import { PagedList } from "../../core/paged-list";
import { Hospital } from "../../core/models";
import { count, thaiTime } from "../../core/format";
import { attachmentSelectionError } from "./attachment-form";

interface HospitalAttachment {
  id: string;
  hospital_id: string;
  filename: string;
  content_type: string;
  category: string;
  size: number;
  created_at: number;
}

@Component({
  selector: "gw-hospital-attachments",
  standalone: true,
  imports: [FormsModule, ModalComponent, LoadMoreDirective],
  templateUrl: "./hospital-attachments.component.html",
  styles: [
    `
      .attachment-list {
        list-style: none;
        padding: 0;
        margin: 18px 0;
      }
      .attachment-list > li {
        padding: 16px 0;
        border-bottom: 1px solid var(--border);
      }
      .attachment-list > li:last-child {
        border-bottom: 0;
      }
      .attachment-name {
        overflow-wrap: anywhere;
        display: block;
      }
      .attachment-meta {
        margin: 5px 0 10px;
      }
      .attachment-actions {
        display: flex;
        gap: 8px;
        flex-wrap: wrap;
      }
      .attachment-delete {
        margin-top: 12px;
        padding: 14px;
        border-radius: 8px;
        background: var(--soft);
      }
      .attachment-delete p {
        margin-top: 0;
      }
      .attachment-upload {
        padding-bottom: 18px;
        border-bottom: 1px solid var(--border);
      }
      .attachment-upload .field {
        margin-bottom: 12px;
      }
      .attachment-selected {
        overflow-wrap: anywhere;
      }
      .attachment-total {
        margin-top: 18px;
      }
    `,
  ],
})
export class HospitalAttachmentsComponent implements OnInit, OnDestroy {
  private readonly api = inject(GatewayApi);
  private readonly host = inject<ElementRef<HTMLElement>>(ElementRef);
  @Input({ required: true }) hospital!: Hospital;
  @Output() readonly closed = new EventEmitter<void>();
  @ViewChild("fileInput") private fileInput?: ElementRef<HTMLInputElement>;
  list!: PagedList<HospitalAttachment>;
  readonly busy = signal(false);
  readonly error = signal("");
  readonly message = signal("");
  readonly deleting = signal<HospitalAttachment | null>(null);
  readonly count = count;
  readonly thaiTime = thaiTime;
  selectedFile: File | null = null;
  category = "document";
  private destroyed = false;
  readonly categories: Record<string, string> = {
    document: "เอกสารทั่วไป",
    slip: "สลิป",
    bill: "บิล / ใบเสร็จ",
    contract: "สัญญา",
  };

  ngOnInit(): void {
    this.list = new PagedList<HospitalAttachment>(
      this.api,
      this.path(),
      "attachments",
    );
    this.list.reset();
  }

  ngOnDestroy(): void {
    this.destroyed = true;
    this.selectedFile = null;
    this.list.destroy();
  }

  path(): string {
    return `hospitals/${encodeURIComponent(this.hospital.id)}/attachments`;
  }

  downloadURL(attachment: HospitalAttachment): string {
    return `/admin/api/${this.path()}/${encodeURIComponent(attachment.id)}`;
  }

  fileChanged(event: Event): void {
    const input = event.target as HTMLInputElement;
    this.error.set("");
    this.message.set("");
    this.selectedFile = input.files?.item(0) || null;
    if (this.selectedFile) {
      const error = attachmentSelectionError(this.selectedFile);
      if (error) {
        this.error.set(error);
        this.selectedFile = null;
        input.value = "";
      }
    }
  }

  size(value: number): string {
    return value < 1024 * 1024
      ? `${count(Math.max(1, Math.ceil(value / 1024)))} KB`
      : `${count(value / (1024 * 1024))} MB`;
  }

  async upload(): Promise<void> {
    if (this.busy() || !this.selectedFile || this.list.loading()) return;
    const selectionError = attachmentSelectionError(this.selectedFile);
    if (selectionError) {
      this.error.set(selectionError);
      return;
    }
    this.busy.set(true);
    this.error.set("");
    this.message.set("");
    const form = new FormData();
    form.append("file", this.selectedFile);
    form.append("category", this.category);
    try {
      await this.api.post<HospitalAttachment>(this.path(), form);
      if (this.destroyed) return;
      this.selectedFile = null;
      if (this.fileInput) this.fileInput.nativeElement.value = "";
      this.message.set("แนบเอกสารแล้ว");
      this.list.reset();
    } catch (error) {
      if (!this.destroyed) this.error.set(this.errorMessage(error));
    } finally {
      if (!this.destroyed) this.busy.set(false);
    }
  }

  askDelete(attachment: HospitalAttachment): void {
    if (this.busy()) return;
    this.deleting.set(attachment);
    this.error.set("");
    setTimeout(() => {
      if (!this.destroyed)
        this.host.nativeElement
          .querySelector<HTMLButtonElement>("[data-delete-confirm]")
          ?.focus();
    });
  }

  async remove(): Promise<void> {
    const attachment = this.deleting();
    if (!attachment || this.busy()) return;
    this.busy.set(true);
    this.error.set("");
    this.message.set("");
    try {
      await this.api.delete(
        `${this.path()}/${encodeURIComponent(attachment.id)}`,
      );
      if (this.destroyed) return;
      this.deleting.set(null);
      this.message.set(`ลบ ${attachment.filename} แล้ว`);
      this.list.reset();
    } catch (error) {
      if (!this.destroyed) this.error.set(this.errorMessage(error));
    } finally {
      if (!this.destroyed) this.busy.set(false);
    }
  }

  private errorMessage(error: unknown): string {
    return error instanceof Error
      ? error.message
      : "ดำเนินการไม่สำเร็จ กรุณาลองอีกครั้ง";
  }
}
