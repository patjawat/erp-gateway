import { Component, inject, OnInit, signal } from "@angular/core";
import { FormsModule } from "@angular/forms";
import { RouterLink, RouterLinkActive, RouterOutlet } from "@angular/router";
import {
  LucideWaypoints,
  LucideSunMoon,
  LucideLogOut,
  LucideLayoutDashboard,
  LucideBuilding,
  LucideChartNoAxesCombined,
  LucideShieldCheck,
  LucideHistory,
  LucideSettings2,
} from "@lucide/angular";
import { GatewayApi } from "./core/api.service";
@Component({
  selector: "app-root",
  standalone: true,
  imports: [
    FormsModule,
    RouterLink,
    RouterLinkActive,
    RouterOutlet,
    LucideWaypoints,
    LucideSunMoon,
    LucideLogOut,
    LucideLayoutDashboard,
    LucideBuilding,
    LucideChartNoAxesCombined,
    LucideShieldCheck,
    LucideHistory,
    LucideSettings2,
  ],
  templateUrl: "./app.html",
})
export class App implements OnInit {
  readonly api = inject(GatewayApi);
  readonly mode = signal<"light" | "dark">(this.savedMode());
  readonly busy = signal(false);
  readonly error = signal("");
  username = "";
  password = "";
  constructor() {
    this.applyMode();
  }
  ngOnInit(): void {
    void this.api.check();
  }
  private savedMode(): "light" | "dark" {
    try {
      return localStorage.getItem("gateway-appearance") === "dark"
        ? "dark"
        : "light";
    } catch {
      return "light";
    }
  }
  private applyMode(): void {
    document.documentElement.dataset["theme"] = this.mode();
    document.documentElement.style.colorScheme = this.mode();
  }
  toggleMode(): void {
    this.mode.update((v) => (v === "light" ? "dark" : "light"));
    this.applyMode();
    try {
      localStorage.setItem("gateway-appearance", this.mode());
    } catch {}
  }
  async login(): Promise<void> {
    if (this.busy()) return;
    this.error.set("");
    if (!this.username.trim() || !this.password) {
      this.error.set("กรอกชื่อผู้ใช้และรหัสผ่าน");
      return;
    }
    this.busy.set(true);
    try {
      await this.api.login(this.username.trim(), this.password);
      this.password = "";
    } catch (e) {
      this.error.set(e instanceof Error ? e.message : "เข้าสู่ระบบไม่สำเร็จ");
    } finally {
      this.busy.set(false);
    }
  }
  async logout(): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true);
    this.error.set("");
    try {
      await this.api.logout();
    } catch (e) {
      this.error.set(e instanceof Error ? e.message : "ออกจากระบบไม่สำเร็จ");
    } finally {
      this.password = "";
      this.busy.set(false);
    }
  }
}
