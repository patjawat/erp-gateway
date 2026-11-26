import { signal } from "@angular/core";
import { GatewayApi } from "./api.service";
export class PagedList<T extends { id: string | number }> {
  readonly items = signal<T[]>([]);
  readonly total = signal(0);
  readonly loading = signal(false);
  readonly error = signal("");
  readonly hasMore = signal(false);
  private cursor: string | null = null;
  private params: Record<string, string | number> = {};
  private version = 0;
  private disposed = false;
  constructor(
    private api: GatewayApi,
    private path: string,
    private collectionKey: string,
  ) {}
  async reset(params: Record<string, string | number> = {}): Promise<void> {
    this.version++;
    this.params = { ...params };
    this.cursor = null;
    this.items.set([]);
    this.total.set(0);
    this.hasMore.set(true);
    this.loading.set(false);
    await this.more();
  }
  async more(): Promise<void> {
    if (this.disposed || this.loading() || !this.hasMore()) return;
    const version = this.version;
    this.loading.set(true);
    this.error.set("");
    try {
      const data = await this.api.get<Record<string, unknown>>(this.path, {
        ...this.params,
        limit: 25,
        ...(this.cursor ? { cursor: this.cursor } : {}),
      });
      if (this.disposed || version !== this.version) return;
      const incoming = data[this.collectionKey];
      if (!Array.isArray(incoming))
        throw new Error("รูปแบบรายการไม่ถูกต้อง กรุณาลองใหม่");
      const rows = new Map(this.items().map((row) => [row.id, row]));
      for (const row of incoming as T[]) rows.set(row.id, row);
      this.items.set([...rows.values()]);
      this.total.set(typeof data["total"] === "number" ? data["total"] : 0);
      this.cursor =
        typeof data["next_cursor"] === "string" && data["next_cursor"]
          ? data["next_cursor"]
          : null;
      this.hasMore.set(this.cursor !== null);
    } catch (error) {
      if (version === this.version && !this.disposed)
        this.error.set(
          error instanceof Error ? error.message : "โหลดรายการไม่สำเร็จ",
        );
    } finally {
      if (version === this.version && !this.disposed) this.loading.set(false);
    }
  }
  destroy(): void {
    this.disposed = true;
    this.version++;
  }
}
