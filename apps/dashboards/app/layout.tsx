import type { Metadata } from "next";
import type { ReactNode } from "react";
import { app } from "./app";
import { Providers } from "./providers";
import "./globals.css";

export const metadata: Metadata = {
  title: `YatraX ${app.name}`,
  description: app.tagline,
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
