# Library Book Management API

> A production-quality REST API for library book management built with **Go**, **Gin**, **PostgreSQL**, and **GORM**.  
> Layered clean architecture · Transactional checkout/return · FIFO reservation queue · Fine calculation · Concurrency-safe

---

## Table of Contents

1. [Project Overview](#1-project-overview)
2. [Architecture Overview](#2-architecture-overview)
3. [Feature List](#3-feature-list)
4. [Database Design Summary](#4-database-design-summary)
5. [Concurrency Handling](#5-concurrency-handling)
6. [Reservation Queue Logic](#6-reservation-queue-logic)
7. [Fine Calculation](#7-fine-calculation)
8. [API Documentation](#8-api-documentation)
9. [Sample Data Setup Guide](#9-sample-data-setup-guide)
10. [Manual Concurrency Testing](#10-manual-concurrency-testing)
11. [Known Limitations](#11-known-limitations)
12. [Future Improvements](#12-future-improvements)
13. [Assumptions Made](#13-assumptions-made)
14. [Role Permission Matrix](#14-role-permission-matrix)
15. [License](#15-license)

---

## 1. Project Overview

This project implements a **Library Book Management REST API** designed to run in production environments. It manages:

- Book catalogue (titles, authors, physical copies)
- Transactional checkouts with a fixed loan period
- First-In-First-Out (FIFO) reservation queues when copies are unavailable
- Overdue fine calculation
- Race-condition-free concurrency via row-level database locks

The goal is a clean, maintainable, well-documented Go service suitable for industry code review.

---

## 2. Architecture Overview

```
HTTP Client
    │
    ▼
┌─────────────────────────────────┐
│  Gin HTTP Layer (handlers)       │  ← Request binding, status codes, response serialisation
└─────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────┐
│  LibraryService (services)       │  ← Business rules, transactions, fine calculation
└─────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────┐
│  Repositories (repositories)     │  ← GORM data access; FOR UPDATE locking
└─────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────┐
│  PostgreSQL                      │  ← UUID PKs, partial indexes, DB constraints
└─────────────────────────────────┘
```

### Directory Layout

```
.
├── cmd/
│   └── main.go               # Entry point — DB wiring, server bootstrap
├── internal/
│   ├── handlers/
│   │   └── handlers.go       # Gin route handlers, request validation, error mapping
│   ├── services/
│   │   └── library_service.go # Business logic, transactions, fine calculation
│   ├── repositories/
│   │   └── repositories.go   # GORM implementations behind Go interfaces
│   └── models/
│       └── models.go         # Domain structs and enums (pure Go)
├── migrations/
│   └── 0001_init.sql         # Manual SQL migration
├── scripts/
│   └── concurrency_test.go   # Manual concurrency stress test
├── configs/
│   └── config.example.env    # Example environment file
├── go.mod
├── README.md
└── DESIGN.md
```

---

## 3. Feature List

| Feature | Status |
|---|---|
| Create books with one or more physical copies | ✅ |
| Add extra copies to existing books | ✅ |
| List all books | ✅ |
| Transactional book checkout (atomic copy lock + record creation) | ✅ |
| Automatic FIFO reservation when no copy available | ✅ |
| Transactional book return with fine calculation | ✅ |
| Auto-assignment of returned copy to next reservation holder | ✅ |
| List user's checkout history | ✅ |
| List reservation queue for a book | ✅ |
| Database-level uniqueness constraints (no double checkouts, no duplicate reservations) | ✅ |
| Structured error responses `{"error":"...", "code":"..."}` | ✅ |
| Structured logs for all critical operations | ✅ |
| Manual concurrency stress test script | ✅ |

---

## 4. Database Design Summary

### Entities

| Table | Key Columns | Notes |
|---|---|---|
| `users` | `id`, `name`, `role` | role ∈ {`STUDENT`, `LIBRARIAN`} |
| `books` | `id`, `title`, `author`, `total_copies` | Denormalised copy count |
| `book_copies` | `id`, `book_id`, `status` | status ∈ {`AVAILABLE`, `CHECKED_OUT`} |
| `checkouts` | `id`, `book_copy_id`, `user_id`, `checkout_at`, `due_date`, `returned_at`, `fine_amount` | `returned_at` NULL = active |
| `reservations` | `id`, `book_id`, `user_id`, `queue_position`, `created_at` | Per-book FIFO queue |

### Unique / Partial Indexes

| Index Name | Definition | Purpose |
|---|---|---|
| `uniq_active_checkout` | `checkouts(book_copy_id) WHERE returned_at IS NULL` | Prevents more than one active checkout per physical copy |
| `uniq_user_book_reservation` | `reservations(book_id, user_id)` | Prevents duplicate reservation by same user for same book |
| `uniq_book_queue_position` | `reservations(book_id, queue_position)` | Prevents two reservations from claiming the same queue slot |

These indexes act as a **last line of defence** at the database level, in addition to application-level guards in the service layer.

---

## 5. Concurrency Handling

### Problem

Multiple clients may simultaneously attempt to check out the last available copy of a book. Without coordination, both could read the copy as `AVAILABLE` and create two active checkouts — violating the one-copy-one-active-checkout invariant.

### Solution: Row-Level Locking + Database Transactions

All critical paths (checkout, return) run inside `db.Transaction`, which maps to a PostgreSQL transaction with `READ COMMITTED` isolation.

| Step | Mechanism |
|---|---|
| Find available copy | `SELECT … FOR UPDATE` — blocks other transactions from reading the same rows until commit |
| Return checkout | `SELECT … FOR UPDATE` on the `checkouts` row — prevents concurrent double-returns |
| Queue position assignment | `MAX(queue_position)` + `SELECT FOR UPDATE` on reservation rows — stable under concurrency |
| Fallback | DB unique partial index `uniq_active_checkout` rejects any constraint violation that slips through |

### Why `SELECT FOR UPDATE`?

`SELECT FOR UPDATE` acquires a **row-level exclusive lock** within the transaction. Other transactions attempting to read the same rows with `FOR UPDATE` will wait, not skip. This means:

- **No dirty reads** — a copy being operated on is invisible to concurrent transactions until the lock is released.
- **No lost updates** — status changes are sequential, not concurrent.

See `DESIGN.md` for a detailed discussion of trade-offs.

---

## 6. Reservation Queue Logic

When all copies of a book are checked out:

1. The service checks whether the requesting user already has an active reservation — if yes, returns `409 Conflict` (`ErrDuplicateReservation`).
2. Otherwise, it acquires a lock on existing reservation rows for the book and computes `queue_position = MAX(queue_position) + 1`.
3. A `Reservation` record is inserted. If a concurrent process claims the same position (unique constraint violation), the operation retries once with a freshly computed position.

When a copy is returned:

1. The service fetches the reservation with the **lowest** `queue_position` for the book.
2. In the same transaction, it immediately creates a new `Checkout` for that user and deletes the reservation.

This guarantees **strict FIFO** ordering of the queue.

---

## 7. Fine Calculation

| Parameter | Value |
|---|---|
| Loan period | 14 days (`LoanPeriodDays`) |
| Fine per overdue day | 10 currency units (`FinePerDay`) |
| Minimum fine (if overdue) | 10 (at least 1 full day charged) |

**Formula:**

```
if returnedAt <= dueDate:
    fine = 0
else:
    daysLate = max(1, floor((returnmidnight(returnedAt) - midnight(dueDate)) / 24h))
    fine     = daysLate × FinePerDay
```

- Both timestamps are **truncated to midnight UTC** before subtraction, so a user returning a book at 11:59 PM on the due date is never penalised for the time-of-day difference.
- The fine is stored as an integer (100 = 100 currency units) to avoid floating-point precision issues.
- The `fine_amount` field on the `Checkout` record is updated atomically during the return transaction.

---

## 8. API Documentation

### Error Response Format

All errors follow a consistent shape:

```json
{
  "error": "human-readable message",
  "code":  "MACHINE_READABLE_CODE"
}
```

| HTTP Status | Code | When |
|---|---|---|
| 400 | `VALIDATION_ERROR` | Bad request body, invalid UUID |
| 404 | `NOT_FOUND` | Book, user, or checkout not found |
| 409 | `BUSINESS_RULE_VIOLATION` | Duplicate reservation, already returned |
| 500 | `INTERNAL_ERROR` | Unexpected server error |

---

### Endpoints

#### `POST /books` — Create Book

Creates a new book and optionally pre-creates physical copies.

**Request**
```json
{
  "title": "Clean Architecture",
  "author": "Robert C. Martin",
  "total_copies": 3
}
```

**Response** `201 Created`
```json
{
  "id": "a3b8d1b6-0b3b-4b1a-9c1a-1a2b3c4d5e6f",
  "title": "Clean Architecture",
  "author": "Robert C. Martin",
  "total_copies": 3
}
```

**curl**
```bash
curl -s -X POST http://localhost:8080/books \
  -H "Content-Type: application/json" \
  -d '{"title":"Clean Architecture","author":"Robert C. Martin","total_copies":3}'
```

---

#### `POST /books/{id}/copies` — Add Book Copy

Adds one physical copy to an existing book.

**Response** `201 Created`
```json
{
  "id": "f1e2d3c4-...",
  "book_id": "a3b8d1b6-...",
  "status": "AVAILABLE"
}
```

**curl**
```bash
curl -s -X POST http://localhost:8080/books/<book_id>/copies
```

---

#### `GET /books` — List All Books

**Response** `200 OK`
```json
[
  { "id": "...", "title": "Clean Architecture", "author": "Robert C. Martin", "total_copies": 3 }
]
```

**curl**
```bash
curl -s http://localhost:8080/books
```

---

#### `POST /books/{id}/checkout` — Checkout Book

Attempts to check out an available copy for the given user.

**Request**
```json
{ "user_id": "<user_uuid>" }
```

**Response** — copy was available `201 Created`
```json
{
  "type": "checkout",
  "checkout": {
    "id": "...",
    "book_copy_id": "...",
    "user_id": "...",
    "checkout_date": "2026-02-21T06:18:57Z",
    "due_date": "2026-03-07T06:18:57Z",
    "returned_at": null,
    "fine_amount": 0
  }
}
```

**Response** — all copies checked out `201 Created`
```json
{
  "type": "reservation",
  "reservation": {
    "id": "...",
    "book_id": "...",
    "user_id": "...",
    "queue_position": 2,
    "created_at": "2026-02-21T06:18:57Z"
  }
}
```

**curl**
```bash
curl -s -X POST http://localhost:8080/books/<book_id>/checkout \
  -H "Content-Type: application/json" \
  -d '{"user_id":"<user_id>"}'
```

---

#### `POST /checkouts/{id}/return` — Return Checkout

Returns a borrowed copy. Computes and stores the fine. If reservations exist, the copy is immediately assigned to the next user in the queue.

**Response** `200 OK` — updated checkout record
```json
{
  "id": "...",
  "book_copy_id": "...",
  "user_id": "...",
  "checkout_date": "2026-02-21T06:18:57Z",
  "due_date": "2026-03-07T06:18:57Z",
  "returned_at": "2026-03-10T09:00:00Z",
  "fine_amount": 30
}
```

**curl**
```bash
curl -s -X POST http://localhost:8080/checkouts/<checkout_id>/return
```

---

#### `GET /users/{id}/checkouts` — List User Checkouts

Returns all checkouts (active and historical) for the given user.

**curl**
```bash
curl -s http://localhost:8080/users/<user_id>/checkouts
```

---

#### `GET /books/{id}/reservations` — List Reservations

Returns the reservation queue for a book, ordered by `queue_position`.

**curl**
```bash
curl -s http://localhost:8080/books/<book_id>/reservations
```

---

## 9. Sample Data Setup Guide

### Prerequisites

- PostgreSQL running locally
- Go 1.22+

### Step 1 — Create database

```sql
CREATE DATABASE library_db;
CREATE USER library_user WITH ENCRYPTED PASSWORD 'library_pass';
GRANT ALL PRIVILEGES ON DATABASE library_db TO library_user;
```

### Step 2 — Apply migration

```bash
psql -d library_db -U library_user -f migrations/0001_init.sql
```

### Step 3 — Insert seed data

```sql
-- Insert a librarian
INSERT INTO users (id, name, role) VALUES
  ('00000000-0000-0000-0000-000000000001', 'Alice Librarian', 'LIBRARIAN');

-- Insert two students
INSERT INTO users (id, name, role) VALUES
  ('00000000-0000-0000-0000-000000000002', 'Bob Student',   'STUDENT'),
  ('00000000-0000-0000-0000-000000000003', 'Carol Student', 'STUDENT'),
  ('00000000-0000-0000-0000-000000000004', 'Dave Student',  'STUDENT');
```

### Step 4 — Configure environment

**Linux / macOS:**
```bash
export DATABASE_URL="postgres://library_user:library_pass@localhost:5432/library_db?sslmode=disable"
export SERVER_ADDR=":8080"
```

**Windows PowerShell:**
```powershell
$env:DATABASE_URL="postgres://library_user:library_pass@localhost:5432/library_db?sslmode=disable"
$env:SERVER_ADDR=":8080"
```

### Step 5 — Install dependencies and run

```bash
go mod tidy
go run ./cmd/main.go
```

### Step 6 — Create a book via API

```bash
BOOK=$(curl -s -X POST http://localhost:8080/books \
  -H "Content-Type: application/json" \
  -d '{"title":"The Go Programming Language","author":"Donovan & Kernighan","total_copies":1}')
echo $BOOK
BOOK_ID=$(echo $BOOK | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
```

---

## 10. Manual Concurrency Testing

### Using the stress test script

```bash
# Ensure the server is running first (Step 4 above).
go run ./scripts/concurrency_test.go \
  <book_id> \
  00000000-0000-0000-0000-000000000002 \
  00000000-0000-0000-0000-000000000003 \
  00000000-0000-0000-0000-000000000004
```

Or using environment variables:
```bash
BOOK_ID=<book_id> \
USER_IDS="00000000-0000-0000-0000-000000000002,00000000-0000-0000-0000-000000000003" \
go run ./scripts/concurrency_test.go
```

**Expected output (1 copy, 3 users):**
```
=== Library Concurrency Test ===
Server : http://localhost:8080
Book   : <book_id>
Users  : 3

Firing all requests simultaneously...
All requests completed.

  [CHCK] user=00000000-0000-0000-0000-000000000002  status=201 type=checkout
  [RESV] user=00000000-0000-0000-0000-000000000003  status=201 type=reservation
  [RESV] user=00000000-0000-0000-0000-000000000004  status=201 type=reservation

--- Summary ---
Checkouts    : 1
Reservations : 2
Failures     : 0
Total        : 3
```

**What proves correctness:**

- Exactly as many checkouts as there are available copies.
- All remaining users are placed in the reservation queue rather than erroring out.
- The DB unique partial index `uniq_active_checkout` would have caused one of the concurrent transactions to fail at the DB level if the application lock had somehow permitted two competing checkouts.

### Verifying the DB directly

```sql
-- Should return at most 1 row per book_copy_id
SELECT book_copy_id, COUNT(*) AS active
FROM checkouts
WHERE returned_at IS NULL
GROUP BY book_copy_id
HAVING COUNT(*) > 1;
-- Expect: 0 rows
```

---

## 11. Known Limitations

| Limitation | Notes |
|---|---|
| No authentication / authorisation | User roles are semantic only. Any caller can pass any `user_id`. |
| No pagination | `GET /books` and checkout list return all rows. |
| No notification system | Reserved users are not notified when a copy becomes available. |
| Manual migrations | No migration runner; SQL must be applied manually via `psql`. |
| Single-retry for queue collisions | Only one retry on `queue_position` unique constraint violation. |
| In-process logging only | Standard `log` package; no log aggregation (Loki, ELK). |
| No health check endpoint | No `/healthz` or `/readyz` endpoints. |

---

## 12. Future Improvements

| Area | Suggestion |
|---|---|
| Authentication | Add JWT-based middleware; enforce role checks at the handler layer. |
| Pagination | Add `?limit=` and `?offset=` query params to list endpoints. |
| Notifications | Emit events (e.g. via a message queue) when reservations are fulfilled. |
| Observability | Integrate structured logging (zerolog/zap) and Prometheus metrics. |
| Migration tooling | Integrate `golang-migrate` or Flyway for versioned, automated migrations. |
| Unit tests | Mock repositories and write table-driven unit tests for service logic. |
| Integration tests | Use `testcontainers-go` to spin up a real PostgreSQL for integration tests. |
| Graceful shutdown | Handle `SIGTERM`/`SIGINT` to drain in-flight requests before exit. |
| Config management | Use `viper` or `envconfig` for typed, validated configuration. |
| OpenAPI spec | Generate Swagger/OpenAPI documentation from route definitions. |

---

## 13. Assumptions Made

1. **No authentication**: User identity is passed as `user_id` in the request body; no token verification.
2. **Integer fines**: Fine amounts are stored as plain integers (e.g. `30` = 30 currency units). No decimal precision needed for this use case.
3. **UTC timestamps**: All timestamps are stored and computed in UTC.
4. **Calendar-day fine rounding**: Fines are based on full calendar days (midnight-to-midnight), not hours.
5. **Single library branch**: There is no concept of library branches or locations.
6. **Unlimited users**: Any UUID can be used as a user ID; the API does not enforce user creation as a prerequisite (users must already exist in the `users` table).
7. **Manual DB operations**: Schema migration is performed manually. The app does not auto-migrate on startup.
8. **One active checkout per copy**: A book copy can only have one active checkout at any time (enforced by `uniq_active_checkout` partial index).
9. **One reservation per user per book**: A user may only hold one reservation slot per book at a time (enforced by `uniq_user_book_reservation`).

---

## 14. Role Permission Matrix

| Action | STUDENT | LIBRARIAN |
|---|---|---|
| `POST /books` — Create book | ✗ | ✓ |
| `POST /books/:id/copies` — Add copy | ✗ | ✓ |
| `GET /books` — List books | ✓ | ✓ |
| `POST /books/:id/checkout` — Checkout | ✓ | ✓ |
| `POST /checkouts/:id/return` — Return | ✓ | ✓ |
| `GET /users/:id/checkouts` — View checkouts | ✓ (own) | ✓ |
| `GET /books/:id/reservations` — View queue | ✓ | ✓ |

> **Note**: Role enforcement is **semantic only** in this implementation. Actual enforcement would require authenticated sessions and middleware-level role checks, which are out of scope for this project.

---

## 15. License

```
MIT License

Copyright (c) 2026 Library Management API Contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```
