export type BackupTime = string | number | null;

export interface RetentionPolicy {
  latest: number;
  daily: number;
  weekly: number;
  monthly: number;
  keep_all: boolean;
}

export interface BackupPolicy {
  version: number;
  revision: number;
  enabled: boolean;
  schedule: {
    kind: "interval" | "daily" | "weekly";
    interval_minutes: number;
    time: string;
    weekdays: number[];
    timezone: string;
  };
  destination: "local" | "drive";
  local_retention: RetentionPolicy;
  drive_retention: RetentionPolicy;
  alerts_enabled: boolean;
  stale_minutes: number;
}

export interface BackupRecord {
  id: string;
  created_at: BackupTime;
  status: string;
  size: number;
  local_available?: boolean;
  snapshot_id?: string;
  offsite_at?: BackupTime;
  remote_snapshot_id?: string;
  recovery_key_id?: string;
  pinned?: boolean;
  error?: string;
}

export interface RestoredWork {
  id: string;
  kind: "notification" | "updater";
  status: string;
  created_at: BackupTime;
  hospital_id: string;
  event_id?: string;
  can_resume: boolean;
}

export interface BackupJob {
  id: string;
  state: string;
  phase?: string;
  error?: string;
  backup_id?: string;
  restore_id?: string;
}

export interface RestoreInspection {
  id: string;
  created_at: BackupTime;
  public_url: string;
  certificate_expired: boolean;
  hospital_ca_expired?: boolean;
  repair_required?: boolean;
  ready: boolean;
  summary?: string | Record<string, string | number>;
}

export interface BackupStatus {
  policy: BackupPolicy;
  latest_local?: BackupRecord | null;
  latest_offsite?: BackupRecord | null;
  active_job?: BackupJob | null;
  latest_job?: BackupJob | null;
  drive: { connected: boolean; folder_id?: string; redirect_url?: string; recovery_ready?: boolean };
  migration?: {
    state: string;
    direction?: "sender" | "receiver";
    session_id?: string;
    destination_url?: string;
    expires_at?: BackupTime;
    restore_id?: string;
    retry_available?: boolean;
  };
  tools_available: boolean;
  recovery_exported: boolean;
  recovery_key_id?: string;
  next_run?: BackupTime;
  alert?: string;
  health?: { local_stale: boolean; offsite_stale: boolean; message: string };
}

export const backupPhases: Record<string, string> = {
  queued: "รอเริ่มงาน",
  waiting: "รอให้งานก่อนหน้าเสร็จ",
  capturing: "กำลังเก็บข้อมูล Gateway",
  verifying: "กำลังตรวจสอบความถูกต้อง",
  packaging: "กำลังเตรียมไฟล์ดาวน์โหลด",
  uploading: "กำลังส่งข้อมูลขึ้น Google Drive",
  downloading: "กำลังดึงข้อมูลสำรองจาก Google Drive",
  transferring: "กำลังส่งข้อมูลไปเครื่องปลายทาง",
  receiving: "กำลังรับข้อมูลจากเครื่องต้นทาง",
  inspecting: "กำลังตรวจไฟล์นำเข้า",
  staging: "กำลังเตรียมข้อมูลบนเครื่องใหม่",
  draining: "กำลังพักเครื่องเดิมและรอให้งานปัจจุบันเสร็จ",
  finalizing: "กำลังเก็บและส่งข้อมูลชุดสุดท้าย",
  activating: "กำลังเปิดใช้งานข้อมูลที่กู้คืน",
  completed: "เสร็จแล้ว",
  succeeded: "เสร็จแล้ว",
  failed: "ไม่สำเร็จ",
  cancelled: "ยกเลิกแล้ว",
  cancelling: "กำลังยกเลิกงาน",
};

export const migrationStates: Record<string, string> = {
  idle: "ยังไม่มีการย้ายเครื่อง",
  receiving: "เปิดรับการย้ายข้อมูล",
  paired: "จับคู่เครื่องแล้ว",
  preparing: "กำลังเตรียมเครื่องปลายทาง",
  prepared: "ข้อมูลรอบแรกพร้อม รอเก็บข้อมูลล่าสุด",
  ready: "ข้อมูลรอบแรกพร้อม รอเก็บข้อมูลล่าสุด",
  frozen: "พักเครื่องเดิมแล้ว รอเปิดใช้เครื่องใหม่",
  fenced: "พักเครื่องเดิมแล้ว รอเปิดใช้เครื่องใหม่",
  final_ready: "ข้อมูลชุดสุดท้ายพร้อมเปิดใช้",
  active: "เครื่องปลายทางเปิดใช้งานแล้ว",
  retired: "ย้ายออกจากเครื่องนี้แล้ว",
  cancelled: "ยกเลิกการย้ายแล้ว",
  failed: "การย้ายยังไม่สำเร็จ กรุณาตรวจงานล่าสุด",
};

export function defaultBackupPolicy(): BackupPolicy {
  return {
    version: 1,
    revision: 1,
    enabled: false,
    schedule: {
      kind: "interval",
      interval_minutes: 15,
      time: "02:00",
      weekdays: [0],
      timezone: "Asia/Bangkok",
    },
    destination: "local",
    local_retention: { latest: 3, daily: 0, weekly: 0, monthly: 0, keep_all: false },
    drive_retention: { latest: 24, daily: 7, weekly: 4, monthly: 3, keep_all: false },
    alerts_enabled: true,
    stale_minutes: 45,
  };
}

export function jobDone(job: BackupJob): boolean {
  return ["completed", "succeeded", "failed", "cancelled"].includes(job.state);
}

export function scheduleGapMinutes(schedule: BackupPolicy["schedule"]): number {
  if (schedule.kind === "interval") return Number.isInteger(schedule.interval_minutes) && schedule.interval_minutes > 0 ? schedule.interval_minutes : 0;
  if (schedule.kind === "daily") return 1440;
  const days = [...new Set(schedule.weekdays)].filter(day => Number.isInteger(day) && day >= 0 && day <= 6).sort();
  if (!days.length) return 0;
  return Math.max(...days.map((day, index) => (index === days.length - 1 ? days[0] + 7 : days[index + 1]) - day)) * 1440;
}

export function policyError(policy: BackupPolicy, driveConnected: boolean): string {
  const schedule = policy.schedule;
  if (!schedule.timezone.trim()) return "ระบุเขตเวลาสำหรับการสำรองข้อมูล";
  try { new Intl.DateTimeFormat("th-TH", { timeZone: schedule.timezone }); }
  catch { return "เขตเวลาไม่ถูกต้อง เช่น Asia/Bangkok"; }
  if (schedule.kind === "interval" && (!Number.isInteger(schedule.interval_minutes) || schedule.interval_minutes < 1 || schedule.interval_minutes > 10080))
    return "ระบุรอบสำรองเป็นนาที ตั้งแต่ 1 ถึง 10080 นาที (7 วัน)";
  if (schedule.kind !== "interval" && !/^([01]\d|2[0-3]):[0-5]\d$/.test(schedule.time))
    return "ระบุเวลาสำรองในรูปแบบ 24 ชั่วโมง";
  if (schedule.kind === "weekly" && !schedule.weekdays.length) return "เลือกวันสำรองอย่างน้อยหนึ่งวัน";
  if (schedule.kind === "weekly" && schedule.weekdays.some(day => !Number.isInteger(day) || day < 0 || day > 6)) return "วันที่สำรองในสัปดาห์ไม่ถูกต้อง";
  if (policy.destination === "drive" && !driveConnected) return "เชื่อมต่อ Google Drive ก่อนเลือกปลายทางนี้";
  for (const retention of [policy.local_retention, policy.drive_retention]) {
    for (const [value, max] of [[retention.latest, 10000], [retention.daily, 3660], [retention.weekly, 520], [retention.monthly, 120]])
      if (!Number.isInteger(value) || value < 0 || value > max) return "จำนวนชุดสำรองต้องเป็นจำนวนเต็มในช่วงที่กำหนดของแต่ละช่อง";
    if (!retention.keep_all && ![retention.latest, retention.daily, retention.weekly, retention.monthly].some(value => value > 0))
      return "เก็บข้อมูลสำรองอย่างน้อยหนึ่งชุด หรือเลือกเก็บจนกว่าจะลบเอง";
  }
  if (!Number.isInteger(policy.stale_minutes) || policy.stale_minutes < 1 || policy.stale_minutes > 525600)
    return "ระบุระยะเวลาแจ้งเตือนตั้งแต่ 1 ถึง 525600 นาที";
  if (policy.stale_minutes < scheduleGapMinutes(schedule))
    return `ระยะเวลาแจ้งเตือนต้องไม่น้อยกว่าระยะห่างรอบสำรอง ${scheduleGapMinutes(schedule)} นาที`;
  return "";
}

export function destinationError(value: string): string {
  try {
    const url = new URL(value.trim());
    if (url.protocol !== "https:" || !url.hostname || url.username || url.password ||
      url.pathname !== "/" || url.search || url.hash || /[\\\r\n]/.test(value) || url.port === "0") throw new Error();
    return "";
  } catch { return "ระบุ URL ปลายทางแบบ HTTPS พร้อมพอร์ตถ้ามี โดยไม่ใส่ path หรือข้อมูลเข้าสู่ระบบ"; }
}
