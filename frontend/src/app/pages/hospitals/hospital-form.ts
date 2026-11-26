import type { Hospital } from "../../core/models";

export interface HospitalDraft {
  code: string;
  name: string;
  province: string;
  api_url: string;
  gateway_key: string;
  start_date: string;
  expiry_date: string;
}

export type HospitalFieldErrors = Partial<Record<keyof HospitalDraft, string>>;

export function emptyHospitalDraft(): HospitalDraft {
  return {
    code: "",
    name: "",
    province: "",
    api_url: "",
    gateway_key: "",
    start_date: "",
    expiry_date: "",
  };
}

export function draftForHospital(hospital: Hospital): HospitalDraft {
  return {
    code: hospital.code,
    name: hospital.name,
    province: hospital.province || "",
    api_url: hospital.api_url,
    gateway_key: "",
    start_date: hospital.start_date || "",
    expiry_date: hospital.expiry_date || "",
  };
}

function validDate(value: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  if (Number(value.slice(0, 4)) < 1) return false;
  const date = new Date(`${value}T12:00:00Z`);
  return (
    Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === value
  );
}

const hospitalStatuses = new Set([
  "all",
  "pending",
  "enabled",
  "disabled",
  "online",
  "offline",
]);

export function hospitalStatusFilter(value: string | null): string {
  return value && hospitalStatuses.has(value) ? value : "all";
}

export function validateRenewalDate(
  hospital: Pick<Hospital, "start_date">,
  expiryDate: string,
): HospitalFieldErrors {
  if (!validDate(expiryDate))
    return { expiry_date: "เลือกวันหมดอายุใหม่ให้ถูกต้อง" };
  if (
    hospital.start_date &&
    validDate(hospital.start_date) &&
    expiryDate < hospital.start_date
  ) {
    return {
      expiry_date: "วันหมดอายุต้องเป็นวันเดียวกับหรือหลังวันเริ่มใช้งาน",
    };
  }
  return {};
}

export function validateHospitalDraft(
  draft: HospitalDraft,
  creating: boolean,
): HospitalFieldErrors {
  const errors: HospitalFieldErrors = {};
  if (!/^\d{5}$/.test(draft.code.trim()))
    errors.code = "กรอกรหัสสถานพยาบาลเป็นตัวเลข 5 หลัก";
  if (!draft.name.trim()) errors.name = "กรอกชื่อหน่วยงาน";
  else if (new TextEncoder().encode(draft.name.trim()).length > 500)
    errors.name = "ชื่อหน่วยงานยาวเกินกำหนด";
  if (new TextEncoder().encode(draft.province.trim()).length > 255)
    errors.province = "ชื่อจังหวัดยาวเกินกำหนด";
  try {
    const url = new URL(draft.api_url.trim());
    if (
      url.protocol !== "https:" ||
      !url.hostname ||
      url.username ||
      url.password ||
      url.search ||
      url.hash ||
      draft.api_url.length > 2048
    ) {
      errors.api_url =
        "ใช้ URL แบบ HTTPS โดยไม่มีข้อมูลเข้าสู่ระบบ query หรือ fragment";
    }
  } catch {
    errors.api_url = "กรอกปลายทางให้ครบ เช่น https://api.example.org";
  }
  if (
    (creating || draft.gateway_key !== "") &&
    (draft.gateway_key.length < 32 ||
      draft.gateway_key.length > 512 ||
      /[\r\n]/.test(draft.gateway_key))
  ) {
    errors.gateway_key =
      "ใช้ Gateway key 32–512 ตัวอักษร โดยไม่มีการขึ้นบรรทัดใหม่";
  }
  if (draft.start_date && !validDate(draft.start_date))
    errors.start_date = "เลือกวันเริ่มใช้งานให้ถูกต้อง";
  if (draft.expiry_date && !validDate(draft.expiry_date))
    errors.expiry_date = "เลือกวันหมดอายุให้ถูกต้อง";
  if (
    draft.start_date &&
    draft.expiry_date &&
    !errors.start_date &&
    !errors.expiry_date &&
    draft.expiry_date < draft.start_date
  ) {
    errors.expiry_date = "วันหมดอายุต้องเป็นวันเดียวกับหรือหลังวันเริ่มใช้งาน";
  }
  return errors;
}

export function hospitalRequestBody(draft: HospitalDraft): HospitalDraft {
  return {
    code: draft.code.trim(),
    name: draft.name.trim(),
    province: draft.province.trim(),
    api_url: draft.api_url.trim().replace(/\/+$/, ""),
    gateway_key: draft.gateway_key,
    start_date: draft.start_date,
    expiry_date: draft.expiry_date,
  };
}

export function changesHospitalConfiguration(
  draft: HospitalDraft,
  hospital: Hospital,
): boolean {
  const body = hospitalRequestBody(draft);
  return (
    body.gateway_key !== "" ||
    body.name !== hospital.name ||
    body.province !== (hospital.province || "") ||
    body.api_url !== hospital.api_url
  );
}

export function renewalRequestBody(
  hospital: Hospital,
  expiryDate: string,
): HospitalDraft {
  return hospitalRequestBody({
    ...draftForHospital(hospital),
    expiry_date: expiryDate,
  });
}
