import { Component, OnDestroy, OnInit, inject, signal } from "@angular/core";
import { FormsModule } from "@angular/forms";
import { GatewayApi } from "../../core/api.service";
import { ModalComponent } from "../../core/modal.component";
import { PagedList } from "../../core/paged-list";
import { LoadMoreDirective } from "../../core/load-more.directive";
import { thaiTime, count } from "../../core/format";
import { errorMessage } from "./management.models";
import {
  BackupJob, BackupPolicy, BackupRecord, BackupStatus, RestoreInspection, RestoredWork,
  backupPhases, defaultBackupPolicy, destinationError, jobDone, migrationStates, policyError, scheduleGapMinutes,
} from "./backup.models";

type BackupModal = "policy" | "drive" | "cloudrestore" | "restore" | "send" | "receive" | "finalize" | "activate" | "cancel" | "canceljob" | "retire" | "restoredwork" | "delete";

@Component({
  selector: "gw-backups-page",
  standalone: true,
  imports: [FormsModule, ModalComponent, LoadMoreDirective],
  templateUrl: "./backups.page.html",
  styleUrl: "./backups.page.css",
})
export class BackupsPage implements OnInit, OnDestroy {
  private readonly api = inject(GatewayApi);
  readonly list = new PagedList<BackupRecord>(this.api, "backups/history", "items");
  readonly driveList = new PagedList<{ id: string; created_at: string | number }>(this.api, "backups/drive/snapshots", "items");
  readonly restoredWork = new PagedList<RestoredWork>(this.api, "backups/restored-work", "items");
  readonly selectedWork = signal<string[]>([]);
  readonly deleteTarget = signal<BackupRecord | null>(null);
  readonly cancelJobTarget = signal<BackupJob | null>(null);
  readonly data = signal<BackupStatus | null>(null);
  readonly loading = signal(false);
  readonly error = signal("");
  readonly message = signal("");
  readonly busy = signal(false);
  readonly modal = signal<BackupModal | null>(null);
  readonly modalError = signal("");
  readonly job = signal<BackupJob | null>(null);
  readonly inspected = signal<RestoreInspection | null>(null);
  readonly receiving = signal<{ code: string; expires_at: string | number; installation_id: string } | null>(null);
  readonly authorizationURL = signal("");
  readonly activationAccepted = signal(false);
  readonly repairRequired = signal(false);
  readonly formatCount = count;
  readonly phases = backupPhases;
  readonly migrationLabels = migrationStates;
  readonly weekdays = ["อาทิตย์", "จันทร์", "อังคาร", "พุธ", "พฤหัสบดี", "ศุกร์", "เสาร์"];
  policy: BackupPolicy = defaultBackupPolicy();
  clientID = "";
  clientSecret = "";
  redirectURL = location.origin + "/backup/oauth/callback";
  destinationURL = "";
  pairingCode = "";
  allowPrivate = false;
  recoverySecret = "";
  recoveryKit = "";
  restoreURL = "";
  restoreFile: File | null = null;
  sourceStopped = false;
  destinationInactive = false;
  workAction: "resume" | "cancel" = "cancel";
  workConfirmed = false;
  deleteConfirmed = false;
  deleteDestination: "local" | "drive" | "all" = "local";
  destinationActiveConfirmed = false;
  cancelJobConfirmed = false;
  private initialDraft = "";
  private destroyed = false;
  private revision = 0;
  private previousScheduleGap = 15;
  private polling = false;
  private pollTimer?: ReturnType<typeof setTimeout>;
  private readonly visibility = () => {
    clearTimeout(this.pollTimer);
    if (!document.hidden && !this.activationAccepted()) { void this.loadStatus(); this.watch(); }
  };

  ngOnInit(): void {
    document.addEventListener("visibilitychange", this.visibility);
    void this.loadStatus();
    void this.list.reset();
  }

  ngOnDestroy(): void {
    this.destroyed = true;
    this.revision++;
    clearTimeout(this.pollTimer);
    document.removeEventListener("visibilitychange", this.visibility);
    this.list.destroy();
    this.driveList.destroy();
    this.restoredWork.destroy();
    this.clearSecrets();
  }

  timestamp(value: string | number | null | undefined): string {
    if (!value) return "ยังไม่มีข้อมูล";
    return thaiTime(typeof value === "number" ? value : Date.parse(value) / 1000);
  }

  size(bytes: number | null | undefined): string {
    if (typeof bytes !== "number" || !Number.isFinite(bytes)) return "—";
    const units = ["B", "KB", "MB", "GB", "TB"];
    let n = bytes, unit = 0;
    while (n >= 1024 && unit < units.length - 1) { n /= 1024; unit++; }
    return count(n) + " " + units[unit];
  }

  statusLabel(value: string): string { return backupPhases[value] || ({ verified: "ตรวจสอบแล้ว", ready: "พร้อมดาวน์โหลด", local: "สำรองในเครื่องแล้ว", uploaded: "สำรองขึ้น Drive แล้ว" }[value]) || "กำลังดำเนินการ"; }
  migrationLabel(): string { const state = this.data()?.migration?.state || "idle"; return migrationStates[state] || "มีการย้ายเครื่องอยู่ กรุณาตรวจสอบสถานะ"; }
  activeJob(): boolean { const job = this.job(); return !!job && !jobDone(job); }
  downloadable(record: BackupRecord): boolean { return record.local_available !== false && !["failed", "capturing", "queued"].includes(record.status) && record.size > 0; }
  currentBackupAvailable(id: string): boolean { const record = this.list.items().find(item => item.id === id); return !record || this.downloadable(record); }
  healthWarning(): string {
    const status = this.data();
    if (!status?.policy.alerts_enabled) return "";
    if (status.policy.destination === "drive" && status.health?.offsite_stale)
      return status.health.message || "ข้อมูลสำรองบน Google Drive เก่ากว่าช่วงที่กำหนด กรุณาตรวจการเชื่อมต่อและงานล่าสุด";
    if (status.health?.local_stale) return "ยังไม่มีข้อมูลสำรองในเครื่องที่สำเร็จภายในช่วงเวลาที่กำหนด กดสำรองข้อมูลตอนนี้หรือตรวจงานล่าสุด";
    return status.alert || "";
  }
  downloadURL(record: BackupRecord): string { return "/admin/api/backups/" + encodeURIComponent(record.id) + "/download"; }
  nextRun(): string { return this.data()?.policy.enabled ? this.timestamp(this.data()?.next_run) : "หยุดการสำรองอัตโนมัติ"; }
  scheduleLabel(): string {
    const s = this.data()?.policy.schedule;
    if (!s) return "ยังไม่ได้ตั้งค่า";
    if (s.kind === "interval") return `ทุก ${count(s.interval_minutes)} นาที`;
    return (s.kind === "daily" ? "ทุกวัน" : (s.weekdays || []).map(day => this.weekdays[day]).join(", ")) + ` เวลา ${s.time} (${s.timezone})`;
  }

  async loadStatus(): Promise<void> {
    if (this.activationAccepted()) return;
    const revision = ++this.revision;
    this.loading.set(true);
    this.error.set("");
    try {
      const data = await this.api.get<BackupStatus>("backups");
      if (this.destroyed || revision !== this.revision) return;
      this.data.set(data);
      const latest = data.active_job || data.latest_job;
      if (latest) { this.job.set(latest); this.watch(); }
    } catch (error) {
      if (!this.destroyed && revision === this.revision) this.error.set(errorMessage(error));
    } finally {
      if (!this.destroyed && revision === this.revision) this.loading.set(false);
    }
  }

  async refresh(): Promise<void> { if (!this.activationAccepted()) await Promise.all([this.loadStatus(), this.list.reset()]); }

  private watch(): void {
    clearTimeout(this.pollTimer);
    if (!this.destroyed && !this.activationAccepted() && !document.hidden && this.activeJob() && !this.polling)
      this.pollTimer = setTimeout(() => void this.poll(), 1500);
  }

  async poll(): Promise<void> {
    const current = this.job();
    if (!current || jobDone(current) || this.polling || this.destroyed || document.hidden || this.activationAccepted()) return;
    this.polling = true;
    try {
      const job = await this.api.get<BackupJob>("backups/jobs/" + encodeURIComponent(current.id));
      if (this.destroyed || this.job()?.id !== current.id) return;
      this.job.set(job);
      if (jobDone(job)) {
        if (job.error || job.state === "failed") this.error.set(job.error || "งานไม่สำเร็จ กรุณาลองอีกครั้ง");
        else this.message.set(job.state === "cancelled" ? "ยกเลิกงานแล้ว ไฟล์สำรองที่ตรวจสอบเสร็จยังดาวน์โหลดได้ เครื่องต้นทางที่พักไว้จะไม่กลับมารับข้อมูลเอง" : "งานเสร็จแล้ว ตรวจสอบผลด้านล่างได้");
        await this.refresh();
        const transfer = this.data()?.migration;
        if (job.restore_id && job.state !== "failed" && !(transfer?.direction === "receiver" && transfer.restore_id === job.restore_id)) await this.inspect(job.restore_id);
      }
    } catch (error) {
      if (!this.destroyed) this.error.set(errorMessage(error));
    } finally {
      this.polling = false;
      this.watch();
    }
  }

  private acceptJob(response: BackupJob | { job: BackupJob }): void {
    const job = "job" in response ? response.job : response;
    if (!job?.id) throw new Error("ไม่ได้รับรหัสงาน กรุณาโหลดสถานะใหม่ก่อนลองอีกครั้ง");
    this.job.set(job);
    if (job.restore_id && jobDone(job) && job.state !== "failed") void this.inspect(job.restore_id);
    this.watch();
  }

  async backUpNow(): Promise<void> {
    if (this.busy() || this.activeJob()) return;
    this.busy.set(true); this.error.set(""); this.message.set("");
    try { const result = await this.api.post<BackupJob | { job: BackupJob }>("backups", {}); if (!this.destroyed) this.acceptJob(result); }
    catch (error) { if (!this.destroyed) this.error.set(errorMessage(error)); }
    finally { if (!this.destroyed) this.busy.set(false); }
  }

  async downloadKit(record?: BackupRecord | string): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true); this.error.set("");
    try {
      const id = typeof record === "string" ? record : record?.id;
      await this.api.download("backups/recovery-kit", id ? { backup_id: id } : {}, id ? "gateway-recovery-kit-" + id + ".json" : "gateway-recovery-kit.json");
      if (!this.destroyed) { this.message.set("ส่งชุดกู้คืนให้เบราว์เซอร์แล้ว เก็บไฟล์นี้แยกจากไฟล์สำรองและไม่เผยแพร่ให้ผู้อื่น"); await this.loadStatus(); }
    } catch (error) { if (!this.destroyed) this.error.set(errorMessage(error)); }
    finally { if (!this.destroyed) this.busy.set(false); }
  }

  open(kind: BackupModal): void {
    if (this.busy()) return;
    this.clearSecrets(); this.modalError.set(""); this.authorizationURL.set(""); this.receiving.set(null);
    this.policy = structuredClone(this.data()?.policy || defaultBackupPolicy());
    this.previousScheduleGap = scheduleGapMinutes(this.policy.schedule);
    this.redirectURL = this.data()?.drive.redirect_url || location.origin + "/backup/oauth/callback";
    this.sourceStopped = false; this.destinationInactive = false;
    this.workConfirmed = false; this.deleteConfirmed = false; this.workAction = "cancel"; this.selectedWork.set([]);
    this.destinationActiveConfirmed = false;
    this.cancelJobConfirmed = false;
    if (kind === "restore") { this.inspected.set(null); this.restoreURL = ""; }
    if (kind === "activate") this.restoreURL = "";
    this.modal.set(kind); this.initialDraft = this.draft();
    if (kind === "cloudrestore" && (this.data()?.drive.connected || this.data()?.drive.recovery_ready)) void this.driveList.reset();
    if (kind === "restoredwork") void this.restoredWork.reset();
  }

  close(): void {
    if (this.busy()) return;
    this.clearSecrets(); this.modal.set(null); this.modalError.set(""); this.receiving.set(null); this.authorizationURL.set("");
    this.initialDraft = "";
    this.deleteTarget.set(null); this.selectedWork.set([]); this.workConfirmed = false; this.deleteConfirmed = false;
    this.cancelJobTarget.set(null); this.cancelJobConfirmed = false;
  }

  private clearSecrets(): void { this.clientSecret = ""; this.clientID = ""; this.pairingCode = ""; this.recoverySecret = ""; this.recoveryKit = ""; this.restoreFile = null; this.destinationURL = ""; }
  private draft(): string { return JSON.stringify([this.policy, this.clientID, this.clientSecret, this.redirectURL, this.destinationURL, this.pairingCode, this.allowPrivate, this.recoverySecret, this.recoveryKit, this.restoreURL, this.restoreFile?.name, this.sourceStopped, this.destinationInactive, this.destinationActiveConfirmed, this.workAction, this.selectedWork(), this.workConfirmed, this.deleteConfirmed, this.deleteDestination, this.cancelJobConfirmed]); }
  dirty(): boolean { return this.draft() !== this.initialDraft; }
  toggleDay(day: number, checked: boolean): void { this.policy.schedule.weekdays = checked ? [...new Set([...this.policy.schedule.weekdays, day])].sort() : this.policy.schedule.weekdays.filter(value => value !== day); this.scheduleChanged(); }
  scheduleGap(): number { return scheduleGapMinutes(this.policy.schedule); }
  scheduleChanged(): void {
    const gap = this.scheduleGap();
    if (gap > 0) {
      if (this.policy.stale_minutes === 3 * this.previousScheduleGap) this.policy.stale_minutes = 3 * gap;
      this.previousScheduleGap = gap;
    }
  }

  private async runModal(action: () => Promise<void>): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true); this.modalError.set("");
    try { await action(); }
    catch (error) { if (!this.destroyed) this.modalError.set(errorMessage(error)); }
    finally { if (!this.destroyed) this.busy.set(false); }
  }

  async savePolicy(): Promise<void> {
    const error = policyError(this.policy, this.data()?.drive.connected === true);
    if (error) { this.modalError.set(error); return; }
    await this.runModal(async () => {
      await this.api.put("backups/policy", this.policy);
      if (this.destroyed) return;
      this.modal.set(null); this.message.set("บันทึกการสำรองอัตโนมัติแล้ว"); await this.loadStatus();
    });
  }

  async connectDrive(): Promise<void> {
    if (!this.clientID.trim() || !this.clientSecret || !this.redirectURL.trim()) { this.modalError.set("กรอก Client ID, Client secret และ Callback URL ที่ลงทะเบียนกับ Google"); return; }
    await this.runModal(async () => {
      const result = await this.api.post<{ authorization_url: string }>("backups/drive/connect", { client_id: this.clientID.trim(), client_secret: this.clientSecret, redirect_url: this.redirectURL.trim() });
      if (this.destroyed) return;
      const url = new URL(result.authorization_url);
      if (url.protocol !== "https:" || url.hostname !== "accounts.google.com") throw new Error("ที่อยู่ยืนยัน Google ไม่ถูกต้อง กรุณาลองเชื่อมต่อใหม่");
      this.clientSecret = ""; this.authorizationURL.set(url.href); this.initialDraft = this.draft();
    });
  }

  async readCloudKit(event: Event): Promise<void> {
    const file = (event.target as HTMLInputElement).files?.[0];
    if (!file) return;
    try {
      if (file.size > 64 * 1024) throw new Error("ชุดกู้คืนใหญ่เกินขอบเขต กรุณาเลือกไฟล์ชุดกู้คืนที่ Gateway ดาวน์โหลดให้");
      const value = await file.text(); JSON.parse(value);
      if (!this.destroyed) this.recoveryKit = value;
    } catch (error) { this.modalError.set(errorMessage(error)); }
    (event.target as HTMLInputElement).value = "";
  }

  async recoverDrive(): Promise<void> {
    if (!this.recoveryKit.trim() || !this.redirectURL.trim()) { this.modalError.set("เลือกชุดกู้คืนและระบุ Callback URL ที่ลงทะเบียนกับ Google"); return; }
    await this.runModal(async () => {
      const result = await this.api.post<{ authorization_url: string }>("backups/drive/recover", { recovery_kit: this.recoveryKit, redirect_url: this.redirectURL.trim() });
      if (this.destroyed) return;
      const url = new URL(result.authorization_url);
      if (url.protocol !== "https:" || url.hostname !== "accounts.google.com") throw new Error("ที่อยู่ยืนยัน Google ไม่ถูกต้อง กรุณาลองอีกครั้ง");
      this.recoveryKit = ""; this.authorizationURL.set(url.href); this.initialDraft = this.draft();
    });
  }

  async refreshDriveCopies(): Promise<void> {
    await this.loadStatus();
    if (!this.destroyed && this.modal() === "cloudrestore" && (this.data()?.drive.connected || this.data()?.drive.recovery_ready)) await this.driveList.reset();
  }

  async restoreDrive(snapshotID: string): Promise<void> {
    await this.runModal(async () => {
      const response = await this.api.post<BackupJob | { job: BackupJob }>("backups/drive/restore", { snapshot_id: snapshotID });
      if (!this.destroyed) { this.inspected.set(null); this.modal.set("restore"); this.acceptJob(response); this.initialDraft = this.draft(); }
    });
  }

  async resumeMigration(): Promise<void> {
    if (this.busy() || this.activeJob()) return;
    this.busy.set(true); this.error.set("");
    try { const result = await this.api.post<BackupJob | { job: BackupJob }>("backups/migration/resume", {}); if (!this.destroyed) this.acceptJob(result); }
    catch (error) { if (!this.destroyed) this.error.set(errorMessage(error)); }
    finally { if (!this.destroyed) this.busy.set(false); }
  }

  async disconnectDrive(): Promise<void> {
    await this.runModal(async () => { await this.api.post("backups/drive/disconnect", {}); if (!this.destroyed) { this.modal.set(null); this.message.set("ยกเลิกการเชื่อมต่อ Drive แล้ว ไฟล์สำรองเดิมยังคงอยู่"); await this.loadStatus(); } });
  }

  selectFile(event: Event): void { this.restoreFile = (event.target as HTMLInputElement).files?.[0] || null; this.inspected.set(null); }
  async readKit(event: Event): Promise<void> {
    const file = (event.target as HTMLInputElement).files?.[0];
    if (!file) return;
    try {
      if (file.size > 64 * 1024) throw new Error("ชุดกู้คืนใหญ่เกินขอบเขต กรุณาเลือกไฟล์ชุดกู้คืนที่ Gateway ดาวน์โหลดให้");
      const kit = JSON.parse(await file.text());
      const secret = kit.recovery_secret || kit.repository_password || kit.secret;
      if (typeof secret !== "string" || !secret) throw new Error("ไม่พบกุญแจในชุดกู้คืน กรุณาตรวจสอบไฟล์");
      if (!this.destroyed) this.recoverySecret = secret;
    } catch (error) { this.modalError.set(errorMessage(error)); }
    (event.target as HTMLInputElement).value = "";
  }

  async importBackup(): Promise<void> {
    if (!this.restoreFile || !this.recoverySecret.trim()) { this.modalError.set("เลือกไฟล์สำรองและใส่กุญแจกู้คืน หรือเลือกไฟล์ชุดกู้คืน"); return; }
    await this.runModal(async () => {
      const form = new FormData(); form.append("file", this.restoreFile!); form.append("recovery_secret", this.recoverySecret.trim());
      if (this.restoreURL.trim()) form.append("public_url", this.restoreURL.trim());
      const response = await this.api.post<BackupJob | { job: BackupJob }>("backups/restore", form);
      if (!this.destroyed) { this.acceptJob(response); this.recoverySecret = ""; this.restoreFile = null; this.initialDraft = this.draft(); }
    });
  }

  async inspect(id: string): Promise<void> {
    try {
      const result = await this.api.get<RestoreInspection>("backups/restore/" + encodeURIComponent(id));
      if (this.destroyed) return;
      this.inspected.set(result); this.restoreURL = this.restoreURL || result.public_url; this.modal.set("restore"); this.initialDraft = this.draft();
    } catch (error) { if (!this.destroyed) this.error.set(errorMessage(error)); }
  }

  summary(): string {
    const value = this.inspected()?.summary;
    if (!value) return "ข้อมูล Gateway พร้อมการตั้งค่าและกุญแจเดิม";
    if (typeof value === "string") return value;
    const labels: Record<string, string> = { hospitals: "หน่วยงาน", sessions: "เซสชัน Mobile", audits: "ประวัติ", notifications: "การแจ้งเตือน" };
    return Object.entries(value).map(([key, n]) => `${labels[key] || key}: ${typeof n === "number" ? count(n) : n}`).join(" · ");
  }
  requiresRepair(info: RestoreInspection): boolean { return info.repair_required === true || info.certificate_expired || info.hospital_ca_expired === true; }

  async activateRestore(): Promise<void> {
    const info = this.inspected();
    if (!info || !this.sourceStopped) { this.modalError.set("ยืนยันว่าหยุดเครื่องต้นทางแล้วก่อนเปิดใช้ข้อมูลชุดนี้"); return; }
    await this.runModal(async () => {
      const response = await this.api.post<BackupJob | { job: BackupJob }>(`backups/restore/${encodeURIComponent(info.id)}/activate`, { confirm_id: info.id, public_url: this.restoreURL.trim(), source_stopped: true });
      if (!this.destroyed) { this.repairRequired.set(this.requiresRepair(info)); this.activationAccepted.set(true); clearTimeout(this.pollTimer); this.acceptJob(response); this.modal.set(null); this.message.set(this.requiresRepair(info) ? "เริ่มกู้คืนข้อมูลแล้ว ให้ซ่อมใบรับรองด้วยกุญแจเดิมในหน้า Setup ก่อนเปิดการเชื่อมต่อ API" : "เริ่มเปิดใช้ข้อมูลที่นำเข้าแล้ว หลังเปิดใช้ให้เข้าสู่ระบบด้วยบัญชีจากไฟล์สำรอง"); }
    });
  }

  async receive(): Promise<void> {
    await this.runModal(async () => {
      const result = await this.api.post<{ code: string; expires_at: string | number; installation_id: string }>("backups/migration/receive", {});
      if (!this.destroyed) { this.receiving.set(result); await this.loadStatus(); }
    });
  }

  async send(): Promise<void> {
    const error = destinationError(this.destinationURL);
    if (error || !this.pairingCode.trim()) { this.modalError.set(error || "กรอกรหัสจับคู่จากเครื่องปลายทาง"); return; }
    await this.runModal(async () => {
      const result = await this.api.post<BackupJob | { job: BackupJob }>("backups/migration/send", { destination_url: this.destinationURL.trim(), pairing_code: this.pairingCode.trim(), allow_private: this.allowPrivate });
      if (!this.destroyed) { this.acceptJob(result); this.pairingCode = ""; this.modal.set(null); await this.loadStatus(); }
    });
  }

  async finalize(): Promise<void> {
    await this.migrationAction("finalize", {}, "กำลังพักเครื่องเดิมและเก็บข้อมูลชุดสุดท้าย เครื่องเดิมจะไม่กลับมาทำงานเอง");
  }
  async activateMigration(): Promise<void> {
    if (!this.sourceStopped) { this.modalError.set("ยืนยันว่าเครื่องต้นทางหยุดรับข้อมูลแล้ว"); return; }
    await this.migrationAction("activate", { session_id: this.data()?.migration?.session_id, source_stopped: true, ...(this.restoreURL.trim() ? { public_url: this.restoreURL.trim() } : {}) }, "เริ่มเปิดใช้งานข้อมูลที่ย้ายมาแล้ว");
  }
  async cancelMigration(): Promise<void> {
    if (!this.destinationInactive) { this.modalError.set("ตรวจสอบและยืนยันว่าเครื่องปลายทางยังไม่เปิดใช้งาน หรือหยุดเครื่องปลายทางแล้ว"); return; }
    await this.migrationAction("cancel", { destination_inactive_confirmed: true }, "ยกเลิกการย้ายตามคำยืนยันแล้ว");
  }
  async retireMigration(): Promise<void> {
    if (!this.destinationActiveConfirmed) { this.modalError.set("ยืนยันว่าเครื่องใหม่เปิดใช้งานและให้บริการได้แล้ว"); return; }
    await this.migrationAction("retire", { destination_active_confirmed: true }, "ปิดงานย้ายเครื่องเดิมแล้ว เครื่องนี้ยังคงหยุดให้บริการและจะไม่กลับมาทำงานเอง");
  }
  openCancelJob(job: BackupJob): void {
    if (this.busy() || jobDone(job) || job.phase === "cancelling") return;
    this.open("canceljob");
    this.cancelJobTarget.set({ ...job });
  }
  async cancelRunningJob(): Promise<void> {
    const target = this.cancelJobTarget();
    const current = this.job();
    if (!target || jobDone(target) || !current || current.id !== target.id || jobDone(current)) { this.modalError.set("งานนี้สิ้นสุดหรือมีงานใหม่แล้ว กรุณาตรวจสถานะล่าสุดก่อน"); return; }
    if (!this.cancelJobConfirmed) { this.modalError.set("ยืนยันว่าต้องการยกเลิกงานที่เลือกก่อน"); return; }
    await this.runModal(async () => {
      const result = await this.api.post<{ cancelling: boolean }>("backups/jobs/" + encodeURIComponent(target.id) + "/cancel", {});
      if (!result.cancelling) throw new Error("ยังไม่ได้รับการยืนยันคำขอยกเลิก กรุณาตรวจสถานะก่อนลองอีกครั้ง");
      if (this.destroyed) return;
      this.job.update(job => job?.id === target.id && !jobDone(job) ? { ...job, phase: "cancelling" } : job);
      this.modal.set(null); this.cancelJobTarget.set(null); this.cancelJobConfirmed = false;
      this.message.set("ส่งคำขอยกเลิกงานแล้ว รอให้ระบบหยุดอย่างปลอดภัย ไฟล์สำรองที่ตรวจสอบแล้วจะยังคงอยู่ และต้นทางที่พักไว้จะไม่กลับมาทำงานเอง");
      this.watch();
    });
  }
  private async migrationAction(action: string, body: unknown, message: string): Promise<void> {
    await this.runModal(async () => {
      const result = await this.api.post<BackupJob | { job: BackupJob }>("backups/migration/" + action, body);
      if (!this.destroyed) { if (action === "activate") { this.activationAccepted.set(true); clearTimeout(this.pollTimer); } if (result && ("job" in result || "id" in result)) this.acceptJob(result); this.modal.set(null); this.message.set(message); await this.loadStatus(); }
    });
  }

  workKey(work: RestoredWork): string { return work.kind + ":" + work.id; }
  selected(work: RestoredWork): boolean { return this.selectedWork().includes(this.workKey(work)); }
  workSelectable(work: RestoredWork): boolean { return !["cancelled", "canceled", "resumed", "completed", "succeeded", "sent"].includes(work.status); }
  toggleWork(work: RestoredWork, checked: boolean): void {
    const key = this.workKey(work);
    this.selectedWork.update(selected => checked ? [...new Set([...selected, key])] : selected.filter(item => item !== key));
    this.workConfirmed = false;
  }
  selectLoadedWork(): void { this.selectedWork.set(this.restoredWork.items().filter(work => this.workSelectable(work)).map(work => this.workKey(work)).slice(0, 100)); this.workConfirmed = false; }
  selectedWorkRows(): RestoredWork[] { return this.restoredWork.items().filter(work => this.selected(work)); }
  selectedWorkCanResume(): boolean { const rows = this.selectedWorkRows(); return rows.length > 0 && rows.every(work => work.kind === "notification" && work.can_resume); }
  workStatusLabel(work: RestoredWork): string {
    return ({ cancelled: "ยกเลิกแล้ว", canceled: "ยกเลิกแล้ว", resumed: "ส่งต่องานแล้ว", completed: "เสร็จแล้ว", succeeded: "เสร็จแล้ว", sent: "ส่งแล้ว" } as Record<string, string>)[work.status] || "รอตรวจสอบก่อนทำงานต่อ";
  }
  async applyRestoredWork(): Promise<void> {
    const rows = this.selectedWorkRows();
    if (!rows.length || rows.length > 100) { this.modalError.set("เลือกงานตั้งแต่ 1 ถึง 100 งานต่อครั้ง"); return; }
    if (!this.workConfirmed) { this.modalError.set("ยืนยันผลของการดำเนินการกับงานที่เลือกก่อน"); return; }
    if (this.workAction === "resume" && !this.selectedWorkCanResume()) { this.modalError.set("ส่งต่อได้เฉพาะงานแจ้งเตือนที่อนุญาต งานอัปเดตเดิมยกเลิกได้เท่านั้น"); return; }
    await this.runModal(async () => {
      let changed = 0;
      for (const kind of ["notification", "updater"] as const) {
        const group = rows.filter(work => work.kind === kind);
        if (!group.length) continue;
        const result = await this.api.post<{ changed: number }>("backups/restored-work", { kind, ids: group.map(work => work.id), action: this.workAction, confirmed: true });
        if (this.destroyed) return;
        changed += result.changed;
        const processed = new Set(group.map(work => this.workKey(work)));
        this.selectedWork.update(selected => selected.filter(key => !processed.has(key)));
        this.restoredWork.items.update(items => items.map(work => processed.has(this.workKey(work)) ? { ...work, status: this.workAction === "resume" ? "resumed" : "cancelled", can_resume: false } : work));
        this.message.set(`ดำเนินการกับงานค้างแล้ว ${count(changed)} งาน`);
      }
      this.workConfirmed = false;
      await this.restoredWork.reset();
    });
  }
  async togglePin(record: BackupRecord): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true); this.error.set("");
    try {
      await this.api.post("backups/" + encodeURIComponent(record.id) + "/pin", { pinned: !record.pinned });
      if (!this.destroyed) { this.list.items.update(items => items.map(item => item.id === record.id ? { ...item, pinned: !record.pinned } : item)); this.message.set(record.pinned ? "เลิกเก็บชุดนี้ไว้แล้ว รอบลบข้อมูลตามนโยบายจะพิจารณาชุดนี้ได้" : "เก็บชุดนี้ไว้แล้ว ระบบจะไม่ลบตามรอบอัตโนมัติ"); }
    } catch (error) { if (!this.destroyed) this.error.set(errorMessage(error)); }
    finally { if (!this.destroyed) this.busy.set(false); }
  }
  openDelete(record: BackupRecord): void { this.open("delete"); this.deleteTarget.set(record); this.deleteDestination = record.local_available === false ? "drive" : "local"; this.initialDraft = this.draft(); }
  deleteDestinationAllowed(): boolean { const record = this.deleteTarget(); return !!record && (this.deleteDestination === "local" ? record.local_available !== false : !!record.remote_snapshot_id); }
  deleteScopeLabel(): string { return this.deleteDestination === "local" ? "สำเนาในเครื่อง" : this.deleteDestination === "drive" ? "สำเนาบน Google Drive" : "สำเนาทั้งในเครื่องและ Google Drive"; }
  async deleteBackup(): Promise<void> {
    const record = this.deleteTarget();
    if (!record || !this.deleteConfirmed) { this.modalError.set("ยืนยันว่าต้องการลบข้อมูลสำรองชุดที่เลือก"); return; }
    if (!this.deleteDestinationAllowed()) { this.modalError.set("ไม่พบสำเนาที่ปลายทางที่เลือก กรุณาเลือกขอบเขตการลบใหม่"); return; }
    await this.runModal(async () => {
      const scope = this.deleteScopeLabel();
      const result = await this.api.delete<BackupRecord>("backups/" + encodeURIComponent(record.id) + "?destination=" + this.deleteDestination, this.deleteDestination === "local" ? 25000 : 10 * 60 * 1000);
      if (result.id !== record.id) throw new Error("ไม่ได้รับสถานะของชุดสำรองที่เลือก กรุณาโหลดสถานะใหม่ก่อนลองอีกครั้ง");
      if (this.destroyed) return;
      this.list.items.update(items => items.map(item => item.id === record.id ? result : item));
      this.modal.set(null); this.deleteTarget.set(null); this.message.set("ลบ" + scope + "ของชุดที่เลือกแล้ว ประวัติยังคงอยู่");
      await this.loadStatus();
    });
  }
}
