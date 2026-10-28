"use client";

import type { SavedPassenger } from "@yatrax/api-client";
import { ApiError } from "@yatrax/api-client";
import { Alert, Button, Card, CardTitle, Input, useYatrax } from "@yatrax/ui";
import { useEffect, useState, type FormEvent } from "react";

/** The passenger's saved co-travellers, offered on the booking form. */
export function SavedPassengers() {
  const { client } = useYatrax();
  const [list, setList] = useState<SavedPassenger[]>([]);
  const [name, setName] = useState("");
  const [age, setAge] = useState(30);
  const [gender, setGender] = useState<SavedPassenger["gender"]>("F");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    client.savedPassengers().then(setList).catch(() => setList([]));
  }, [client]);

  async function add(e: FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      const p = await client.addSavedPassenger({ name, age, gender, berth_pref: "" });
      setList([...list, p]);
      setName("");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not save");
    }
  }

  return (
    <Card>
      <CardTitle hint="Offered when you book">Saved passengers</CardTitle>
      <ul className="mb-3 divide-y divide-line text-sm">
        {list.map((p) => (
          <li key={p.id} className="flex items-center justify-between py-1.5">
            <span>
              {p.name} <span className="text-muted">· {p.age}{p.gender}</span>
            </span>
            <button
              onClick={async () => {
                await client.deleteSavedPassenger(p.id);
                setList(list.filter((x) => x.id !== p.id));
              }}
              className="text-xs text-muted hover:text-danger"
            >
              Remove
            </button>
          </li>
        ))}
        {list.length === 0 && <li className="py-1.5 text-muted">None yet.</li>}
      </ul>
      <form onSubmit={add} className="grid grid-cols-[1fr_5rem_6rem_auto] gap-2">
        <Input aria-label="Name" placeholder="Name" value={name} onChange={(e) => setName(e.target.value)} />
        <Input aria-label="Age" type="number" min={5} max={125} value={age} onChange={(e) => setAge(Number(e.target.value))} />
        <select aria-label="Gender" value={gender} onChange={(e) => setGender(e.target.value as SavedPassenger["gender"])} className="h-10 rounded-lg border border-line bg-surface px-2 text-sm">
          <option value="F">Female</option>
          <option value="M">Male</option>
          <option value="O">Other</option>
        </select>
        <Button type="submit" variant="secondary">
          Save
        </Button>
      </form>
      {error && (
        <div className="mt-2">
          <Alert>{error}</Alert>
        </div>
      )}
    </Card>
  );
}
