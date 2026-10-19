# Collection sync through Google Drive — design

Date: 2026-10-19
Status: draft, for the owner's review. Desktop side and the shared format;
the mobile app (E:\TCGScannerMobile) implements the same format later.

## Intent

The owner scans on the phone and manages, prices and exports on the PC.
Both apps must show one and the same collection, with no server of ours:
the app is open source, there is no infrastructure to run, and a user's
data stays in the user's own cloud account.

Decided with the owner (2026-10-19):

- **Full two-way share.** Whatever one app changes, the other sees:
  copies, custom collections, price references. Deletions included.
- **Google Drive as the transport, through a Google sign-in in the app**,
  not through a synced folder on disk. A visible folder `TCGScanner` in
  the user's Drive; the app only ever sees files it created there
  (`drive.file` scope). No hidden app folder.
- **Automatic.** Sync at launch and after each scan; no button to press
  in the normal case. A "Sync now" exists for the impatient.
- **Ask before merging** when a device connects and both it and Drive
  already hold cards. Merging is the expected answer; the question is a
  safety net.
- **The two apps must end up with the same features**; the mobile app's
  collections (M2) will follow this format. This document covers the
  desktop app and the format only.

## Goals

1. One collection across devices, converging without data loss, whatever
   the order in which devices go online.
2. Works offline: changes queue locally and leave at the next
   opportunity.
3. A user signs in once per device and never thinks about it again.
4. The owner registers the app with Google once; users do nothing.
5. A format the mobile app can implement with no SQLite or Go specifics:
   plain JSON Lines, documented below.

## Non-goals

- The mobile implementation (same format, its own design when M2 comes).
- Syncing settings, price guides, the scan language, the selected model.
- Real-time sync: a few seconds after a change, or at the next launch, is
  the target; not live.
- Other clouds. The journal format does not know about Drive; a "folder
  on disk" transport could be added later, it is not in this version.
- Journal compaction. An event is about 150 bytes; ten thousand changes
  are 1.5 MB, and Drive uploads the whole file: fine for years of a
  personal collection. Compaction is a later feature with a recorded
  snapshot format.
- Sharing one collection between two different Google accounts.

## Anti-goals (a failure even if it "works")

- A device overwriting another device's data. Each device writes only
  its own journal, ever.
- A merge that silently drops copies. Merging adds; the only removals
  are explicit `remove`/`delete` events.
- A secret the app cannot ship. Everything embedded in the binary is
  public by Google's own rules for installed apps.
- A sync that blocks the UI or the scan loop.

## How it works

### Each device keeps a journal, Drive holds all journals

Drive cannot append to a file: every upload replaces the whole file. So
one shared "collection file" would be overwritten by whichever device
uploads last, and Drive would litter "conflicted copy" files. Instead:

- Every device has a stable id and writes **one file of its own**,
  `journal-<device id>.jsonl`, in the `TCGScanner` folder. It never
  touches the others' files.
- The file is an **append-only log of events**: "copy added", "copy
  removed", "collection renamed"… one JSON object per line, in the order
  the device made them.
- **The collection is the fold of all journals.** Each app reads every
  journal, applies the events it has not applied yet, and keeps its own
  database as the materialised result.

Two devices never write the same file, so there is nothing to conflict
on. Offline changes are just lines that leave later. A removal is a line
like any other.

### Identity

Today's rows are numbered per database (`collections.id`,
`cards.id` AUTOINCREMENT). Two databases cannot agree on those. The
migration adds:

- `collections.uid TEXT` and `cards.uid TEXT`, UUID v4, unique index,
  filled for existing rows at migration time. The integer ids stay for
  the UI and the bindings; the uid is what crosses devices.
- `price_refs` is already keyed by `card_id`, which is global.
- The device: `settings.sync_device_id` (UUID v4, generated once) and
  `settings.sync_device_name` (the computer name, `DESKTOP-…`, shown to
  the user on other devices; the phone will send its model name).

### The journal format (shared with the mobile app)

File name: `journal-<device id>.jsonl`, UTF-8, one JSON object per line,
`\n` separated. Readers ignore unknown fields and unknown `op` values
(forward compatibility), and skip malformed lines with a log.

Line 1 is the device record; every later line is an event with a
per-device sequence number `seq` starting at 1 and increasing by 1, and
an `at` timestamp (RFC 3339, UTC, millisecond precision, from the
device's clock).

```jsonl
{"v":1,"op":"device","device":"3f2c…","name":"DESKTOP-BQPJU99","app":"desktop","at":"2026-10-19T14:02:11.120Z"}
{"v":1,"seq":1,"at":"2026-09-30T18:40:02.000Z","op":"collection.create","uid":"9a1e…","name":"To sell"}
{"v":1,"seq":2,"at":"2026-09-30T18:41:10.500Z","op":"copy.add","uid":"c0ff…","card_id":"OP01-006_p4","game":"onepiece","foil":true,"lang":"en","collection":"9a1e…","scanned_at":"2026-09-30T18:41:10.500Z","model":"tcg"}
{"v":1,"seq":3,"at":"2026-10-02T09:12:00.010Z","op":"copy.update","uid":"c0ff…","foil":false,"lang":"ja","collection":""}
{"v":1,"seq":4,"at":"2026-10-02T09:15:30.000Z","op":"copy.remove","uid":"c0ff…"}
{"v":1,"seq":5,"at":"2026-10-08T20:00:00.000Z","op":"price_ref.set","card_id":"OP01-006_p4","product":799296,"non_english":799297,"label":"Jolly Roger Foil (OP01-006j) · PRB-01: The Best"}
{"v":1,"seq":6,"at":"2026-10-08T20:05:00.000Z","op":"price_ref.clear","card_id":"OP01-006_p4"}
{"v":1,"seq":7,"at":"2026-10-09T10:00:00.000Z","op":"collection.rename","uid":"9a1e…","name":"For trade"}
{"v":1,"seq":8,"at":"2026-10-09T10:01:00.000Z","op":"collection.delete","uid":"9a1e…"}
```

Events and their fields (all required unless noted):

| op | fields | meaning |
|---|---|---|
| `collection.create` | `uid`, `name` | a custom collection appears |
| `collection.rename` | `uid`, `name` | |
| `collection.delete` | `uid` | its copies become unfiled (as the desktop does today with `ON DELETE SET NULL`) |
| `copy.add` | `uid`, `card_id`, `game`, `foil`, `lang`, `collection` (uid or `""`), `scanned_at`, `model` | a physical copy enters the collection |
| `copy.update` | `uid`, `card_id`, `game`, `foil`, `lang`, `collection` | the copy's full current values, not a delta; `card_id` changes when the user corrects a misread card by hand |
| `copy.remove` | `uid` | the copy leaves the collection |
| `price_ref.set` | `card_id`, `product`, `non_english`, `label` | the owner's Cardmarket product for that card id |
| `price_ref.clear` | `card_id` | |

`copy.update` carries every mutable field so that "last writer wins" is
a whole-copy decision and readers never merge field by field.

`game` is derived data: each app resolves it from `card_id` with its own
catalogue when it applies a `copy.add` or `copy.update`, and only falls
back to the event's `game` when it cannot. The desktop's startup
`backfillCardGames` therefore emits no event; it only repairs the local
derivation.

### Merge rules

The state is computed per entity: a custom collection by `uid`, a copy by
`uid`, a price reference by `card_id`.

1. Within one journal, events apply in `seq` order.
2. Across journals, two events on the same entity are ordered by `at`;
   the later one wins. Equal `at`: the higher device id wins (any fixed
   rule works, it only has to be the same everywhere).
3. **A removal is final.** Once a copy has a `copy.remove` (or a
   collection a `collection.delete`), later `update`/`rename` events for
   it are ignored, even with a later `at`. A copy that was removed on one
   device and edited on another while offline stays removed: the user
   removed a card from their collection on purpose, and the edit was to
   a card they no longer own. The desktop keeps a `sync_tombstones` table
   (`uid`, `deleted_at`) to enforce this across sync cycles.
4. `copy.add` for a uid already present is ignored (idempotent: a journal
   may be re-read from the start).
5. Device clocks are trusted. A device with a badly wrong clock can lose
   a "last writer wins" race it should have won; that is accepted and
   documented, not detected.

### The desktop's own journal lives in SQLite

To keep the file and the database from ever disagreeing, the desktop
appends its events in the **same transaction** as the row change, into a
`sync_events` table (`seq INTEGER PRIMARY KEY`, `at TEXT`, `line TEXT`).
The Drive file is rendered from this table at upload time. Nothing else
writes the journal.

Recording starts with the migration and never stops, connected or not:
the migration that adds the uids also writes the **bootstrap events**
into `sync_events`, from the rows that exist at that moment, in
chronological order: one `collection.create` per custom collection with
`at` = its `created_at`, one `copy.add` per card with `at` = its
`scanned_at`, one `price_ref.set` per reference with `at` = its
`chosen_at`. From then on every write path appends. So the journal is
always complete, `seq` numbers are never reused, and connecting to Drive
later, disconnecting, and reconnecting never rebuild anything: the
upload simply resumes after `uploaded_seq`. A PC that scanned for months
before connecting still orders naturally against a phone's later events,
because the bootstrap events carry the original dates.

The write paths that emit events: `createCollection`, `renameCollection`,
`deleteCollection`, `insertCard`, `updateCard`, `moveCardToCustom`,
`deleteCard`, `setPriceRef`, `clearPriceRef`. Applying another device's
journal goes through the same row functions with `emit = false`: a
pulled event is never re-emitted (otherwise two devices would echo each
other forever).

### The sync cycle (desktop)

Triggers: app start (once the database is open), the window regaining
focus (the frontend's `window` focus event calls `SyncNow`), a local
change (debounced: one cycle a few seconds after the last change), the
"Sync now" button, and, while the previous cycle failed, a retry timer.
One cycle runs at a time; triggers during a cycle coalesce into one more
cycle after it. Cycles run in a goroutine, never on the UI or scan path.

A cycle:

1. Make sure the access token is valid (refresh it with the stored
   refresh token; a refusal means the user revoked access: status
   "Disconnected, sign in again").
2. Find the `TCGScanner` folder (id cached in settings; re-found by name
   if the cache is stale; created if absent).
3. List the folder's `journal-*.jsonl` files with their `modifiedTime`.
4. For every file that is not this device's and whose `modifiedTime` is
   newer than the last one applied: download it, parse, apply every line
   with `seq` greater than the last applied `seq` for that device, in one
   transaction per file, and record the new `applied_seq` and
   `modifiedTime` in `sync_sources` (`device_id`, `name`, `app`,
   `file_id`, `applied_seq`, `modified_time`, `last_seen`). A file whose
   highest `seq` is lower than `applied_seq` (the device was reset) is
   re-applied from `seq` 1; the rules above make that safe.
5. If `sync_events` holds lines beyond `uploaded_seq`: render the whole
   journal and upload it (create the file the first time, update its
   content afterwards). Record `uploaded_seq`.
6. Emit `sync:status` to the frontend; if step 4 applied anything, also
   emit `collection:changed` so open views refetch (the collection
   hooks already refetch on their own events; this adds one trigger).

Failures: a network error leaves the status "Offline, will retry" and
nothing is marked applied or uploaded; the next trigger retries. A
malformed line is logged and skipped, never fatal. Drive API errors
(quota, 5xx) are reported in the status line with the HTTP reason.

### First connection and the merge question

"Connect Google Drive" runs the OAuth flow for an installed app: the
app opens the system browser on Google's consent page with a loopback
redirect (`http://127.0.0.1:<random port>/`) and PKCE, receives the code
on that port, exchanges it for tokens. Scopes: `drive.file`, `openid`,
`email` (the last two only to display which account is connected). The
refresh token is stored in `settings` (`sync_google_token`) in the local
database, which already holds the user's collection: same trust level,
and no OS keychain dependency in v1.

Then the app looks at the folder:

- **Drive holds no journal:** push this device's journal silently (it is
  complete since the migration, see above).
- **Drive holds journals and this device has no copies:** pull them, no
  question asked, then push this device's journal: custom collections
  and price references it may hold without any copy are shared like
  everything else.
- **Drive holds journals and this device has copies:** show the merge
  dialog before applying or uploading anything: "Your Drive already holds
  a collection: 312 copies from Pixel 8 and DESKTOP-OLD. This PC has 108
  copies. Merge both into one collection? Nothing is removed; copies
  present on both sides are kept as separate copies." Buttons: **Merge**
  (continue the cycle) and **Not now** (the app forgets the token and
  stays disconnected; the user can connect again later). No "replace"
  option in v1: it would be the only destructive path in the feature.

Disconnect: forget the token, the folder id, `sync_sources` and
`uploaded_seq`; keep the device id, the events and the data, and keep
recording events. Nothing on Drive is touched: the user deletes the
folder from Drive if they want.
Reconnecting the same device resumes where it left off; a reinstalled
app (new database) gets a new device id, and the previous journal of
that PC shows as one more device in the list.

### Desktop UI

The sidebar's **Settings** entry (today a "Soon" tag) opens a Settings
screen with one section in this version, **Sync with Google Drive**:

- Not connected: a sentence ("Keep this collection in sync with your
  other devices through a `TCGScanner` folder in your Google Drive. Only
  that folder is accessible to the app.") and **Connect Google Drive**.
- Connected: "Connected as florent@…", the status line ("Up to date ·
  12:04", "Syncing…", "Offline, will retry", "Sync failed: <reason>",
  "Disconnected, sign in again"), the devices seen in the folder with
  their last activity ("DESKTOP-BQPJU99 (this PC)", "Pixel 8 · last
  change 2 h ago"), and the buttons **Sync now** and **Disconnect**.

Nocturne tokens and the existing `n-*` classes, English strings, like
the rest of the app. Nothing elsewhere changes visibly except that
collection views refresh when a sync brings changes.

### Google side: the owner's one-time setup

In the Google Cloud console, once:

1. A project (any name), the Drive API enabled.
2. An OAuth consent screen, user type External, app name "TCGScanner",
   scopes `…/auth/drive.file`, `openid`, `email`. Publishing status set
   to **In production**: with only non-sensitive scopes Google requires
   no verification, so any Google account can sign in, with no user
   limit and no "unverified app" warning. (Check the current scope
   classification in the console at setup time; if Google reclassified
   `drive.file`, the fallback is the `drive.appdata` hidden folder.)
3. An OAuth client of type **Desktop app**. Its client id and client
   secret are committed in `sync_google.go`: Google documents that the
   secret of an installed app is not confidential, and PKCE protects the
   flow.
4. Later, for the mobile app: an **Android** client bound to the APK's
   signing key (or Play App Signing's key). A fork built with another
   key needs its own project; a user installing the owner's build does
   not.

### Privacy and security

- `drive.file` means the app sees only files it created. It cannot list
  or read the rest of the user's Drive.
- Journals contain card ids, finishes, languages, timestamps, custom
  collection names, device names. No account data, no prices.
- The refresh token is stored locally, next to the collection it
  protects. Disconnect deletes it.
- Nothing of ours is in the path: no server, no telemetry.

## Testing

Go, unit:
- journal encoding/decoding round trip, unknown ops and malformed lines
  skipped;
- the merge rules: last writer wins across devices, equal timestamps,
  removal final, idempotent add, re-applying a reset journal;
- the migration: uids filled and unique, bootstrap events written once
  from the existing rows in chronological order, idempotent on re-run;
- every write path emits exactly one event with the right fields, and
  applying a foreign journal emits none;
- a full sync cycle against a fake Drive (`httptest`): first connection
  in the three cases, pull, push, offline, token refusal.

Frontend: `tsc` + Vite build.

Live, by the owner: a second "device" without the phone is a second copy
of the built app run from another folder with its own `collection.db`,
signed in to the same account; scan on one, see it on the other after a
focus change; remove on one, gone on the other; the merge dialog on a
third copy that already has cards.

## Rollout

Desktop first, in this order: migration and uids; journal and events in
the write paths; merge/apply; Drive client and OAuth; the sync cycle;
the Settings screen. Then the owner's Google setup and the live pass.
The mobile app implements the same format in its M2 collections work.

## Left to the builder

- Drive access through plain `net/http` calls to the four endpoints
  (`files.list`, `files.create` multipart, `files.update` media,
  `files.get?alt=media`) rather than the Google client library, to keep
  the binary small; `golang.org/x/oauth2` for the token dance is fine.
- Debounce delay after a change, the retry timer's period, request
  timeouts.
- Exact wording of the status line and the dialog beyond the sentences
  above.
- How the Settings screen is laid out within the Nocturne shell.
