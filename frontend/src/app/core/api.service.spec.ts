import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { TestBed } from "@angular/core/testing";
import { provideHttpClient } from "@angular/common/http";
import {
  HttpTestingController,
  provideHttpClientTesting,
} from "@angular/common/http/testing";
import { ApiError, GatewayApi } from "./api.service";
describe("Gateway authentication and writes", () => {
  let api: GatewayApi;
  let http: HttpTestingController;
  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [provideHttpClient(), provideHttpClientTesting()],
    });
    api = TestBed.inject(GatewayApi);
    http = TestBed.inject(HttpTestingController);
  });
  afterEach(() => {
    http.verify();
    TestBed.resetTestingModule();
  });
  it("keeps the server session token in memory and sends CSRF on writes", async () => {
    const login = api.login("admin", "secret");
    const request = http.expectOne("/admin/api/login");
    expect(request.request.headers.has("X-CSRF-Token")).toBe(false);
    request.flush({ data: { csrf_token: "csrf-test" } });
    await login;
    expect(api.authenticated()).toBe(true);
    const write = api.post("hospitals", { code: "10001" });
    const change = http.expectOne("/admin/api/hospitals");
    expect(change.request.headers.get("X-CSRF-Token")).toBe("csrf-test");
    expect(change.request.credentials).toBe("same-origin");
    change.flush({ data: { id: "1" } });
    await write;
  });
  it("clears authenticated state and csrf after a server 401", async () => {
    const check = api.check();
    http.expectOne("/admin/api/session").flush({ data: { csrf_token: "old" } });
    await check;
    const read = api.get("dashboard").catch((e) => e);
    http
      .expectOne("/admin/api/dashboard")
      .flush(
        { error: { code: "ADMIN_AUTH_REQUIRED", message: "หมดเวลา" } },
        { status: 401, statusText: "Unauthorized" },
      );
    expect(await read).toBeInstanceOf(ApiError);
    expect(api.authenticated()).toBe(false);
    const write = api.post("logout", {});
    const request = http.expectOne("/admin/api/logout");
    expect(request.request.headers.get("X-CSRF-Token")).toBe("");
    request.flush({ data: {} });
    await write;
  });
  it("does not let an old 401 invalidate a newly authenticated session", async () => {
    const check = api.check();
    http.expectOne("/admin/api/session").flush({ data: { csrf_token: "old" } });
    await check;
    const old = api.get("dashboard").catch((e) => e);
    const oldRequest = http.expectOne("/admin/api/dashboard");
    api.expire();
    const login = api.login("admin", "secret");
    http.expectOne("/admin/api/login").flush({ data: { csrf_token: "new" } });
    await login;
    oldRequest.flush(
      { error: { message: "old session" } },
      { status: 401, statusText: "Unauthorized" },
    );
    await old;
    expect(api.authenticated()).toBe(true);
    const save = api.post("logout", {});
    const current = http.expectOne("/admin/api/logout");
    expect(current.request.headers.get("X-CSRF-Token")).toBe("new");
    current.flush({ data: {} });
    await save;
  });
  it("preserves a settings-saved message across required relogin", () => {
    api.expire("บันทึกการตั้งค่าแล้ว กรุณาเข้าสู่ระบบอีกครั้ง");
    expect(api.sessionNotice()).toContain("บันทึกการตั้งค่าแล้ว");
    expect(api.authenticated()).toBe(false);
  });
  it("does not show a session-expired warning on a first visit", async () => {
    const pending = api.check();
    http
      .expectOne("/admin/api/session")
      .flush(
        { error: { message: "เข้าสู่ระบบก่อน" } },
        { status: 401, statusText: "Unauthorized" },
      );
    await pending;
    expect(api.authenticated()).toBe(false);
    expect(api.sessionError()).toBe("");
  });
  it("does not force JSON content type on attachment uploads", async () => {
    const data = new FormData();
    data.append(
      "file",
      new File(["pdf"], "sample.pdf", { type: "application/pdf" }),
    );
    const pending = api.post("hospitals/1/attachments", data);
    const request = http.expectOne("/admin/api/hospitals/1/attachments");
    expect(request.request.body).toBe(data);
    expect(request.request.headers.has("Content-Type")).toBe(false);
    request.flush({ data: { id: "attachment" } });
    await pending;
  });
});
