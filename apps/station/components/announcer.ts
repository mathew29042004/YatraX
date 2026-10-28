"use client";

// Speaks announcements the way stations do: a chime, then each language in turn, using
// the browser's speech synthesis. A language is spoken only if the device has a voice for
// it (Windows, Android and macOS ship Indian English and Hindi; Tamil and Malayalam voices
// depend on the device) — otherwise its text is shown and the next language plays.
import type { Announcement, AnnouncementLine } from "@yatrax/api-client";
import { useCallback, useEffect, useRef, useState } from "react";

function chime(ctx: AudioContext) {
  // Three rising tones, like the jingle before an Indian Railways announcement.
  const notes = [659, 784, 988];
  notes.forEach((f, i) => {
    const o = ctx.createOscillator();
    const g = ctx.createGain();
    o.type = "sine";
    o.frequency.value = f;
    const t = ctx.currentTime + i * 0.32;
    g.gain.setValueAtTime(0, t);
    g.gain.linearRampToValueAtTime(0.25, t + 0.03);
    g.gain.exponentialRampToValueAtTime(0.001, t + 0.6);
    o.connect(g).connect(ctx.destination);
    o.start(t);
    o.stop(t + 0.65);
  });
  return new Promise((r) => setTimeout(r, notes.length * 320 + 500));
}

function pickVoice(voices: SpeechSynthesisVoice[], bcp47: string) {
  const lang = bcp47.split("-")[0]!;
  return (
    voices.find((v) => v.lang.toLowerCase() === bcp47.toLowerCase()) ??
    voices.find((v) => v.lang.toLowerCase().startsWith(lang + "-")) ??
    voices.find((v) => v.lang.toLowerCase() === lang)
  );
}

export function useAnnouncer() {
  const [enabled, setEnabled] = useState(false);
  const [speaking, setSpeaking] = useState<{ id: string; lang: string } | null>(null);
  const [voices, setVoices] = useState<SpeechSynthesisVoice[]>([]);
  const queue = useRef<Announcement[]>([]);
  const busy = useRef(false);
  const audio = useRef<AudioContext | null>(null);

  useEffect(() => {
    if (typeof window === "undefined" || !("speechSynthesis" in window)) return;
    const load = () => setVoices(window.speechSynthesis.getVoices());
    load();
    window.speechSynthesis.addEventListener("voiceschanged", load);
    return () => window.speechSynthesis.removeEventListener("voiceschanged", load);
  }, []);

  const hasVoice = useCallback((bcp47: string) => !!pickVoice(voices, bcp47), [voices]);

  const sayLine = (l: AnnouncementLine) =>
    new Promise<void>((resolve) => {
      const v = pickVoice(window.speechSynthesis.getVoices(), l.bcp47);
      if (!v) return resolve(); // no voice for this language on this device: the text is on screen
      const u = new SpeechSynthesisUtterance(l.speech);
      u.voice = v;
      u.lang = v.lang;
      u.rate = 0.92;
      u.onend = () => resolve();
      u.onerror = () => resolve();
      window.speechSynthesis.speak(u);
    });

  const drain = useCallback(async () => {
    if (busy.current) return;
    busy.current = true;
    while (queue.current.length) {
      const a = queue.current.shift()!;
      if (audio.current) await chime(audio.current);
      for (const l of a.lines) {
        setSpeaking({ id: a.id, lang: l.lang });
        await sayLine(l);
      }
      setSpeaking(null);
    }
    busy.current = false;
  }, []);

  /** Browsers only allow sound after a click, so the station master switches announcements on. */
  const enable = () => {
    audio.current ??= new AudioContext();
    audio.current.resume();
    setEnabled(true);
  };
  const disable = () => {
    setEnabled(false);
    queue.current = [];
    if (typeof window !== "undefined") window.speechSynthesis?.cancel();
  };

  const announce = useCallback(
    (a: Announcement) => {
      queue.current.push(a);
      drain();
    },
    [drain],
  );

  return { enabled, enable, disable, announce, speaking, hasVoice, supported: typeof window !== "undefined" && "speechSynthesis" in window };
}

export const langName: Record<string, string> = {
  en: "English", hi: "Hindi", ta: "Tamil", ml: "Malayalam", te: "Telugu", kn: "Kannada", mr: "Marathi", bn: "Bengali",
  gu: "Gujarati", pa: "Punjabi", or: "Odia", as: "Assamese", ur: "Urdu", kok: "Konkani",
};
