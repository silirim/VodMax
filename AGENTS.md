# Working on VodMax

Instructions for anyone changing this repository, people and coding agents
alike. Read [docs/direzione.md](docs/direzione.md) (what VodMax is, its
principles, scenarios and direction) and the
[decision records](docs/decisioni/README.md) before proposing a feature.
Code structure and the HTTP API: [docs/sviluppo.md](docs/sviluppo.md).

## Hard rules

- **The provider through the subscription's connection.** Every request
  to a playlist provider goes through `internal/provider` and
  `Service.tunnel`. Never add another HTTP client towards providers, never
  fall back from a VPN to the system network. A subscription without a VPN
  uses the normal network only when "only through a VPN" is off
  (decisions 0012 and 0039).
- **No real data in the repository**: no provider URLs or names, no
  credentials, no private addresses, no home host names, no personal
  catalogues. Tests use fake providers (`httptest`) and synthetic data.
  `gitleaks` runs in CI; check your diff for addresses before pushing.
- **Secrets stay sealed.** Credentials and API keys are encrypted with
  `secret.Box` and never returned by the API (only "configured: yes/no").
  Never log them; errors from provider clients are stripped of URLs.
- **No content, no telemetry.** VodMax never supplies or suggests playlists,
  channels or providers, and never phones home except for what the admin
  turns on (update check on GitHub, TMDB with their key, an online
  assistant with their key).
- **Public wording**: the name, logo, site, README and release notes do not
  use the word "IPTV"; talk about playlists, TV, films and series.
- **The model proposes, VodMax checks.** A language model only picks among
  codes VodMax gives it; rules are saved by the person after checking the
  form; titles for "a film with…" are checked on their detail page. Never
  send credentials, provider addresses or device data to a model.

## Code

- **Go** (version in `go.mod`), module `github.com/silirim/vodmax`, one
  binary `cmd/vodmax-server`, SQLite in `internal/store`. Keep it lean:
  heavy parts (assistant engine and models, GPU drivers) are downloaded
  only when the admin turns them on.
- Comments in English, explaining why rather than what, at the density of
  the surrounding code. Match the naming and idioms of the file you touch.
- **Web interface** (`internal/web/static`): plain HTML, CSS and JavaScript,
  no framework, no CDN, no build step. Everything must work with a remote
  control: elements are `focusable`, arrows move between them, OK and Back
  work. Test key presses, not only clicks. Text inputs that submit get
  `enterkeyhint` (Android keyboards otherwise close without sending).
  The CSS targets webOS 22+ and old Android WebViews: long text must wrap
  (`overflow-wrap` plus `word-break`).
- **Texts are written in Italian in the code** and translated through flat
  JSON files keyed by the Italian text: `internal/web/static/i18n/` for the
  interface (`t("…")`), `internal/service/i18n/` for server messages
  (`invalid(…)`, `errors.New(…)`, `T(ctx, …)`). Add every new text to
  `it.json` (value = key) and `en.json`; tests fail otherwise. Keep `{name}`
  placeholders and `%s %d %v %w` verbs.
- **Database**: migrations are append-only in `internal/store/store.go`;
  never edit an existing one. A migration that rebuilds a parent table is
  listed in `noForeignKeys`. Caches that rebuild themselves (metadata,
  people index) are not part of backups.
- An optional feature that is off leaves no trace in the interface.

## Checks

```sh
gofmt -l .            # must print nothing
go vet ./...
go test -race ./...
```

CI runs the same plus gitleaks. Never commit or push with a failing test:
gate the commit on the test exit code. Interface changes are also checked
in a browser at TV and phone widths (no horizontal overflow).

## Decisions

A change people will notice (a feature, a default, something that leaves
the house) gets a record in `docs/decisioni/NNNN-titolo.md`, in Italian:
problem, decision, what is left out ("Non ancora"). Status **Proposta**
until the owner approves it, then **Approvata** with the date and the words
of the approval. Add it to the index in `docs/decisioni/README.md`, and
update `docs/direzione.md` when the direction changes.

## Commits

- Subject in Italian, saying what changes for the people using VodMax
  ("Stasera: cerca nel catalogo quello che chiedi"); body with the reason
  and anything surprising.
- One topic per commit; tests and docs in the same commit as the code.

## Releases

Versions are `X.Y.Z`, betas `X.Y.Z-beta.N` (offered only to who opts in).

1. Bump `versionCode` and `versionName` in `clients/android/app/build.gradle`
   (the webOS `appinfo.json` keeps the numeric `X.Y.Z`).
2. `scripts/release.sh X.Y.Z[-beta.N]` builds `dist/<version>/`: server
   archives for amd64, arm64 and armv7, the signed APK, the webOS package and
   `SHA256SUMS`. It needs Go, JDK 17 and the Android SDK/NDK, and Node for
   webOS.
3. Check the APK signature with `apksigner verify --print-certs` (it must be
   the release key, not the debug key) and `sha256sum -c SHA256SUMS`.
4. Look at the diff since the previous tag for addresses or secrets.
5. Commit "Versione X", annotated tag `vX`, push `main` and the tag. CI
   builds and publishes the Docker image for the tag.
6. GitHub release (pre-release for betas) with the files of `dist/<version>/`.
   Notes in English, then `---` and a short Italian part titled
   **In breve**. Say when the database schema changes ("a backup first is
   wise").

Servers update with `vodmax-update` (stable releases only; a beta by naming
its tag). Android apps update themselves from GitHub releases.
