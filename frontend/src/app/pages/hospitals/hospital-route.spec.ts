import { TestBed } from "@angular/core/testing";
import { ActivatedRoute, convertToParamMap } from "@angular/router";
import { BehaviorSubject } from "rxjs";
import { afterEach, describe, expect, it, vi } from "vitest";
import { GatewayApi } from "../../core/api.service";
import { HospitalsPage } from "./hospitals.page";

describe("hospital route filters", () => {
  let page: HospitalsPage | undefined;
  afterEach(() => {
    page?.ngOnDestroy();
    TestBed.resetTestingModule();
  });

  it("applies the overview status before its single initial server request", async () => {
    const params = new BehaviorSubject(
      convertToParamMap({ status: "pending" }),
    );
    const get = vi
      .fn()
      .mockResolvedValue({ hospitals: [], total: 0, next_cursor: null });
    TestBed.configureTestingModule({
      providers: [
        { provide: GatewayApi, useValue: { get } },
        {
          provide: ActivatedRoute,
          useValue: { queryParamMap: params.asObservable() },
        },
      ],
    });
    page = TestBed.runInInjectionContext(() => new HospitalsPage());
    page.ngOnInit();
    await Promise.resolve();
    expect(get).toHaveBeenCalledExactlyOnceWith("hospitals", {
      q: "",
      status: "pending",
      limit: 25,
    });
    expect(page.status).toBe("pending");

    params.next(convertToParamMap({ status: "pending", unrelated: "changed" }));
    await Promise.resolve();
    expect(get).toHaveBeenCalledTimes(1);

    params.next(convertToParamMap({ status: "offline" }));
    await Promise.resolve();
    expect(get).toHaveBeenLastCalledWith("hospitals", {
      q: "",
      status: "offline",
      limit: 25,
    });
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("normalizes unsupported route filters before requesting the server", async () => {
    const get = vi
      .fn()
      .mockResolvedValue({ hospitals: [], total: 0, next_cursor: null });
    TestBed.configureTestingModule({
      providers: [
        { provide: GatewayApi, useValue: { get } },
        {
          provide: ActivatedRoute,
          useValue: {
            queryParamMap: new BehaviorSubject(
              convertToParamMap({ status: "unknown" }),
            ),
          },
        },
      ],
    });
    page = TestBed.runInInjectionContext(() => new HospitalsPage());
    page.ngOnInit();
    await Promise.resolve();
    expect(get).toHaveBeenCalledExactlyOnceWith("hospitals", {
      q: "",
      status: "all",
      limit: 25,
    });
  });
});
