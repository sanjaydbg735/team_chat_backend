# Data Model

The schema lives in [`scripts/migrations`](../scripts/migrations) and is applied
in order:

- `000001_init.sql` — `conversations`, `messages`
- `000002_multi_channel.sql` — `users`, `channel_members`, and `name` /
  `created_by` columns on `conversations`

---

## Entity Relationship Diagram

```text
   ┌─────────────────────────┐                ┌──────────────────────────────┐
   │         users           │                │   conversations (channels)   │
   ├─────────────────────────┤                ├──────────────────────────────┤
   │ id           PK         │                │ id           PK              │
   │ username     UNIQUE     │                │ type         DM | GROUP      │
   │ created_at              │                │ name         (nullable)      │
   └───────────┬─────────────┘                │ created_by   (nullable)      │
               │                              │ created_at                   │
               │                              └───────────┬──────────────────┘
               │                                          │
               │ 1                                      1 │
               │                                          │
               │        ┌───────────────────────────┐    │
               │  M     │      channel_members       │  M │
               └───────▶│───────────────────────────│◀───┘
                        │ channel_id  PK, FK ────────┼──▶ conversations.id
                        │ user_id     PK, FK ────────┼──▶ users.id
                        │ joined_at                  │
                        └───────────────────────────┘

   ┌─────────────────────────┐
   │        messages         │     conversation_id  FK ──▶ conversations.id
   ├─────────────────────────┤     sender_id          ──▶ users.id (author)
   │ id            PK (Snow) │
   │ conversation_id  FK     │     Relationships:
   │ sender_id               │       users  1───M  channel_members  M───1  conversations
   │ content                 │       conversations 1───M  messages
   │ created_at              │       users 1───M  messages (authors)
   └─────────────────────────┘
```

Legend: `PK` = primary key, `FK` = foreign key, `1───M` = one-to-many.
A `channel_members` row is the M:N join between `users` and `conversations`.

> Naming note: the table is `conversations` for historical reasons; the domain
> model calls it `Channel`. They are the same concept. A **DM is a channel** of
> `type = 'DM'` with exactly two members — there is no separate DM table.

---

## Tables

### `users`
| Column | Type | Notes |
|---|---|---|
| `id` | `VARCHAR(64)` PK | Snowflake ID (stored as string) |
| `username` | `VARCHAR(100)` UNIQUE | enforced unique at DB + service layer |
| `created_at` | `TIMESTAMP` | default `CURRENT_TIMESTAMP` |

### `conversations` (channels)
| Column | Type | Notes |
|---|---|---|
| `id` | `VARCHAR(64)` PK | Snowflake ID |
| `type` | `ENUM('DM','GROUP')` | drives validation (DM ⇒ 2 members) |
| `name` | `VARCHAR(100)` NULL | display name; typically empty for DMs |
| `created_by` | `VARCHAR(64)` NULL | user ID of creator |
| `created_at` | `TIMESTAMP` | default `CURRENT_TIMESTAMP` |

### `channel_members` (membership join table)
| Column | Type | Notes |
|---|---|---|
| `channel_id` | `VARCHAR(64)` | FK → `conversations.id` |
| `user_id` | `VARCHAR(64)` | FK → `users.id` |
| `joined_at` | `TIMESTAMP` | default `CURRENT_TIMESTAMP` |
| — | PRIMARY KEY `(channel_id, user_id)` | prevents duplicate membership |
| — | INDEX `idx_user_channels (user_id)` | fast "channels for a user" lookup |

### `messages`
| Column | Type | Notes |
|---|---|---|
| `id` | `BIGINT UNSIGNED` PK | Snowflake ID (time-sortable) |
| `conversation_id` | `VARCHAR(64)` | FK → `conversations.id` |
| `sender_id` | `VARCHAR(64)` | author user ID |
| `content` | `TEXT` | message body |
| `created_at` | `TIMESTAMP(3)` | millisecond precision |
| — | INDEX `idx_conv_msg (conversation_id, id)` | delta-sync range scans |

---

## Index Rationale

| Index | Query it serves | Why it matters |
|---|---|---|
| `messages(conversation_id, id)` | `WHERE conversation_id=? AND id>? ORDER BY id LIMIT 100` | Delta-sync becomes an index-range scan; no full-table scan even at billions of rows. `id` being a time-sortable Snowflake means the index is also chronological. |
| `channel_members(user_id)` | `SELECT channel_id WHERE user_id=?` | Runs on **every WebSocket connect** to build the subscription set; must be O(log n). |
| `channel_members` PK `(channel_id, user_id)` | `IsMember(channel,user)` and dedupe | Authorization check on every message send. |
| `users(username)` UNIQUE | registration check | Prevents duplicate usernames atomically. |

---

## Why `VARCHAR` IDs instead of `BIGINT` everywhere?

`messages.id` is `BIGINT UNSIGNED` (pure numeric Snowflake, kept compact because
the messages table is the largest and most index-sensitive). `users` and
`conversations` use `VARCHAR(64)` IDs so they can carry human-readable prefixes
in the future (e.g. `usr_`, `chan_`) without a migration. The trade-off is a few
extra bytes per row on the smaller tables — negligible versus the flexibility.

---

## Roadmap (🟡)

- **Read replicas** for delta-sync reads to offload the primary.
- **Horizontal sharding** of `messages` by `conversation_id` once a single
  primary is saturated (the composite index already aligns with this shard key).
- **`last_read` per (user, channel)** to power unread badges and read receipts.
- **Soft deletes / edits** (`deleted_at`, `edited_at`) for message mutation.
