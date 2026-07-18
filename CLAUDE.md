# CLAUDE.md — cic-module-oracle-cloud

## Mi ez a repo

OCI (Oracle Cloud) provisioner-modul — az első a `cic-module-<provider>`
névtérben, és a first-party **referencia**, amit a jövőbeli harmadik feles
modulok követnek. Sandboxolt WASM guest, ami a relay capability-határán át éri el
a hálózatot és a titkokat.

A `base-repo` `wasm/main` template-jéből származtatva.

## Ki üzemelteti — a folyamat NEM a workdir orchestrátoré

Ezt a repót (és a `cic-module-*` testvéreket) a **`cic-module-wasm-claude`**
agent építi. Az **operatív folyamat és a szabálykészlet ott él**, nem itt és nem a
`workdir` (cic-factory) orchestrátornál:
`~/.claude-personal/agents/cic-module-wasm-claude/CLAUDE.md`.

- A `workdir` orchestrátor job-specet gyárt, review-l, merge-el — az egy másik
  folyamat. Ez a repo a `cic-module-wasm-claude` agent modul-építő munkájáé.
- Az alábbi szabályok a repo-helyi láthatóság kedvéért itt is szerepelnek, de az
  autoritatív forrás az agent `CLAUDE.md`-je.

## Olvasd el session elején

A teljes terv a [`docs/design/`](docs/design/) alatt:
- [`architecture.md`](docs/design/architecture.md) — bizalmi modell, rétegek,
  miért WASM (harmadik feles modulok), az OCI SDK build-time forrássá bontása
- [`roadmap.md`](docs/design/roadmap.md) — fázisok P0–P5, stabil darab-ID-kkal
- `specs/` — provider ABI, capability-manifest, host-határ, OCI séma-pipeline,
  state-modell
- [`docs/design/relay-requirements.md`](docs/design/relay-requirements.md) — a
  relay felé jelzett hiányok (RO, lásd lent)
- a döntési alap: [`theads/`](theads/)

## KRITIKUS — a CIC-Relay read-only innen

A modul a relay **capability-határára** épül (trust-flow, airlock, audit,
ProofTrace), de a **CIC-Relay külön repo, amit innen SOHA nem szerkesztünk.**

- Relay-hibát/-igényt találsz → **ne javítsd a relayben, és ne duplikáld a relay
  logikáját ide, hogy megkerüld.** Írd a
  [`docs/design/relay-requirements.md`](docs/design/relay-requirements.md)-be
  (`R#` id, relay-forrásból vett bizonyíték), és jelezd.
- **Relay-állítás előtt a forrást olvasd, ne grepből következtess.**
- Blokkoló relay-hiány **jelzendő**, nem megkerülendő.

## Munkamodell (részletesen az agent CLAUDE.md-ben)

- `devel` ág → PR a `main`-re; a `main` a stabil/release ág.
- Minden commit Vault-aláírt; `MANIFEST.sha256` commit után regenerálandó.
- CI minden pusholásra: `make check` + wasm build/test + `docs.link-check` +
  tesztek; a `docs/**` belső linkek oldódjanak fel.

## Nyelv

Túlnyomórészt **Go** (wazero host, template, OCI extractor, verifikáció); a
guest-nyelv elvben nyílt, a referencia a template miatt Go/TinyGo. A repo docs-a
angol (template-örökség); ez a `CLAUDE.md` magyar (Claude-utasítás).
