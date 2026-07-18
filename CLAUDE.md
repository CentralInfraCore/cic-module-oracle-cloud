# CLAUDE.md — cic-module-oracle-cloud

## Mi ez a repo

OCI (Oracle Cloud) provisioner-modul — az első a `cic-module-<provider>`
névtérben, és a first-party **referencia**, amit a jövőbeli harmadik feles
modulok követnek. Sandboxolt WASM guest, ami a relay capability-határán át éri el
a hálózatot és a titkokat.

A `base-repo` `wasm/main` template-jéből származtatva.

## Olvasd el session elején

A teljes terv a [`docs/design/`](docs/design/) alatt él:
- [`architecture.md`](docs/design/architecture.md) — bizalmi modell, rétegek,
  miért WASM (harmadik feles modulok), az OCI SDK build-time forrássá bontása
- [`roadmap.md`](docs/design/roadmap.md) — fázisok P0–P5, stabil darab-ID-kkal
- `specs/` — a provider ABI, a capability-manifest, a host-határ, az OCI
  séma-pipeline, a state-modell
- a döntési alap: [`theads/`](theads/)

## KRITIKUS — a CIC-Relay read-only innen

A `cic-module-oracle-cloud` a relay **capability-határára** épül, de a relay egy
**külön repo, amit ebből a repóból SOHA nem szerkesztünk.**

- Relay-hibát vagy -igényt találsz? **Ne javítsd a relayben, és ne kerüld meg
  azzal, hogy a relay logikáját ide duplikálod.** Írd fel a
  [`docs/design/relay-requirements.md`](docs/design/relay-requirements.md)-be
  (`R#` id-vel, a relay-forrásból vett bizonyítékkal), és jelezd a felhasználónak
  / a relay tulajdonosának (issue a `CIC-Relay`-ben).
- A specek a relayt **megfigyelésként** írják le (RO) — ha valamit állítasz a
  relayről, a tényleges forrásból ellenőrizd, ne grepből következtesd.
- Ha egy relay-hiány blokkol, az **jelzendő**, nem megkerülendő.

Ez a szabály azért van, mert a relay a bizalmi/custody-mag; a benne lévő
trust-flow (`cic_ffi_run_flow`), a airlock és az audit a rendszer bizonyítási
alapja. Ad-hoc módosítás vagy duplikáció aláásná.

## Munkamodell

- Munka a **`devel`** ágon; PR a **`main`**-re. A `main` a stabil/release ág.
- Minden commit Vault-aláírt (a közös `commit-msg` hook, `hooksPath`).
- A CI (`make check` + wasm build/test + `docs.link-check` + tesztek) minden
  pusholásra fut; `docs/**` belső linkek feloldódjanak.
- `MANIFEST.sha256` minden commit után regenerálandó (a `manifest-verify` gate).

## Nyelv

- A körülvevő gépezet **túlnyomórészt Go**: a wazero host (relay), a template, az
  OCI séma-extractor (`go/ast`), a verifikáció. A guest-modul nyelve elvben nyílt
  (a host-függvény ABI nyelvfüggetlen), de a first-party referencia a template
  miatt észszerűen Go/TinyGo. Harmadik felek bármit hozhatnak, ami WASM-ra fordul.
- Dokumentáció/kód: a repo docs-a angol (a template öröksége); ez a `CLAUDE.md`
  magyar (Claude-utasítás), az ökoszisztéma-konvenció szerint.

## Ökoszisztéma-kontextus

A szülő `CIC/CLAUDE.md` (ha jelen van a workspace-ben) tartalmazza a boot
sequence-t, a háromszintű státuszt (implemented/scaffold/concept) és a reasoning
módokat. Azt olvasd először. Ez a repo az ottani `cic-graph` MCP KB-ból
ellenőrizhető — állítás előtt node/file-szintű alátámasztás.
