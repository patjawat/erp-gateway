import { Component, inject, OnDestroy, OnInit, signal } from "@angular/core";
import { ActivatedRoute, RouterLink } from "@angular/router";
import {
  LucideRefreshCw,
  LucideSmartphone,
  LucideTabletSmartphone,
  LucideArrowRight,
  LucideBuilding,
  LucideShieldCheck,
} from "@lucide/angular";
import { GatewayApi } from "../core/api.service";
import { Dashboard } from "../core/models";
import { count, thaiTime, usageReady } from "../core/format";
@Component({
  standalone: true,
  imports: [
    RouterLink,
    LucideRefreshCw,
    LucideSmartphone,
    LucideTabletSmartphone,
    LucideArrowRight,
    LucideBuilding,
    LucideShieldCheck,
  ],
  template: `
    <div class="page-heading">
      <div>
        <h1 data-page-heading tabindex="-1">
          {{ usageOnly ? "การใช้งานแอป" : "ภาพรวมการใช้งาน" }}
        </h1>
        <p class="page-subtitle">
          {{
            usageOnly
              ? "ติดตามการเข้าถึงแอปของบุคลากรในแต่ละหน่วยงาน"
              : "มองเห็นความเคลื่อนไหว และดูแลสิ่งสำคัญได้ทันเวลา"
          }}
        </p>
      </div>
      <div class="page-actions">
        <button
          type="button"
          class="button"
          (click)="load()"
          [disabled]="loading()"
        >
          <svg lucideRefreshCw aria-hidden="true"></svg>โหลดข้อมูลล่าสุด</button
        ><a class="button button-primary" routerLink="/hospitals"
          >จัดการหน่วยงาน</a
        >
      </div>
    </div>
    @if (error()) {
      <div class="notice notice-error" role="alert">
        {{ error() }}
        <button class="button button-small" type="button" (click)="load()">
          ลองใหม่
        </button>
      </div>
    }
    @if (loading() && !data()) {
      <p class="loading" role="status">กำลังโหลดภาพรวม…</p>
    }
    @if (data(); as d) {
      <div class="overview-grid">
        <section class="panel usage-panel" aria-labelledby="usage-heading">
          <div class="section-heading">
            <h2 id="usage-heading">บุคลากรที่ใช้แอป</h2>
            <span class="badge">30 วันที่ผ่านมา</span>
          </div>
          <div class="adoption-summary">
            <strong class="adoption-value"
              >{{
                ready(d) && d.usage.adoption_30d !== null
                  ? count(d.usage.adoption_30d)
                  : "—"
              }}
              @if (ready(d) && d.usage.adoption_30d !== null) {
                <span>%</span>
              }
            </strong>
            <div>
              @if (ready(d)) {
                <strong
                  >{{ count(d.usage.active_30d) }} จาก
                  {{ count(d.usage.personnel) }} คน</strong
                >
              } @else {
                <strong>{{
                  d.usage.status === "stale"
                    ? "ข้อมูลการใช้งานล่าช้า"
                    : "ยังไม่มีข้อมูลการใช้งาน"
                }}</strong>
              }
              <p class="muted">
                ข้อมูลจาก {{ count(d.usage.coverage.reporting) }} ใน
                {{ count(d.usage.coverage.total) }} หน่วยงาน
              </p>
            </div>
          </div>
          @if (ready(d) && d.usage.adoption_30d !== null) {
            <div
              class="usage-track"
              role="meter"
              aria-label="อัตราบุคลากรที่ใช้แอปใน 30 วัน"
              [attr.aria-valuenow]="d.usage.adoption_30d"
              aria-valuemin="0"
              aria-valuemax="100"
            >
              <span [style.width.%]="d.usage.adoption_30d"></span>
            </div>
          }
          @if (!ready(d)) {
            <p class="help">
              ข้อมูลจะแสดงเมื่อหน่วยงานส่งสรุปที่รองรับ
              ช่วงที่ยังไม่มีรายงานจะไม่ถูกนับเป็นศูนย์
            </p>
          } @else if (d.usage.personnel === 0) {
            <p class="help">ยังไม่มีฐานบุคลากรสำหรับคำนวณเปอร์เซ็นต์</p>
          }
          <div class="device-grid">
            <div class="device-stat">
              <span class="device-icon"
                ><svg lucideSmartphone aria-hidden="true"></svg
              ></span>
              <div>
                <span class="muted">Android</span
                ><strong
                  >{{ ready(d) ? count(d.usage.android_30d) : "—" }}
                  <small>การติดตั้งที่พบ</small></strong
                >
              </div>
            </div>
            <div class="device-stat">
              <span class="device-icon"
                ><svg lucideTabletSmartphone aria-hidden="true"></svg
              ></span>
              <div>
                <span class="muted">iOS</span
                ><strong
                  >{{ ready(d) ? count(d.usage.ios_30d) : "—" }}
                  <small>การติดตั้งที่พบ</small></strong
                >
              </div>
            </div>
          </div>
          <p class="help">
            นับคนไม่ซ้ำใน 30 วันเทียบกับบุคลากรปัจจุบัน
            การติดตั้งแยกจากจำนวนคนและยอดดาวน์โหลด Store
          </p>
          <p class="help">
            ข้อมูลการใช้งานล่าสุด {{ thaiTime(d.usage.as_of) }}
          </p>
        </section>
        <aside class="attention-section">
          <div class="section-heading"><h2>สิ่งที่ต้องดูแล</h2></div>
          <a
            class="attention-row"
            routerLink="/hospitals"
            [queryParams]="{ status: 'pending' }"
            ><svg lucideBuilding aria-hidden="true"></svg>
            <div>
              <strong
                >รออนุมัติ {{ count(d.hospitals.pending) }} หน่วยงาน</strong
              >
              <p class="muted">ตรวจข้อมูลและการเชื่อมต่อก่อนเปิดใช้งาน</p>
            </div>
            <svg lucideArrowRight aria-hidden="true"></svg></a
          ><a
            class="attention-row"
            routerLink="/hospitals"
            [queryParams]="{ status: 'offline' }"
            ><svg lucideBuilding aria-hidden="true"></svg>
            <div>
              <strong
                >เชื่อมต่อไม่ได้
                {{ count(d.hospitals.offline) }} หน่วยงาน</strong
              >
              <p class="muted">ตรวจปลายทางและทดสอบการเชื่อมต่อ</p>
            </div>
            <svg lucideArrowRight aria-hidden="true"></svg></a
          ><a class="attention-row" routerLink="/certificates"
            ><svg lucideShieldCheck aria-hidden="true"></svg>
            <div>
              <strong>ใบรับรอง Gateway</strong>
              <p class="muted">
                {{
                  d.certificates.pending_requests
                    ? "มี CSR รอนำเข้าใบรับรอง"
                    : "ตรวจอายุใบรับรองและสถานะการเชื่อมต่อ"
                }}
              </p>
            </div>
            <svg lucideArrowRight aria-hidden="true"></svg
          ></a>
          <p class="help manual-note">
            วันหมดอายุหน่วยงานใช้ติดตามการต่ออายุ
            คุณเป็นผู้ระงับหรือเปิดใช้งานเองในหน้าหน่วยงาน
          </p>
        </aside>
      </div>
      <section
        class="registry-summary"
        aria-labelledby="registry-summary-heading"
      >
        <div class="section-heading">
          <h2 id="registry-summary-heading">หน่วยงานที่ดูแล</h2>
          <a routerLink="/hospitals"
            >ดูทั้งหมด <svg lucideArrowRight aria-hidden="true"></svg
          ></a>
        </div>
        <div class="summary-grid">
          <div>
            <span class="muted">ทั้งหมด</span
            ><strong
              >{{ count(d.hospitals.total) }} <small>หน่วยงาน</small></strong
            >
          </div>
          <div>
            <span class="muted">เปิดใช้งาน</span
            ><strong>{{ count(d.hospitals.enabled) }}</strong>
          </div>
          <div>
            <span class="muted">เชื่อมต่อได้</span
            ><strong>{{ count(d.hospitals.online) }}</strong>
          </div>
          <div>
            <span class="muted">ระงับ / ยังไม่เปิด</span
            ><strong>{{ count(d.hospitals.disabled) }}</strong>
          </div>
        </div>
      </section>
      <p class="help overview-as-of">ข้อมูลภาพรวม ณ {{ thaiTime(d.as_of) }}</p>
    }
  `,
})
export class OverviewPage implements OnInit, OnDestroy {
  private api = inject(GatewayApi);
  readonly usageOnly = !!inject(ActivatedRoute).snapshot.data["usage"];
  readonly data = signal<Dashboard | null>(null);
  readonly loading = signal(false);
  readonly error = signal("");
  readonly count = count;
  readonly thaiTime = thaiTime;
  private version = 0;
  ready(value: Dashboard): boolean {
    return usageReady(value.usage.status);
  }
  ngOnInit(): void {
    void this.load();
  }
  async load(): Promise<void> {
    if (this.loading()) return;
    const version = ++this.version;
    this.loading.set(true);
    this.error.set("");
    try {
      const value = await this.api.get<Dashboard>("dashboard");
      if (version === this.version) this.data.set(value);
    } catch (e) {
      if (version === this.version)
        this.error.set(e instanceof Error ? e.message : "โหลดข้อมูลไม่สำเร็จ");
    } finally {
      if (version === this.version) this.loading.set(false);
    }
  }
  ngOnDestroy(): void {
    this.version++;
  }
}
