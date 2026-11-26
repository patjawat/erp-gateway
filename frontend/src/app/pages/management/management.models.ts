export interface CertificateInfo {
  subject: string;
  serial: string;
  not_before: number;
  not_after: number;
  days_remaining: number;
  status: string;
  fingerprint: string;
}

export interface CertificateRequest {
  csr_pem: string;
  fingerprint: string;
  created_at: number;
}

export interface CertificatesResponse {
  gateway: CertificateInfo | null;
  issuer: { status: string; message: string };
  capabilities: { csr: boolean; import: boolean; renew: boolean };
  pending_request: CertificateRequest | null;
}

export interface RuntimeSettings {
  public_url: string;
  trusted_proxy_cidrs: string[];
  approved_upstreams: string[];
  private_upstream_cidrs: string[];
}

export interface SettingsResponse {
  configured: boolean;
  editable: boolean;
  lan_http_enabled?: boolean;
  settings: RuntimeSettings | null;
  message: string;
}

export interface AuditEvent {
  id: number;
  hospital_id: string;
  hospital_code: string;
  action: string;
  reason: string;
  created_at: number;
}

export function errorMessage(error: unknown): string {
  return error instanceof Error
    ? error.message
    : "ดำเนินการไม่สำเร็จ กรุณาลองอีกครั้ง";
}

export const auditLabels: Record<string, string> = {
  create: "เพิ่มหน่วยงาน",
  created: "เพิ่มหน่วยงาน",
  update: "แก้ไขข้อมูลหน่วยงาน",
  test: "ทดสอบการเชื่อมต่อ",
  approve: "อนุมัติและเปิดใช้งาน",
  suspend: "ระงับการใช้งาน",
  enable: "เปิดใช้งานอีกครั้ง",
  upload_attachment: "แนบเอกสาร",
  delete_attachment: "ลบเอกสารแนบ",
  settings_apply: "ใช้การตั้งค่าใหม่",
  recovery_export: "ดาวน์โหลดไฟล์กู้คืน",
  certificate_csr: "สร้างคำขอใบรับรอง",
  certificate_activate: "เปิดใช้ใบรับรอง",
};

export const auditReasons: Record<string, string> = {
  "configuration changed; approval reset":
    "เปลี่ยนข้อมูลการเชื่อมต่อ และตั้งสถานะรออนุมัติใหม่",
  "usage dates changed": "แก้ไขวันเริ่มใช้งานหรือวันหมดอายุ",
  "Validated public runtime settings applied; credentials and identity retained":
    "บันทึกการตั้งค่าเครือข่าย โดยคงบัญชี กุญแจ และใบรับรองเดิม",
  "Encrypted config/identity export requested; database backup is separate":
    "ส่งออกการตั้งค่าและตัวตนแบบเข้ารหัส ฐานข้อมูลต้องสำรองแยก",
  "Gateway client identity; existing key retained":
    "สร้างคำขอใบรับรอง Gateway โดยคงกุญแจเดิม",
  "Gateway client certificate activated; SPKI retained":
    "เปิดใช้ใบรับรอง Gateway โดยคงตัวตนกุญแจเดิม",
};

export function lines(value: string): string[] {
  return value
    .split(/[\n,]+/)
    .map((line) => line.trim())
    .filter(Boolean);
}

export function auditDateBounds(
  start: string,
  end: string,
): Record<string, number> {
  const result: Record<string, number> = {};
  if (start)
    result["after"] = Math.floor(Date.parse(start + "T00:00:00+07:00") / 1000);
  if (end)
    result["before"] =
      Math.floor(Date.parse(end + "T00:00:00+07:00") / 1000) + 86400;
  return result;
}
