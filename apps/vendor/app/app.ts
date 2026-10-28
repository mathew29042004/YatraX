import type { AppIdentity } from "@yatrax/ui";

export const app: AppIdentity = {
  name: "Catering",
  tagline: "Meals to the berth, on time",
  roles: ["vendor"],
};

export const apiUrl = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
