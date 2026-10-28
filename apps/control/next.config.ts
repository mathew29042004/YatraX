import type { NextConfig } from "next";

const config: NextConfig = {
  transpilePackages: ["@yatrax/ui", "@yatrax/api-client"],
  reactStrictMode: true,
  // Separate dev output so `pnpm build` can run while `pnpm dev` is serving.
  distDir: process.env.NODE_ENV === "development" ? ".next-dev" : ".next",
};

export default config;
