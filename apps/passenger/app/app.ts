import type { AppIdentity } from "@yatrax/ui";

export const app: AppIdentity = {
  name: "Passenger",
  tagline: "Book, track and travel",
  roles: ["passenger"],
  // Search is public, so sign-in moves off the home page.
  signInPath: "/login",
  homePath: "/account",
};

export const apiUrl = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
