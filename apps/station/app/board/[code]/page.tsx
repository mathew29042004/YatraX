"use client";

// The public display: full-screen boards and spoken announcements, no sign-in. Put it on
// a screen on the concourse; tap once to let the browser play sound.
import { useParams } from "next/navigation";
import { Announcements } from "@/components/desk";
import { DisplayBoard } from "@/components/display-board";

export default function PublicBoard() {
  const { code } = useParams<{ code: string }>();
  const station = (code ?? "MAS").toUpperCase();
  return (
    <main className="min-h-screen bg-black">
      <h1 className="sr-only">Train arrivals and departures, {station}</h1>
      <DisplayBoard code={station} hours={4} big />
      <div className="mx-auto max-w-5xl p-4">
        <Announcements code={station} compact />
      </div>
    </main>
  );
}
