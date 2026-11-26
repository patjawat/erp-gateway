import "@angular/compiler";
import { Injector, runInInjectionContext, signal } from "@angular/core";
import { afterEach, describe, expect, it, vi } from "vitest";
import { GatewayApi } from "../../core/api.service";
import { BackupsPage } from "./backups.page";
import { BackupStatus, RestoredWork, defaultBackupPolicy, destinationError, policyError, scheduleGapMinutes } from "./backup.models";

const status = (): BackupStatus => ({ policy: defaultBackupPolicy(), drive: { connected: false }, tools_available: true, recovery_exported: false, migration: { state: "idle" } });
const instances: BackupsPage[] = [];
function setup() {
  const api = { authenticated: signal(true), get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn(), download: vi.fn(), expire: vi.fn() };
  const injector = Injector.create({ providers: [{ provide: GatewayApi, useValue: api }] });
  const page = runInInjectionContext(injector, () => new BackupsPage());
  page.data.set(status());
  instances.push(page);
  return { api, page };
}
afterEach(() => { instances.splice(0).forEach(page => page.ngOnDestroy()); vi.restoreAllMocks(); });

describe("Gateway complete backup and migration", () => {
  it("starts manual backup while Drive and automatic backup are disabled, without duplicate submissions", async () => {
    const { api, page } = setup();
    let finish!: (value: unknown) => void;
    api.post.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    const first = page.backUpNow();
    await page.backUpNow();
    expect(api.post).toHaveBeenCalledOnce();
    expect(api.post).toHaveBeenCalledWith("backups", {});
    finish({ job: { id: "capture-1", state: "running", phase: "capturing" } });
    await first;
    expect(page.job()?.id).toBe("capture-1");
    await page.backUpNow();
    expect(api.post).toHaveBeenCalledOnce();
  });

  it("loads only the first bounded history page, supports an explicit next page, and deduplicates IDs", async () => {
    const { api, page } = setup();
    api.get.mockImplementation((path: string) => Promise.resolve(path === "backups" ? status() : { items: [{ id: "backup-1", size: 10, status: "completed" }], total: 52, next_cursor: "next" }));
    page.ngOnInit();
    await vi.waitFor(() => expect(page.list.items()).toHaveLength(1));
    expect(api.get.mock.calls.filter(([path]) => path === "backups/history")).toHaveLength(1);
    expect(api.get).toHaveBeenCalledWith("backups/history", { limit: 25 });
    api.get.mockResolvedValue({ items: [{ id: "backup-1", size: 11 }, { id: "backup-2", size: 20 }], total: 52, next_cursor: null });
    await page.list.more();
    expect(api.get).toHaveBeenLastCalledWith("backups/history", { limit: 25, cursor: "next" });
    expect(page.list.items()).toHaveLength(2);
    expect(page.list.hasMore()).toBe(false);
  });

  it("preserves saved policy and the edit draft when saving fails", async () => {
    const { api, page } = setup();
    page.open("policy");
    page.policy.schedule.interval_minutes = 30;
    api.put.mockRejectedValue(new Error("มีการแก้ไขนโยบายจากหน้าอื่น กรุณาโหลดใหม่"));
    await page.savePolicy();
    expect(page.data()?.policy.schedule.interval_minutes).toBe(15);
    expect(page.policy.schedule.interval_minutes).toBe(30);
    expect(page.modal()).toBe("policy");
    expect(page.modalError()).toContain("โหลดใหม่");
  });

  it("rejects Drive destination until connected, empty retention and invalid weekdays", () => {
    const policy = defaultBackupPolicy();
    policy.destination = "drive";
    expect(policyError(policy, false)).toContain("เชื่อมต่อ Google Drive");
    policy.destination = "local";
    policy.schedule.kind = "weekly"; policy.schedule.weekdays = [];
    expect(policyError(policy, false)).toContain("เลือกวันสำรอง");
    policy.schedule.weekdays = [1];
    policy.local_retention.latest = 0;
    expect(policyError(policy, false)).toContain("อย่างน้อยหนึ่งชุด");
    policy.local_retention.keep_all = true;
    policy.stale_minutes = 30240;
    expect(policyError(policy, false)).toBe("");
  });

  it("keeps the alert target aligned with interval, daily and the longest weekly gap", () => {
    const { page } = setup();
    page.open("policy");
    page.policy.schedule.kind = "daily"; page.scheduleChanged();
    expect(page.policy.stale_minutes).toBe(4320);
    page.policy.schedule.kind = "weekly"; page.policy.schedule.weekdays = [1, 2, 3, 4, 5]; page.scheduleChanged();
    expect(scheduleGapMinutes(page.policy.schedule)).toBe(4320);
    expect(page.policy.stale_minutes).toBe(12960);
    page.policy.stale_minutes = 7000;
    page.policy.schedule.weekdays = [1]; page.scheduleChanged();
    expect(page.policy.stale_minutes).toBe(7000);
    expect(policyError(page.policy, false)).toContain("10080");
    page.policy.stale_minutes = 30240;
    expect(policyError(page.policy, false)).toBe("");
    page.policy.schedule.kind = "interval"; page.policy.schedule.interval_minutes = 10081;
    expect(policyError(page.policy, false)).toContain("7 วัน");
  });

  it.each(["http://10.10.8.127:8080", "https://user:secret@new.example.org", "https://new.example.org/path", "https://new.example.org?x=1", "https://new.example.org#x"])("rejects an unsuitable migration URL %s", value => {
    expect(destinationError(value)).not.toBe("");
  });

  it("sends a scoped pairing request and clears the one-time code only after success", async () => {
    const { api, page } = setup();
    page.open("send"); page.destinationURL = "https://10.10.8.127:8443"; page.pairingCode = "pair-only"; page.allowPrivate = true;
    api.post.mockRejectedValueOnce(new Error("ตรวจใบรับรองไม่ผ่าน"));
    await page.send();
    expect(page.pairingCode).toBe("pair-only");
    expect(page.modalError()).toContain("ใบรับรอง");
    api.post.mockResolvedValue({ job: { id: "send-1", state: "running" } });
    api.get.mockResolvedValue(status());
    await page.send();
    expect(api.post).toHaveBeenLastCalledWith("backups/migration/send", { destination_url: "https://10.10.8.127:8443", pairing_code: "pair-only", allow_private: true });
    expect(page.pairingCode).toBe("");
    expect(page.modal()).toBeNull();
  });

  it("stages an uploaded file without activation, and requires source-stop confirmation to replace data", async () => {
    const { api, page } = setup();
    page.open("restore"); page.restoreFile = new File(["encrypted repository"], "gateway-backup.tar"); page.recoverySecret = "recovery-only";
    api.post.mockResolvedValue({ job: { id: "inspect-1", state: "running" } });
    await page.importBackup();
    const [path, form] = api.post.mock.calls[0];
    expect(path).toBe("backups/restore");
    expect(form.get("file")).toBeInstanceOf(File);
    expect(form.get("recovery_secret")).toBe("recovery-only");
    expect(page.recoverySecret).toBe("");
    page.inspected.set({ id: "restore-1", created_at: "2026-11-26T12:00:00Z", public_url: "https://old.example.org", ready: true, certificate_expired: true });
    await page.activateRestore();
    expect(api.post).toHaveBeenCalledOnce();
    expect(page.modalError()).toContain("หยุดเครื่องต้นทาง");
    page.sourceStopped = true; page.restoreURL = "https://new.example.org";
    api.post.mockResolvedValue({ job: { id: "activate-1", state: "running" } });
    await page.activateRestore();
    expect(api.post).toHaveBeenLastCalledWith("backups/restore/restore-1/activate", { confirm_id: "restore-1", public_url: "https://new.example.org", source_stopped: true });
  });

  it("uses native streaming links for backup files and a separate small recovery-kit download", async () => {
    const { api, page } = setup();
    expect(page.downloadURL({ id: "a/b", created_at: null, status: "completed", size: 10 })).toBe("/admin/api/backups/a%2Fb/download");
    expect(api.download).not.toHaveBeenCalled();
    api.download.mockResolvedValue(undefined); api.get.mockResolvedValue(status());
    await page.downloadKit();
    expect(api.download).toHaveBeenCalledWith("backups/recovery-kit", {}, "gateway-recovery-kit.json");
    await page.downloadKit({ id: "old-key-backup", created_at: null, status: "completed", size: 10 });
    expect(api.download).toHaveBeenLastCalledWith("backups/recovery-kit", { backup_id: "old-key-backup" }, "gateway-recovery-kit-old-key-backup.json");
  });

  it("does not open the ordinary restore inspector for a prepared transfer base", async () => {
    const { api, page } = setup();
    page.job.set({ id: "receiver-verify", state: "running" });
    vi.spyOn(document, "hidden", "get").mockReturnValue(false);
    api.get.mockImplementation((path: string) => Promise.resolve(path === "backups/jobs/receiver-verify" ? { id: "receiver-verify", state: "completed", restore_id: "base-inspection" } : path === "backups" ? { ...status(), migration: { direction: "receiver", state: "prepared", restore_id: "base-inspection" } } : { items: [], total: 0, next_cursor: null }));
    await page.poll();
    expect(page.modal()).toBeNull();
    expect(api.get.mock.calls.some(([path]) => path === "backups/restore/base-inspection")).toBe(false);
  });

  it("does not poll a hidden page or after destruction", async () => {
    const { api, page } = setup();
    page.job.set({ id: "job-1", state: "running" });
    vi.spyOn(document, "hidden", "get").mockReturnValue(true);
    await page.poll();
    expect(api.get).not.toHaveBeenCalled();
    page.ngOnDestroy();
    await page.poll();
    expect(api.get).not.toHaveBeenCalled();
  });

  it("ignores a stale status response after a newer request", async () => {
    const { api, page } = setup();
    let finish!: (value: BackupStatus) => void;
    api.get.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
    const first = page.loadStatus();
    api.get.mockResolvedValue({ ...status(), recovery_exported: true });
    await page.loadStatus();
    finish(status()); await first;
    expect(page.data()?.recovery_exported).toBe(true);
  });

  it("shows the last persisted failed job after a page reload without starting a poll loop", async () => {
    const { api, page } = setup();
    api.get.mockResolvedValue({ ...status(), latest_job: { id: "failed-1", state: "failed", error: "พื้นที่ว่างไม่พอ" } });
    await page.loadStatus();
    expect(page.job()?.error).toBe("พื้นที่ว่างไม่พอ");
    expect(page.activeJob()).toBe(false);
    await page.poll();
    expect(api.get).toHaveBeenCalledOnce();
  });

  it("resumes a stored transfer without submitting the pairing code again", async () => {
    const { api, page } = setup();
    api.post.mockResolvedValue({ job: { id: "retry-1", state: "running" } });
    await page.resumeMigration();
    expect(api.post).toHaveBeenCalledWith("backups/migration/resume", {});
    expect(page.job()?.id).toBe("retry-1");
  });

  it("recovers Drive credentials from the offline kit and clears it after the consent URL is prepared", async () => {
    const { api, page } = setup();
    page.open("cloudrestore"); page.recoveryKit = '{"recovery_secret":"fixture-only"}';
    page.redirectURL = "https://new.example.org/backup/oauth/callback";
    api.post.mockResolvedValue({ authorization_url: "https://accounts.google.com/o/oauth2/v2/auth?state=fixture" });
    await page.recoverDrive();
    expect(api.post).toHaveBeenCalledWith("backups/drive/recover", { recovery_kit: '{"recovery_secret":"fixture-only"}', redirect_url: page.redirectURL });
    expect(page.recoveryKit).toBe("");
    expect(page.authorizationURL()).toContain("accounts.google.com");
  });

  it("paginates Drive snapshots only while its recovery modal is open", async () => {
    const { api, page } = setup();
    page.data.set({ ...status(), drive: { connected: true } });
    api.get.mockResolvedValue({ items: [{ id: "remote-1", created_at: "2026-11-26T12:00:00Z" }], next_cursor: "next" });
    page.open("cloudrestore");
    await vi.waitFor(() => expect(page.driveList.items()).toHaveLength(1));
    expect(api.get).toHaveBeenCalledOnce();
    expect(api.get).toHaveBeenCalledWith("backups/drive/snapshots", { limit: 25 });
    page.close();
    expect(page.recoveryKit).toBe("");
  });

  it("keeps a verified local download and its matching kit available during Drive upload", async () => {
    const { api, page } = setup();
    page.job.set({ id: "upload-1", state: "running", phase: "uploading", backup_id: "ready-local" });
    const record = { id: "ready-local", status: "completed", size: 10, local_available: true, created_at: null };
    expect(page.activeJob()).toBe(true);
    expect(page.downloadable(record)).toBe(true);
    api.get.mockResolvedValue(status()); api.download.mockResolvedValue(undefined);
    await page.downloadKit("ready-local");
    expect(api.download).toHaveBeenCalledWith("backups/recovery-kit", { backup_id: "ready-local" }, "gateway-recovery-kit-ready-local.json");
    await page.backUpNow();
    expect(api.post).not.toHaveBeenCalled();
  });

  const notification: RestoredWork = { id: "notification:1", kind: "notification", status: "quarantined", created_at: 1, hospital_id: "12345", can_resume: true };
  const updater: RestoredWork = { id: "updater:old-job", kind: "updater", status: "blocked", created_at: 1, hospital_id: "12345", can_resume: false };

  it("loads only the first restored-work page when its modal opens", async () => {
    const { api, page } = setup();
    api.get.mockImplementation((path: string) => Promise.resolve(path === "backups" ? status() : path === "backups/restored-work" ? { items: [notification], total: 80, next_cursor: "more-work" } : { items: [], total: 0, next_cursor: null }));
    page.ngOnInit();
    expect(api.get.mock.calls.some(([path]) => path === "backups/restored-work")).toBe(false);
    page.open("restoredwork");
    await vi.waitFor(() => expect(page.restoredWork.items()).toHaveLength(1));
    expect(api.get.mock.calls.filter(([path]) => path === "backups/restored-work")).toHaveLength(1);
    expect(api.get).toHaveBeenCalledWith("backups/restored-work", { limit: 25 });
    expect(page.restoredWork.hasMore()).toBe(true);
  });

  it("requires explicit acknowledgement before notification resume and never resumes updater jobs", async () => {
    const { api, page } = setup();
    page.restoredWork.items.set([notification, updater]);
    page.toggleWork(notification, true); page.workAction = "resume";
    await page.applyRestoredWork();
    expect(api.post).not.toHaveBeenCalled();
    page.toggleWork(updater, true); page.workConfirmed = true;
    await page.applyRestoredWork();
    expect(api.post).not.toHaveBeenCalled();
    expect(page.modalError()).toContain("ยกเลิกได้เท่านั้น");
    page.toggleWork(updater, false); page.workConfirmed = true;
    api.post.mockResolvedValue({ changed: 1 });
    api.get.mockResolvedValue({ items: [], total: 0, next_cursor: null });
    await page.applyRestoredWork();
    expect(api.post).toHaveBeenCalledWith("backups/restored-work", { kind: "notification", ids: ["notification:1"], action: "resume", confirmed: true });
    expect(page.selectedWork()).toHaveLength(0);
  });

  it("preserves only unprocessed selected jobs when a grouped cancellation partially fails", async () => {
    const { api, page } = setup();
    page.restoredWork.items.set([notification, updater]);
    page.toggleWork(notification, true); page.toggleWork(updater, true); page.workConfirmed = true;
    api.post.mockImplementation((_path, body) => body.kind === "notification" ? Promise.resolve({ changed: 1 }) : Promise.reject(new Error("บันทึกงานอัปเดตไม่สำเร็จ")));
    await page.applyRestoredWork();
    expect(page.selectedWork()).toEqual([page.workKey(updater)]);
    expect(page.restoredWork.items()[0].status).toBe("cancelled");
    expect(page.restoredWork.items()[1].status).toBe("blocked");
    expect(page.modalError()).toContain("ไม่สำเร็จ");
  });

  it("updates a pin in place and preserves the loaded history after a rejected change", async () => {
    const { api, page } = setup();
    const record = { id: "backup-1", status: "completed", size: 10, created_at: null, pinned: false };
    page.list.items.set([record]);
    api.post.mockResolvedValue({ pinned: true });
    await page.togglePin(record);
    expect(api.post).toHaveBeenCalledWith("backups/backup-1/pin", { pinned: true });
    expect(page.list.items()[0].pinned).toBe(true);
    api.post.mockRejectedValue(new Error("ยังใช้ชุดนี้ย้ายเครื่องอยู่"));
    await page.togglePin(page.list.items()[0]);
    expect(page.list.items()[0].pinned).toBe(true);
    expect(api.get).not.toHaveBeenCalled();
  });

  it("requires delete confirmation and leaves protected snapshots visible after server rejection", async () => {
    const { api, page } = setup();
    const record = { id: "backup-1", status: "completed", size: 10, created_at: null };
    page.list.items.set([record]); page.list.total.set(1); page.openDelete(record);
    await page.deleteBackup();
    expect(api.delete).not.toHaveBeenCalled();
    page.deleteConfirmed = true; api.delete.mockRejectedValue(new Error("ต้องเก็บสำเนาที่ตรวจสอบแล้วอย่างน้อยหนึ่งชุด"));
    await page.deleteBackup();
    expect(page.list.items()).toHaveLength(1);
    expect(page.modal()).toBe("delete");
    api.delete.mockResolvedValue({ ...record, local_available: false }); api.get.mockResolvedValue(status());
    await page.deleteBackup();
    expect(api.delete).toHaveBeenLastCalledWith("backups/backup-1?destination=local", 25000);
    expect(page.list.items()).toHaveLength(1);
    expect(page.list.items()[0].local_available).toBe(false);
    expect(page.list.total()).toBe(1);
  });

  it("deletes only the explicitly selected destination and preserves the surviving copy in history", async () => {
    const { api, page } = setup();
    const record = { id: "backup-both", status: "completed", size: 10, created_at: null, local_available: true, remote_snapshot_id: "remote-snapshot", offsite_at: 1 };
    page.list.items.set([record]); page.list.total.set(1); page.openDelete(record);
    expect(page.deleteDestination).toBe("local");
    page.deleteDestination = "drive"; page.deleteConfirmed = true;
    api.delete.mockResolvedValue({ ...record, remote_snapshot_id: "" }); api.get.mockResolvedValue(status());
    await page.deleteBackup();
    expect(api.delete).toHaveBeenLastCalledWith("backups/backup-both?destination=drive", 600000);
    expect(page.list.items()[0].local_available).toBe(true);
    expect(page.list.items()[0].remote_snapshot_id).toBe("");
    expect(page.list.total()).toBe(1);
    page.openDelete({ ...record, local_available: false });
    expect(page.deleteDestination).toBe("drive");
    page.deleteDestination = "all"; page.deleteConfirmed = true;
    api.delete.mockResolvedValue({ ...record, local_available: false, remote_snapshot_id: "" });
    await page.deleteBackup();
    expect(api.delete).toHaveBeenLastCalledWith("backups/backup-both?destination=all", 600000);
  });

  it("rejects deletion scopes whose copy does not exist", async () => {
    const { api, page } = setup();
    page.openDelete({ id: "local-only", status: "completed", size: 10, created_at: null, local_available: true });
    page.deleteDestination = "drive"; page.deleteConfirmed = true;
    expect(page.deleteDestinationAllowed()).toBe(false);
    await page.deleteBackup();
    expect(api.delete).not.toHaveBeenCalled();
    page.openDelete({ id: "remote-only", status: "completed", size: 10, created_at: null, local_available: false, remote_snapshot_id: "remote" });
    page.deleteDestination = "local"; page.deleteConfirmed = true;
    expect(page.deleteDestinationAllowed()).toBe(false);
    await page.deleteBackup();
    expect(api.delete).not.toHaveBeenCalled();
  });

  it("retires the old source only after confirming the destination works, without resuming the source", async () => {
    const { api, page } = setup();
    page.open("retire");
    await page.retireMigration();
    expect(api.post).not.toHaveBeenCalled();
    page.destinationActiveConfirmed = true;
    api.post.mockResolvedValue({ retired: true }); api.get.mockResolvedValue(status());
    await page.retireMigration();
    expect(api.post).toHaveBeenCalledExactlyOnceWith("backups/migration/retire", { destination_active_confirmed: true });
    expect(page.message()).toContain("จะไม่กลับมาทำงานเอง");
  });

  it("cancels only the confirmed selected running job and keeps its verified local file available", async () => {
    const { api, page } = setup();
    const job = { id: "selected-job", state: "running", phase: "uploading", backup_id: "verified-local" };
    page.job.set(job); page.openCancelJob(job);
    await page.cancelRunningJob();
    expect(api.post).not.toHaveBeenCalled();
    page.cancelJobConfirmed = true; api.post.mockResolvedValue({ cancelling: true });
    await page.cancelRunningJob();
    expect(api.post).toHaveBeenCalledExactlyOnceWith("backups/jobs/selected-job/cancel", {});
    expect(page.job()?.phase).toBe("cancelling");
    expect(page.job()?.backup_id).toBe("verified-local");
    expect(page.currentBackupAvailable("verified-local")).toBe(true);
    expect(page.modal()).toBeNull();
    expect(page.message()).toContain("จะไม่กลับมาทำงานเอง");
  });

  it("does not cancel terminal jobs or replace the selected job ID with a newer running job", async () => {
    const { api, page } = setup();
    for (const state of ["completed", "failed", "cancelled"]) {
      const terminal = { id: "finished-job", state };
      page.job.set(terminal); page.openCancelJob(terminal);
      expect(page.modal()).toBeNull();
      page.cancelJobTarget.set(terminal); page.cancelJobConfirmed = true;
      await page.cancelRunningJob();
    }
    const old = { id: "old-job", state: "running" };
    page.job.set(old); page.openCancelJob(old); page.cancelJobConfirmed = true;
    page.job.set({ id: "new-job", state: "running" });
    await page.cancelRunningJob();
    expect(api.post).not.toHaveBeenCalled();
    expect(page.modalError()).toContain("มีงานใหม่แล้ว");
  });

  it("keeps cancellation confirmation and current job state after the server rejects a cancel", async () => {
    const { api, page } = setup();
    const job = { id: "job-1", state: "running", phase: "verifying" };
    page.job.set(job); page.openCancelJob(job); page.cancelJobConfirmed = true;
    api.post.mockRejectedValue(new Error("ผ่านจุดเปิดใช้งานแล้ว ยกเลิกไม่ได้"));
    await page.cancelRunningJob();
    expect(page.job()).toEqual(job);
    expect(page.modal()).toBe("canceljob");
    expect(page.cancelJobConfirmed).toBe(true);
    expect(page.modalError()).toContain("ยกเลิกไม่ได้");
  });
});
