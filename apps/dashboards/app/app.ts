import type { AppIdentity } from "@yatrax/ui";

export const app: AppIdentity = {
  name: "Staff Portal",
  tagline: "Zone and division dashboards",
  roles: ["tte", "station", "vendor", "controller"],
};

export const apiUrl = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
