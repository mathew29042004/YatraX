# YatraX TTE app

An Expo (React Native) app for travelling ticket examiners, offline-first. See the
"TTE app (Phase 6)" section of the [project README](../../README.md).

```bash
pnpm --filter @yatrax/tte dev     # web build on http://localhost:3005
pnpm --filter @yatrax/tte start   # Expo Go on a phone; set EXPO_PUBLIC_API_URL=http://<LAN IP>:8080
```

- `src/logic.ts`: the offline rules (board, no-show after two stations, RAC → berth, fares), mirroring the server
- `src/ticket.ts`: Ed25519 QR ticket verification with the bundle's public key
- `src/store.tsx`: session, downloaded bundle and the outbox of operations, kept in AsyncStorage; syncs when online
- `src/screens/`: sign-in and duties, and the run screen (chart, scan, RAC, fine, more)

The demo TTE is `TTE00001` / `yatrax123` (synthetic data).
