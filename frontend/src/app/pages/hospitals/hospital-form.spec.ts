import { describe, expect, it } from "vitest";
import { Hospital } from "../../core/models";
import { attachmentSelectionError } from "./attachment-form";
import {
  changesHospitalConfiguration,
  draftForHospital,
  hospitalStatusFilter,
  renewalRequestBody,
  validateHospitalDraft,
  validateRenewalDate,
} from "./hospital-form";

const hospital: Hospital = {
  id: "site-one",
  code: "10001",
  name: "โรงพยาบาลทดสอบ",
  province: "เลย",
  api_url: "https://api.example.org",
  approval_status: "approved",
  enabled: true,
  connection_status: "online",
  checked_at: null,
  created_at: 1,
  start_date: "2025-01-01",
  expiry_date: "2026-12-25",
};

describe("hospital form contracts", () => {
  it("requires a key when adding and preserves blank keys when editing", () => {
    const draft = draftForHospital(hospital);
    expect(validateHospitalDraft(draft, true).gateway_key).toBeTruthy();
    expect(validateHospitalDraft(draft, false).gateway_key).toBeUndefined();
  });

  it("rejects credential-bearing URLs and dates before start", () => {
    const draft = {
      ...draftForHospital(hospital),
      api_url: "https://user:password@example.org",
      expiry_date: "2024-12-31",
    };
    const errors = validateHospitalDraft(draft, false);
    expect(errors.api_url).toBeTruthy();
    expect(errors.expiry_date).toBeTruthy();
  });

  it("rejects impossible calendar dates", () => {
    expect(
      validateHospitalDraft(
        { ...draftForHospital(hospital), start_date: "2026-02-30" },
        false,
      ).start_date,
    ).toBeTruthy();
    expect(
      validateHospitalDraft(
        { ...draftForHospital(hospital), start_date: "0000-01-01" },
        false,
      ).start_date,
    ).toBeTruthy();
  });

  it("renewal preserves configuration so the backend does not reset approval", () => {
    const body = renewalRequestBody(hospital, "2027-12-25");
    expect(body).toEqual({
      code: hospital.code,
      name: hospital.name,
      province: hospital.province,
      api_url: hospital.api_url,
      gateway_key: "",
      start_date: hospital.start_date,
      expiry_date: "2027-12-25",
    });
    expect(changesHospitalConfiguration(body, hospital)).toBe(false);
  });

  it("past dates remain tracking metadata, without changing enable or approval fields", () => {
    const body = renewalRequestBody(hospital, "2025-02-01");
    expect(validateHospitalDraft(body, false)).toEqual({});
    expect(body).not.toHaveProperty("enabled");
    expect(body).not.toHaveProperty("approval_status");
    expect(changesHospitalConfiguration(body, hospital)).toBe(false);
  });

  it("configuration changes require separate confirmation in the page", () => {
    expect(
      changesHospitalConfiguration(
        { ...draftForHospital(hospital), name: "ชื่อใหม่" },
        hospital,
      ),
    ).toBe(true);
    expect(
      changesHospitalConfiguration(
        { ...draftForHospital(hospital), gateway_key: "a".repeat(32) },
        hospital,
      ),
    ).toBe(true);
  });

  it("renewal validates only its visible date, without rejecting unrelated stored fields", () => {
    const existing = {
      ...hospital,
      code: "legacy",
      name: "",
      api_url: "http://legacy.example.org",
    };
    expect(validateRenewalDate(existing, "2027-12-25")).toEqual({});
    expect(
      validateRenewalDate(existing, "2024-12-31").expiry_date,
    ).toBeTruthy();
    expect(validateRenewalDate(existing, "").expiry_date).toBeTruthy();
  });

  it("accepts supported status links and normalizes unknown or missing query values", () => {
    expect(hospitalStatusFilter("pending")).toBe("pending");
    expect(hospitalStatusFilter("offline")).toBe("offline");
    expect(hospitalStatusFilter("not-a-filter")).toBe("all");
    expect(hospitalStatusFilter(null)).toBe("all");
  });
});

describe("attachment selection", () => {
  it("accepts supported files at the size boundary and rejects oversized or unsupported files", () => {
    expect(
      attachmentSelectionError({
        name: "contract.PDF",
        size: 10 * 1024 * 1024,
      }),
    ).toBe("");
    expect(
      attachmentSelectionError({
        name: "contract.pdf",
        size: 10 * 1024 * 1024 + 1,
      }),
    ).toBeTruthy();
    expect(
      attachmentSelectionError({ name: "script.html", size: 10 }),
    ).toBeTruthy();
    expect(
      attachmentSelectionError({ name: "empty.pdf", size: 0 }),
    ).toBeTruthy();
  });
});
