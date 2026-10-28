"use client";

import type { Quota, SearchParams } from "@yatrax/api-client";
import { Alert, Spinner } from "@yatrax/ui";
import { useSearchParams } from "next/navigation";
import { Suspense } from "react";
import { Results } from "@/components/results";
import { SearchForm } from "@/components/search-form";
import { SiteHeader } from "@/components/site-header";

function SearchPage() {
  const qs = useSearchParams();
  const params: SearchParams = {
    from: qs.get("from") ?? "",
    to: qs.get("to") ?? "",
    date: qs.get("date") ?? "",
    quota: (qs.get("quota") as Quota) ?? "GN",
    class: qs.get("class") ?? "",
    nearby: qs.get("nearby") === "1",
    flex: Number(qs.get("flex") ?? 0),
  };
  const valid = params.from && params.to && params.date;
  return (
    <main id="main" tabIndex={-1} className="mx-auto max-w-6xl space-y-5 px-4 py-6">
      {/* key: remount the form when the URL changes so it shows the new search */}
      <SearchForm key={`form-${qs}`} initial={params} compact />
      {valid ? <Results key={`results-${qs}`} params={params} /> : <Alert>Pick a From station, a To station and a date.</Alert>}
    </main>
  );
}

export default function Page() {
  return (
    <>
      <SiteHeader />
      <Suspense fallback={<div className="p-6"><Spinner /></div>}>
        <SearchPage />
      </Suspense>
    </>
  );
}
