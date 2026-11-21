// Strings of the Settings screen. Kept out of en.ts: only lazy screens use them
// (the initial bundle has a size budget).
export const ss = {
  title: "Settings",
  stats: "Usage",
  running: "Running",
  queued: "Queued",
  waiting: "Waiting",
  costToday: "Cost today",
  costMonth: "Cost this month",
  about: "About",
  version: "App version",
  backend: "Backend",
  healthy: "Healthy",
  unhealthy: "Unhealthy",
  session: "Session",
} as const;
