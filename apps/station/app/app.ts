import type { AppIdentity } from "@yatrax/ui";

export const app: AppIdentity = {
  name: "Station",
  tagline: "Boards, platforms and announcements",
  roles: ["station"],
};

export const apiUrl = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
