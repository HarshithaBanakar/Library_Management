# Library Book Management API (Go, Gin, PostgreSQL)

Production-style REST API for a Library Book Management System built in Go with Gin, PostgreSQL, and GORM. It follows a layered/clean architecture style: handlers → services → repositories → models.

## Tech Stack

- Go (latest stable)
- Gin (HTTP routing)
- PostgreSQL
- GORM ORM
- UUID primary keys

## Project Structure

- `cmd/main.go`: application entrypoint, wiring DB and HTTP server.
- `internal/models`: domain entities and enums.
- `internal/repositories`: data access layer (GORM).
- `internal/services`: business logic, transactions, fine calculation.
- `internal/handlers`: HTTP handlers (Gin).
- `configs/`: example environment configuration.
- `migrations/`: SQL migrations (schema).
- `README.md`: setup and API docs.
- `DESIGN.md`: architecture and diagrams.

## Getting Started

### Prerequisites

- Go installed (1.22+).
- PostgreSQL running locally.
- A migration tool (e.g. `golang-migrate`) or manual access to run SQL files.

### 1. Create database and user

```sql
CREATE DATABASE library_db;
CREATE USER library_user WITH ENCRYPTED PASSWORD 'library_pass';
GRANT ALL PRIVILEGES ON DATABASE library_db TO library_user;
```

### 2. Apply migrations

Run the SQL migration:

```bash
psql -d library_db -U library_user -f migrations/0001_init.sql
```

Or use your preferred migration tool pointed at `migrations/`.

### 3. Configure environment

Copy the example config and export variables:

```bash
cp configs/config.example.env .env
```

Ensure at least:

```bash
export DATABASE_URL="postgres://library_user:library_pass@localhost:5432/library_db?sslmode=disable"
export SERVER_ADDR=":8080"
```

On Windows PowerShell:

```powershell
$env:DATABASE_URL="postgres://library_user:library_pass@localhost:5432/library_db?sslmode=disable"
$env:SERVER_ADDR=":8080"
```

### 4. Install dependencies

```bash
go mod tidy
```

### 5. Run the server

```bash
go run ./cmd/main.go
```

Server listens on `http://localhost:8080` by default.

## API Documentation

### Roles

- **STUDENT**: can check out/return books and list their own checkouts.
- **LIBRARIAN**: can create books and add copies.

There is **no authentication** in this project. You specify `user_id` explicitly when calling student endpoints.

### Core Entities

- **User**: `{ id, name, role }`
- **Book**: `{ id, title, author, total_copies }`
- **BookCopy**: `{ id, book_id, status }`
- **Checkout**: `{ id, book_copy_id, user_id, checkout_date, due_date, returned_at, fine_amount }`
- **Reservation**: `{ id, book_id, user_id, queue_position, created_at }`

### Endpoints

#### Librarian

- **POST `/books`**

  Create a new book (optionally with initial copies).

  Request body:

  ```json
  {
    "title": "Clean Architecture",
    "author": "Robert C. Martin",
    "total_copies": 3
  }
  ```

  Response `201 Created`:

  ```json
  {
    "id": "...",
    "title": "Clean Architecture",
    "author": "Robert C. Martin",
    "total_copies": 3
  }
  ```

  Sample curl:

  ```bash
  curl -X POST http://localhost:8080/books \
    -H "Content-Type: application/json" \
    -d '{"title":"Clean Architecture","author":"Robert C. Martin","total_copies":3}'
  ```

- **POST `/books/{id}/copies`**

  Add a new physical copy for a book.

  Sample curl:

  ```bash
  curl -X POST http://localhost:8080/books/<book_id>/copies
  ```

#### Student

- **POST `/books/{id}/checkout`**

  Attempt to check out a copy of a book for a specific user.

  Request body:

  ```json
  {
    "user_id": "<user_uuid>"
  }
  ```

  - If a copy is immediately available:

    ```json
    {
      "type": "checkout",
      "checkout": {
        "id": "...",
        "book_copy_id": "...",
        "user_id": "...",
        "checkout_date": "2026-02-20T12:00:00Z",
        "due_date": "2026-03-06T12:00:00Z",
        "returned_at": null,
        "fine_amount": 0
      }
    }
    ```

  - If no copy is available and the user is queued:

    ```json
    {
      "type": "reservation",
      "reservation": {
        "id": "...",
        "book_id": "...",
        "user_id": "...",
        "queue_position": 3,
        "created_at": "2026-02-20T12:00:00Z"
      }
    }
    ```

  Sample curl:

  ```bash
  curl -X POST http://localhost:8080/books/<book_id>/checkout \
    -H "Content-Type: application/json" \
    -d '{"user_id":"<user_id>"}'
  ```

- **POST `/checkouts/{id}/return`**

  Return a checked-out copy. This may automatically assign the copy to the next reservation in the queue.

  Response `200 OK` (updated checkout with `returned_at` and `fine_amount`):

  ```bash
  curl -X POST http://localhost:8080/checkouts/<checkout_id>/return
  ```

- **GET `/users/{id}/checkouts`**

  List all checkouts for a user (both active and returned).

  Sample curl:

  ```bash
  curl http://localhost:8080/users/<user_id>/checkouts
  ```

#### General

- **GET `/books`**

  List all books.

  ```bash
  curl http://localhost:8080/books
  ```

- **GET `/books/{id}/reservations`**

  List all reservations for a given book, ordered by `queue_position`.

  ```bash
  curl http://localhost:8080/books/<book_id>/reservations
  ```

## Transaction Handling

### Checkout Flow

Implemented in `internal/services/library_service.go` (`CheckoutBook`):

1. **Begin transaction** using `db.Transaction`.
2. Lock one `AVAILABLE` `BookCopy` for the requested `book_id` using GORM's `SELECT ... FOR UPDATE` via `clause.Locking`.
3. If a copy is found:
   - Update its status to `CHECKED_OUT`.
   - Create a `Checkout` record with `checkout_date` and `due_date`.
4. If no copy is found:
   - Compute `queue_position = MAX(queue_position) + 1` within the transaction.
   - Insert a `Reservation` record for the requesting user.
5. Commit the transaction.

Because the row is selected with a locking clause, concurrent transactions cannot both grab the same copy, ensuring **race-free checkout**.

### Return Flow

Implemented in `internal/services/library_service.go` (`ReturnCheckout`):

1. **Begin transaction**.
2. Lock the `Checkout` row for a given ID (`FOR UPDATE`).
3. If already returned, fail fast.
4. Compute the fine based on due date vs. return time and update `returned_at` and `fine_amount`.
5. Mark the associated `BookCopy` as `AVAILABLE`.
6. Look up the earliest reservation for the book (`queue_position ASC, created_at ASC`).
7. If a reservation exists:
   - Mark the same `BookCopy` as `CHECKED_OUT` again.
   - Delete the reservation.
   - Create a **new Checkout** for the reserved user.
8. Commit the transaction.

All state changes for return, queue consumption, and new checkout are atomic.

## Reservation Queue Logic

- Each `Reservation` stores:
  - `book_id`
  - `user_id`
  - `queue_position`
  - `created_at`
- On enqueue (no available copies):
  - Within the same transaction:
    - Fetch `MAX(queue_position)` for the book.
    - Insert a new reservation with `queue_position = max + 1`.
- On return:
  - Fetch the **lowest** `queue_position` for the book.
  - Delete that reservation when its copy is assigned.
  - Automatically create a checkout for that user.

This guarantees FIFO semantics for reservations per book.

## Fine Calculation

Fine calculation is implemented in the service layer (`calculateFine`):

- If `returned_at <= due_date`: fine is `0`.
- If returned after the due date:
  - \(\text{days\_late} = \max(1, \lfloor \frac{\text{returnedAt-dueDate}}{24h} \rfloor)\)
  - \(\text{fine} = \text{days\_late} \times 10\)
- The computed `fine_amount` is stored on the `Checkout` record and returned via API.

## Role Permission Matrix

Even though there is no authentication layer, the intended semantics are:

| Action                         | STUDENT | LIBRARIAN |
|--------------------------------|---------|-----------|
| Create book                    | No      | Yes       |
| Add book copy                  | No      | Yes       |
| Checkout book copy             | Yes     | Yes       |
| Return book                    | Yes     | Yes       |
| View own checkouts             | Yes     | Yes       |
| View reservations per book     | Yes     | Yes       |

Enforcement of roles would typically be added in middleware or the handler layer if authentication were introduced.

## Clean Architecture Notes

- **Handlers**: Only handle HTTP concerns (binding, status codes, serialization). They call services and do not know about GORM.
- **Services**: Contain business rules, transactions, fine calculations, and concurrency handling (checkout/return workflows).
- **Repositories**: Encapsulate GORM access patterns and SQL details, exposing simple Go interfaces.
- **Models**: Pure Go structs that map to database entities with minimal annotations.

This separation makes it straightforward to:

- Write unit tests for services by mocking repositories.
- Swap Gin/GORM or the persistence layer without changing core domain logic.

