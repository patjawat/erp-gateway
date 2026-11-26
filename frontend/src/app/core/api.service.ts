import { inject, Injectable, signal } from "@angular/core";
import {
  HttpClient,
  HttpErrorResponse,
  HttpHeaders,
  HttpParams,
} from "@angular/common/http";
import { firstValueFrom } from "rxjs";

export class ApiError extends Error {
  constructor(
    message: string,
    public status = 0,
    public code = "",
  ) {
    super(message);
  }
}
@Injectable({ providedIn: "root" })
export class GatewayApi {
  private http = inject(HttpClient);
  private csrf = "";
  private authVersion = 0;
  readonly authenticated = signal(false);
  readonly checking = signal(true);
  readonly sessionError = signal("");
  readonly sessionNotice = signal("");
  async check(): Promise<void> {
    const version = this.authVersion;
    this.checking.set(true);
    this.sessionError.set("");
    try {
      const value = await this.get<{ csrf_token: string }>("session");
      if (version === this.authVersion) {
        this.csrf = value.csrf_token;
        this.authenticated.set(true);
      }
    } catch (error) {
      if (version === this.authVersion) {
        this.expire();
        if (!(error instanceof ApiError) || error.status !== 401)
          this.sessionError.set(
            error instanceof Error
              ? error.message
              : "ตรวจสอบการเข้าสู่ระบบไม่ได้",
          );
      }
    } finally {
      this.checking.set(false);
    }
  }
  async login(username: string, password: string): Promise<void> {
    const version = ++this.authVersion;
    const value = await this.post<{ csrf_token: string }>("login", {
      username,
      password,
    });
    if (version !== this.authVersion) return;
    this.csrf = value.csrf_token;
    this.sessionError.set("");
    this.sessionNotice.set("");
    this.authenticated.set(true);
  }
  async logout(): Promise<void> {
    const version = this.authVersion;
    try {
      await this.post("logout", {});
    } finally {
      if (version === this.authVersion) this.expire();
    }
  }
  expire(notice = ""): void {
    this.authVersion++;
    this.csrf = "";
    this.sessionNotice.set(notice);
    this.authenticated.set(false);
  }
  get<T>(
    path: string,
    params: Record<string, string | number> = {},
  ): Promise<T> {
    return this.request<T>("GET", path, undefined, params);
  }
  post<T = unknown>(path: string, body?: unknown): Promise<T> {
    return this.request<T>("POST", path, body);
  }
  put<T = unknown>(path: string, body?: unknown): Promise<T> {
    return this.request<T>("PUT", path, body);
  }
  delete<T = unknown>(path: string): Promise<T> {
    return this.request<T>("DELETE", path);
  }
  private headers(method: string, path: string): HttpHeaders {
    let headers = new HttpHeaders({ Accept: "application/json" });
    if (method !== "GET" && path !== "login")
      headers = headers.set("X-CSRF-Token", this.csrf);
    return headers;
  }
  private async request<T>(
    method: string,
    path: string,
    body?: unknown,
    params: Record<string, string | number> = {},
  ): Promise<T> {
    const version = this.authVersion;
    try {
      const result = await firstValueFrom(
        this.http.request<{ data: T }>(method, "/admin/api/" + path, {
          body,
          params: new HttpParams({ fromObject: params }),
          headers: this.headers(method, path),
          credentials: "same-origin",
          redirect: "error",
          cache: "no-store",
          timeout: body instanceof FormData ? 90000 : 25000,
        }),
      );
      return result.data;
    } catch (error) {
      throw this.failure(error, path, version);
    }
  }
  private failure(error: unknown, path: string, version: number): ApiError {
    if (error instanceof HttpErrorResponse) {
      if (
        error.status === 401 &&
        path !== "login" &&
        version === this.authVersion
      ) {
        const wasAuthenticated = this.authenticated();
        this.expire();
        if (path !== "session" || wasAuthenticated)
          this.sessionError.set("หมดเวลาเข้าสู่ระบบ กรุณาเข้าสู่ระบบอีกครั้ง");
      }
      return new ApiError(
        error.error?.error?.message ||
          (error.status === 0
            ? "เชื่อมต่อ Gateway ไม่ได้ กรุณาลองอีกครั้ง"
            : "ดำเนินการไม่สำเร็จ กรุณาลองอีกครั้ง"),
        error.status,
        error.error?.error?.code || "",
      );
    }
    return new ApiError("เชื่อมต่อ Gateway ไม่ได้ กรุณาลองอีกครั้ง");
  }
  async download(path: string, body?: unknown): Promise<void> {
    const version = this.authVersion;
    try {
      const response = await firstValueFrom(
        this.http.post("/admin/api/" + path, body, {
          observe: "response",
          responseType: "blob",
          headers: this.headers("POST", path),
          credentials: "same-origin",
          redirect: "error",
          cache: "no-store",
          timeout: 45000,
        }),
      );
      if (version !== this.authVersion) return;
      if (!response.body) throw new Error("empty");
      const url = URL.createObjectURL(response.body);
      const a = document.createElement("a");
      a.href = url;
      a.download =
        "gateway-recovery-" + new Date().toISOString().slice(0, 10) + ".json";
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (error) {
      throw this.failure(error, path, version);
    }
  }
}
