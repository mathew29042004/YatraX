// Small UI kit in the YatraX colours (the web apps share theme tokens; these mirror them).
import type { ReactNode } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, TextInput, View, type TextInputProps, type ViewStyle } from "react-native";

export const color = {
  ink: "#0f172a",
  muted: "#64748b",
  line: "#e2e8f0",
  surface: "#ffffff",
  canvas: "#f5f7fb",
  accent: "#0f766e", // TTE app: teal
  accentSoft: "#ccfbf1",
  ok: "#15803d",
  okSoft: "#dcfce7",
  warn: "#b45309",
  warnSoft: "#fff1d6",
  danger: "#b91c1c",
  dangerSoft: "#fee2e2",
};

export function Card({ children, style }: { children: ReactNode; style?: ViewStyle }) {
  return <View style={[s.card, style]}>{children}</View>;
}

export function H({ children }: { children: ReactNode }) {
  return <Text style={s.h}>{children}</Text>;
}

export function Muted({ children }: { children: ReactNode }) {
  return <Text style={s.muted}>{children}</Text>;
}

export function Button({ title, onPress, kind = "primary", disabled, busy, small }: {
  title: string; onPress: () => void; kind?: "primary" | "secondary" | "danger" | "ghost"; disabled?: boolean; busy?: boolean; small?: boolean;
}) {
  const bg = { primary: color.accent, secondary: color.surface, danger: color.danger, ghost: "transparent" }[kind];
  const fg = kind === "primary" || kind === "danger" ? "#fff" : kind === "ghost" ? color.accent : color.ink;
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={title}
      accessibilityState={{ disabled: !!(disabled || busy), busy: !!busy }}
      onPress={onPress}
      disabled={disabled || busy}
      style={({ pressed }) => [s.btn, small && s.btnSmall, { backgroundColor: bg, opacity: disabled ? 0.45 : pressed ? 0.8 : 1 },
        kind === "secondary" && { borderWidth: 1, borderColor: color.line }]}
    >
      {busy ? <ActivityIndicator color={fg} /> : <Text style={[s.btnText, small && { fontSize: 13 }, { color: fg }]}>{title}</Text>}
    </Pressable>
  );
}

export function Field({ label, ...props }: TextInputProps & { label: string }) {
  return (
    <View style={{ marginBottom: 10 }}>
      <Text style={s.label}>{label}</Text>
      <TextInput placeholderTextColor={color.muted} style={s.input} accessibilityLabel={label} {...props} />
    </View>
  );
}

export function Pill({ text, tone = "neutral" }: { text: string; tone?: "neutral" | "ok" | "warn" | "danger" | "accent" }) {
  const [bg, fg] = {
    neutral: [color.canvas, color.muted], ok: [color.okSoft, color.ok], warn: [color.warnSoft, color.warn],
    danger: [color.dangerSoft, color.danger], accent: [color.accentSoft, color.accent],
  }[tone];
  return (
    <View style={[s.pill, { backgroundColor: bg }]}>
      <Text style={[s.pillText, { color: fg }]}>{text}</Text>
    </View>
  );
}

export function Chips<T extends string | number>({ items, value, onChange, label }: {
  items: Array<{ key: T; label: string }>; value: T; onChange: (v: T) => void; label?: (k: T) => string;
}) {
  return (
    <View style={s.chips}>
      {items.map((it) => (
        <Pressable key={String(it.key)} onPress={() => onChange(it.key)} accessibilityRole="button" accessibilityLabel={label ? label(it.key) : it.label}
          style={[s.chip, it.key === value && { backgroundColor: color.accent, borderColor: color.accent }]}>
          <Text style={[s.chipText, it.key === value && { color: "#fff" }]}>{it.label}</Text>
        </Pressable>
      ))}
    </View>
  );
}

export const s = StyleSheet.create({
  card: { backgroundColor: color.surface, borderRadius: 12, borderWidth: 1, borderColor: color.line, padding: 14, marginBottom: 12 },
  h: { fontSize: 16, fontWeight: "600", color: color.ink, marginBottom: 6 },
  muted: { fontSize: 13, color: color.muted },
  btn: { borderRadius: 8, paddingVertical: 11, paddingHorizontal: 14, alignItems: "center", justifyContent: "center" },
  btnSmall: { paddingVertical: 7, paddingHorizontal: 10 },
  btnText: { fontSize: 15, fontWeight: "600" },
  label: { fontSize: 12, fontWeight: "600", color: color.muted, marginBottom: 4, textTransform: "uppercase" },
  input: { borderWidth: 1, borderColor: color.line, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 9, fontSize: 15, color: color.ink, backgroundColor: color.surface },
  pill: { borderRadius: 6, paddingHorizontal: 7, paddingVertical: 2, alignSelf: "flex-start" },
  pillText: { fontSize: 12, fontWeight: "600" },
  chips: { flexDirection: "row", flexWrap: "wrap", gap: 6, marginBottom: 10 },
  chip: { borderWidth: 1, borderColor: color.line, borderRadius: 999, paddingHorizontal: 10, paddingVertical: 5, backgroundColor: color.surface },
  chipText: { fontSize: 13, color: color.ink, fontWeight: "500" },
  row: { flexDirection: "row", alignItems: "center", gap: 8 },
});
