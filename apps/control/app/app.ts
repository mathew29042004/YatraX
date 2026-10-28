import type { AppIdentity } from "@yatrax/ui";

export const app: AppIdentity = {
  name: "Control Centre",
  tagline: "Live section control for the network",
  roles: ["controller"],
};

export const apiUrl = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
