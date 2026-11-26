import { Component, OnDestroy, OnInit, inject, signal } from "@angular/core";
import { FormsModule } from "@angular/forms";
import { GatewayApi } from "../../core/api.service";
import { PagedList } from "../../core/paged-list";
import { LoadMoreDirective } from "../../core/load-more.directive";
import { count, thaiDate, thaiTime } from "../../core/format";
import {
  AuditEvent,
  auditDateBounds,
  auditLabels,
  auditReasons,
} from "./management.models";

@Component({
  selector: "gw-history-page",
  standalone: true,
  imports: [FormsModule, LoadMoreDirective],
  templateUrl: "./history.page.html",
})
export class HistoryPage implements OnInit, OnDestroy {
  private readonly api = inject(GatewayApi);
  readonly list = new PagedList<AuditEvent>(this.api, "audit", "events");
  readonly actions = Object.entries(auditLabels).filter(
    ([key]) => key !== "created",
  );
  readonly dateError = signal("");
  readonly formatTime = thaiTime;
  readonly formatDate = thaiDate;
  readonly formatCount = count;
  readonly actionLabels = auditLabels;
  readonly reasons = auditReasons;
  query = "";
  action = "";
  start = "";
  end = "";
  private timer?: ReturnType<typeof setTimeout>;

  ngOnInit(): void {
    this.list.reset();
  }
  ngOnDestroy(): void {
    clearTimeout(this.timer);
    this.list.destroy();
  }

  searchChanged(): void {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => this.applyFilters(), 300);
  }

  applyFilters(): void {
    clearTimeout(this.timer);
    this.dateError.set("");
    if (this.start && this.end && this.start > this.end) {
      this.dateError.set("วันสิ้นสุดต้องไม่อยู่ก่อนวันเริ่มต้น");
      return;
    }
    this.list.reset({
      q: this.query.trim(),
      action: this.action,
      ...auditDateBounds(this.start, this.end),
    });
  }
}
