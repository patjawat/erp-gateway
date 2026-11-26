import { describe, expect, it, vi } from "vitest";
import { GatewayApi } from "./api.service";
import { PagedList } from "./paged-list";
const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
};
describe("server cursor lists", () => {
  it("loads one bounded page, follows cursor once and deduplicates stable IDs", async () => {
    const next = deferred<unknown>();
    const get = vi
      .fn()
      .mockResolvedValueOnce({
        hospitals: [{ id: "1", name: "เดิม" }],
        total: 2,
        next_cursor: "cursor-1",
      })
      .mockReturnValueOnce(next.promise);
    const list = new PagedList<{ id: string; name: string }>(
      { get } as unknown as GatewayApi,
      "hospitals",
      "hospitals",
    );
    await list.reset({ q: "รพ." });
    expect(get).toHaveBeenCalledTimes(1);
    expect(get.mock.calls[0]).toEqual(["hospitals", { q: "รพ.", limit: 25 }]);
    const pending = list.more();
    await list.more();
    expect(get).toHaveBeenCalledTimes(2);
    expect(get.mock.calls[1][1].cursor).toBe("cursor-1");
    next.resolve({
      hospitals: [
        { id: "1", name: "ใหม่" },
        { id: "2", name: "อีกแห่ง" },
      ],
      total: 2,
      next_cursor: null,
    });
    await pending;
    expect(list.items().map((x) => x.id)).toEqual(["1", "2"]);
    expect(list.items()[0].name).toBe("ใหม่");
    await list.more();
    expect(get).toHaveBeenCalledTimes(2);
    expect(list.hasMore()).toBe(false);
  });
  it("ignores a stale request after search changes", async () => {
    const old = deferred<unknown>();
    const get = vi
      .fn()
      .mockReturnValueOnce(old.promise)
      .mockResolvedValueOnce({
        events: [{ id: 2 }],
        total: 1,
        next_cursor: null,
      });
    const list = new PagedList<{ id: number }>(
      { get } as unknown as GatewayApi,
      "audit",
      "events",
    );
    const initial = list.reset({ q: "เก่า" });
    await list.reset({ q: "ใหม่" });
    old.resolve({ events: [{ id: 1 }], total: 50, next_cursor: "old" });
    await initial;
    expect(list.items()).toEqual([{ id: 2 }]);
    expect(list.total()).toBe(1);
    expect(list.hasMore()).toBe(false);
  });
  it("retains loaded rows and cursor when loading the next page fails", async () => {
    const get = vi
      .fn()
      .mockResolvedValueOnce({
        events: [{ id: 1 }],
        total: 2,
        next_cursor: "next",
      })
      .mockRejectedValueOnce(new Error("เครือข่ายขัดข้อง"))
      .mockResolvedValueOnce({
        events: [{ id: 2 }],
        total: 2,
        next_cursor: null,
      });
    const list = new PagedList<{ id: number }>(
      { get } as unknown as GatewayApi,
      "audit",
      "events",
    );
    await list.reset();
    await list.more();
    expect(list.items()).toEqual([{ id: 1 }]);
    expect(list.error()).toContain("เครือข่าย");
    expect(list.hasMore()).toBe(true);
    await list.more();
    expect(get.mock.calls[2][1].cursor).toBe("next");
    expect(list.items()).toEqual([{ id: 1 }, { id: 2 }]);
  });
  it("does not update a page after it has been destroyed", async () => {
    const request = deferred<unknown>();
    const list = new PagedList<{ id: number }>(
      { get: () => request.promise } as unknown as GatewayApi,
      "audit",
      "events",
    );
    const pending = list.reset();
    list.destroy();
    request.resolve({ events: [{ id: 1 }], total: 1, next_cursor: null });
    await pending;
    expect(list.items()).toEqual([]);
  });
});
