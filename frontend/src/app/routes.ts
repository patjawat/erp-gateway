import { Routes } from "@angular/router";
export const routes: Routes = [
  { path: "", pathMatch: "full", redirectTo: "overview" },
  {
    path: "overview",
    loadComponent: () =>
      import("./pages/overview.page").then((m) => m.OverviewPage),
  },
  {
    path: "hospitals",
    loadComponent: () =>
      import("./pages/hospitals/hospitals.page").then((m) => m.HospitalsPage),
  },
  {
    path: "usage",
    loadComponent: () =>
      import("./pages/overview.page").then((m) => m.OverviewPage),
    data: { usage: true },
  },
  {
    path: "certificates",
    loadComponent: () =>
      import("./pages/management/certificates.page").then(
        (m) => m.CertificatesPage,
      ),
  },
  {
    path: "history",
    loadComponent: () =>
      import("./pages/management/history.page").then((m) => m.HistoryPage),
  },
  {
    path: "backups",
    loadComponent: () =>
      import("./pages/management/backups.page").then((m) => m.BackupsPage),
  },
  {
    path: "settings",
    loadComponent: () =>
      import("./pages/management/settings.page").then((m) => m.SettingsPage),
  },
  { path: "**", redirectTo: "overview" },
];
