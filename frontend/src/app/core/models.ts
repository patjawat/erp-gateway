export interface HospitalUsage {
  status: string;
  as_of: number | null;
  personnel: number | null;
  active_30d: number | null;
  adoption_30d: number | null;
  android_30d: number | null;
  ios_30d: number | null;
}
export interface Hospital {
  id: string;
  code: string;
  name: string;
  province: string;
  api_url: string;
  approval_status: string;
  enabled: boolean;
  connection_status: string;
  checked_at: number | null;
  created_at: number;
  start_date: string;
  expiry_date: string;
  usage?: HospitalUsage;
}
export interface HospitalSummary {
  total: number;
  enabled: number;
  pending: number;
  disabled: number;
  online: number;
  offline: number;
  unknown: number;
}
export interface UsageSummary {
  status: string;
  personnel: number | null;
  active_30d: number | null;
  adoption_30d: number | null;
  android_30d: number | null;
  ios_30d: number | null;
  as_of: number | null;
  coverage: { reporting: number; total: number };
}
export interface Dashboard {
  as_of: number;
  hospitals: HospitalSummary;
  usage: UsageSummary;
  certificates: {
    pending_requests: number;
    issuer_status: string;
    gateway: { status: string; not_after?: string };
  };
}
