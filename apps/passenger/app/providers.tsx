"use client";

import { I18nProvider, SessionProvider } from "@yatrax/ui";
import type { ReactNode } from "react";
import { apiUrl, app } from "./app";

export function Providers({ children }: { children: ReactNode }) {
  return (
    <SessionProvider apiUrl={apiUrl} app={app}>
      <I18nProvider>{children}</I18nProvider>
    </SessionProvider>
  );
}
